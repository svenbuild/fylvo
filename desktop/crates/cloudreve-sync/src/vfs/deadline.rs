//! Every FUSE request that may wait gets an absolute deadline. A watchdog thread (not tokio, so
//! a stuck task or lock cannot stop it) answers `EIO` to a request that is still open at its
//! deadline. Whoever takes the reply first answers it; the other side then does nothing. So no
//! program waits on the drive longer than the deadline.

use fuser::Errno;
use std::cmp::Reverse;
use std::collections::{BinaryHeap, HashMap};
use std::sync::{Arc, Condvar, Mutex};
use std::time::{Duration, Instant};

/// Listing, lookup and changes of the server tree.
pub(crate) const METADATA: Duration = Duration::from_secs(20);
/// Reading and writing content (may load from the server first).
pub(crate) const CONTENT: Duration = Duration::from_secs(60);

/// A FUSE reply that can fail.
pub(crate) trait FailReply: Send + 'static {
    fn fail(self, errno: Errno);
}

macro_rules! fail_reply {
    ($($reply:ty),*) => {
        $(impl FailReply for $reply {
            fn fail(self, errno: Errno) {
                self.error(errno)
            }
        })*
    };
}

fail_reply!(
    fuser::ReplyEntry,
    fuser::ReplyAttr,
    fuser::ReplyData,
    fuser::ReplyEmpty,
    fuser::ReplyOpen,
    fuser::ReplyWrite,
    fuser::ReplyCreate,
    fuser::ReplyDirectory
);

type Expiry = Box<dyn FnOnce() + Send>;

struct Queue {
    order: BinaryHeap<Reverse<(Instant, u64)>>,
    actions: HashMap<u64, Expiry>,
    next: u64,
    stopped: bool,
}

/// The watchdog of one drive.
#[derive(Clone)]
pub(crate) struct Watchdog {
    shared: Arc<(Mutex<Queue>, Condvar)>,
}

impl Watchdog {
    pub(crate) fn start() -> Self {
        let shared = Arc::new((
            Mutex::new(Queue {
                order: BinaryHeap::new(),
                actions: HashMap::new(),
                next: 0,
                stopped: false,
            }),
            Condvar::new(),
        ));
        let thread_shared = shared.clone();
        std::thread::Builder::new()
            .name("fylvo-drive-watchdog".into())
            .spawn(move || run(thread_shared))
            .expect("start the drive watchdog");
        Self { shared }
    }

    fn schedule(&self, at: Instant, action: Expiry) {
        let (lock, wake) = &*self.shared;
        let mut queue = lock.lock().unwrap_or_else(|e| e.into_inner());
        let id = queue.next;
        queue.next += 1;
        queue.order.push(Reverse((at, id)));
        queue.actions.insert(id, action);
        wake.notify_one();
    }

    pub(crate) fn stop(&self) {
        let (lock, wake) = &*self.shared;
        lock.lock().unwrap_or_else(|e| e.into_inner()).stopped = true;
        wake.notify_one();
    }

    /// Hold `reply` until it is taken or `limit` has passed.
    pub(crate) fn slot<R: FailReply>(&self, reply: R, limit: Duration) -> Slot<R> {
        let slot = Slot(Arc::new(Mutex::new(Some(reply))));
        let expiring = slot.clone();
        self.schedule(
            Instant::now() + limit,
            Box::new(move || {
                if let Some(reply) = expiring.take() {
                    tracing::warn!(target: "vfs", "A drive request took too long, answered with an I/O error");
                    reply.fail(Errno::EIO);
                }
            }),
        );
        slot
    }
}

fn run(shared: Arc<(Mutex<Queue>, Condvar)>) {
    let (lock, wake) = &*shared;
    let mut queue = lock.lock().unwrap_or_else(|e| e.into_inner());
    loop {
        if queue.stopped {
            // Open requests are answered now, the drive goes away.
            let actions: Vec<Expiry> = queue.actions.drain().map(|(_, a)| a).collect();
            drop(queue);
            actions.into_iter().for_each(|action| action());
            return;
        }
        let now = Instant::now();
        let mut due = Vec::new();
        while let Some(Reverse((at, id))) = queue.order.peek().copied() {
            if at > now {
                break;
            }
            queue.order.pop();
            if let Some(action) = queue.actions.remove(&id) {
                due.push(action);
            }
        }
        if !due.is_empty() {
            drop(queue);
            due.into_iter().for_each(|action| action());
            queue = lock.lock().unwrap_or_else(|e| e.into_inner());
            continue;
        }
        let wait = queue
            .order
            .peek()
            .map(|Reverse((at, _))| at.saturating_duration_since(now))
            .unwrap_or(Duration::from_secs(3600));
        queue = wake
            .wait_timeout(queue, wait)
            .unwrap_or_else(|e| e.into_inner())
            .0;
    }
}

/// A reply that either the worker or the watchdog sends.
pub(crate) struct Slot<R>(Arc<Mutex<Option<R>>>);

impl<R> Clone for Slot<R> {
    fn clone(&self) -> Self {
        Self(self.0.clone())
    }
}

impl<R: FailReply> Slot<R> {
    /// The reply, if nobody answered yet.
    pub(crate) fn take(&self) -> Option<R> {
        self.0.lock().unwrap_or_else(|e| e.into_inner()).take()
    }

    /// Whether the request was answered already (by the watchdog: it timed out). A server change
    /// for an expired request is not started.
    pub(crate) fn expired(&self) -> bool {
        self.0.lock().unwrap_or_else(|e| e.into_inner()).is_none()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicU32, Ordering};

    struct Probe(Arc<AtomicU32>);

    impl FailReply for Probe {
        fn fail(self, _errno: Errno) {
            self.0.fetch_add(1, Ordering::SeqCst);
        }
    }

    #[test]
    fn an_overdue_request_is_answered_once() {
        let watchdog = Watchdog::start();
        let failed = Arc::new(AtomicU32::new(0));
        let slow = watchdog.slot(Probe(failed.clone()), Duration::from_millis(50));
        let fast = watchdog.slot(Probe(failed.clone()), Duration::from_millis(50));
        // The fast one is answered by its worker in time.
        assert!(fast.take().is_some());
        std::thread::sleep(Duration::from_millis(300));
        assert_eq!(failed.load(Ordering::SeqCst), 1);
        assert!(slow.expired());
        // The worker of the slow one comes too late and does nothing.
        assert!(slow.take().is_none());
        watchdog.stop();
    }

    #[test]
    fn stopping_answers_open_requests() {
        let watchdog = Watchdog::start();
        let failed = Arc::new(AtomicU32::new(0));
        let _open = watchdog.slot(Probe(failed.clone()), Duration::from_secs(600));
        watchdog.stop();
        std::thread::sleep(Duration::from_millis(100));
        assert_eq!(failed.load(Ordering::SeqCst), 1);
    }
}

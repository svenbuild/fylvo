//! Fylvo: a gate for sync work, so the updater installs only while nothing runs.
//!
//! Every piece of work that can change files (a handler that a mount spawns, a batch of
//! remote events, a waiting upload retry) holds a [`Busy`] guard while it runs. The updater
//! calls [`close_if_idle`]: it closes the gate only when no guard is held and its own check
//! (queued tasks) also says idle, in one step under the same lock. After that new work waits
//! at the gate until [`reopen`] (the update did not happen) or the app ends.

use std::sync::Mutex;
use tokio::sync::Notify;

struct Gate {
    running: usize,
    /// Work that arrived while the gate was closed and waits for it.
    waiting: usize,
    closed: bool,
}

static GATE: Mutex<Gate> = Mutex::new(Gate { running: 0, waiting: 0, closed: false });
static OPENED: Notify = Notify::const_new();

/// Held while a piece of sync work runs.
#[must_use]
pub struct Busy(());

impl Drop for Busy {
    fn drop(&mut self) {
        let mut gate = GATE.lock().unwrap_or_else(|e| e.into_inner());
        gate.running = gate.running.saturating_sub(1);
    }
}

/// Counts a caller waiting at the closed gate; also when its wait is cancelled.
struct Waiting(());

impl Drop for Waiting {
    fn drop(&mut self) {
        let mut gate = GATE.lock().unwrap_or_else(|e| e.into_inner());
        gate.waiting = gate.waiting.saturating_sub(1);
    }
}

/// Waits until the gate is open and counts the caller as running.
pub async fn enter() -> Busy {
    let mut waiting: Option<Waiting> = None;
    loop {
        let opened = OPENED.notified();
        tokio::pin!(opened);
        // Register before looking, so a reopen in between is not missed.
        opened.as_mut().enable();
        {
            let mut gate = GATE.lock().unwrap_or_else(|e| e.into_inner());
            if !gate.closed {
                if let Some(mark) = waiting.take() {
                    // Uncounted here, under the lock its drop would take.
                    gate.waiting = gate.waiting.saturating_sub(1);
                    std::mem::forget(mark);
                }
                gate.running += 1;
                return Busy(());
            }
            if waiting.is_none() {
                gate.waiting += 1;
                waiting = Some(Waiting(()));
            }
        }
        opened.await;
    }
}

/// Counts the caller as running without waiting, for work that is already under way when it
/// starts (a retry handed over from a finished task).
pub fn enter_now() -> Busy {
    let mut gate = GATE.lock().unwrap_or_else(|e| e.into_inner());
    gate.running += 1;
    Busy(())
}

/// Closes the gate if no work runs and `queued()` reports nothing waiting either. Both are
/// decided under the gate's lock, so no new work can slip in between. `queued` must not block
/// for long and must not enter the gate.
pub fn close_if_idle(queued: impl FnOnce() -> bool) -> bool {
    let mut gate = GATE.lock().unwrap_or_else(|e| e.into_inner());
    if gate.closed {
        return true;
    }
    if gate.running > 0 || queued() {
        return false;
    }
    gate.closed = true;
    true
}

/// After the gate was closed: true if nothing runs and nothing arrived at the gate since.
/// The updater asks this after a short pause (longer than the 2 s file event delay), so work
/// that was on its way when the gate closed is not cut off.
pub fn still_idle() -> bool {
    let gate = GATE.lock().unwrap_or_else(|e| e.into_inner());
    gate.closed && gate.running == 0 && gate.waiting == 0
}

/// Opens the gate again; waiting work goes on.
pub fn reopen() {
    GATE.lock().unwrap_or_else(|e| e.into_inner()).closed = false;
    OPENED.notify_waiters();
}

/// How many pieces of work run right now.
pub fn running() -> usize {
    GATE.lock().unwrap_or_else(|e| e.into_inner()).running
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::Duration;

    // One test: the gate is global for the process.
    #[tokio::test]
    async fn gate_closes_only_when_idle_and_holds_new_work() {
        let busy = enter().await;
        assert!(!close_if_idle(|| false), "running work keeps the gate open");
        drop(busy);
        assert!(!close_if_idle(|| true), "queued work keeps the gate open");

        // A retry handed over while the updater looks keeps it open too.
        let retry = enter_now();
        assert!(!close_if_idle(|| false));
        drop(retry);

        assert!(close_if_idle(|| false));
        assert!(still_idle());
        let cancelled = tokio::spawn(async { enter().await });
        tokio::time::sleep(Duration::from_millis(50)).await;
        assert!(!still_idle());
        cancelled.abort();
        let _ = cancelled.await;
        assert!(still_idle(), "a cancelled wait does not count");

        let waiting = tokio::spawn(async { enter().await });
        tokio::time::sleep(Duration::from_millis(50)).await;
        assert!(!waiting.is_finished(), "new work waits while the gate is closed");
        assert_eq!(running(), 0);
        assert!(!still_idle(), "work arrived at the closed gate");

        reopen();
        let busy = tokio::time::timeout(Duration::from_secs(5), waiting).await.unwrap().unwrap();
        assert_eq!(running(), 1);
        drop(busy);
        assert_eq!(running(), 0);
    }
}

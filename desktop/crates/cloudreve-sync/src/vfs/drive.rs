//! The state of one mounted drive: the known tree, open files, local changes, the connection.
//! The FUSE glue (`fs.rs`) calls these functions; everything that may wait is `async` and runs
//! on tokio, the rest answers at once.

use super::chunks::{content_range_matches, partly_written, READ_AHEAD, CHUNK};
use super::content::{clean_key, now_secs, Cell, Contents};
use super::deadline::Watchdog;
use super::names;
use super::store::{ChangeRow, EntryRow, Store};
use crate::inventory::InventoryDb;
use cloudreve_api::api::explorer::ExplorerApiExt;
use cloudreve_api::api::ExplorerApi;
use cloudreve_api::error::{ApiError, ErrorCode};
use cloudreve_api::models::explorer::{
    file_type, CreateFileService, DeleteFileService, FileResponse, FileURLService,
    GetFileInfoService, MoveFileService, RenameFileService,
};
use cloudreve_api::models::uri::CrUri;
use cloudreve_api::Client;
use fuser::{Errno, FileAttr, FileType, INodeNo};
use std::collections::{BTreeMap, HashMap};
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Mutex, MutexGuard};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

pub(crate) const ROOT: u64 = 1;
/// A listing younger than this is not asked again.
const FRESH: Duration = Duration::from_secs(60);
/// Free space the drive always leaves on the disk of its store.
const DISK_RESERVE: u64 = 2 * 1024 * 1024 * 1024;
/// Without a byte for this long a download counts as stuck.
const STALL: Duration = Duration::from_secs(30);
/// Wait after the last close before an upload starts.
pub(crate) const UPLOAD_DELAY: Duration = Duration::from_secs(2);

#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct Remote {
    pub id: String,
    /// The server version (`primary_entity`), "" for folders.
    pub version: String,
}

/// A local change of a file not uploaded yet.
pub(crate) struct Change {
    pub cell: Arc<Cell>,
    /// A temp name of an editor: kept here, never uploaded.
    pub local_only: bool,
    /// Counts writes, so an upload knows whether the file changed while it ran.
    pub counter: u64,
    /// When the upload may start (`None` while open for writing).
    pub due: Option<Instant>,
    pub failed: Option<String>,
    pub attempts: u32,
    /// Found "changed and open" after a crash: uploaded as a conflict copy, never over the
    /// server file.
    pub recovered: bool,
}

pub(crate) struct Listing {
    /// When the server last listed it (`None`: from the store, stale).
    pub fresh_at: Option<Instant>,
    pub children: BTreeMap<String, u64>,
    /// Counts local changes of the children: a server listing asked for before one of them
    /// is not applied (it would undo it).
    pub generation: u64,
    /// False for a folder made up at start to hold a recorded change (never listed): it is
    /// listed before it is answered when the server can be reached.
    pub complete: bool,
}


pub(crate) struct Node {
    pub parent: u64,
    pub name: String,
    pub is_dir: bool,
    pub size: u64,
    pub mtime: SystemTime,
    /// The server file (for a changed file: the version the change started from).
    pub remote: Option<Remote>,
    pub change: Option<Change>,
    pub listing: Option<Listing>,
    pub writers: u32,
    pub uploading: bool,
    /// The content key of the last open (the kernel may keep its page cache if unchanged).
    pub last_key: Option<String>,
}

impl Node {
    pub(crate) fn new(parent: u64, name: &str, is_dir: bool) -> Self {
        Self {
            parent,
            name: name.to_string(),
            is_dir,
            size: 0,
            mtime: SystemTime::now(),
            remote: None,
            change: None,
            listing: None,
            writers: 0,
            uploading: false,
            last_key: None,
        }
    }
}

pub(crate) struct Tree {
    pub nodes: HashMap<u64, Node>,
    next: u64,
}

impl Tree {
    fn new() -> Self {
        let mut nodes = HashMap::new();
        nodes.insert(ROOT, Node::new(ROOT, "", true));
        Self { nodes, next: ROOT + 1 }
    }

    /// The path of a node relative to the drive root, `None` if it is no longer in the tree.
    pub(crate) fn path(&self, mut ino: u64) -> Option<String> {
        let mut parts = Vec::new();
        while ino != ROOT {
            let node = self.nodes.get(&ino)?;
            if node.parent == 0 {
                return None;
            }
            parts.push(node.name.as_str());
            ino = node.parent;
        }
        parts.reverse();
        Some(parts.join("/"))
    }

    pub(crate) fn child(&self, parent: u64, name: &str) -> Option<u64> {
        self.nodes.get(&parent)?.listing.as_ref()?.children.get(name).copied()
    }

    pub(crate) fn add(&mut self, parent: u64, node: Node) -> u64 {
        let ino = self.next;
        self.next += 1;
        let name = node.name.clone();
        self.nodes.insert(ino, node);
        if let Some(listing) = self.nodes.get_mut(&parent).and_then(|p| p.listing.as_mut()) {
            listing.children.insert(name, ino);
            listing.generation += 1;
        }
        ino
    }

    /// Take a node out of its folder (open handles keep it).
    fn detach(&mut self, ino: u64) {
        let Some(node) = self.nodes.get_mut(&ino) else {
            return;
        };
        let (parent, name) = (node.parent, node.name.clone());
        node.parent = 0;
        if let Some(listing) = self.nodes.get_mut(&parent).and_then(|p| p.listing.as_mut()) {
            if listing.children.get(&name) == Some(&ino) {
                listing.children.remove(&name);
            }
            listing.generation += 1;
        }
    }

    fn move_to(&mut self, ino: u64, parent: u64, name: &str) {
        self.detach(ino);
        if let Some(node) = self.nodes.get_mut(&ino) {
            node.parent = parent;
            node.name = name.to_string();
        }
        if let Some(listing) = self.nodes.get_mut(&parent).and_then(|p| p.listing.as_mut()) {
            listing.children.insert(name.to_string(), ino);
            listing.generation += 1;
        }
    }
}

pub(crate) struct Handle {
    pub ino: u64,
    pub cell: Arc<Cell>,
    pub write: bool,
}

/// Static facts of a drive.
pub(crate) struct DriveInfo {
    pub id: String,
    pub mount_point: PathBuf,
    pub remote_base: String,
    pub instance_url: String,
    pub data_dir: PathBuf,
}

pub(crate) struct Drive {
    pub info: DriveInfo,
    pub client: Arc<Client>,
    pub inventory: Arc<InventoryDb>,
    pub store: Store,
    pub contents: Contents,
    pub tree: Mutex<Tree>,
    pub handles: Mutex<HashMap<u64, Handle>>,
    next_fh: AtomicU64,
    pub online: AtomicBool,
    probing: AtomicBool,
    pub read_only: AtomicBool,
    pub stopping: AtomicBool,
    pub watchdog: Watchdog,
    pub rt: tokio::runtime::Handle,
    list_locks: Mutex<HashMap<u64, Arc<tokio::sync::Mutex<()>>>>,
    urls: Mutex<HashMap<String, (String, Instant)>>,
    fetch_slots: tokio::sync::Semaphore,
    meta_slots: tokio::sync::Semaphore,
    pub inval: Mutex<Option<std::sync::mpsc::Sender<u64>>>,
    pub pins: Mutex<Vec<String>>,
    pub upload_wake: tokio::sync::Notify,
    pub pin_wake: tokio::sync::Notify,
    http: reqwest::Client,
    uid: u32,
    gid: u32,
}

/// Unix seconds of a time (before 1970: 0).
pub(crate) fn unix(time: SystemTime) -> i64 {
    time.duration_since(UNIX_EPOCH).map(|d| d.as_secs() as i64).unwrap_or(0)
}

fn from_unix(secs: i64) -> SystemTime {
    UNIX_EPOCH + Duration::from_secs(secs.max(0) as u64)
}

fn parse_time(text: &str) -> SystemTime {
    chrono::DateTime::parse_from_rfc3339(text)
        .map(|t| SystemTime::from(t.with_timezone(&chrono::Utc)))
        .unwrap_or(UNIX_EPOCH)
}

/// What a server answer means for the program that asked.
pub(crate) fn errno_of(error: &ApiError) -> Errno {
    match error {
        ApiError::RequestError(_) => Errno::ENETUNREACH,
        ApiError::LockConflict { .. } => Errno::EBUSY,
        ApiError::LoginRequired(_) | ApiError::RefreshTokenExpired => Errno::EACCES,
        ApiError::ApiError { code, .. } => match *code {
            c if c == ErrorCode::NotFound as i32 || c == ErrorCode::ParentNotExist as i32 => Errno::ENOENT,
            c if c == ErrorCode::PermissionDenied as i32 => Errno::EACCES,
            c if c == ErrorCode::ObjectExisted as i32 || c == ErrorCode::NameCaseTaken as i32 => Errno::EEXIST,
            _ => Errno::EIO,
        },
        _ => Errno::EIO,
    }
}

impl Drive {
    #[allow(clippy::too_many_arguments)]
    pub(crate) fn new(
        info: DriveInfo,
        client: Arc<Client>,
        inventory: Arc<InventoryDb>,
        store: Store,
        contents: Contents,
        rt: tokio::runtime::Handle,
    ) -> Self {
        let pins = store.pins().unwrap_or_default();
        Self {
            info,
            client,
            inventory,
            store,
            contents,
            tree: Mutex::new(Tree::new()),
            handles: Mutex::new(HashMap::new()),
            next_fh: AtomicU64::new(1),
            online: AtomicBool::new(true),
            probing: AtomicBool::new(false),
            read_only: AtomicBool::new(false),
            stopping: AtomicBool::new(false),
            watchdog: Watchdog::start(),
            rt,
            list_locks: Mutex::new(HashMap::new()),
            urls: Mutex::new(HashMap::new()),
            fetch_slots: tokio::sync::Semaphore::new(4),
            meta_slots: tokio::sync::Semaphore::new(8),
            inval: Mutex::new(None),
            pins: Mutex::new(pins),
            upload_wake: tokio::sync::Notify::new(),
            pin_wake: tokio::sync::Notify::new(),
            http: reqwest::Client::builder()
                .connect_timeout(Duration::from_secs(10))
                .build()
                .unwrap_or_default(),
            uid: unsafe { libc::getuid() },
            gid: unsafe { libc::getgid() },
        }
    }

    pub(crate) fn tree(&self) -> MutexGuard<'_, Tree> {
        self.tree.lock().unwrap_or_else(|e| e.into_inner())
    }

    fn handles(&self) -> MutexGuard<'_, HashMap<u64, Handle>> {
        self.handles.lock().unwrap_or_else(|e| e.into_inner())
    }

    pub(crate) fn is_online(&self) -> bool {
        self.online.load(Ordering::SeqCst)
    }

    pub(crate) fn uri_of(&self, path: &str) -> Result<String, Errno> {
        let mut uri = CrUri::new(&self.info.remote_base).map_err(|_| Errno::EIO)?;
        if !path.is_empty() {
            uri.join(&path.split('/').collect::<Vec<_>>());
        }
        Ok(uri.to_string())
    }

    fn invalidate(&self, ino: u64) {
        if let Some(tx) = self.inval.lock().unwrap_or_else(|e| e.into_inner()).as_ref() {
            let _ = tx.send(ino);
        }
    }

    pub(crate) fn is_pinned(&self, path: &str) -> bool {
        self.pins
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .iter()
            .any(|pin| names::is_below(path, pin))
    }

    // ---------------------------------------------------------------- connection

    /// A request failed: find out whether the server can be reached at all. Offline, requests
    /// that need the server fail at once until a probe every 30 s succeeds.
    pub(crate) fn note_failure(self: &Arc<Self>, errno: Errno) {
        if errno != Errno::ENETUNREACH || self.probing.swap(true, Ordering::SeqCst) {
            return;
        }
        let drive = self.clone();
        self.rt.spawn(async move {
            loop {
                if drive.ping().await {
                    let was_offline = !drive.online.swap(true, Ordering::SeqCst);
                    if was_offline {
                        tracing::info!(target: "vfs", drive = %drive.info.id, "Drive is online again");
                        drive.upload_wake.notify_one();
                        drive.pin_wake.notify_one();
                    }
                    break;
                }
                if drive.online.swap(false, Ordering::SeqCst) {
                    tracing::info!(target: "vfs", drive = %drive.info.id, "Drive is offline");
                }
                if drive.stopping.load(Ordering::SeqCst) {
                    break;
                }
                tokio::time::sleep(Duration::from_secs(30)).await;
            }
            drive.probing.store(false, Ordering::SeqCst);
        });
    }

    async fn ping(&self) -> bool {
        let url = format!("{}/api/v4/site/ping", self.info.instance_url.trim_end_matches('/'));
        matches!(
            tokio::time::timeout(Duration::from_secs(5), self.http.get(url).send()).await,
            Ok(Ok(response)) if response.status().is_success()
        )
    }

    fn api_failed(self: &Arc<Self>, error: &ApiError) -> Errno {
        let errno = errno_of(error);
        tracing::info!(target: "vfs", error = %error, errno = ?errno, "Server request failed");
        self.note_failure(errno);
        errno
    }

    // ---------------------------------------------------------------- attributes

    pub(crate) fn attr_of(&self, ino: u64, node: &Node) -> FileAttr {
        let read_only = self.read_only.load(Ordering::SeqCst);
        let perm = match (node.is_dir, read_only) {
            (true, false) => 0o755,
            (true, true) => 0o555,
            (false, false) => 0o644,
            (false, true) => 0o444,
        };
        FileAttr {
            ino: INodeNo(ino),
            size: node.size,
            blocks: node.size.div_ceil(512),
            atime: node.mtime,
            mtime: node.mtime,
            ctime: node.mtime,
            crtime: node.mtime,
            kind: if node.is_dir { FileType::Directory } else { FileType::RegularFile },
            perm,
            nlink: if node.is_dir { 2 } else { 1 },
            uid: self.uid,
            gid: self.gid,
            rdev: 0,
            flags: 0,
            blksize: 4096,
        }
    }

    pub(crate) fn getattr(&self, ino: u64) -> Result<FileAttr, Errno> {
        let tree = self.tree();
        let node = tree.nodes.get(&ino).ok_or(Errno::ENOENT)?;
        Ok(self.attr_of(ino, node))
    }

    /// Offline, only what can be read is shown: folders, changed files and complete copies.
    fn visible(&self, node: &Node) -> bool {
        if self.is_online() || node.is_dir || node.change.is_some() {
            return true;
        }
        node.remote.as_ref().is_some_and(|remote| {
            self.contents
                .get(&clean_key(&remote.id, &remote.version))
                .is_some_and(|cell| cell.lock().is_complete())
        })
    }

    // ---------------------------------------------------------------- listings

    /// Make sure folder `ino` has a listing: the one in memory, else the stored one (both
    /// refreshed in the background when old), else from the server. Scanners get no folder
    /// that was never listed.
    pub(crate) async fn ensure_listing(self: &Arc<Self>, ino: u64, scanner: bool) -> Result<(), Errno> {
        let (state, path) = {
            let tree = self.tree();
            let node = tree.nodes.get(&ino).ok_or(Errno::ENOENT)?;
            if !node.is_dir {
                return Err(Errno::ENOTDIR);
            }
            let state = node.listing.as_ref().map(|l| (l.complete, l.fresh_at.is_some_and(|at| at.elapsed() < FRESH)));
            (state, tree.path(ino).ok_or(Errno::ENOENT)?)
        };
        match state {
            Some((true, true)) => return Ok(()),
            Some((true, false)) => {
                self.refresh_in_background(ino);
                return Ok(());
            }
            // Made up at start: what is known is all there is offline (or for a scanner).
            Some((false, _)) if scanner || !self.is_online() => return Ok(()),
            Some((false, _)) => return self.refresh(ino).await,
            None => {}
        }
        if self.load_stored_listing(ino, &path) {
            self.refresh_in_background(ino);
            return Ok(());
        }
        if scanner {
            return Err(Errno::EACCES);
        }
        if !self.is_online() {
            return Err(Errno::ENETUNREACH);
        }
        self.refresh(ino).await
    }

    /// Take the stored listing of a folder that has none in memory (no server request).
    pub(crate) fn load_stored_listing(&self, ino: u64, path: &str) -> bool {
        match self.store.load_listing(path) {
            Ok(Some(rows)) => {
                let files: Vec<Listed> = rows.into_iter().map(Listed::from_row).collect();
                self.apply_listing(ino, files, None, None);
                true
            }
            _ => false,
        }
    }

    fn refresh_in_background(self: &Arc<Self>, ino: u64) {
        if !self.is_online() {
            return;
        }
        let drive = self.clone();
        self.rt.spawn(async move {
            let _ = drive.refresh(ino).await;
        });
    }

    fn list_lock(&self, ino: u64) -> Arc<tokio::sync::Mutex<()>> {
        self.list_locks
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .entry(ino)
            .or_default()
            .clone()
    }

    /// List folder `ino` on the server (one request per folder at a time), then its subfolders
    /// in the background, so opening one of them is instant.
    pub(crate) async fn refresh(self: &Arc<Self>, ino: u64) -> Result<(), Errno> {
        self.refresh_one(ino).await?;
        self.prefetch_children(ino);
        Ok(())
    }

    /// List up to 16 subfolders of `ino` that were never listed (bounded by the request slots).
    fn prefetch_children(self: &Arc<Self>, ino: u64) {
        const PREFETCH: usize = 16;
        let children: Vec<u64> = {
            let tree = self.tree();
            let Some(listing) = tree.nodes.get(&ino).and_then(|n| n.listing.as_ref()) else { return };
            listing
                .children
                .values()
                .filter(|child| tree.nodes.get(child).is_some_and(|n| n.is_dir && n.listing.is_none()))
                .take(PREFETCH)
                .copied()
                .collect()
        };
        for child in children {
            let drive = self.clone();
            self.rt.spawn(async move {
                let path = drive.tree().path(child);
                let Some(path) = path else { return };
                if !drive.load_stored_listing(child, &path) {
                    let _ = drive.refresh_one(child).await;
                }
            });
        }
    }

    async fn refresh_one(self: &Arc<Self>, ino: u64) -> Result<(), Errno> {
        let lock = self.list_lock(ino);
        let _one = lock.lock().await;
        let (path, generation) = {
            let tree = self.tree();
            let node = tree.nodes.get(&ino).ok_or(Errno::ENOENT)?;
            if node.listing.as_ref().and_then(|l| l.fresh_at).is_some_and(|at| at.elapsed() < Duration::from_secs(2)) {
                return Ok(());
            }
            (tree.path(ino).ok_or(Errno::ENOENT)?, node.listing.as_ref().map(|l| l.generation))
        };
        let uri = self.uri_of(&path)?;
        let _slot = self.meta_slots.acquire().await.map_err(|_| Errno::EIO)?;
        let mut files = Vec::new();
        let mut previous = None;
        let mut capability = None;
        loop {
            let page = match self.client.list_files_all(previous.as_ref(), &uri, 1000).await {
                Ok(page) => page,
                Err(e) => {
                    let errno = self.api_failed(&e);
                    if errno == Errno::ENOENT {
                        self.mark_parent_stale(ino);
                    }
                    return Err(errno);
                }
            };
            if capability.is_none() {
                capability = page.res.props.capability.clone();
            }
            files.extend(page.res.files.iter().map(Listed::from_response));
            let more = page.more;
            previous = Some(page);
            if !more {
                break;
            }
        }
        if ino == ROOT {
            if let Some(read_only) = crate::drive::guard::is_read_only_capability(capability.as_deref()) {
                self.read_only.store(read_only, Ordering::SeqCst);
            }
        }
        let rows: Vec<EntryRow> = files.iter().map(Listed::to_row).collect();
        if !self.apply_listing(ino, files, Some(Instant::now()), generation) {
            tracing::debug!(target: "vfs", path = %path, "A listing came after a local change, asked again later");
            return Ok(());
        }
        if let Err(e) = self.store.save_listing(&path, &rows, now_secs()) {
            tracing::warn!(target: "vfs", error = %e, "Failed to store a listing");
        }
        Ok(())
    }

    fn mark_parent_stale(&self, ino: u64) {
        let mut tree = self.tree();
        let parent = tree.nodes.get(&ino).map(|n| n.parent).unwrap_or(0);
        if let Some(listing) = tree.nodes.get_mut(&parent).and_then(|n| n.listing.as_mut()) {
            listing.fresh_at = None;
        }
    }

    /// Mark the folder at `path` (and its parent) as old, after a server event.
    pub(crate) fn mark_stale(&self, path: &str) {
        let mut tree = self.tree();
        for folder in [path, names::parent_of(path)] {
            if let Some(ino) = Self::find(&tree, folder) {
                if let Some(listing) = tree.nodes.get_mut(&ino).and_then(|n| n.listing.as_mut()) {
                    listing.fresh_at = None;
                }
            }
        }
    }

    pub(crate) fn mark_all_stale(&self) {
        let mut tree = self.tree();
        for node in tree.nodes.values_mut() {
            if let Some(listing) = node.listing.as_mut() {
                listing.fresh_at = None;
            }
        }
    }

    /// The node at `path` among the folders the tree has listed.
    pub(crate) fn find(tree: &Tree, path: &str) -> Option<u64> {
        let mut ino = ROOT;
        if path.is_empty() {
            return Some(ino);
        }
        for part in path.split('/') {
            ino = tree.child(ino, part)?;
        }
        Some(ino)
    }

    /// Merge a server listing into the tree. Local changes and files being uploaded keep their
    /// own size and date and are never removed by a listing. `generation`: the folder's
    /// generation when the listing was asked for (`None`: the folder had no listing); if a
    /// local change came in between, nothing is applied and `false` returned.
    fn apply_listing(&self, ino: u64, files: Vec<Listed>, fresh_at: Option<Instant>, generation: Option<u64>) -> bool {
        let mut changed_versions = Vec::new();
        let mut tree = self.tree();
        let (old, next_generation) = match tree.nodes.get_mut(&ino) {
            Some(node) => {
                let current = node.listing.as_ref().map(|l| l.generation);
                if current.is_some() && current != generation {
                    if let Some(listing) = node.listing.as_mut() {
                        listing.fresh_at = None;
                    }
                    return false;
                }
                let generation = current.unwrap_or(0);
                (node.listing.take().map(|l| l.children).unwrap_or_default(), generation)
            }
            None => return false,
        };
        let mut children = BTreeMap::new();
        for file in files {
            if names::is_trash_name(&file.name) && ino == ROOT {
                continue;
            }
            let existing = old.get(&file.name).copied().filter(|child| {
                tree.nodes.get(child).is_some_and(|n| n.is_dir == file.is_dir)
            });
            let remote = Remote { id: file.id.clone(), version: file.version.clone() };
            match existing {
                Some(child) => {
                    let node = tree.nodes.get_mut(&child).expect("child exists");
                    if node.change.is_none() && !node.uploading {
                        if !node.is_dir && node.remote.as_ref() != Some(&remote) {
                            changed_versions.push(child);
                        }
                        node.remote = Some(remote);
                        node.size = file.size;
                        node.mtime = file.mtime;
                    } else if node.remote.is_none() && node.is_dir {
                        node.remote = Some(remote);
                    }
                    children.insert(file.name, child);
                }
                None => {
                    let mut node = Node::new(ino, &file.name, file.is_dir);
                    node.remote = Some(remote);
                    node.size = file.size;
                    node.mtime = file.mtime;
                    let name = file.name.clone();
                    let new_ino = tree.next;
                    tree.next += 1;
                    tree.nodes.insert(new_ino, node);
                    children.insert(name, new_ino);
                }
            }
        }
        // Gone from the server: kept only while it holds a local change or uploads (and the name
        // is still free; a file replaced by a folder on the server gives way).
        for (name, child) in old {
            if children.get(&name) == Some(&child) {
                continue;
            }
            let keep = !children.contains_key(&name)
                && tree.nodes.get(&child).is_some_and(|n| n.change.is_some() || n.uploading);
            if keep {
                children.insert(name, child);
            } else if let Some(node) = tree.nodes.get_mut(&child) {
                node.parent = 0;
            }
        }
        if let Some(node) = tree.nodes.get_mut(&ino) {
            node.listing = Some(Listing { fresh_at, children, generation: next_generation, complete: true });
        }
        drop(tree);
        for child in changed_versions {
            self.invalidate(child);
        }
        true
    }

    /// Make the nodes along `path` known (from the store or the server). Returns the node.
    pub(crate) async fn resolve(self: &Arc<Self>, path: &str) -> Result<u64, Errno> {
        let mut ino = ROOT;
        if path.is_empty() {
            return Ok(ino);
        }
        for part in path.split('/') {
            self.ensure_listing(ino, false).await?;
            ino = self.tree().child(ino, part).ok_or(Errno::ENOENT)?;
        }
        Ok(ino)
    }

    pub(crate) async fn lookup(self: &Arc<Self>, parent: u64, name: &str, scanner: bool) -> Result<FileAttr, Errno> {
        self.ensure_listing(parent, scanner).await?;
        let tree = self.tree();
        let ino = tree.child(parent, name).ok_or(Errno::ENOENT)?;
        let node = tree.nodes.get(&ino).ok_or(Errno::ENOENT)?;
        if !self.visible(node) {
            return Err(Errno::ENOENT);
        }
        Ok(self.attr_of(ino, node))
    }

    /// A lookup answered from a listing in memory, `None` if the folder must be listed first.
    pub(crate) fn lookup_now(self: &Arc<Self>, parent: u64, name: &str) -> Option<Result<FileAttr, Errno>> {
        if !self.listed_now(parent) {
            return None;
        }
        let tree = self.tree();
        let answer = match tree.child(parent, name).and_then(|ino| Some((ino, tree.nodes.get(&ino)?))) {
            Some((ino, node)) if self.visible(node) => Ok(self.attr_of(ino, node)),
            _ => Err(Errno::ENOENT),
        };
        Some(answer)
    }

    /// The entries of a folder listed in memory, `None` if it must be listed first.
    pub(crate) fn readdir_now(self: &Arc<Self>, ino: u64) -> Option<Vec<(u64, FileType, String)>> {
        if !self.listed_now(ino) {
            return None;
        }
        Some(self.entries(ino))
    }

    /// The entries of a folder with their attributes (`readdirplus`).
    pub(crate) fn entries_plus(&self, ino: u64) -> Vec<(String, FileAttr)> {
        let tree = self.tree();
        let Some(listing) = tree.nodes.get(&ino).and_then(|n| n.listing.as_ref()) else {
            return Vec::new();
        };
        listing
            .children
            .iter()
            .filter_map(|(name, child)| {
                let node = tree.nodes.get(child)?;
                self.visible(node).then(|| (name.clone(), self.attr_of(*child, node)))
            })
            .collect()
    }

    /// Whether folder `ino` has a listing in memory (old ones are refreshed in the background).
    pub(crate) fn listed_now(self: &Arc<Self>, ino: u64) -> bool {
        let stale = {
            let tree = self.tree();
            let Some(listing) = tree.nodes.get(&ino).and_then(|n| n.listing.as_ref()) else { return false };
            if !listing.complete {
                return false;
            }
            listing.fresh_at.is_none_or(|at| at.elapsed() >= FRESH)
        };
        if stale {
            self.refresh_in_background(ino);
        }
        true
    }

    fn entries(&self, ino: u64) -> Vec<(u64, FileType, String)> {
        let tree = self.tree();
        let Some(listing) = tree.nodes.get(&ino).and_then(|n| n.listing.as_ref()) else {
            return Vec::new();
        };
        listing
            .children
            .iter()
            .filter_map(|(name, child)| {
                let node = tree.nodes.get(child)?;
                self.visible(node).then(|| {
                    (*child, if node.is_dir { FileType::Directory } else { FileType::RegularFile }, name.clone())
                })
            })
            .collect()
    }

    /// The entries of folder `ino` (without "." and "..").
    pub(crate) async fn readdir(self: &Arc<Self>, ino: u64, scanner: bool) -> Result<Vec<(u64, FileType, String)>, Errno> {
        self.ensure_listing(ino, scanner).await?;
        Ok(self.entries(ino))
    }

    // ---------------------------------------------------------------- content

    /// The content of a file for reading or writing: its local change, or the cached copy of
    /// its server version. Counted as open.
    fn cell_for(&self, ino: u64) -> Result<(Arc<Cell>, String), Errno> {
        let tree = self.tree();
        let node = tree.nodes.get(&ino).ok_or(Errno::ENOENT)?;
        if node.is_dir {
            return Err(Errno::EISDIR);
        }
        let path = tree.path(ino).unwrap_or_default();
        if let Some(change) = &node.change {
            change.cell.lock().open += 1;
            return Ok((change.cell.clone(), path));
        }
        let remote = node.remote.as_ref().ok_or(Errno::EIO)?;
        let cell = self
            .contents
            .clean(&remote.id, &remote.version, node.size, &path, true)
            .map_err(|e| {
                tracing::warn!(target: "vfs", error = %e, "Failed to create a cache file");
                Errno::EIO
            })?;
        Ok((cell, path))
    }

    fn release_cell(&self, cell: &Arc<Cell>) {
        let mut state = cell.lock();
        state.open = state.open.saturating_sub(1);
        state.last_used = now_secs();
        if !state.dropped && state.open == 0 && !state.changed && state.loaded_bytes() > 0 {
            let _ = self.store.save_content(&state.row());
        }
    }

    /// Open a file. Returns the handle and whether the kernel may keep its page cache.
    pub(crate) fn open(&self, ino: u64, write: bool, scanner: bool) -> Result<(u64, bool), Errno> {
        if self.stopping.load(Ordering::SeqCst) {
            return Err(Errno::EIO);
        }
        if write && self.read_only.load(Ordering::SeqCst) {
            return Err(Errno::EROFS);
        }
        let (cell, _path) = self.cell_for(ino)?;
        let (complete, key) = {
            let state = cell.lock();
            (state.is_complete(), state.key.clone())
        };
        if !complete && (scanner || !self.is_online()) {
            self.release_cell(&cell);
            return Err(if scanner { Errno::EACCES } else { Errno::ENETUNREACH });
        }
        let keep_cache = {
            let mut tree = self.tree();
            let node = tree.nodes.get_mut(&ino).ok_or(Errno::ENOENT)?;
            let keep = node.change.is_none() && node.last_key.as_deref() == Some(key.as_str());
            node.last_key = Some(key);
            if write {
                node.writers += 1;
                if let Some(change) = node.change.as_mut() {
                    change.due = None;
                }
            }
            keep
        };
        let fh = self.next_fh.fetch_add(1, Ordering::SeqCst);
        self.handles().insert(fh, Handle { ino, cell, write });
        Ok((fh, keep_cache))
    }

    pub(crate) fn handle_cell(&self, fh: u64) -> Result<(u64, Arc<Cell>, bool), Errno> {
        let handles = self.handles();
        let handle = handles.get(&fh).ok_or(Errno::EBADF)?;
        Ok((handle.ino, handle.cell.clone(), handle.write))
    }

    /// The chunks a read needs from the server (empty: answer from the file now).
    pub(crate) fn read_needs(&self, cell: &Cell, offset: u64, size: u32) -> bool {
        !cell.lock().missing_runs(offset, offset + size as u64).is_empty()
    }

    pub(crate) async fn read(self: &Arc<Self>, ino: u64, cell: Arc<Cell>, offset: u64, size: u32) -> Result<Vec<u8>, Errno> {
        let path = self.tree().path(ino).unwrap_or_else(|| cell.lock().path.clone());
        // Only what this read needs first; the rest follows while the program works.
        self.ensure_range(&cell, &path, offset, offset + size as u64).await?;
        self.read_ahead(ino, &cell, offset + size as u64);
        cell.read_at(offset, size).map_err(Errno::from)
    }

    /// Load the next part of a file in the background when a read comes near the end of what
    /// is loaded (keeps a video or a copy streaming without waits).
    pub(crate) fn read_ahead(self: &Arc<Self>, ino: u64, cell: &Arc<Cell>, from: u64) {
        if !self.is_online() || cell.lock().missing_runs(from, from + READ_AHEAD).is_empty() {
            return;
        }
        // One loader per file: if one runs, it is already ahead.
        if cell.fetch.try_lock().is_err() {
            return;
        }
        let drive = self.clone();
        let cell = cell.clone();
        self.rt.spawn(async move {
            let path = drive.tree().path(ino).unwrap_or_else(|| cell.lock().path.clone());
            let _ = drive.ensure_range(&cell, &path, from, from + READ_AHEAD).await;
        });
    }

    fn download_url_cached(&self, key: &str) -> Option<String> {
        let urls = self.urls.lock().unwrap_or_else(|e| e.into_inner());
        urls.get(key).filter(|(_, until)| Instant::now() < *until).map(|(url, _)| url.clone())
    }

    async fn download_url(self: &Arc<Self>, key: &str, uri: &str, version: &str) -> Result<String, Errno> {
        if let Some(url) = self.download_url_cached(key) {
            return Ok(url);
        }
        let mut request = FileURLService::default();
        request.uris.push(uri.to_string());
        if !version.is_empty() {
            request.entity = Some(version.to_string());
        }
        let answer = self.client.get_file_url(&request).await.map_err(|e| self.api_failed(&e))?;
        let url = answer.urls.first().map(|u| u.url.clone()).ok_or(Errno::EIO)?;
        let until = chrono::DateTime::parse_from_rfc3339(&answer.expires)
            .ok()
            .and_then(|t| (t.with_timezone(&chrono::Utc) - chrono::Utc::now()).to_std().ok())
            .map(|left| Instant::now() + left.saturating_sub(Duration::from_secs(30)))
            .unwrap_or_else(|| Instant::now() + Duration::from_secs(300));
        self.urls
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .insert(key.to_string(), (url.clone(), until));
        Ok(url)
    }

    /// Load the missing chunks of bytes `start..end` of `cell` from the server version it
    /// belongs to.
    pub(crate) async fn ensure_range(self: &Arc<Self>, cell: &Arc<Cell>, path: &str, start: u64, end: u64) -> Result<(), Errno> {
        if cell.lock().missing_runs(start, end).is_empty() {
            return Ok(());
        }
        let _one = cell.fetch.lock().await;
        let (runs, key, version, remote_len) = {
            let state = cell.lock();
            (state.missing_runs(start, end), state.key.clone(), state.version.clone(), state.remote_len)
        };
        if runs.is_empty() {
            return Ok(());
        }
        if !self.is_online() {
            return Err(Errno::ENETUNREACH);
        }
        let needed: u64 = runs.iter().map(|(a, b)| (b - a) * CHUNK).sum();
        super::make_room(needed)?;
        if super::free_space(&self.info.data_dir) < DISK_RESERVE + needed {
            return Err(Errno::ENOSPC);
        }
        let _slot = self.fetch_slots.acquire().await.map_err(|_| Errno::EIO)?;
        let uri = self.uri_of(path)?;
        for (first, last) in runs {
            let from = first * CHUNK;
            let to = (last * CHUNK).min(remote_len);
            let mut result = self.fetch_range(cell, &key, &uri, &version, from, to, remote_len).await;
            if result.is_err() && self.is_online() {
                // An expired link: once more with a new one.
                self.urls.lock().unwrap_or_else(|e| e.into_inner()).remove(&key);
                result = self.fetch_range(cell, &key, &uri, &version, from, to, remote_len).await;
            }
            result?;
        }
        let state = cell.lock();
        if !state.dropped {
            if let Err(e) = self.store.save_content(&state.row()) {
                tracing::warn!(target: "vfs", error = %e, "Failed to record cached chunks");
            }
        }
        Ok(())
    }

    #[allow(clippy::too_many_arguments)]
    async fn fetch_range(
        self: &Arc<Self>,
        cell: &Arc<Cell>,
        key: &str,
        uri: &str,
        version: &str,
        from: u64,
        to: u64,
        total: u64,
    ) -> Result<(), Errno> {
        use futures::StreamExt;
        let url = self.download_url(key, uri, version).await?;
        let request = self.http.get(&url).header("Range", format!("bytes={}-{}", from, to - 1));
        let response = match tokio::time::timeout(STALL, request.send()).await {
            Ok(Ok(response)) => response,
            Ok(Err(e)) => {
                tracing::warn!(target: "vfs", error = %e, "Download failed");
                self.note_failure(Errno::ENETUNREACH);
                return Err(Errno::ENETUNREACH);
            }
            Err(_) => return Err(Errno::EIO),
        };
        let status = response.status().as_u16();
        let header = response
            .headers()
            .get("content-range")
            .and_then(|v| v.to_str().ok())
            .unwrap_or("")
            .to_string();
        let whole = status == 200 && from == 0 && to == total;
        if !(status == 206 && content_range_matches(&header, from, to - 1, total)) && !whole {
            tracing::warn!(target: "vfs", status, header = %header, "Unexpected answer to a range request");
            return Err(Errno::EIO);
        }
        let mut stream = response.bytes_stream();
        let mut offset = from;
        let mut pending: Vec<u8> = Vec::with_capacity(CHUNK as usize);
        loop {
            let next = tokio::time::timeout(STALL, stream.next()).await.map_err(|_| Errno::EIO)?;
            let Some(piece) = next else { break };
            let piece = piece.map_err(|_| Errno::EIO)?;
            pending.extend_from_slice(&piece);
            // Whole chunks are written as they arrive (each only if still missing).
            while pending.len() as u64 >= CHUNK || (offset + pending.len() as u64 >= to && !pending.is_empty()) {
                let take = (CHUNK as usize).min(pending.len()).min((to - offset) as usize);
                if take == 0 {
                    break;
                }
                let rest = pending.split_off(take);
                self.store_chunk(cell, offset, &pending)?;
                offset += take as u64;
                pending = rest;
            }
            if offset >= to {
                break;
            }
        }
        if offset != to {
            tracing::warn!(target: "vfs", got = offset - from, wanted = to - from, "A download ended early");
            return Err(Errno::EIO);
        }
        Ok(())
    }

    /// Write one chunk loaded from the server, unless a local write filled it meanwhile.
    fn store_chunk(&self, cell: &Cell, offset: u64, data: &[u8]) -> Result<(), Errno> {
        use std::os::unix::fs::FileExt;
        let chunk = offset / CHUNK;
        let mut state = cell.lock();
        if state.dropped || state.chunks.get(chunk) || offset >= state.remote_len {
            return Ok(());
        }
        let len = (data.len() as u64).min(state.remote_len - offset) as usize;
        cell.file.write_all_at(&data[..len], offset).map_err(Errno::from)?;
        state.chunks.set(chunk);
        Ok(())
    }

    // ---------------------------------------------------------------- local changes

    fn change_row(path: &str, node: &Node, change: &Change) -> ChangeRow {
        ChangeRow {
            path: path.to_string(),
            key: change.cell.key(),
            file_id: node.remote.as_ref().map(|r| r.id.clone()),
            base: node.remote.as_ref().map(|r| r.version.clone()),
            local_only: change.local_only as i32,
            open: (node.writers > 0) as i32,
            size: node.size as i64,
            mtime: unix(node.mtime),
        }
    }

    /// Record the change of node `ino` as it is now.
    pub(crate) fn persist_change(&self, ino: u64) {
        let row = {
            let tree = self.tree();
            let Some(node) = tree.nodes.get(&ino) else { return };
            let (Some(change), Some(path)) = (node.change.as_ref(), tree.path(ino)) else { return };
            Self::change_row(&path, node, change)
        };
        if let Err(e) = self.store.save_change(&row) {
            tracing::error!(target: "vfs", error = %e, path = %row.path, "Failed to record a local change");
        }
        let state = row_state(&self.contents, &row.key);
        if let Some(state) = state {
            let _ = self.store.save_content(&state);
        }
    }

    /// Make `cell` (open on `ino`) the local change of the file, recorded before the first
    /// write is answered.
    fn begin_change(&self, ino: u64, cell: &Arc<Cell>) -> Result<(), Errno> {
        {
            let tree = self.tree();
            let node = tree.nodes.get(&ino).ok_or(Errno::ENOENT)?;
            if node.change.is_some() {
                return Ok(());
            }
        }
        let old = self.contents.make_changed(cell).map_err(|e| {
            tracing::error!(target: "vfs", error = %e, "Failed to start a local change");
            Errno::EIO
        })?;
        if let Some(old) = old {
            let _ = self.store.delete_content(&old);
        }
        {
            let mut tree = self.tree();
            let path = tree.path(ino).unwrap_or_default();
            let node = tree.nodes.get_mut(&ino).ok_or(Errno::ENOENT)?;
            cell.lock().path = path;
            node.change = Some(Change {
                cell: cell.clone(),
                local_only: names::is_local_only(&node.name),
                counter: 0,
                due: None,
                failed: None,
                attempts: 0,
                recovered: false,
            });
        }
        self.persist_change(ino);
        Ok(())
    }

    /// Whether a write needs the server or a first record first (else it is done at once).
    pub(crate) fn write_needs_work(&self, ino: u64, cell: &Cell, offset: u64, len: u64) -> bool {
        let changed = self.tree().nodes.get(&ino).is_some_and(|n| n.change.is_some());
        let remote_len = cell.lock().remote_len;
        !changed || !partly_written_missing(cell, offset, offset + len, remote_len).is_empty()
    }

    /// Write to an open file (loads only the chunks the write covers partly).
    pub(crate) async fn write(self: &Arc<Self>, ino: u64, cell: Arc<Cell>, offset: u64, data: Vec<u8>) -> Result<u32, Errno> {
        let remote_len = cell.lock().remote_len;
        let end = offset + data.len() as u64;
        for chunk in partly_written_missing(&cell, offset, end, remote_len) {
            let path = self.tree().path(ino).unwrap_or_default();
            self.ensure_range(&cell, &path, chunk * CHUNK, (chunk + 1) * CHUNK).await?;
        }
        self.begin_change(ino, &cell)?;
        self.write_now(ino, &cell, offset, &data)
    }

    /// The write itself (the file is a local change and the chunks it covers partly are there).
    pub(crate) fn write_now(&self, ino: u64, cell: &Arc<Cell>, offset: u64, data: &[u8]) -> Result<u32, Errno> {
        use std::os::unix::fs::FileExt;
        if self.stopping.load(Ordering::SeqCst) {
            return Err(Errno::EIO);
        }
        if super::free_space(&self.info.data_dir) < DISK_RESERVE {
            return Err(Errno::ENOSPC);
        }
        let end = offset + data.len() as u64;
        {
            let mut state = cell.lock();
            if state.dropped {
                return Err(Errno::EIO);
            }
            cell.file.write_all_at(data, offset).map_err(Errno::from)?;
            let remote_len = state.remote_len;
            let (first, last) = super::chunks::chunks_of(offset, end.min(remote_len));
            state.chunks.set_range(first, last);
        }
        let mut tree = self.tree();
        if let Some(node) = tree.nodes.get_mut(&ino) {
            node.size = node.size.max(end);
            node.mtime = SystemTime::now();
            if let Some(change) = node.change.as_mut() {
                change.counter += 1;
            }
        }
        Ok(data.len() as u32)
    }

    /// `truncate`, `chmod`, `utimens`. Only size and mtime matter, and only for files.
    pub(crate) async fn setattr(
        self: &Arc<Self>,
        ino: u64,
        size: Option<u64>,
        mtime: Option<SystemTime>,
        fh: Option<u64>,
    ) -> Result<FileAttr, Errno> {
        let is_dir = self.tree().nodes.get(&ino).ok_or(Errno::ENOENT)?.is_dir;
        if let (Some(size), false) = (size, is_dir) {
            if self.read_only.load(Ordering::SeqCst) {
                return Err(Errno::EROFS);
            }
            let (cell, opened_here) = match fh.and_then(|fh| self.handle_cell(fh).ok()) {
                Some((_, cell, _)) => (cell, false),
                None => (self.cell_for(ino)?.0, true),
            };
            let result = self.truncate(ino, &cell, size);
            if opened_here {
                self.release_cell(&cell);
                let no_writers = self.tree().nodes.get(&ino).is_some_and(|n| n.writers == 0);
                if no_writers {
                    self.schedule_upload(ino);
                }
            }
            result?;
        }
        if let Some(mtime) = mtime {
            let mut tree = self.tree();
            if let Some(node) = tree.nodes.get_mut(&ino) {
                if node.change.is_some() {
                    node.mtime = mtime;
                }
            }
        }
        if mtime.is_some() {
            self.persist_change(ino);
        }
        self.getattr(ino)
    }

    fn truncate(&self, ino: u64, cell: &Arc<Cell>, size: u64) -> Result<(), Errno> {
        if self.stopping.load(Ordering::SeqCst) {
            return Err(Errno::EIO);
        }
        self.begin_change(ino, cell)?;
        {
            let mut state = cell.lock();
            cell.file.set_len(size).map_err(Errno::from)?;
            state.remote_len = state.remote_len.min(size);
        }
        {
            let mut tree = self.tree();
            if let Some(node) = tree.nodes.get_mut(&ino) {
                node.size = size;
                node.mtime = SystemTime::now();
                if let Some(change) = node.change.as_mut() {
                    change.counter += 1;
                }
            }
        }
        self.persist_change(ino);
        Ok(())
    }

    /// Plan the upload of a closed change.
    pub(crate) fn schedule_upload(&self, ino: u64) {
        {
            let mut tree = self.tree();
            let Some(node) = tree.nodes.get_mut(&ino) else { return };
            if node.writers > 0 {
                return;
            }
            let Some(change) = node.change.as_mut() else { return };
            change.due = Some(Instant::now() + UPLOAD_DELAY);
            change.attempts = 0;
            change.failed = None;
        }
        self.persist_change(ino);
        self.upload_wake.notify_one();
    }

    pub(crate) fn release(&self, fh: u64) {
        let Some(handle) = self.handles().remove(&fh) else { return };
        self.release_cell(&handle.cell);
        // A file read while open may have passed the limit; it can go now.
        let _ = super::make_room(0);
        if !handle.write {
            return;
        }
        let closed = {
            let mut tree = self.tree();
            match tree.nodes.get_mut(&handle.ino) {
                Some(node) => {
                    node.writers = node.writers.saturating_sub(1);
                    node.writers == 0 && node.change.is_some()
                }
                None => false,
            }
        };
        if closed {
            self.schedule_upload(handle.ino);
        }
    }

    pub(crate) fn fsync(&self, ino: u64, fh: u64) -> Result<(), Errno> {
        let (_, cell, _) = self.handle_cell(fh)?;
        cell.file.sync_all().map_err(Errno::from)?;
        self.persist_change(ino);
        Ok(())
    }

    /// A new file (only local until it is closed and uploaded).
    pub(crate) async fn create(self: &Arc<Self>, parent: u64, name: &str) -> Result<(FileAttr, u64), Errno> {
        self.check_writable()?;
        if parent == ROOT && names::is_trash_name(name) {
            return Err(Errno::EPERM);
        }
        self.ensure_listing(parent, false).await?;
        let path = {
            let tree = self.tree();
            if tree.child(parent, name).is_some() {
                return Err(Errno::EEXIST);
            }
            names::join(&tree.path(parent).ok_or(Errno::ENOENT)?, name)
        };
        let cell = self.contents.new_local(&path).map_err(|_| Errno::EIO)?;
        cell.lock().open = 1;
        let ino = {
            let mut tree = self.tree();
            if tree.child(parent, name).is_some() {
                drop(tree);
                self.contents.remove(&cell);
                return Err(Errno::EEXIST);
            }
            let mut node = Node::new(parent, name, false);
            node.writers = 1;
            node.change = Some(Change {
                cell: cell.clone(),
                local_only: names::is_local_only(name),
                counter: 0,
                due: None,
                failed: None,
                attempts: 0,
                recovered: false,
            });
            tree.add(parent, node)
        };
        self.persist_change(ino);
        let fh = self.next_fh.fetch_add(1, Ordering::SeqCst);
        self.handles().insert(fh, Handle { ino, cell, write: true });
        Ok((self.getattr(ino)?, fh))
    }

    fn check_writable(&self) -> Result<(), Errno> {
        if self.stopping.load(Ordering::SeqCst) {
            return Err(Errno::EIO);
        }
        if self.read_only.load(Ordering::SeqCst) {
            return Err(Errno::EROFS);
        }
        Ok(())
    }

    // ---------------------------------------------------------------- server tree changes

    fn check_online(&self) -> Result<(), Errno> {
        if self.is_online() { Ok(()) } else { Err(Errno::ENETUNREACH) }
    }

    /// The server's file at `path` must still be the one the tree knows (else someone replaced
    /// it meanwhile; the folder is listed again).
    async fn check_same(self: &Arc<Self>, path: &str, remote: &Remote, parent: u64) -> Result<String, Errno> {
        let uri = self.uri_of(path)?;
        let info = self
            .client
            .get_file_info(&GetFileInfoService { uri: Some(uri.clone()), id: None, extended: None, folder_summary: None })
            .await
            .map_err(|e| self.api_failed(&e))?;
        if info.id != remote.id {
            tracing::info!(target: "vfs", path = %path, "The server has another file at this path now");
            self.mark_stale(&self.tree().path(parent).unwrap_or_default());
            return Err(Errno::ENOENT);
        }
        Ok(uri)
    }

    /// Wait (up to `limit`) until the upload of `ino` has finished.
    async fn wait_for_upload(&self, ino: u64, limit: Duration) -> Result<(), Errno> {
        let until = Instant::now() + limit;
        loop {
            let uploading = self.tree().nodes.get(&ino).is_some_and(|n| n.uploading);
            if !uploading {
                return Ok(());
            }
            if Instant::now() > until {
                return Err(Errno::EBUSY);
            }
            tokio::time::sleep(Duration::from_millis(200)).await;
        }
    }

    pub(crate) async fn mkdir(self: &Arc<Self>, parent: u64, name: &str, expired: impl Fn() -> bool) -> Result<FileAttr, Errno> {
        self.check_writable()?;
        if parent == ROOT && names::is_trash_name(name) {
            return Err(Errno::EPERM);
        }
        self.ensure_listing(parent, false).await?;
        self.check_online()?;
        let path = {
            let tree = self.tree();
            if tree.child(parent, name).is_some() {
                return Err(Errno::EEXIST);
            }
            names::join(&tree.path(parent).ok_or(Errno::ENOENT)?, name)
        };
        if expired() {
            return Err(Errno::EIO);
        }
        let uri = self.uri_of(&path)?;
        let file = self
            .client
            .create_file(&CreateFileService { uri, file_type: "folder".into(), err_on_conflict: Some(true), metadata: None })
            .await
            .map_err(|e| self.api_failed(&e))?;
        let ino = {
            let mut tree = self.tree();
            let mut node = Node::new(parent, name, true);
            node.remote = Some(Remote { id: file.id.clone(), version: String::new() });
            node.mtime = parse_time(&file.updated_at);
            node.listing = Some(Listing {
                fresh_at: Some(Instant::now()),
                children: BTreeMap::new(),
                generation: 0,
                complete: true,
            });
            tree.add(parent, node)
        };
        self.getattr(ino)
    }

    pub(crate) async fn unlink(self: &Arc<Self>, parent: u64, name: &str, dir: bool, expired: impl Fn() -> bool) -> Result<(), Errno> {
        self.check_writable()?;
        self.ensure_listing(parent, false).await?;
        let ino = self.tree().child(parent, name).ok_or(Errno::ENOENT)?;
        self.wait_for_upload(ino, Duration::from_secs(15)).await?;
        let (path, remote, is_dir) = {
            let tree = self.tree();
            let node = tree.nodes.get(&ino).ok_or(Errno::ENOENT)?;
            (tree.path(ino).ok_or(Errno::ENOENT)?, node.remote.clone(), node.is_dir)
        };
        if dir != is_dir {
            return Err(if dir { Errno::ENOTDIR } else { Errno::EISDIR });
        }
        if is_dir {
            self.ensure_listing(ino, false).await?;
            let empty = self.tree().nodes.get(&ino).and_then(|n| n.listing.as_ref()).is_some_and(|l| l.children.is_empty());
            if !empty {
                return Err(Errno::ENOTEMPTY);
            }
        }
        if let Some(remote) = &remote {
            self.check_online()?;
            if expired() {
                return Err(Errno::EIO);
            }
            let uri = self.check_same(&path, remote, parent).await?;
            self.client
                .delete_files(&DeleteFileService { uris: vec![uri], unlink: None, skip_soft_delete: None })
                .await
                .map_err(|e| self.api_failed(&e))?;
        }
        let change = {
            let mut tree = self.tree();
            tree.detach(ino);
            tree.nodes.get_mut(&ino).and_then(|n| n.change.take())
        };
        if let Some(change) = change {
            let _ = self.store.delete_change(&path);
            self.contents.remove(&change.cell);
            let _ = self.store.delete_content(&change.cell.key());
        }
        let _ = self.store.forget_tree(&path);
        self.mark_stale(names::parent_of(&path));
        Ok(())
    }

    pub(crate) async fn rename(
        self: &Arc<Self>,
        parent: u64,
        name: &str,
        new_parent: u64,
        new_name: &str,
        no_replace: bool,
        expired: impl Fn() -> bool,
    ) -> Result<(), Errno> {
        self.check_writable()?;
        if new_parent == ROOT && names::is_trash_name(new_name) {
            return Err(Errno::EPERM);
        }
        self.ensure_listing(parent, false).await?;
        self.ensure_listing(new_parent, false).await?;
        let ino = self.tree().child(parent, name).ok_or(Errno::ENOENT)?;
        self.wait_for_upload(ino, Duration::from_secs(15)).await?;
        let (old_path, new_path, remote, is_dir, target) = {
            let tree = self.tree();
            let node = tree.nodes.get(&ino).ok_or(Errno::ENOENT)?;
            let target = tree.child(new_parent, new_name).filter(|t| *t != ino);
            (
                tree.path(ino).ok_or(Errno::ENOENT)?,
                names::join(&tree.path(new_parent).ok_or(Errno::ENOENT)?, new_name),
                node.remote.clone(),
                node.is_dir,
                target,
            )
        };
        if old_path == new_path {
            return Ok(());
        }
        // A save right after the last one: the file it replaces may still be uploading.
        if let Some(target) = target {
            self.wait_for_upload(target, Duration::from_secs(15)).await?;
        }
        if is_dir && names::is_below(&new_path, &old_path) {
            return Err(Errno::EINVAL);
        }
        if remote.is_none() {
            // Case 1: only here so far (new file or temp name): a local rename. Over a server
            // file it gives that file new content.
            return self.rename_local(ino, new_parent, new_name, &old_path, &new_path, target, no_replace);
        }
        if target.is_some() {
            return Err(Errno::EEXIST);
        }
        // Case 2: on the server first.
        self.check_online()?;
        let same_folder = parent == new_parent;
        if !same_folder && name != new_name {
            return Err(Errno::EXDEV);
        }
        if expired() {
            return Err(Errno::EIO);
        }
        let remote = remote.expect("checked");
        let uri = self.check_same(&old_path, &remote, parent).await?;
        let result = if same_folder {
            self.client
                .rename_file(&RenameFileService { uri: uri.clone(), new_name: new_name.to_string() })
                .await
                .map(|_| ())
        } else {
            let dst = self.uri_of(names::parent_of(&new_path))?;
            self.client
                .move_files(&MoveFileService { uris: vec![uri.clone()], dst, copy: None })
                .await
        };
        if let Err(e) = result {
            // Unknown outcome after a lost answer: both folders are listed again.
            self.mark_stale(names::parent_of(&old_path));
            self.mark_stale(names::parent_of(&new_path));
            return Err(self.api_failed(&e));
        }
        self.tree().move_to(ino, new_parent, new_name);
        let _ = self.store.move_changes(&old_path, &new_path);
        let _ = self.store.forget_tree(&old_path);
        self.mark_stale(names::parent_of(&old_path));
        self.mark_stale(names::parent_of(&new_path));
        let _ = is_dir;
        Ok(())
    }

    #[allow(clippy::too_many_arguments)]
    fn rename_local(
        &self,
        ino: u64,
        new_parent: u64,
        new_name: &str,
        old_path: &str,
        new_path: &str,
        target: Option<u64>,
        no_replace: bool,
    ) -> Result<(), Errno> {
        let mut dropped = None;
        {
            let mut tree = self.tree();
            if let Some(target) = target {
                if no_replace {
                    return Err(Errno::EEXIST);
                }
                let target_node = tree.nodes.get(&target).ok_or(Errno::ENOENT)?;
                if target_node.is_dir {
                    return Err(Errno::EISDIR);
                }
                if target_node.uploading {
                    return Err(Errno::EBUSY);
                }
                // The kernel now shows this node under the target name: it takes the target's
                // server identity, the old target node goes.
                let target_remote = target_node.remote.clone();
                tree.detach(target);
                if let Some(old) = tree.nodes.get_mut(&target).and_then(|n| n.change.take()) {
                    dropped = Some(old.cell);
                }
                if let Some(node) = tree.nodes.get_mut(&ino) {
                    node.remote = target_remote;
                }
            }
            tree.move_to(ino, new_parent, new_name);
            if let Some(change) = tree.nodes.get_mut(&ino).and_then(|n| n.change.as_mut()) {
                change.local_only = names::is_local_only(new_name);
                change.cell.lock().path = new_path.to_string();
            }
        }
        let _ = self.store.delete_change(old_path);
        let _ = self.store.delete_change(new_path);
        if let Some(cell) = dropped {
            self.contents.remove(&cell);
            let _ = self.store.delete_content(&cell.key());
        }
        self.persist_change(ino);
        self.schedule_upload(ino);
        Ok(())
    }

    // ---------------------------------------------------------------- status

    /// Size and free space for `df` and the file manager. Unknown (not asked yet, or no limit)
    /// is reported as plenty: a file manager refuses to copy into a drive that reports 0 free.
    pub(crate) fn statfs(&self) -> (u64, u64) {
        const PLENTY: u64 = 1 << 50;
        let quota = self.store.meta("quota").ok().flatten().unwrap_or_default();
        let mut parts = quota.split('/').map(|p| p.parse::<u64>().unwrap_or(0));
        let used = parts.next().unwrap_or(0);
        let total = parts.next().unwrap_or(0);
        if total == 0 {
            return (PLENTY, PLENTY);
        }
        (total, total.saturating_sub(used))
    }
}

/// The chunks a write covers partly that are not loaded yet.
fn partly_written_missing(cell: &Cell, start: u64, end: u64, remote_len: u64) -> Vec<u64> {
    let state = cell.lock();
    partly_written(start, end, remote_len)
        .into_iter()
        .filter(|chunk| !state.chunks.get(*chunk))
        .collect()
}

fn row_state(contents: &Contents, key: &str) -> Option<super::store::ContentRow> {
    contents.get(key).map(|cell| cell.lock().row())
}

/// A file or folder as a listing names it.
pub(crate) struct Listed {
    pub name: String,
    pub is_dir: bool,
    pub size: u64,
    pub mtime: SystemTime,
    pub id: String,
    pub version: String,
}

impl Listed {
    pub(crate) fn from_response(file: &FileResponse) -> Self {
        let is_dir = file.file_type == file_type::FOLDER;
        Self {
            name: file.name.clone(),
            is_dir,
            size: if is_dir { 0 } else { file.size.max(0) as u64 },
            mtime: parse_time(&file.updated_at),
            id: file.id.clone(),
            version: file.primary_entity.clone().unwrap_or_default(),
        }
    }

    fn from_row(row: EntryRow) -> Self {
        Self {
            name: row.name,
            is_dir: row.is_dir != 0,
            size: row.size.max(0) as u64,
            mtime: from_unix(row.mtime),
            id: row.file_id,
            version: row.version,
        }
    }

    fn to_row(&self) -> EntryRow {
        EntryRow {
            name: self.name.clone(),
            is_dir: self.is_dir as i32,
            size: self.size as i64,
            mtime: unix(self.mtime),
            file_id: self.id.clone(),
            version: self.version.clone(),
        }
    }
}

//! Fylvo (Linux): the virtual drive. The server folder is mounted at a local folder with FUSE;
//! names show at once, content is loaded when read and kept in a cache with a size limit,
//! changes are uploaded after the file is closed. Design: `desktop/docs/linux-drive-plan.md`.

mod callers;
mod chunks;
mod content;
mod deadline;
mod drive;
mod fs;
mod names;
mod store;
mod upload;
#[cfg(test)]
mod e2e;

use anyhow::{Context, Result, bail};
use cloudreve_api::Client;
use drive::{Drive, DriveInfo};
use fuser::{Errno, INodeNo, MountOption};
use std::os::unix::process::CommandExt;
use std::path::{Path, PathBuf};
use std::process::{Child, ChildStdin, Command, Stdio};
use std::sync::atomic::Ordering;
use std::sync::{Arc, Mutex, Weak};
use std::time::Duration;

use crate::inventory::InventoryDb;

/// Where the drives keep their store (`~/.fylvo/drives/<id>`).
pub fn data_dir(drive_id: &str) -> PathBuf {
    dirs::home_dir()
        .unwrap_or_else(|| PathBuf::from("."))
        .join(crate::DATA_DIR)
        .join("drives")
        .join(drive_id)
}

pub use crate::drive::manager::VfsStatus;

/// A mounted drive.
pub struct VirtualDrive {
    drive: Arc<Drive>,
    session: Mutex<Option<fuser::BackgroundSession>>,
    /// The pipe the unmount helper waits on: it unmounts when the app ends in any way.
    guard: Mutex<Option<(Child, ChildStdin)>>,
    tasks: Mutex<Vec<tokio::task::JoinHandle<()>>>,
}

static DRIVES: Mutex<Vec<Weak<Drive>>> = Mutex::new(Vec::new());

fn drives() -> Vec<Arc<Drive>> {
    let mut list = DRIVES.lock().unwrap_or_else(|e| e.into_inner());
    list.retain(|d| d.strong_count() > 0);
    list.iter().filter_map(Weak::upgrade).collect()
}

/// A limit set by tests (0: none).
#[cfg(test)]
pub(crate) static TEST_LIMIT: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);

/// The cache limit for all drives together, in bytes.
pub fn cache_limit() -> u64 {
    #[cfg(test)]
    {
        let limit = TEST_LIMIT.load(Ordering::SeqCst);
        if limit > 0 {
            return limit;
        }
    }
    let gb = crate::config::ConfigManager::try_get()
        .map(|c| c.drive_cache_gb())
        .unwrap_or(crate::config::DEFAULT_DRIVE_CACHE_GB);
    gb.saturating_mul(1024 * 1024 * 1024)
}

/// Free bytes on the disk that holds `path`.
pub(crate) fn free_space(path: &Path) -> u64 {
    use std::os::unix::ffi::OsStrExt;
    let Ok(c_path) = std::ffi::CString::new(path.as_os_str().as_bytes()) else {
        return u64::MAX;
    };
    let mut stat: libc::statvfs = unsafe { std::mem::zeroed() };
    if unsafe { libc::statvfs(c_path.as_ptr(), &mut stat) } != 0 {
        return u64::MAX;
    }
    stat.f_bavail as u64 * stat.f_frsize as u64
}

/// Every cached file stays open: allow as many open files as the system lets this user have
/// (the usual soft limit of 1024 is reached by a cache of small files).
fn raise_open_file_limit() {
    let mut limit = libc::rlimit { rlim_cur: 0, rlim_max: 0 };
    if unsafe { libc::getrlimit(libc::RLIMIT_NOFILE, &mut limit) } == 0 && limit.rlim_cur < limit.rlim_max {
        limit.rlim_cur = limit.rlim_max;
        unsafe { libc::setrlimit(libc::RLIMIT_NOFILE, &limit) };
    }
}

/// What eviction and the settings need to know of a cached file.
struct CellInfo {
    key: String,
    path: String,
    file_id: String,
    version: String,
    last_used: i64,
    open: bool,
    changed: bool,
    loaded: u64,
}

fn cells(drive: &Drive) -> Vec<CellInfo> {
    drive
        .contents
        .all()
        .iter()
        .map(|cell| {
            let s = cell.lock();
            CellInfo {
                key: s.key.clone(),
                path: s.path.clone(),
                file_id: s.file_id.clone(),
                version: s.version.clone(),
                last_used: s.last_used,
                open: s.open > 0,
                changed: s.changed,
                loaded: s.loaded_bytes(),
            }
        })
        .collect()
}

/// Kept for an "always offline" folder: below a pin and the version the file has now (older
/// versions of a pinned file count like any cached file). Unknown files (not listed since the
/// start) are kept.
fn pinned(drive: &Drive, cell: &CellInfo) -> bool {
    if !drive.is_pinned(&cell.path) {
        return false;
    }
    let tree = drive.tree();
    Drive::find(&tree, &cell.path)
        .and_then(|ino| tree.nodes.get(&ino))
        .and_then(|node| node.remote.as_ref())
        .is_none_or(|remote| remote.id == cell.file_id && remote.version == cell.version)
}

/// Cached bytes of a drive counted against the limit: loaded server content that is neither
/// pinned nor a local change.
fn counted(drive: &Drive) -> u64 {
    cells(drive)
        .iter()
        .filter(|c| !c.changed && !pinned(drive, c))
        .map(|c| c.loaded)
        .sum()
}

/// Evict least recently used content of all drives until `needed` more bytes fit in the limit.
/// Open, changed and pinned content stays; the limit may be passed by files open right now.
pub(crate) fn make_room(needed: u64) -> Result<(), Errno> {
    let all = drives();
    let limit = cache_limit();
    let mut used: u64 = all.iter().map(|d| counted(d)).sum();
    if used + needed <= limit {
        return Ok(());
    }
    let mut candidates = Vec::new();
    for drive in &all {
        for cell in cells(drive) {
            if !cell.open && !cell.changed && cell.loaded > 0 && !pinned(drive, &cell) {
                candidates.push((cell.last_used, drive.clone(), cell.key));
            }
        }
    }
    candidates.sort_by_key(|(last_used, _, _)| *last_used);
    for (_, drive, key) in candidates {
        if used + needed <= limit {
            break;
        }
        if let Some(freed) = drive.contents.evict(&key) {
            let _ = drive.store.delete_content(&key);
            used = used.saturating_sub(freed);
        }
    }
    Ok(())
}

impl VirtualDrive {
    /// Mount drive `id` at `mount_point`. `remote_base` is the server folder (a `cloudreve://`
    /// URI).
    #[allow(clippy::too_many_arguments)]
    pub async fn start(
        id: &str,
        mount_point: &Path,
        remote_base: &str,
        instance_url: &str,
        client: Arc<Client>,
        inventory: Arc<InventoryDb>,
    ) -> Result<Arc<Self>> {
        prepare_mount_point(mount_point)?;
        raise_open_file_limit();
        let data_dir = data_dir(id);
        std::fs::create_dir_all(&data_dir).context("create the drive's data folder")?;
        let store = store::Store::open(&data_dir.join("index.db"))?;
        let (contents, lost) = content::Contents::load(data_dir.join("content"), store.load_contents()?)?;
        for key in lost {
            let _ = store.delete_content(&key);
        }
        let info = DriveInfo {
            id: id.to_string(),
            mount_point: mount_point.to_path_buf(),
            remote_base: remote_base.to_string(),
            instance_url: instance_url.to_string(),
            data_dir,
        };
        let drive = Arc::new(Drive::new(info, client, inventory, store, contents, tokio::runtime::Handle::current()));
        DRIVES.lock().unwrap_or_else(|e| e.into_inner()).push(Arc::downgrade(&drive));

        // Changes recorded before (also those of a crash) come back first (from the store, no
        // server request: the mount does not wait for the network).
        upload::restore(&drive);

        let mut config = fuser::Config::default();
        config.mount_options = vec![
            MountOption::FSName("fylvo".into()),
            MountOption::Subtype("fylvo".into()),
            MountOption::DefaultPermissions,
            MountOption::NoAtime,
            MountOption::NoDev,
            MountOption::NoSuid,
            MountOption::RW,
        ];
        config.n_threads = Some(4);
        let fs = fs::DriveFs { drive: drive.clone() };
        let mount_point_owned = mount_point.to_path_buf();
        let session = tokio::task::spawn_blocking(move || fuser::Session::new(fs, &mount_point_owned, &config))
            .await
            .context("mount the drive")?
            .with_context(|| format!("mount the drive at {}", mount_point.display()))?;
        start_invalidation_thread(&drive, session.notifier());
        let session = session.spawn().context("start the drive")?;
        let guard = match start_unmount_helper(mount_point) {
            Ok(guard) => Some(guard),
            Err(e) => {
                tracing::warn!(target: "vfs", error = %e, "No unmount helper, a crash may leave a dead mount until the next start");
                None
            }
        };
        let this = Arc::new(Self {
            drive: drive.clone(),
            session: Mutex::new(Some(session)),
            guard: Mutex::new(guard),
            tasks: Mutex::new(Vec::new()),
        });
        // The top folder (and its subfolders) are listed right away.
        let first = drive.clone();
        tokio::spawn(async move {
            let _ = first.ensure_listing(drive::ROOT, false).await;
            let _ = first.refresh(drive::ROOT).await;
        });
        let tasks = vec![
            tokio::spawn(upload::run(drive.clone())),
            tokio::spawn(events(drive.clone())),
            tokio::spawn(pin_loop(drive.clone())),
        ];
        *this.tasks.lock().unwrap_or_else(|e| e.into_inner()) = tasks;
        set_bookmark(mount_point, true);
        tracing::info!(target: "vfs", drive = %id, mount_point = %mount_point.display(), "Drive mounted");
        Ok(this)
    }

    pub fn mount_point(&self) -> &Path {
        &self.drive.info.mount_point
    }

    /// Unmount. Programs with open files get errors from now on; nothing written after this.
    pub fn stop(&self) {
        let drive = &self.drive;
        if drive.stopping.swap(true, Ordering::SeqCst) {
            return;
        }
        for task in self.tasks.lock().unwrap_or_else(|e| e.into_inner()).drain(..) {
            task.abort();
        }
        lazy_unmount(&drive.info.mount_point);
        // The session thread ends once the kernel lets go of the connection.
        if let Some(session) = self.session.lock().unwrap_or_else(|e| e.into_inner()).take() {
            std::thread::spawn(move || drop(session));
        }
        if let Some((mut child, stdin)) = self.guard.lock().unwrap_or_else(|e| e.into_inner()).take() {
            drop(stdin);
            let _ = child.wait();
        }
        drive.watchdog.stop();
        tracing::info!(target: "vfs", drive = %drive.info.id, "Drive unmounted");
    }

    /// Whether the drive has changes not uploaded yet or files open for writing.
    pub fn has_pending_work(&self) -> bool {
        let tree = self.drive.tree();
        tree.nodes.iter().any(|(ino, n)| {
            (n.writers > 0 || n.change.as_ref().is_some_and(|c| !c.local_only)) && tree.path(*ino).is_some()
        })
    }

    pub fn status(&self) -> VfsStatus {
        let drive = &self.drive;
        let (waiting, failed) = {
            let tree = drive.tree();
            let changes: Vec<_> = tree
                .nodes
                .iter()
                .filter(|(ino, _)| tree.path(**ino).is_some())
                .filter_map(|(_, n)| n.change.as_ref())
                .filter(|c| !c.local_only)
                .collect();
            (changes.len(), changes.iter().filter(|c| c.failed.is_some()).count())
        };
        let all = cells(drive);
        let pinned_bytes = all.iter().filter(|c| !c.changed && pinned(drive, c)).map(|c| c.loaded).sum();
        let cache_used = all.iter().filter(|c| !c.changed && !pinned(drive, c)).map(|c| c.loaded).sum();
        VfsStatus {
            online: drive.is_online(),
            cache_used,
            cache_limit: cache_limit(),
            pinned_bytes,
            pins: drive.pins.lock().unwrap_or_else(|e| e.into_inner()).clone(),
            changes_waiting: waiting,
            changes_failed: failed,
        }
    }

    /// The path of `local` relative to the drive root, if it is in this drive (by components,
    /// never by asking the file system).
    pub fn relative(&self, local: &Path) -> Option<String> {
        let rel = local.strip_prefix(&self.drive.info.mount_point).ok()?;
        let text = rel.to_str()?.trim_end_matches('/').to_string();
        Some(text)
    }

    /// Keep `path` (relative) on this computer.
    pub fn pin(&self, path: &str) -> Result<()> {
        self.drive.store.add_pin(path)?;
        let mut pins = self.drive.pins.lock().unwrap_or_else(|e| e.into_inner());
        if !pins.iter().any(|p| p == path) {
            pins.push(path.to_string());
            pins.sort();
        }
        drop(pins);
        self.drive.pin_wake.notify_one();
        Ok(())
    }

    /// Stop keeping `path` and free its space.
    pub fn unpin(&self, path: &str) -> Result<()> {
        self.drive.store.remove_pin(path)?;
        self.drive
            .pins
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .retain(|p| p != path);
        self.free_space(Some(path));
        Ok(())
    }

    /// Drop the cached content (below `path`, or all) that is not open, changed or pinned.
    pub fn free_space(&self, path: Option<&str>) {
        let drive = &self.drive;
        for cell in cells(drive) {
            if path.is_some_and(|p| !names::is_below(&cell.path, p)) || pinned(drive, &cell) {
                continue;
            }
            if drive.contents.evict(&cell.key).is_some() {
                let _ = drive.store.delete_content(&cell.key);
            }
        }
    }

    /// Before a removal (still mounted): load the parts of changed files never loaded, so they
    /// can be kept whole. Fails if one cannot be completed (offline): nothing is removed then.
    pub async fn prepare_removal(&self) -> Result<()> {
        let drive = &self.drive;
        let changed: Vec<(u64, std::sync::Arc<content::Cell>)> = {
            let tree = drive.tree();
            tree.nodes
                .iter()
                .filter_map(|(ino, n)| n.change.as_ref().filter(|c| !c.local_only).map(|c| (*ino, c.cell.clone())))
                .collect()
        };
        for (ino, cell) in changed {
            let remote_len = cell.lock().remote_len;
            if cell.lock().missing_runs(0, remote_len).is_empty() {
                continue;
            }
            let path = drive.tree().path(ino).unwrap_or_else(|| cell.lock().path.clone());
            if drive.ensure_range(&cell, &path, 0, remote_len).await.is_err() {
                bail!("{path} is changed but not complete on this computer, and the server cannot be reached");
            }
        }
        Ok(())
    }

    /// After `stop`: wait (up to a minute) until running uploads are done.
    pub async fn wait_idle(&self) {
        for _ in 0..300 {
            if !self.drive.tree().nodes.values().any(|n| n.uploading) {
                return;
            }
            tokio::time::sleep(Duration::from_millis(200)).await;
        }
    }

    /// Whether the server holds `path` (relative) as a folder. For "View online". Answered
    /// from the tree, never from the server.
    pub fn is_folder(&self, path: &str) -> Option<bool> {
        let tree = self.drive.tree();
        let ino = Drive::find(&tree, path)?;
        tree.nodes.get(&ino).map(|n| n.is_dir)
    }

    /// After `stop`: copy the changes that were never uploaded to `target` (temp names left
    /// out). Returns how many files were saved.
    pub fn export_unsent(&self, target: &Path) -> Result<usize> {
        if !self.drive.stopping.load(Ordering::SeqCst) {
            bail!("the drive is still mounted");
        }
        let rows = self.drive.store.load_changes()?;
        let mut saved = 0;
        for row in rows.iter().filter(|r| r.local_only == 0) {
            // Gone or no change anymore: an upload finished it.
            let Some(cell) = self.drive.contents.get(&row.key) else { continue };
            // Under the gate: no write can be half in the copy.
            let _gate = cell.gate.write().unwrap_or_else(|e| e.into_inner());
            let key = {
                let state = cell.lock();
                if !state.changed {
                    continue;
                }
                if !state.missing_runs(0, state.remote_len).is_empty() {
                    bail!("{} is not complete on this computer", row.path);
                }
                state.key.clone()
            };
            let destination = target.join(&row.path);
            if let Some(parent) = destination.parent() {
                std::fs::create_dir_all(parent)?;
            }
            std::fs::copy(self.drive.contents.path_of(&key), &destination)
                .with_context(|| format!("save {}", row.path))?;
            saved += 1;
        }
        Ok(saved)
    }

    /// Number of changes that were never uploaded (temp names left out). An error if the
    /// store cannot say (then nothing may be deleted).
    pub fn unsent_count(&self) -> Result<usize> {
        Ok(self.drive.store.load_changes()?.iter().filter(|r| r.local_only == 0).count())
    }

    /// Delete the store of a removed drive (and its place in the file manager's side bar).
    pub fn delete_data(&self) {
        set_bookmark(&self.drive.info.mount_point, false);
        let _ = std::fs::remove_dir_all(&self.drive.info.data_dir);
    }

    /// Remember the quota for `df` and the file manager (never asked while answering).
    pub fn set_quota(&self, used: u64, total: u64) {
        self.drive.set_quota(used, total);
        let _ = self.drive.store.set_meta("quota", &format!("{used}/{total}"));
    }
}

impl Drop for VirtualDrive {
    fn drop(&mut self) {
        self.stop();
    }
}

/// The mount point must be an empty folder (created if missing). A dead mount left by a crash
/// is removed first.
fn prepare_mount_point(path: &Path) -> Result<()> {
    // Only our own dead mount is removed; a mount of something else stays and is refused.
    match mount_type(path) {
        Some(kind) if kind == "fuse.fylvo" => {
            tracing::info!(target: "vfs", path = %path.display(), "Removing an old mount");
            lazy_unmount(path);
        }
        Some(kind) => bail!("{} is in use by another mount ({kind})", path.display()),
        None => {}
    }
    match std::fs::read_dir(path) {
        Ok(mut entries) => {
            if entries.next().is_some() {
                bail!("the folder {} is not empty", path.display());
            }
        }
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
            std::fs::create_dir_all(path).context("create the drive folder")?;
        }
        Err(e) => return Err(e).context("open the drive folder"),
    }
    Ok(())
}

/// The drive in the side bar of GNOME Files and other GTK file managers
/// (`~/.config/gtk-3.0/bookmarks`, one `file://` URI per line).
fn set_bookmark(mount_point: &Path, present: bool) {
    let Some(config) = dirs::config_dir() else { return };
    let Ok(uri) = url::Url::from_file_path(mount_point) else { return };
    let file = config.join("gtk-3.0").join("bookmarks");
    let text = std::fs::read_to_string(&file).unwrap_or_default();
    let updated = bookmarks_with(&text, uri.as_str(), present);
    if updated != text {
        let _ = std::fs::create_dir_all(config.join("gtk-3.0"));
        if let Err(e) = std::fs::write(&file, updated) {
            tracing::debug!(target: "vfs", error = %e, "Failed to update the file manager bookmarks");
        }
    }
}

fn bookmarks_with(text: &str, uri: &str, present: bool) -> String {
    let is_ours = |line: &str| line == uri || line.starts_with(&format!("{uri} "));
    let mut lines: Vec<&str> = text.lines().filter(|line| !is_ours(line)).collect();
    if present {
        lines.push(uri);
    }
    let mut out = lines.join("\n");
    if !out.is_empty() {
        out.push('\n');
    }
    out
}

/// The file system type of a mount at exactly `path` (`/proc/self/mountinfo`), if any.
fn mount_type(path: &Path) -> Option<String> {
    let text = std::fs::read_to_string("/proc/self/mountinfo").ok()?;
    let wanted = path.to_string_lossy();
    // Field 5 is the mount point (spaces and the like escaped as \040); after " - " comes the
    // file system type. The last matching line is the mount on top.
    text.lines()
        .filter(|line| line.split(' ').nth(4).is_some_and(|point| unescape_mountinfo(point) == wanted))
        .last()
        .and_then(|line| line.split(" - ").nth(1))
        .and_then(|rest| rest.split(' ').next())
        .map(str::to_string)
}

fn unescape_mountinfo(text: &str) -> String {
    let mut out = Vec::new();
    let bytes = text.as_bytes();
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'\\' && i + 3 < bytes.len() {
            if let Ok(value) = u8::from_str_radix(&text[i + 1..i + 4], 8) {
                out.push(value);
                i += 4;
                continue;
            }
        }
        out.push(bytes[i]);
        i += 1;
    }
    String::from_utf8_lossy(&out).to_string()
}

fn lazy_unmount(path: &Path) {
    for program in ["fusermount3", "fusermount"] {
        match Command::new(program).arg("-u").arg("-z").arg(path).stderr(Stdio::null()).status() {
            Ok(status) if status.success() => return,
            Ok(_) => {}
            Err(_) => continue,
        }
    }
}

/// A shell in its own session that waits for the app's pipe to close (the app ended in any
/// way, also `kill -9`) and then unmounts. The mount point is an argument, never script text.
fn start_unmount_helper(mount_point: &Path) -> Result<(Child, ChildStdin)> {
    let mut command = Command::new("sh");
    command
        .arg("-c")
        .arg("cat >/dev/null; fusermount3 -u -z \"$1\" 2>/dev/null || fusermount -u -z \"$1\" 2>/dev/null")
        .arg("fylvo-unmount")
        .arg(mount_point)
        .stdin(Stdio::piped())
        .stdout(Stdio::null())
        .stderr(Stdio::null());
    unsafe {
        command.pre_exec(|| {
            libc::setsid();
            Ok(())
        });
    }
    let mut child = command.spawn().context("start the unmount helper")?;
    let stdin = child.stdin.take().context("unmount helper pipe")?;
    Ok((child, stdin))
}

/// Kernel cache invalidations come from one thread: they may wait for pages a read holds.
fn start_invalidation_thread(drive: &Arc<Drive>, notifier: fuser::Notifier) {
    let (tx, rx) = std::sync::mpsc::channel::<u64>();
    *drive.inval.lock().unwrap_or_else(|e| e.into_inner()) = Some(tx);
    std::thread::Builder::new()
        .name("fylvo-drive-inval".into())
        .spawn(move || {
            while let Ok(ino) = rx.recv() {
                let _ = notifier.inval_inode(INodeNo(ino), 0, 0);
            }
        })
        .ok();
}

/// Server events mark folders as old; files below pinned folders are loaded again.
async fn events(drive: Arc<Drive>) {
    use cloudreve_api::api::explorer::FileEventsApi;
    use cloudreve_api::models::explorer::FileEvent;
    let base = cloudreve_api::models::uri::CrUri::new(&drive.info.remote_base).ok();
    loop {
        if drive.stopping.load(Ordering::SeqCst) {
            return;
        }
        match drive.client.subscribe_file_events(&drive.info.remote_base).await {
            Ok(mut subscription) => {
                drive.mark_all_stale();
                drive.pin_wake.notify_one();
                loop {
                    match subscription.next_event().await {
                        Ok(Some(FileEvent::Event(events))) => {
                            for event in events {
                                for uri in [&event.from, &event.to] {
                                    if let Some(path) = relative_of(base.as_ref(), uri) {
                                        drive.mark_stale(&path);
                                        drive.mark_stale(names::parent_of(&path));
                                    }
                                }
                            }
                            drive.pin_wake.notify_one();
                        }
                        Ok(Some(FileEvent::ReconnectRequired)) | Ok(None) => break,
                        Ok(Some(_)) => {}
                        Err(_) => break,
                    }
                }
            }
            Err(e) => {
                drive.note_failure(drive::errno_of(&e));
            }
        }
        tokio::time::sleep(Duration::from_secs(10)).await;
    }
}

fn relative_of(base: Option<&cloudreve_api::models::uri::CrUri>, uri: &str) -> Option<String> {
    if uri.is_empty() {
        return None;
    }
    let base = base?;
    let target = cloudreve_api::models::uri::CrUri::new(uri).ok()?;
    let relative = crate::drive::utils::remote_path_to_local_relative_path(&target, base).ok()?;
    Some(relative.to_string_lossy().trim_matches('/').to_string())
}

/// Load pinned folders fully and keep them current: at start, after server events and when
/// the connection is back.
async fn pin_loop(drive: Arc<Drive>) {
    loop {
        if drive.stopping.load(Ordering::SeqCst) {
            return;
        }
        if drive.is_online() {
            let pins = drive.pins.lock().unwrap_or_else(|e| e.into_inner()).clone();
            for pin in pins {
                if let Err(e) = load_pinned(&drive, &pin).await {
                    tracing::info!(target: "vfs", path = %pin, error = ?e, "An offline folder is not complete yet");
                }
            }
        }
        // Several events in a row lead to one pass.
        drive.pin_wake.notified().await;
        tokio::time::sleep(Duration::from_secs(2)).await;
    }
}

async fn load_pinned(drive: &Arc<Drive>, path: &str) -> Result<(), Errno> {
    let ino = drive.resolve(path).await?;
    let mut folders = vec![ino];
    while let Some(folder) = folders.pop() {
        let is_dir = drive.tree().nodes.get(&folder).ok_or(Errno::ENOENT)?.is_dir;
        if !is_dir {
            load_file(drive, folder).await?;
            continue;
        }
        drive.refresh(folder).await?;
        let entries = {
            let tree = drive.tree();
            tree.nodes
                .get(&folder)
                .and_then(|n| n.listing.as_ref())
                .map(|l| l.children.values().copied().collect::<Vec<_>>())
                .unwrap_or_default()
        };
        folders.extend(entries);
    }
    Ok(())
}

async fn load_file(drive: &Arc<Drive>, ino: u64) -> Result<(), Errno> {
    let (remote, size, path, changed) = {
        let tree = drive.tree();
        let node = tree.nodes.get(&ino).ok_or(Errno::ENOENT)?;
        (node.remote.clone(), node.size, tree.path(ino).unwrap_or_default(), node.change.is_some())
    };
    let Some(remote) = remote.filter(|_| !changed) else {
        return Ok(());
    };
    let cell = drive
        .contents
        .clean(&remote.id, &remote.version, size, &path, true)
        .map_err(|_| Errno::EIO)?;
    let result = drive.ensure_range(&cell, &path, 0, size).await;
    {
        let mut state = cell.lock();
        state.open = state.open.saturating_sub(1);
    }
    let row = cell.lock().row();
    let _ = drive.store.save_content(&row);
    result
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_drive_is_added_to_and_removed_from_the_bookmarks() {
        let start = "file:///home/u/Documents\nfile:///home/u/Music Music\n";
        let added = bookmarks_with(start, "file:///home/u/Fylvo", true);
        assert_eq!(added, "file:///home/u/Documents\nfile:///home/u/Music Music\nfile:///home/u/Fylvo\n");
        assert_eq!(bookmarks_with(&added, "file:///home/u/Fylvo", true), added);
        assert_eq!(bookmarks_with(&added, "file:///home/u/Fylvo", false), start);
        assert_eq!(bookmarks_with("", "file:///home/u/Fylvo", false), "");
    }

    #[test]
    fn mountinfo_paths_are_unescaped() {
        assert_eq!(unescape_mountinfo("/home/u/My\\040Drive"), "/home/u/My Drive");
        assert_eq!(unescape_mountinfo("/plain"), "/plain");
    }

    #[test]
    fn a_folder_with_files_is_refused_as_mount_point() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join("x"), b"1").unwrap();
        assert!(prepare_mount_point(dir.path()).is_err());
        let new = dir.path().join("new");
        prepare_mount_point(&new).unwrap();
        assert!(new.is_dir());
    }
}

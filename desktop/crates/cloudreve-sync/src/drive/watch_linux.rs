//! Fylvo: local changes on Linux (stage 4 of `desktop/docs/linux-plan.md`).
//!
//! Windows asks the app before a rename (Cloud Files callback) and can refuse it. Linux cannot:
//! inotify reports renames and moves when they are done. This module
//! - runs one watcher for all sync folders, so a move between two of them arrives as one
//!   paired rename,
//! - drops the app's own moves (download, update, conflict copies, server renames followed
//!   here, moves back), registered in `OWN_MOVES` before they happen,
//! - turns a bare move out of a folder ("Move to Trash") into a deletion and a bare move in
//!   into a creation,
//! - handles a user rename after the fact (`Mount::process_rename_linux`).

use crate::drive::commands::MountCommand;
use crate::drive::mounts::Mount;
use crate::drive::sync::{SyncMode, group_fs_events};
use crate::drive::utils::{local_path_to_cr_uri, move_no_replace};
use crate::inventory::FileMetadata;
use crate::tasks::TaskPayload;
use crate::utils::toast;
use anyhow::{Context, Result};
use cloudreve_api::ApiError;
use cloudreve_api::api::ExplorerApi;
use cloudreve_api::error::ErrorCode;
use cloudreve_api::models::explorer::{GetFileInfoService, MoveFileService, RenameFileService};
use notify_debouncer_full::notify::event::{CreateKind, EventKind, ModifyKind, RemoveKind, RenameMode};
use notify_debouncer_full::notify::{RecommendedWatcher, RecursiveMode};
use notify_debouncer_full::{DebounceEventResult, DebouncedEvent, Debouncer, RecommendedCache};
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use std::time::{Duration, Instant};
use tokio::sync::mpsc::UnboundedSender;

/// How long a registered own move waits for its events.
const OWN_MOVE_TTL: Duration = Duration::from_secs(60);

struct OwnMove {
    from: PathBuf,
    to: PathBuf,
    from_seen: bool,
    to_seen: bool,
    at: Instant,
}

/// The app's own moves, for all drives (the watcher is shared).
static OWN_MOVES: Mutex<Vec<OwnMove>> = Mutex::new(Vec::new());

fn own_moves() -> std::sync::MutexGuard<'static, Vec<OwnMove>> {
    let mut moves = OWN_MOVES.lock().unwrap_or_else(|e| e.into_inner());
    moves.retain(|m| m.at.elapsed() < OWN_MOVE_TTL && !(m.from_seen && m.to_seen));
    moves
}

/// Register a move the app is about to make, so its events are not taken for the user's.
pub fn register_own_move(from: &Path, to: &Path) {
    own_moves().push(OwnMove {
        from: from.to_path_buf(),
        to: to.to_path_buf(),
        from_seen: false,
        to_seen: false,
        at: Instant::now(),
    });
}

/// Forget a registered move that did not happen, so it never swallows a user action.
pub fn forget_own_move(from: &Path, to: &Path) {
    own_moves().retain(|m| !(m.from == from && m.to == to));
}

/// Whether a rename `from` -> `to` is a registered own move (also two own moves joined by the
/// debouncer, a -> b and b -> c reported as a -> c); consumes what it matches.
fn is_own(from: &Path, to: &Path) -> bool {
    let mut moves = own_moves();
    if let Some(index) = moves.iter().position(|m| m.from == from && m.to == to) {
        moves[index].from_seen = true;
        moves[index].to_seen = true;
        return true;
    }
    let first = moves.iter().position(|m| m.from == from);
    if let Some(first) = first {
        let middle = moves[first].to.clone();
        if let Some(second) = moves.iter().position(|m| m.from == middle && m.to == to) {
            for index in [first, second] {
                moves[index].from_seen = true;
                moves[index].to_seen = true;
            }
            return true;
        }
    }
    false
}

/// Drop the app's own moves and turn bare halves of moves into deletions and creations.
pub(crate) fn prepare_events(events: Vec<DebouncedEvent>) -> Vec<DebouncedEvent> {
    let mut out = Vec::with_capacity(events.len());
    for mut event in events {
        match event.kind {
            EventKind::Modify(ModifyKind::Name(RenameMode::Both)) if event.paths.len() == 2 => {
                if is_own(&event.paths[0], &event.paths[1]) {
                    continue;
                }
            }
            // A lone half of a move is always the user's: one inotify instance watches all sync
            // folders, so the app's own moves arrive whole. (Matching halves could swallow a
            // later "Move to Trash" of the same path.)
            EventKind::Modify(ModifyKind::Name(RenameMode::From)) if event.paths.len() == 1 => {
                event.event.kind = EventKind::Remove(RemoveKind::Any);
            }
            EventKind::Modify(ModifyKind::Name(RenameMode::To)) if event.paths.len() == 1 => {
                event.event.kind = EventKind::Create(CreateKind::Any);
            }
            _ => {}
        }
        out.push(event);
    }
    out
}

/// How long a sync pass waits before it acts on what may be half of a user rename the watcher
/// has not reported yet (it reports after 2 seconds).
const RENAME_SETTLE: Duration = Duration::from_secs(5);

/// How long a rename the server could not be asked about waits before it is tried again.
const RENAME_RETRY: Duration = Duration::from_secs(60);

/// Renames waiting for the server (network): both names are left alone by sync passes.
static PENDING_RENAMES: Mutex<Vec<(PathBuf, PathBuf, Option<u64>)>> = Mutex::new(Vec::new());

/// Ends a pending rename; returns the inode of the file it was about if it was one (a retry).
fn finish_pending_rename(from: &Path, to: &Path) -> Option<Option<u64>> {
    let mut pending = PENDING_RENAMES.lock().unwrap_or_else(|e| e.into_inner());
    let index = pending.iter().position(|(f, t, _)| f == from && t == to)?;
    Some(pending.remove(index).2)
}

fn in_pending_rename(path: &Path) -> bool {
    PENDING_RENAMES
        .lock()
        .unwrap_or_else(|e| e.into_inner())
        .iter()
        .any(|(from, to, _)| path.starts_with(from) || path.starts_with(to))
}

/// Paths put off by `rename_settled`, synced again together a moment later (one sync for many
/// new files, not one each).
static RESYNC: Mutex<Vec<(PathBuf, Instant, UnboundedSender<MountCommand>)>> = Mutex::new(Vec::new());

/// Sends the put-off paths whose wait is over, per drive, until none is left.
async fn resync_loop() {
    loop {
        tokio::time::sleep(Duration::from_secs(1)).await;
        let ready: Vec<(PathBuf, UnboundedSender<MountCommand>)> = {
            let mut resync = RESYNC.lock().unwrap_or_else(|e| e.into_inner());
            let (ready, waiting): (Vec<_>, Vec<_>) = std::mem::take(&mut *resync)
                .into_iter()
                .partition(|(_, at, _)| at.elapsed() >= RENAME_SETTLE);
            *resync = waiting;
            ready.into_iter().map(|(path, _, tx)| (path, tx)).collect()
        };
        let mut by_drive: Vec<(UnboundedSender<MountCommand>, Vec<PathBuf>)> = Vec::new();
        for (path, tx) in ready {
            match by_drive.iter_mut().find(|(known, _)| known.same_channel(&tx)) {
                Some((_, paths)) => paths.push(path),
                None => by_drive.push((tx, vec![path])),
            }
        }
        for (tx, paths) in by_drive {
            let _ = tx.send(MountCommand::Sync {
                local_paths: paths,
                mode: SyncMode::PathOnly,
                user_initiated: false,
            });
        }
        if RESYNC.lock().unwrap_or_else(|e| e.into_inner()).is_empty() {
            return;
        }
    }
}

/// Paths a sync pass found as a possible half of a rename, with when it first saw them.
static UNSETTLED: Mutex<Vec<(PathBuf, Instant)>> = Mutex::new(Vec::new());

/// Whether a sync pass may act now on `path`, which may be half of a rename: "on the server,
/// missing here, row present" or "here, no row". The first time it is not; the path is planned
/// again a little later, after the watcher had its say.
pub(crate) fn rename_settled(
    path: &Path,
    parent_tracked: bool,
    commands: &UnboundedSender<MountCommand>,
) -> bool {
    if in_pending_rename(path) {
        return false;
    }
    // In a folder the sync does not know yet, nothing can be the other half of a rename of a
    // synced file.
    if !parent_tracked {
        return true;
    }
    let mut unsettled = UNSETTLED.lock().unwrap_or_else(|e| e.into_inner());
    unsettled.retain(|(_, at)| at.elapsed() < RENAME_SETTLE * 6);
    if let Some(index) = unsettled.iter().position(|(known, _)| known == path) {
        if unsettled[index].1.elapsed() >= RENAME_SETTLE {
            unsettled.remove(index);
            return true;
        }
        return false;
    }
    let now = Instant::now();
    unsettled.push((path.to_path_buf(), now));
    let mut resync = RESYNC.lock().unwrap_or_else(|e| e.into_inner());
    let start = resync.is_empty();
    resync.push((path.to_path_buf(), now, commands.clone()));
    if start {
        tokio::spawn(resync_loop());
    }
    false
}

/// Folders created in the sync folders a moment ago. A move into a folder made just before is
/// reported by inotify only as a deletion (the new folder was not watched yet).
static RECENT_FOLDERS: Mutex<Vec<(PathBuf, Instant)>> = Mutex::new(Vec::new());
const RECENT_FOLDER_TTL: Duration = Duration::from_secs(60);
/// At most this many entries are looked at per search.
const MOVE_SEARCH_LIMIT: usize = 20_000;

fn note_new_folders(events: &[DebouncedEvent]) {
    let mut recent = RECENT_FOLDERS.lock().unwrap_or_else(|e| e.into_inner());
    recent.retain(|(_, at)| at.elapsed() < RECENT_FOLDER_TTL);
    for event in events {
        if matches!(event.kind, EventKind::Create(_)) {
            if let Some(path) = event.paths.first() {
                if std::fs::symlink_metadata(path).is_ok_and(|m| m.is_dir()) {
                    recent.push((path.clone(), Instant::now()));
                }
            }
        }
    }
}

/// Where the file or folder with `inode` is now, if it lies in a folder created a moment ago.
fn find_in_new_folders(inode: u64, root: &Path) -> Option<PathBuf> {
    use std::os::unix::fs::MetadataExt;
    let device = std::fs::symlink_metadata(root).ok()?.dev();
    let folders: Vec<PathBuf> = {
        let recent = RECENT_FOLDERS.lock().unwrap_or_else(|e| e.into_inner());
        recent
            .iter()
            .map(|(path, _)| path.clone())
            .filter(|path| {
                path.starts_with(root) && !crate::drive::local_linux::is_foreign_below(path, root)
            })
            .collect()
    };
    let mut seen = 0;
    let mut stack = folders;
    while let Some(dir) = stack.pop() {
        let Ok(entries) = std::fs::read_dir(&dir) else {
            continue;
        };
        for entry in entries.flatten() {
            seen += 1;
            if seen > MOVE_SEARCH_LIMIT {
                return None;
            }
            let Ok(meta) = entry.metadata() else {
                continue;
            };
            if meta.file_type().is_symlink() || meta.dev() != device {
                continue;
            }
            if meta.ino() == inode {
                return Some(entry.path());
            }
            if meta.is_dir() && !crate::drive::local_linux::is_foreign(&entry.path()) {
                stack.push(entry.path());
            }
        }
    }
    None
}

/// A sync folder the shared watcher reports to.
struct Route {
    root: PathBuf,
    commands: UnboundedSender<MountCommand>,
}

static ROUTES: Mutex<Vec<Route>> = Mutex::new(Vec::new());
static WATCHER: Mutex<Option<Debouncer<RecommendedWatcher, RecommendedCache>>> = Mutex::new(None);

fn route_of(routes: &[Route], path: &Path) -> Option<usize> {
    routes
        .iter()
        .enumerate()
        .filter(|(_, route)| path.starts_with(&route.root))
        .max_by_key(|(_, route)| route.root.as_os_str().len())
        .map(|(index, _)| index)
}

/// Hand the events of one batch to the drives they belong to. A move between two sync folders
/// goes to the drive it left (see `process_rename_linux`).
fn dispatch(result: DebounceEventResult) {
    let events = match result {
        Ok(events) => events,
        Err(errors) => {
            tracing::error!(target: "drive::watch", errors = ?errors, "Failed to watch the sync folders");
            let full = errors.iter().any(|e| {
                matches!(e.kind, notify_debouncer_full::notify::ErrorKind::MaxFilesWatch)
            });
            if full {
                toast::send_warning_toast(&t!("watchLimitTitle"), &t!("watchLimitMessage"));
            }
            return;
        }
    };
    let rescan = events.iter().any(|e| e.need_rescan());
    let events = prepare_events(events);
    note_new_folders(&events);
    let routes = ROUTES.lock().unwrap_or_else(|e| e.into_inner());
    let mut batches: Vec<Vec<DebouncedEvent>> = routes.iter().map(|_| Vec::new()).collect();
    for event in events {
        if let Some(index) = event.paths.first().and_then(|path| route_of(&routes, path)) {
            // Behind a symlink or in another file system inside the folder: not synced.
            let root = &routes[index].root;
            if event
                .paths
                .iter()
                .any(|path| crate::drive::local_linux::is_foreign_below(path, root))
            {
                continue;
            }
            batches[index].push(event);
        }
    }
    for (route, batch) in routes.iter().zip(batches) {
        if rescan {
            // Events were lost: compare the whole folder again.
            let _ = route.commands.send(MountCommand::Sync {
                local_paths: vec![route.root.clone()],
                mode: SyncMode::FullHierarchy,
                user_initiated: false,
            });
        }
        if !batch.is_empty() {
            let _ = route.commands.send(MountCommand::ProcessFsEvents {
                events: group_fs_events(batch),
            });
        }
    }
}

/// Start watching `root` for the drive behind `commands`.
pub fn watch(root: &Path, commands: UnboundedSender<MountCommand>) -> Result<()> {
    let mut watcher = WATCHER.lock().unwrap_or_else(|e| e.into_inner());
    if watcher.is_none() {
        *watcher = Some(notify_debouncer_full::new_debouncer_opt(
            Duration::from_secs(2),
            None,
            dispatch,
            RecommendedCache::new(),
            notify_debouncer_full::notify::Config::default().with_follow_symlinks(false),
        )?);
    }
    ROUTES
        .lock()
        .unwrap_or_else(|e| e.into_inner())
        .push(Route {
            root: root.to_path_buf(),
            commands,
        });
    let watched = watcher
        .as_mut()
        .expect("watcher was just created")
        .watch(root, RecursiveMode::Recursive);
    if watched.is_err() {
        ROUTES
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .retain(|route| route.root != root);
    }
    watched.with_context(|| format!("failed to watch {}", root.display()))
}

/// Stop watching `root`.
pub fn unwatch(root: &Path) {
    PENDING_RENAMES
        .lock()
        .unwrap_or_else(|e| e.into_inner())
        .retain(|(from, _, _)| !from.starts_with(root));
    ROUTES
        .lock()
        .unwrap_or_else(|e| e.into_inner())
        .retain(|route| route.root != root);
    if let Some(watcher) = WATCHER.lock().unwrap_or_else(|e| e.into_inner()).as_mut() {
        let _ = watcher.unwatch(root);
    }
}

/// How the server answered a rename or move.
enum ServerAnswer {
    Done,
    /// The server has no such file (any more): the new name is a new file here.
    Gone,
    /// The new name exists on the server already.
    NameTaken,
    /// Refused (read-only, rights, 40905): the local rename is undone.
    Refused(anyhow::Error),
    /// Not answered (network): tried again later.
    Failed(anyhow::Error),
    /// Moved on the server, but its rename could not be sent (network): the server has the file
    /// under the old name in the new folder.
    MovedNotRenamed(PathBuf),
}

fn classify(error: anyhow::Error) -> ServerAnswer {
    match error.downcast_ref::<ApiError>() {
        Some(ApiError::ApiError { code, .. })
            if *code == ErrorCode::NotFound as i32 || *code == ErrorCode::ParentNotExist as i32 =>
        {
            ServerAnswer::Gone
        }
        Some(ApiError::ApiError { code, .. }) if *code == ErrorCode::ObjectExisted as i32 => {
            ServerAnswer::NameTaken
        }
        Some(ApiError::ApiError { .. }) => ServerAnswer::Refused(error),
        _ => ServerAnswer::Failed(error),
    }
}

impl Mount {
    /// Fylvo: deleted paths that are really moves into a folder made a moment ago (see
    /// `RECENT_FOLDERS`) are handled as renames; the rest is returned.
    pub(crate) async fn resolve_hidden_moves(&self, paths: Vec<PathBuf>) -> Vec<PathBuf> {
        let root = self.config.read().await.sync_path.clone();
        let mut rest = Vec::with_capacity(paths.len());
        for path in paths {
            let moved = self
                .row_at(&path)
                .and_then(|row| crate::drive::local_linux::stored_stamp(&row))
                // Only files: a folder's stamp is its inode alone, which a new folder can get
                // right after an old one was deleted. Folders fall back to delete plus upload.
                .filter(|stamp| stamp.content.is_some())
                .and_then(|stamp| {
                    find_in_new_folders(stamp.inode, &root).filter(|found| {
                        crate::drive::local_linux::LocalStamp::of_path(found)
                            .is_some_and(|now| now.content == stamp.content)
                    })
                });
            match moved {
                Some(to) if to != path => {
                    tracing::info!(target: "drive::watch", from = %path.display(), to = %to.display(), "A deletion is a move into a new folder");
                    if let Err(e) = self.process_rename_linux(path.clone(), to).await {
                        tracing::warn!(target: "drive::watch", path = %path.display(), error = ?e, "Failed to handle the move");
                    }
                }
                _ => rest.push(path),
            }
        }
        rest
    }

    /// Fylvo: the old path of a synced file that is now at `path` (same inode, the old path is
    /// gone), if any. A file found there is a moved one, not a new one.
    pub(crate) fn moved_from(&self, path: &Path) -> Option<PathBuf> {
        let stamp = crate::drive::local_linux::LocalStamp::of_path(path)?;
        let rows = self.inventory.query_by_local_inode(&self.id, stamp.inode).ok()?;
        stamp.content?;
        rows.into_iter()
            .filter(|row| {
                crate::drive::local_linux::stored_stamp(row).is_some_and(|s| s.content == stamp.content)
            })
            .map(|row| PathBuf::from(row.local_path))
            .find(|old| old != path && std::fs::symlink_metadata(old).is_err())
    }

    /// The row at `path`, if any.
    fn row_at(&self, path: &Path) -> Option<FileMetadata> {
        self.inventory.query_by_path(path.to_str()?).ok().flatten()
    }

    /// Whether the server still has the file this row was synced from at `path` (same file
    /// ID). A file another device replaced meanwhile is not renamed, moved or deleted.
    /// `None`: the server could not be asked (network); `Some(false)`: another file is there.
    pub(crate) async fn server_has_row_file(&self, path: &Path, row: &FileMetadata) -> Option<bool> {
        let Some(id) = row.metadata.get(crate::drive::case::FILE_ID_KEY) else {
            return Some(false);
        };
        let (root, base) = {
            let config = self.config.read().await;
            (config.sync_path.clone(), config.remote_path.clone())
        };
        let Ok(uri) = local_path_to_cr_uri(path.to_path_buf(), root, base) else {
            return Some(false);
        };
        match self
            .cr_client
            .get_file_info(&GetFileInfoService {
                uri: Some(uri.to_string()),
                id: None,
                extended: None,
                folder_summary: None,
            })
            .await
        {
            Ok(file) => Some(!id.is_empty() && file.id == *id),
            Err(err) => match classify(err.into()) {
                ServerAnswer::Failed(_) => None,
                _ => Some(false),
            },
        }
    }

    /// Rename or move `from` to `to` on the server (a move keeps the name, so a move with a
    /// new name is a move and then a rename).
    async fn rename_on_server(&self, from: &Path, to: &Path) -> ServerAnswer {
        let (root, base) = {
            let config = self.config.read().await;
            (config.sync_path.clone(), config.remote_path.clone())
        };
        let uri = |path: &Path| {
            local_path_to_cr_uri(path.to_path_buf(), root.clone(), base.clone())
                .map(|uri| uri.to_string())
        };
        let (Some(from_parent), Some(to_parent), Some(to_name)) =
            (from.parent(), to.parent(), to.file_name())
        else {
            return ServerAnswer::Failed(anyhow::anyhow!("a sync folder cannot be renamed"));
        };
        let to_name = to_name.to_string_lossy().to_string();
        let mut current = from.to_path_buf();
        if from_parent != to_parent {
            let (Ok(source), Ok(target)) = (uri(from), uri(to_parent)) else {
                return ServerAnswer::Failed(anyhow::anyhow!("path outside the sync folder"));
            };
            let request = MoveFileService {
                uris: vec![source],
                dst: target.clone(),
                copy: None,
            };
            let mut moved = self.cr_client.move_files(&request).await;
            // The target folder is new here and not on the server yet (a folder made for a
            // selection): created there, then moved.
            if let Err(ApiError::ApiError { code, .. }) = &moved {
                if *code == ErrorCode::ParentNotExist as i32 || *code == ErrorCode::NotFound as i32 {
                    let created = self
                        .cr_client
                        .create_file(&cloudreve_api::models::explorer::CreateFileService {
                            uri: target,
                            file_type: "folder".to_string(),
                            err_on_conflict: Some(false),
                            metadata: None,
                        })
                        .await;
                    if created.is_ok() {
                        moved = self.cr_client.move_files(&request).await;
                    }
                }
            }
            if let Err(err) = moved {
                return classify(err.into());
            }
            current = to_parent.join(from.file_name().unwrap_or_default());
        }
        if current != to {
            let Ok(source) = uri(&current) else {
                return ServerAnswer::Failed(anyhow::anyhow!("path outside the sync folder"));
            };
            let renamed = self
                .cr_client
                .rename_file(&RenameFileService {
                    uri: source,
                    new_name: to_name,
                })
                .await;
            if let Err(err) = renamed {
                let err: anyhow::Error = err.into();
                // Not an answer of the server (network): see `classify`.
                let unanswered = !matches!(err.downcast_ref::<ApiError>(), Some(ApiError::ApiError { .. }));
                if current != from && unanswered {
                    return ServerAnswer::MovedNotRenamed(current);
                }
                if current != from {
                    // Moved, but not renamed: the local file takes the name the server has.
                    register_own_move(to, &current);
                    if move_no_replace(to, &current).is_err() {
                        forget_own_move(to, &current);
                        return ServerAnswer::Failed(anyhow::anyhow!(
                            "moved on the server, but neither renamed there nor here"
                        ));
                    }
                    let _ = self.inventory.rename_path(
                        from.to_str().unwrap_or_default(),
                        current.to_str().unwrap_or_default(),
                    );
                    crate::drive::case::report_name_case_taken(to, &err);
                    return ServerAnswer::Done;
                }
                return classify(err);
            }
        }
        ServerAnswer::Done
    }

    /// Keep a rename the server could not be asked about (network): the sync passes leave both
    /// names alone meanwhile (`rename_settled`), and the rename is handled again in a minute.
    fn retry_rename_later(&self, from: PathBuf, to: PathBuf) {
        {
            let inode = crate::drive::local_linux::LocalStamp::of_path(&to).map(|s| s.inode);
            let mut pending = PENDING_RENAMES.lock().unwrap_or_else(|e| e.into_inner());
            if !pending.iter().any(|(f, t, _)| *f == from && *t == to) {
                pending.push((from.clone(), to.clone(), inode));
            }
        }
        let commands = self.command_tx.clone();
        tokio::spawn(async move {
            tokio::time::sleep(RENAME_RETRY).await;
            let event = notify_debouncer_full::notify::Event::new(EventKind::Modify(
                ModifyKind::Name(RenameMode::Both),
            ))
            .add_path(from)
            .add_path(to);
            let _ = commands.send(MountCommand::ProcessFsEvents {
                events: group_fs_events(vec![DebouncedEvent::new(event, Instant::now())]),
            });
        });
    }

    /// Undo a local rename the server refused (the Windows app refuses it before it happens).
    fn move_back(&self, from: &Path, to: &Path) -> bool {
        register_own_move(to, from);
        match move_no_replace(to, from) {
            Ok(()) => true,
            Err(err) => {
                forget_own_move(to, from);
                tracing::warn!(target: "drive::watch", from = %to.display(), to = %from.display(), error = ?err, "Could not undo the rename");
                let _ = self.command_tx.send(MountCommand::Sync {
                    local_paths: vec![from.to_path_buf(), to.to_path_buf()],
                    mode: SyncMode::PathOnly,
                    user_initiated: false,
                });
                false
            }
        }
    }

    /// A change of `path` to send: an upload, or for a folder a full sync (uploads its tree).
    pub(crate) async fn upload_path(&self, path: &Path) {
        if path.is_dir() {
            let _ = self.command_tx.send(MountCommand::Sync {
                local_paths: vec![path.to_path_buf()],
                mode: SyncMode::FullHierarchy,
                user_initiated: false,
            });
        } else if let Err(err) = self.enqueue_upload(TaskPayload::upload(path.to_path_buf())).await {
            tracing::warn!(target: "drive::watch", path = %path.display(), error = ?err, "Failed to enqueue upload task");
        }
    }

    /// A user rename or move, seen after it happened. See the table in the plan.
    pub(crate) async fn process_rename_linux(&self, from: PathBuf, to: PathBuf) -> Result<()> {
        // A retry is re-registered by `retry_rename_later` when it is needed again. A retry is
        // only about the file it was made for: moved on or back meanwhile, the sync of both
        // names decides instead.
        if let Some(inode) = finish_pending_rename(&from, &to) {
            let now = crate::drive::local_linux::LocalStamp::of_path(&to).map(|s| s.inode);
            if inode.is_none() || now != inode || std::fs::symlink_metadata(&from).is_ok() {
                let _ = self.command_tx.send(MountCommand::Sync {
                    local_paths: vec![from, to],
                    mode: SyncMode::PathOnly,
                    user_initiated: false,
                });
                return Ok(());
            }
        }
        let root = self.config.read().await.sync_path.clone();
        let matcher = self.ignore_matcher.read().await;
        let (from_ignored, to_ignored) = (matcher.is_match(&from), matcher.is_match(&to));
        drop(matcher);
        let from_row = if from_ignored { None } else { self.row_at(&from) };

        // Into another sync folder: refused as on Windows, copying works.
        if !to.starts_with(&root) {
            toast::send_warning_toast(&t!("moveBetweenDrivesTitle"), &t!("moveBetweenDrivesMessage"));
            if !self.move_back(&from, &to) {
                // Could not be undone: a deletion here, a new file there.
                self.remove_paths(vec![from]).await;
                crate::drive::watch_linux::sync_path_anywhere(&to);
            }
            return Ok(());
        }
        // Put in place from a temp name, or something unknown renamed: a change of the target.
        let Some(from_row) = from_row else {
            if !to_ignored {
                self.upload_path(&to).await;
            }
            return Ok(());
        };
        // Moved to an ignored name (vim's backup, the trash at the top of a mount): a deletion.
        if to_ignored {
            self.remove_paths(vec![from]).await;
            return Ok(());
        }
        // Over a synced file: that file has new content, the old name is gone.
        if self.row_at(&to).is_some() {
            self.upload_path(&to).await;
            self.remove_paths(vec![from]).await;
            return Ok(());
        }

        let _ = self.task_queue.cancel_by_path(from.clone()).await;
        let _ = self.task_queue.cancel_by_path(to.clone()).await;
        if self.is_read_only() {
            self.refuse_read_only_change(&to).await;
            self.move_back(&from, &to);
            return Ok(());
        }
        match self.server_has_row_file(&from, &from_row).await {
            Some(true) => {}
            Some(false) => {
                // Replaced on the server meanwhile: keep its file, this one is new.
                self.upload_path(&to).await;
                return Ok(());
            }
            None => {
                self.retry_rename_later(from, to);
                return Ok(());
            }
        }
        match self.rename_on_server(&from, &to).await {
            ServerAnswer::Done => {
                if self.row_at(&from).is_some() {
                    self.inventory
                        .rename_path(
                            from.to_str().context("path is not UTF-8")?,
                            to.to_str().context("path is not UTF-8")?,
                        )
                        .context("failed to rename in inventory")?;
                }
                // Changed while it was renamed: the change goes up under the new name.
                if !to.is_dir() {
                    let info = crate::drive::local::LocalFileInfo::from_path(&to)?;
                    if info.exists && !info.in_sync() {
                        self.upload_path(&to).await;
                    }
                }
            }
            ServerAnswer::Gone => self.upload_path(&to).await,
            ServerAnswer::NameTaken => {
                // The server has another file under the new name: undone here, the server's
                // file comes down with the next sync.
                self.move_back(&from, &to);
                toast::send_warning_toast(&t!("renameRefusedTitle"), &t!("renameRefusedMessage"));
                let _ = self.command_tx.send(MountCommand::Sync {
                    local_paths: vec![to.clone()],
                    mode: SyncMode::PathOnly,
                    user_initiated: false,
                });
            }
            ServerAnswer::Refused(err) => {
                tracing::info!(target: "drive::watch", from = %from.display(), to = %to.display(), error = ?err, "Server refused the rename, undoing it here");
                self.move_back(&from, &to);
                if crate::drive::case::is_name_case_taken(&err) {
                    // The existing 40905 message, once per run.
                    crate::drive::case::report_name_case_taken(&to, &err);
                } else {
                    toast::send_warning_toast(&t!("renameRefusedTitle"), &t!("renameRefusedMessage"));
                }
            }
            ServerAnswer::Failed(err) => {
                tracing::warn!(target: "drive::watch", from = %from.display(), to = %to.display(), error = ?err, "Rename not sent, trying again later");
                self.retry_rename_later(from, to);
            }
            ServerAnswer::MovedNotRenamed(current) => {
                tracing::warn!(target: "drive::watch", from = %from.display(), to = %to.display(), "Moved on the server, its rename is tried again later");
                self.inventory
                    .rename_path(
                        from.to_str().context("path is not UTF-8")?,
                        current.to_str().context("path is not UTF-8")?,
                    )
                    .context("failed to rename in inventory")?;
                self.retry_rename_later(current, to);
            }
        }
        Ok(())
    }
}

/// Ask the drive that owns `path` to sync it (after a move into it that could not be undone).
pub(crate) fn sync_path_anywhere(path: &Path) {
    let routes = ROUTES.lock().unwrap_or_else(|e| e.into_inner());
    if let Some(index) = route_of(&routes, path) {
        let _ = routes[index].commands.send(MountCommand::Sync {
            local_paths: vec![path.to_path_buf()],
            mode: SyncMode::FullHierarchy,
            user_initiated: false,
        });
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use notify_debouncer_full::notify::Event;

    fn event(kind: EventKind, paths: &[&str]) -> DebouncedEvent {
        let mut event = Event::new(kind);
        for path in paths {
            event = event.add_path(PathBuf::from(path));
        }
        DebouncedEvent::new(event, Instant::now())
    }

    fn kinds(events: &[DebouncedEvent]) -> Vec<EventKind> {
        events.iter().map(|e| e.kind).collect()
    }

    #[test]
    fn bare_halves_become_deletions_and_creations() {
        let out = prepare_events(vec![
            event(EventKind::Modify(ModifyKind::Name(RenameMode::From)), &["/t1/S/a.txt"]),
            event(EventKind::Modify(ModifyKind::Name(RenameMode::To)), &["/t1/S/b.txt"]),
        ]);
        assert_eq!(
            kinds(&out),
            [EventKind::Remove(RemoveKind::Any), EventKind::Create(CreateKind::Any)]
        );
    }

    #[test]
    fn own_moves_are_dropped_whole_and_only_once() {
        register_own_move(Path::new("/t2/S/a"), Path::new("/t2/S/~fylvo-backup-a.tmp"));
        register_own_move(Path::new("/t2/S/x"), Path::new("/t2/S/y"));
        let rename = || {
            event(
                EventKind::Modify(ModifyKind::Name(RenameMode::Both)),
                &["/t2/S/a", "/t2/S/~fylvo-backup-a.tmp"],
            )
        };
        assert!(prepare_events(vec![rename()]).is_empty());
        // Used up: the same rename again is the user's.
        assert_eq!(prepare_events(vec![rename()]).len(), 1);

        // A lone half is the user's even if a registered move names the path.
        let from = event(EventKind::Modify(ModifyKind::Name(RenameMode::From)), &["/t2/S/x"]);
        assert_eq!(kinds(&prepare_events(vec![from])), [EventKind::Remove(RemoveKind::Any)]);
    }

    #[test]
    fn a_joined_chain_of_own_moves_is_dropped() {
        register_own_move(Path::new("/t4/S/a"), Path::new("/t4/S/~fylvo-backup-a.tmp"));
        register_own_move(Path::new("/t4/S/~fylvo-backup-a.tmp"), Path::new("/t4/S/__conflict__a"));
        let joined = event(
            EventKind::Modify(ModifyKind::Name(RenameMode::Both)),
            &["/t4/S/a", "/t4/S/__conflict__a"],
        );
        assert!(prepare_events(vec![joined]).is_empty());
    }

    #[test]
    fn a_forgotten_own_move_swallows_nothing() {
        register_own_move(Path::new("/t3/S/a"), Path::new("/t3/S/b"));
        forget_own_move(Path::new("/t3/S/a"), Path::new("/t3/S/b"));
        let from = event(EventKind::Modify(ModifyKind::Name(RenameMode::From)), &["/t3/S/a"]);
        assert_eq!(kinds(&prepare_events(vec![from])), [EventKind::Remove(RemoveKind::Any)]);
    }

    #[test]
    fn events_go_to_the_innermost_sync_folder() {
        let (tx, _rx) = tokio::sync::mpsc::unbounded_channel();
        let routes = vec![
            Route { root: PathBuf::from("/h/Fylvo"), commands: tx.clone() },
            Route { root: PathBuf::from("/h/Fylvo2"), commands: tx },
        ];
        assert_eq!(route_of(&routes, Path::new("/h/Fylvo2/a")), Some(1));
        assert_eq!(route_of(&routes, Path::new("/h/Fylvo/a")), Some(0));
        assert_eq!(route_of(&routes, Path::new("/h/other")), None);
    }
}

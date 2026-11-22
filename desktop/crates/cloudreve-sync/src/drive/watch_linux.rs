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

/// Which part of a move an event reports.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum Half {
    From,
    To,
    Both,
}

/// Whether an event is (part of) a registered own move; consumes what it matches.
fn is_own(half: Half, from: Option<&Path>, to: Option<&Path>) -> bool {
    let mut moves = own_moves();
    let found = moves.iter_mut().find(|m| match half {
        Half::Both => Some(m.from.as_path()) == from && Some(m.to.as_path()) == to,
        Half::From => !m.from_seen && Some(m.from.as_path()) == from,
        Half::To => !m.to_seen && Some(m.to.as_path()) == to,
    });
    match found {
        Some(m) => {
            match half {
                Half::Both => {
                    m.from_seen = true;
                    m.to_seen = true;
                }
                Half::From => m.from_seen = true,
                Half::To => m.to_seen = true,
            }
            true
        }
        None => false,
    }
}

/// Drop the app's own moves and turn bare halves of moves into deletions and creations.
pub(crate) fn prepare_events(events: Vec<DebouncedEvent>) -> Vec<DebouncedEvent> {
    let mut out = Vec::with_capacity(events.len());
    for mut event in events {
        match event.kind {
            EventKind::Modify(ModifyKind::Name(RenameMode::Both)) if event.paths.len() == 2 => {
                if is_own(Half::Both, Some(&event.paths[0]), Some(&event.paths[1])) {
                    continue;
                }
            }
            EventKind::Modify(ModifyKind::Name(RenameMode::From)) if event.paths.len() == 1 => {
                if is_own(Half::From, Some(&event.paths[0]), None) {
                    continue;
                }
                event.event.kind = EventKind::Remove(RemoveKind::Any);
            }
            EventKind::Modify(ModifyKind::Name(RenameMode::To)) if event.paths.len() == 1 => {
                if is_own(Half::To, None, Some(&event.paths[0])) {
                    continue;
                }
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

/// Paths a sync pass found as a possible half of a rename, with when it first saw them.
static UNSETTLED: Mutex<Vec<(PathBuf, Instant)>> = Mutex::new(Vec::new());

/// Whether a sync pass may act now on `path`, which may be half of a rename: "on the server,
/// missing here, row present" or "here, no row". The first time it is not; the path is planned
/// again a little later, after the watcher had its say.
pub(crate) fn rename_settled(path: &Path, commands: &UnboundedSender<MountCommand>) -> bool {
    let mut unsettled = UNSETTLED.lock().unwrap_or_else(|e| e.into_inner());
    unsettled.retain(|(_, at)| at.elapsed() < RENAME_SETTLE * 6);
    if let Some(index) = unsettled.iter().position(|(known, _)| known == path) {
        if unsettled[index].1.elapsed() >= RENAME_SETTLE {
            unsettled.remove(index);
            return true;
        }
        return false;
    }
    unsettled.push((path.to_path_buf(), Instant::now()));
    let commands = commands.clone();
    let path = path.to_path_buf();
    tokio::spawn(async move {
        tokio::time::sleep(RENAME_SETTLE).await;
        let _ = commands.send(MountCommand::Sync {
            local_paths: vec![path],
            mode: SyncMode::PathOnly,
            user_initiated: false,
        });
    });
    false
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
    let routes = ROUTES.lock().unwrap_or_else(|e| e.into_inner());
    let mut batches: Vec<Vec<DebouncedEvent>> = routes.iter().map(|_| Vec::new()).collect();
    for event in events {
        if let Some(index) = event.paths.first().and_then(|path| route_of(&routes, path)) {
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
        *watcher = Some(notify_debouncer_full::new_debouncer(
            Duration::from_secs(2),
            None,
            dispatch,
        )?);
    }
    ROUTES
        .lock()
        .unwrap_or_else(|e| e.into_inner())
        .push(Route {
            root: root.to_path_buf(),
            commands,
        });
    watcher
        .as_mut()
        .expect("watcher was just created")
        .watch(root, RecursiveMode::Recursive)
        .with_context(|| format!("failed to watch {}", root.display()))
}

/// Stop watching `root`.
pub fn unwatch(root: &Path) {
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
    /// Not answered (network): the next sync sorts it out.
    Failed(anyhow::Error),
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
    /// The row at `path`, if any.
    fn row_at(&self, path: &Path) -> Option<FileMetadata> {
        self.inventory.query_by_path(path.to_str()?).ok().flatten()
    }

    /// Whether the server still has the file this row was synced from at `path` (same file
    /// ID). A file another device replaced meanwhile is not renamed, moved or deleted.
    pub(crate) async fn server_has_row_file(&self, path: &Path, row: &FileMetadata) -> bool {
        let Some(id) = row.metadata.get(crate::drive::case::FILE_ID_KEY) else {
            return false;
        };
        let (root, base) = {
            let config = self.config.read().await;
            (config.sync_path.clone(), config.remote_path.clone())
        };
        let Ok(uri) = local_path_to_cr_uri(path.to_path_buf(), root, base) else {
            return false;
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
            Ok(file) => !id.is_empty() && file.id == *id,
            Err(_) => false,
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
            let moved = self
                .cr_client
                .move_files(&MoveFileService {
                    uris: vec![source],
                    dst: target,
                    copy: None,
                })
                .await;
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
                if current != from {
                    // Moved, but not renamed: the local file takes the name the server has.
                    register_own_move(to, &current);
                    if move_no_replace(to, &current).is_ok() {
                        let _ = self.inventory.rename_path(
                            from.to_str().unwrap_or_default(),
                            current.to_str().unwrap_or_default(),
                        );
                    } else {
                        forget_own_move(to, &current);
                    }
                    crate::drive::case::report_name_case_taken(to, &err.into());
                    return ServerAnswer::Done;
                }
                return classify(err.into());
            }
        }
        ServerAnswer::Done
    }

    /// Undo a local rename the server refused (the Windows app refuses it before it happens).
    fn move_back(&self, from: &Path, to: &Path) -> bool {
        register_own_move(to, from);
        match move_no_replace(to, from) {
            Ok(()) => true,
            Err(err) => {
                forget_own_move(to, from);
                tracing::warn!(target: "drive::watch", from = %to.display(), to = %from.display(), error = ?err, "Could not undo the rename");
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
        if !self.server_has_row_file(&from, &from_row).await {
            // Replaced on the server meanwhile: keep its file, this one is new.
            self.upload_path(&to).await;
            return Ok(());
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
                self.upload_path(&to).await;
                self.remove_paths(vec![from]).await;
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
                tracing::warn!(target: "drive::watch", from = %from.display(), to = %to.display(), error = ?err, "Rename not sent, syncing both names later");
                let _ = self.command_tx.send(MountCommand::Sync {
                    local_paths: vec![from.clone(), to.clone()],
                    mode: SyncMode::PathOnly,
                    user_initiated: false,
                });
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
    fn own_moves_are_dropped_also_in_halves_and_only_once() {
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

        let from = event(EventKind::Modify(ModifyKind::Name(RenameMode::From)), &["/t2/S/x"]);
        let to = event(EventKind::Modify(ModifyKind::Name(RenameMode::To)), &["/t2/S/y"]);
        assert!(prepare_events(vec![from, to]).is_empty());
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

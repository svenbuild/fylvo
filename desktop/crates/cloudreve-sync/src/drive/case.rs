//! Fylvo: names that are one file on Windows.
//!
//! NTFS ignores upper and lower case: `foto.jpg` and `Foto.jpg` are the same file. The server
//! and the rest of the client compare names byte by byte. Without this module a case-only
//! rename on the server looked like "delete `foto.jpg`, create `Foto.jpg`", and the delete hit
//! the very file the new name points to. Two server names that differ only in case cannot
//! both exist in one folder here.
//!
//! Unicode forms (composed `ü` and `u` + combining mark) are not touched: NTFS keeps them apart
//! as two names, and so does the client.

use crate::{
    cfapi::placeholder::{LocalFileInfo, OpenOptions},
    drive::{
        commands::MountCommand, guard::is_under_any_case, mounts::Mount, sync::SyncMode,
        utils::move_no_replace,
    },
    inventory::{ConflictState, TaskStatus},
    tasks::{TaskKind, TaskPayload},
};
use anyhow::{Context, Result};
use chrono::{DateTime, Utc};
use cloudreve_api::{
    ApiError,
    error::ErrorCode,
    models::explorer::{FileResponse, file_type},
};
use std::{
    collections::{BTreeMap, HashMap, HashSet},
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    time::{Duration, Instant},
};
use windows::Win32::{
    Foundation::LPARAM,
    Globalization::{LCMAP_UPPERCASE, LCMapStringEx, LOCALE_NAME_INVARIANT},
};

/// A name as Windows compares file names: upper case with the system's invariant table (one
/// UTF-16 unit to one, so `ü` = `Ü` but `ß` stays `ß`, not `SS`). No Unicode normalisation.
pub fn ntfs_fold(name: &str) -> String {
    let wide: Vec<u16> = name.encode_utf16().collect();
    if wide.is_empty() {
        return String::new();
    }
    let mut upper = vec![0u16; wide.len()];
    let written = unsafe {
        LCMapStringEx(
            LOCALE_NAME_INVARIANT,
            LCMAP_UPPERCASE,
            &wide,
            Some(&mut upper),
            None,
            None,
            LPARAM(0),
        )
    };
    if written <= 0 {
        // Not expected; compare exactly then, as before this module.
        return name.to_string();
    }
    String::from_utf16_lossy(&upper[..written as usize])
}

/// A SQL `LIKE` pattern (escape character `!`) that matches every spelling of `text` that may be
/// the same on NTFS, and some more: SQLite's `LIKE` ignores the case of ASCII letters only, so
/// every other character, and `i` and `s` (`ı` and `ſ` are upper case `I` and `S`), match any one
/// character. Check the hits with `same_on_ntfs`.
pub(crate) fn ntfs_like_pattern(text: &str) -> String {
    let mut pattern = String::with_capacity(text.len());
    for c in text.chars() {
        match c {
            '%' | '_' | '!' => {
                pattern.push('!');
                pattern.push(c);
            }
            c if !c.is_ascii() || "iIsS".contains(c) => pattern.push('_'),
            c => pattern.push(c),
        }
    }
    pattern
}

/// Whether two paths are the same file on NTFS.
pub fn same_on_ntfs(a: &Path, b: &Path) -> bool {
    ntfs_fold(&a.to_string_lossy()) == ntfs_fold(&b.to_string_lossy())
}

/// Folded names that occur more than once among `names` (server entries of one folder).
pub fn case_twins<'a>(names: impl Iterator<Item = &'a str>) -> HashSet<String> {
    let mut seen = HashSet::new();
    let mut twins = HashSet::new();
    for name in names {
        let key = ntfs_fold(name);
        if !seen.insert(key.clone()) {
            twins.insert(key);
        }
    }
    twins
}

/// The first folder between `parent` (included) and `sync_root` (excluded) that exists on disk
/// spelled differently. A sync under it would read the server folder of one spelling and
/// change the local folder of the other.
pub(crate) fn case_mismatched_ancestor(
    parent: &Path,
    sync_root: &Path,
    on_disk: impl Fn(&Path) -> Option<PathBuf>,
) -> Option<PathBuf> {
    let mut current = parent;
    while current.starts_with(sync_root) && current != sync_root {
        if let Some(disk) = on_disk(current) {
            if disk != current {
                return Some(current.to_path_buf());
            }
        }
        current = current.parent()?;
    }
    None
}

/// Whether `path` or a folder above it (up to `sync_root`) exists on disk spelled differently.
/// A transfer under such a path would reach the server under a name it does not have.
pub fn spelled_differently_on_disk(path: &Path, sync_root: &Path) -> bool {
    case_mismatched_ancestor(path, sync_root, LocalFileInfo::on_disk_path).is_some()
}

/// `path` with every name below `sync_root` spelled as on disk (names not on disk unchanged).
pub fn disk_spelling(path: &Path, sync_root: &Path) -> PathBuf {
    let Ok(relative) = path.strip_prefix(sync_root) else {
        return path.to_path_buf();
    };
    let mut spelled = sync_root.to_path_buf();
    for part in relative.components() {
        let next = spelled.join(part);
        spelled = LocalFileInfo::on_disk_path(&next).unwrap_or(next);
    }
    spelled
}

/// A case rename that has to wait (a transfer runs, a deletion waits for the user's answer).
/// It is tried again a little later.
#[derive(Debug)]
struct RetryLater(&'static str);

impl std::fmt::Display for RetryLater {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(self.0)
    }
}

impl std::error::Error for RetryLater {}

/// How long a waiting case rename waits before it is tried again.
const RETRY_DELAY: Duration = Duration::from_secs(60);

/// What a sync pass does about names that are one file here.
#[derive(Debug, Default, PartialEq)]
pub(crate) struct CasePlan {
    /// The paths to plan, at most one per local file.
    pub paths: Vec<PathBuf>,
    /// Local renames (on-disk name, server name) to do before planning.
    pub renames: Vec<(PathBuf, PathBuf)>,
    /// Server entries left on the server: another server name with the same local file is
    /// synced instead.
    pub hidden: Vec<PathBuf>,
    /// Names where the local file may be a different object than the server entry with that
    /// name. Nothing is done for them (no rename, no delete, no upload, no download).
    pub held: Vec<PathBuf>,
}

/// Key of the server's file ID in the inventory's metadata map (written by Fylvo, see
/// `with_remote_file` and `cloud_file_to_metadata_entry`).
pub const FILE_ID_KEY: &str = "fylvo:file_id";

/// What the inventory knows about a path.
#[derive(Debug, Clone)]
pub(crate) struct KnownEntry {
    pub is_folder: bool,
    /// The server's creation time.
    pub created_at: i64,
    pub etag: String,
    /// The server's file ID, if the row was written by a Fylvo version that stores it.
    pub file_id: Option<String>,
}

/// Decide the case questions of one folder. `requested` are the paths a sync pass was asked for,
/// `remote` the server entries found for them (keyed by the server's own name), `on_disk`
/// returns the path as it is spelled on disk if something exists there, `known` the
/// inventory row stored under exactly that path.
pub(crate) fn plan_case(
    requested: &[PathBuf],
    remote: &HashMap<PathBuf, FileResponse>,
    on_disk: impl Fn(&Path) -> Option<PathBuf>,
    known: impl Fn(&Path) -> Option<KnownEntry>,
) -> CasePlan {
    #[derive(Default)]
    struct Group {
        requested: Vec<PathBuf>,
        remote: Vec<PathBuf>,
    }
    let mut groups: BTreeMap<String, Group> = BTreeMap::new();
    for path in requested {
        let group = groups.entry(ntfs_fold(&path.to_string_lossy())).or_default();
        if !group.requested.contains(path) {
            group.requested.push(path.clone());
        }
    }
    for path in remote.keys() {
        groups
            .entry(ntfs_fold(&path.to_string_lossy()))
            .or_default()
            .remote
            .push(path.clone());
    }

    let mut plan = CasePlan::default();
    for (_, mut group) in groups {
        group.remote.sort();
        let any = group.requested.first().or(group.remote.first()).unwrap();
        let disk = on_disk(any);

        if group.remote.is_empty() {
            // Nothing on the server: plan the file as it is on disk.
            plan.paths.push(disk.unwrap_or_else(|| any.clone()));
            continue;
        }

        let keeper = match &disk {
            // Nothing here yet: the first server name gets the local file.
            None => group.remote[0].clone(),
            // The disk already has one of the server names: that one stays.
            Some(disk) if group.remote.contains(disk) => disk.clone(),
            // The disk spells it differently: rename only if it is surely the same object.
            Some(disk) => {
                let server = &group.remote[0];
                if group.remote.len() == 1 && is_same_object(disk, server, &remote[server], &known)
                {
                    plan.renames.push((disk.clone(), server.clone()));
                    server.clone()
                } else {
                    plan.held.extend(group.remote.iter().cloned());
                    continue;
                }
            }
        };
        plan.hidden
            .extend(group.remote.iter().filter(|p| **p != keeper).cloned());
        plan.paths.push(keeper);
    }
    plan
}

/// Whether the local file at `disk` represents the server entry `server` (same folder, other
/// spelling). Exactly one of the two names must have an inventory row, and it must carry the
/// server entry's file ID. A row without ID (older data) counts only for a file with the same
/// creation time and content version (etag); a folder without ID is never assumed the same.
fn is_same_object(
    disk: &Path,
    server: &Path,
    remote: &FileResponse,
    known: &impl Fn(&Path) -> Option<KnownEntry>,
) -> bool {
    let row = match (known(disk), known(server)) {
        (Some(row), None) | (None, Some(row)) => row,
        _ => return false,
    };
    let remote_is_folder = remote.file_type == file_type::FOLDER;
    if row.is_folder != remote_is_folder {
        return false;
    }
    if let Some(id) = &row.file_id {
        return !id.is_empty() && *id == remote.id;
    }
    let created = remote
        .created_at
        .parse::<DateTime<Utc>>()
        .map(|time| time.timestamp());
    !remote_is_folder
        && created.is_ok_and(|time| time == row.created_at)
        && !row.etag.is_empty()
        && remote.primary_entity.as_deref() == Some(row.etag.as_str())
}

/// How long a registered own rename waits for its callbacks.
const OWN_RENAME_TTL: Duration = Duration::from_secs(60);

/// Renames the app makes itself (to the server's spelling of a name). The cfapi rename
/// callbacks pass them without telling the server: the server has the new name already.
#[derive(Clone, Default)]
pub struct OwnRenames(Arc<Mutex<Vec<(PathBuf, PathBuf, Instant)>>>);

impl OwnRenames {
    pub fn register(&self, from: &Path, to: &Path) {
        let mut renames = self.0.lock().unwrap();
        renames.retain(|(_, _, at)| at.elapsed() < OWN_RENAME_TTL);
        renames.push((from.to_path_buf(), to.to_path_buf(), Instant::now()));
    }

    /// Whether renaming `source` to `target` is a registered own rename. `consume` removes it
    /// (the rename completed or failed).
    pub fn matches(&self, source: &Path, target: &Path, consume: bool) -> bool {
        let mut renames = self.0.lock().unwrap();
        let found = renames.iter().position(|(from, to, at)| {
            at.elapsed() < OWN_RENAME_TTL
                && same_on_ntfs(source, from)
                && same_on_ntfs(target, to)
                && target.file_name() == to.file_name()
        });
        match found {
            Some(index) if consume => {
                renames.remove(index);
                true
            }
            Some(_) => true,
            None => false,
        }
    }
}

/// Mark a placeholder in sync if none of its bytes is on disk. Checked and marked through one
/// exclusive handle, so no write can come in between.
async fn mark_in_sync_if_empty(path: &Path) -> Result<bool> {
    let mut handle = OpenOptions::new()
        .write_access()
        .exclusive()
        .open_with_retry(path)
        .await?;
    let empty = handle
        .info()?
        .is_some_and(|info| info.on_disk_data_size() == 0 && info.modified_data_size() == 0);
    if empty {
        handle.mark_in_sync(true, None)?;
    }
    Ok(empty)
}

impl Mount {
    /// Apply the case decisions of one folder before it is planned. Returns the paths to plan;
    /// paths whose rename failed are left out of this pass.
    pub(crate) async fn resolve_case(
        &self,
        paths: &[PathBuf],
        remote: &HashMap<PathBuf, FileResponse>,
    ) -> (Vec<PathBuf>, Vec<(PathBuf, anyhow::Error)>) {
        let inventory = self.inventory.clone();
        let plan = plan_case(paths, remote, LocalFileInfo::on_disk_path, |path| {
            let row = inventory.query_by_path(path.to_str()?).ok()??;
            Some(KnownEntry {
                is_folder: row.is_folder,
                created_at: row.created_at,
                file_id: row.metadata.get(FILE_ID_KEY).cloned(),
                etag: row.etag,
            })
        });

        let mut errors = Vec::new();
        let mut failed: HashSet<PathBuf> = HashSet::new();
        let matcher = self.ignore_matcher.read().await;
        let renames: Vec<(PathBuf, PathBuf)> = plan
            .renames
            .into_iter()
            .filter(|(from, to)| {
                // An ignored spelling is not synced at all: no rename, nothing planned.
                let ignored = matcher.is_match(from) || matcher.is_match(to);
                if ignored {
                    failed.insert(to.clone());
                }
                !ignored
            })
            .collect();
        drop(matcher);
        for (from, to) in &renames {
            if let Err(err) = self.rename_local_case(from, to).await {
                // One waiting retry per name, however many passes run into it.
                if err.is::<RetryLater>() && self.case_retries.lock().unwrap().insert(to.clone()) {
                    let command_tx = self.command_tx.clone();
                    let retries = self.case_retries.clone();
                    let to = to.clone();
                    tokio::spawn(async move {
                        tokio::time::sleep(RETRY_DELAY).await;
                        retries.lock().unwrap().remove(&to);
                        let _ = command_tx.send(MountCommand::Sync {
                            local_paths: vec![to],
                            mode: SyncMode::PathOnly,
                            user_initiated: false,
                        });
                    });
                }
                tracing::warn!(target: "drive::case", id = %self.id, from = %from.display(), to = %to.display(), error = ?err, "Case rename not done now, tried again later");
                failed.insert(to.clone());
                errors.push((from.clone(), err));
            } else {
                tracing::info!(target: "drive::case", id = %self.id, from = %from.display(), to = %to.display(), "Renamed local file to the server's spelling");
            }
        }
        report_case_clash(&plan.hidden, "caseClashMessage");
        report_case_clash(&plan.held, "caseHeldMessage");

        let paths = plan
            .paths
            .into_iter()
            .filter(|path| !failed.contains(path))
            .collect();
        (paths, errors)
    }

    /// Rename a local file or folder to the server's spelling of the same name, with its
    /// inventory rows. Never replaces another file.
    async fn rename_local_case(&self, from: &Path, to: &Path) -> Result<()> {
        let from_str = from.to_str().context("path is not UTF-8")?;
        let to_str = to.to_str().context("path is not UTF-8")?;
        // Inventory rows under both spellings: unclear which is right, leave it.
        if self.inventory.has_rows_at_or_under(from_str)?
            && self.inventory.has_rows_at_or_under(to_str)?
        {
            report_case_clash(&[to.to_path_buf()], "caseHeldMessage");
            anyhow::bail!("inventory rows exist under both spellings");
        }
        // A held mass deletion or a kept path under it is keyed by the old spelling: rename
        // only after the user answered.
        if self.guard_touches(from) {
            return Err(RetryLater("a held deletion waits for the user's answer").into());
        }
        let info = LocalFileInfo::from_path(from)?;
        // Only what the sync itself placed here: a plain file or folder with this name was put
        // here by someone else (for example a replacement made while the app was closed).
        if !info.is_placeholder() {
            report_case_clash(&[to.to_path_buf()], "caseHeldMessage");
            anyhow::bail!("not a synced item, left as it is");
        }

        // Tasks under the old spelling would reach the server under the old name: stop them
        // before the rename and queue them again under the name that is on disk afterwards.
        let tasks = self.stop_tasks_under(from).await?;

        self.own_renames.register(from, to);
        if let Err(err) = move_no_replace(from, to) {
            self.own_renames.matches(from, to, true);
            self.queue_tasks_again(&tasks, from, from).await;
            return Err(err);
        }
        if let Err(err) = self.inventory.rename_path(from_str, to_str) {
            self.own_renames.register(to, from);
            if let Err(undo) = move_no_replace(to, from) {
                self.own_renames.matches(to, from, true);
                tracing::error!(target: "drive::case", id = %self.id, path = %to.display(), error = ?undo, "Could not undo the case rename");
                self.queue_tasks_again(&tasks, from, to).await;
            } else {
                self.queue_tasks_again(&tasks, from, from).await;
            }
            return Err(err.context("inventory rename refused"));
        }
        self.queue_tasks_again(&tasks, from, to).await;

        if info.is_directory {
            // Changes inside that the server reported meanwhile were skipped while the
            // spellings differed (see `case_mismatched_ancestor`): compare the folder again.
            let _ = self.command_tx.send(MountCommand::Sync {
                local_paths: vec![to.to_path_buf()],
                mode: SyncMode::FullHierarchy,
                user_initiated: false,
            });
            return Ok(());
        }
        // A file that is not in sync now (changed here, or the rename cleared the flag). The
        // rename itself worked: problems from here on are only logged.
        let now = LocalFileInfo::from_path(to).unwrap_or_else(|_| LocalFileInfo::missing());
        if !now.exists || now.in_sync() {
            return Ok(());
        }
        // Without any byte on disk there is no local change to lose: in sync again.
        if info.in_sync() && now.is_placeholder() && now.partial_on_disk() {
            match mark_in_sync_if_empty(to).await {
                Ok(true) => return Ok(()),
                Ok(false) => {}
                Err(err) => {
                    tracing::warn!(target: "drive::case", id = %self.id, path = %to.display(), error = ?err, "Could not mark the renamed file in sync");
                    return Ok(());
                }
            }
        }
        // Local bytes: uploaded, which keeps a change made at any moment (an unchanged file
        // only gives the server a new version with the same content). Not in a read-only
        // drive, which keeps a local change as a conflict copy on the next server change.
        let upload_queued = tasks
            .iter()
            .any(|(kind, path)| *kind == TaskKind::Upload && same_on_ntfs(path, from));
        if !upload_queued && !self.is_read_only() {
            if let Err(err) = self.enqueue_upload(TaskPayload::upload(to.to_path_buf())).await {
                tracing::warn!(target: "drive::case", id = %self.id, path = %to.display(), error = ?err, "Upload after the case rename could not be queued");
            }
        }
        Ok(())
    }

    /// Stop the pending tasks at or under `path` (any case spelling). Refuses while one of them
    /// runs: a running download could still write through the old spelling after the rename.
    /// Each pending task is cancelled only while it is still pending (the queue claims a task
    /// the same way before it starts), so a task either runs and the rename waits, or the
    /// rename goes on and the task never starts. Returns the stopped tasks.
    async fn stop_tasks_under(&self, path: &Path) -> Result<Vec<(TaskKind, PathBuf)>> {
        let active = self
            .task_queue
            .list_active_tasks()
            .context("could not read the task list")?;
        let mut stopped = Vec::new();
        for task in active
            .iter()
            .filter(|task| is_under_any_case(Path::new(&task.local_path), &[path.to_path_buf()]))
        {
            // A task of a kind this code cannot queue again is left alone: wait for it.
            let Some(kind) = TaskKind::from_str(&task.task_type) else {
                self.queue_tasks_again(&stopped, path, path).await;
                return Err(RetryLater("a task of unknown kind under this name").into());
            };
            let cancelled = task.status == TaskStatus::Pending
                && match self.inventory.change_task_status_if(
                    &task.id,
                    TaskStatus::Pending,
                    TaskStatus::Cancelled,
                ) {
                    Ok(cancelled) => cancelled,
                    Err(err) => {
                        self.queue_tasks_again(&stopped, path, path).await;
                        return Err(err);
                    }
                };
            if !cancelled {
                self.queue_tasks_again(&stopped, path, path).await;
                return Err(RetryLater("a transfer under this name is running").into());
            }
            stopped.push((kind, PathBuf::from(&task.local_path)));
        }
        Ok(stopped)
    }

    /// Queue stopped tasks again. Paths at or under `old` (any case spelling) get `new` in
    /// place of `old`; the others are queued unchanged. Downloads of renamed paths are not
    /// queued again: the sync after the rename decides, so a download never runs over a file
    /// changed meanwhile. Ignored paths are left out.
    async fn queue_tasks_again(&self, tasks: &[(TaskKind, PathBuf)], old: &Path, new: &Path) {
        let depth = old.components().count();
        for (kind, path) in tasks {
            let head: PathBuf = path.components().take(depth).collect();
            let renamed = same_on_ntfs(&head, old) && old != new;
            if renamed && *kind == TaskKind::Download {
                continue;
            }
            let path = if same_on_ntfs(&head, old) {
                let mut rebased = new.to_path_buf();
                rebased.extend(path.components().skip(depth));
                rebased
            } else {
                path.clone()
            };
            if self.ignore_matcher.read().await.is_match(&path) {
                continue;
            }
            let result = match kind {
                TaskKind::Upload => {
                    // The user's "overwrite the server's version" lives on in the inventory.
                    let force = path.to_str().is_some_and(|p| {
                        self.inventory.query_by_path(p).ok().flatten().is_some_and(|row| {
                            row.conflict_state == Some(ConflictState::Override)
                        })
                    });
                    self.enqueue_upload(
                        TaskPayload::upload(path.clone()).with_force_override(force),
                    )
                    .await
                }
                TaskKind::Download => self
                    .task_queue
                    .enqueue(TaskPayload::download(path.clone()))
                    .await,
            };
            if let Err(err) = result {
                tracing::warn!(target: "drive::case", id = %self.id, path = %path.display(), error = ?err, "Could not queue a stopped task again");
            }
        }
    }

}

/// Names already reported in this run.
static CASE_NOTIFIED: Mutex<Option<HashSet<PathBuf>>> = Mutex::new(None);

/// Tell the user once per run about names that cannot be synced as they are.
pub(crate) fn report_case_clash(paths: &[PathBuf], message_key: &str) {
    let first = {
        let mut notified = CASE_NOTIFIED.lock().unwrap();
        let notified = notified.get_or_insert_with(HashSet::new);
        let fresh: Vec<&PathBuf> = paths
            .iter()
            .filter(|path| notified.insert((*path).clone()))
            .collect();
        for path in &fresh {
            tracing::warn!(target: "drive::case", path = %path.display(), kind = message_key, "Name differs only in case, not synced");
        }
        match fresh.first() {
            Some(first) => (*first).clone(),
            None => return,
        }
    };
    let name = first
        .file_name()
        .map(|name| name.to_string_lossy().to_string())
        .unwrap_or_default();
    crate::utils::toast::send_warning_toast(
        &t!("caseClashTitle"),
        &t!(message_key, "name" => name),
    );
}

/// Whether the server refused a name because its folder has the name in other upper/lower
/// case (40905). Such a write never succeeds as it is: it is reported, not tried again.
pub fn is_name_case_taken(error: &anyhow::Error) -> bool {
    error.chain().any(|cause| {
        matches!(cause.downcast_ref::<ApiError>(), Some(ApiError::ApiError { code, .. })
            if *code == ErrorCode::NameCaseTaken as i32)
    })
}

/// Report a write of `path` the server refused with 40905, once per run.
pub fn report_name_case_taken(path: &Path, error: &anyhow::Error) {
    if is_name_case_taken(error) {
        report_case_clash(&[path.to_path_buf()], "caseTakenMessage");
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const CREATED: &str = "2026-11-19T10:00:00Z";
    const CREATED_TS: i64 = 1_795_082_400;
    const ETAG: &str = "e1";
    const ID: &str = "id1";

    fn row(folder: bool, file_id: Option<&str>) -> KnownEntry {
        KnownEntry {
            is_folder: folder,
            created_at: CREATED_TS,
            etag: if folder { String::new() } else { ETAG.to_string() },
            file_id: file_id.map(str::to_string),
        }
    }

    fn p(name: &str) -> PathBuf {
        PathBuf::from(r"C:\S").join(name)
    }

    fn server(names: &[&str]) -> HashMap<PathBuf, FileResponse> {
        server_with(names, false)
    }

    fn server_with(names: &[&str], folder: bool) -> HashMap<PathBuf, FileResponse> {
        names
            .iter()
            .map(|name| {
                (
                    p(name),
                    FileResponse {
                        name: name.to_string(),
                        file_type: if folder { file_type::FOLDER } else { file_type::FILE },
                        id: ID.to_string(),
                        created_at: CREATED.to_string(),
                        primary_entity: (!folder).then(|| ETAG.to_string()),
                        ..Default::default()
                    },
                )
            })
            .collect()
    }

    /// The disk holds `names`; lookups ignore case like NTFS.
    fn disk(names: &'static [&'static str]) -> impl Fn(&Path) -> Option<PathBuf> {
        move |path: &Path| {
            names
                .iter()
                .map(|name| p(name))
                .find(|on_disk| same_on_ntfs(on_disk, path))
        }
    }

    /// The inventory has a row for each of `names`, matching the server entries.
    fn known(names: &'static [&'static str], folder: bool) -> impl Fn(&Path) -> Option<KnownEntry> {
        move |path: &Path| {
            names
                .iter()
                .any(|name| p(name) == path)
                .then(|| row(folder, Some(ID)))
        }
    }

    fn nothing_known(_: &Path) -> Option<KnownEntry> {
        None
    }

    #[test]
    fn created_constant_matches() {
        assert_eq!(CREATED.parse::<DateTime<Utc>>().unwrap().timestamp(), CREATED_TS);
    }

    #[test]
    fn fold_is_like_windows() {
        assert_eq!(ntfs_fold("foto.JPG"), ntfs_fold("Foto.jpg"));
        assert_eq!(ntfs_fold("Übung"), ntfs_fold("übung"));
        // Greek with a subscript: one name on NTFS, two characters in Rust's full upper case.
        assert_eq!(ntfs_fold("\u{1F80}"), ntfs_fold("\u{1F88}"));
        // Windows does not turn one character into two.
        assert_ne!(ntfs_fold("Straße"), ntfs_fold("STRASSE"));
        // Composed and decomposed umlauts are two names on NTFS.
        assert_ne!(ntfs_fold("M\u{00FC}ller"), ntfs_fold("Mu\u{0308}ller"));
        assert_ne!(ntfs_fold("M\u{00DC}LLER"), ntfs_fold("mu\u{0308}ller"));
    }

    #[test]
    fn fold_matches_the_real_file_system() {
        // The test PC's NTFS decides: a second spelling must open the same file.
        let dir = tempfile::tempdir().unwrap();
        for (created, other) in [("fÖto.txt", "FöTO.TXT"), ("\u{1F80}.txt", "\u{1F88}.txt")] {
            std::fs::write(dir.path().join(created), b"x").unwrap();
            assert!(dir.path().join(other).exists(), "{other}");
            assert!(same_on_ntfs(Path::new(created), Path::new(other)));
            assert_eq!(
                LocalFileInfo::on_disk_path(&dir.path().join(other)),
                Some(dir.path().join(created))
            );
        }
        assert_eq!(LocalFileInfo::on_disk_path(&dir.path().join("missing.txt")), None);
    }

    #[test]
    fn transfers_see_other_spellings_on_the_real_disk() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();
        std::fs::create_dir(root.join("Photos")).unwrap();
        std::fs::write(root.join("Photos").join("a.txt"), b"x").unwrap();
        assert!(!spelled_differently_on_disk(&root.join(r"Photos\a.txt"), root));
        assert!(spelled_differently_on_disk(&root.join(r"photos\a.txt"), root));
        assert!(spelled_differently_on_disk(&root.join(r"Photos\A.TXT"), root));
        // Not on disk at all: nothing to compare.
        assert!(!spelled_differently_on_disk(&root.join(r"Photos\new.txt"), root));
        // Each name spelled as on disk; a name not on disk stays as given.
        assert_eq!(disk_spelling(&root.join(r"photos\A.TXT"), root), root.join(r"Photos\a.txt"));
        assert_eq!(disk_spelling(&root.join(r"PHOTOS\New.txt"), root), root.join(r"Photos\New.txt"));
    }

    #[test]
    fn server_case_rename_full_sync_renames_locally() {
        // Disk `foto.jpg` (synced), server renamed it to `Foto.jpg`. The full sync sees both.
        let plan = plan_case(
            &[p("foto.jpg"), p("Foto.jpg")],
            &server(&["Foto.jpg"]),
            disk(&["foto.jpg"]),
            known(&["foto.jpg"], false),
        );
        assert_eq!(plan.paths, [p("Foto.jpg")]);
        assert_eq!(plan.renames, [(p("foto.jpg"), p("Foto.jpg"))]);
        assert!(plan.hidden.is_empty() && plan.held.is_empty());
    }

    #[test]
    fn server_case_rename_event_renames_locally() {
        // The event syncs only the new name; the disk still has the old one.
        let plan = plan_case(
            &[p("Foto.jpg")],
            &server(&["Foto.jpg"]),
            disk(&["foto.jpg"]),
            known(&["foto.jpg"], false),
        );
        assert_eq!(plan.paths, [p("Foto.jpg")]);
        assert_eq!(plan.renames, [(p("foto.jpg"), p("Foto.jpg"))]);
    }

    #[test]
    fn server_case_rename_of_folder_renames_locally() {
        let plan = plan_case(
            &[p("Fotos"), p("fotos")],
            &server_with(&["fotos"], true),
            disk(&["Fotos"]),
            known(&["Fotos"], true),
        );
        assert_eq!(plan.paths, [p("fotos")]);
        assert_eq!(plan.renames, [(p("Fotos"), p("fotos"))]);
    }

    #[test]
    fn already_renamed_needs_nothing() {
        let plan = plan_case(
            &[p("Foto.jpg")],
            &server(&["Foto.jpg"]),
            disk(&["Foto.jpg"]),
            known(&["Foto.jpg"], false),
        );
        assert_eq!(plan, CasePlan { paths: vec![p("Foto.jpg")], ..Default::default() });
    }

    #[test]
    fn local_case_rename_while_off_takes_the_server_name() {
        // The user renamed `foto.jpg` to `Foto.jpg` while the app was closed. The server name
        // wins, like for any rename made while the app was off, but nothing is deleted.
        let plan = plan_case(
            &[p("Foto.jpg"), p("foto.jpg")],
            &server(&["foto.jpg"]),
            disk(&["Foto.jpg"]),
            known(&["foto.jpg"], false),
        );
        assert_eq!(plan.paths, [p("foto.jpg")]);
        assert_eq!(plan.renames, [(p("Foto.jpg"), p("foto.jpg"))]);
    }

    #[test]
    fn unknown_local_file_with_other_case_is_held() {
        // A new local `Foto.jpg` next to a server `foto.jpg` from elsewhere: maybe two different
        // files. Nothing is renamed, uploaded or deleted.
        let plan = plan_case(
            &[p("Foto.jpg")],
            &server(&["foto.jpg"]),
            disk(&["Foto.jpg"]),
            nothing_known,
        );
        assert!(plan.paths.is_empty() && plan.renames.is_empty());
        assert_eq!(plan.held, [p("foto.jpg")]);
    }

    #[test]
    fn other_object_with_other_case_is_held() {
        // Local `a.jpg` was server `a.jpg`; that was deleted and a different `A.jpg` remains.
        let plan_with = |remote: HashMap<PathBuf, FileResponse>, known: KnownEntry| {
            plan_case(&[p("a.jpg")], &remote, disk(&["a.jpg"]), |path: &Path| {
                (path == p("a.jpg")).then(|| known.clone())
            })
        };
        let other_id = KnownEntry { file_id: Some("id2".into()), ..row(false, None) };
        let rows_without_id = [
            KnownEntry { is_folder: true, ..row(false, None) },
            KnownEntry { created_at: CREATED_TS + 1, ..row(false, None) },
            KnownEntry { etag: "e2".into(), ..row(false, None) },
            KnownEntry { etag: String::new(), ..row(false, None) },
        ];
        // Twins created in the same second with the same content: the ID tells them apart.
        for known in std::iter::once(other_id).chain(rows_without_id) {
            let plan = plan_with(server(&["A.jpg"]), known);
            assert_eq!(plan.held, [p("A.jpg")]);
            assert!(plan.renames.is_empty() && plan.paths.is_empty());
        }
        // A folder is only renamed with a matching ID.
        let folders = server_with(&["A.jpg"], true);
        assert_eq!(plan_with(folders.clone(), row(true, None)).held, [p("A.jpg")]);
        assert_eq!(plan_with(folders, row(true, Some(ID))).renames.len(), 1);
        // A file row of an older version without ID: same creation time and etag suffice.
        assert_eq!(plan_with(server(&["A.jpg"]), row(false, None)).renames.len(), 1);
    }

    #[test]
    fn rows_under_both_spellings_are_held() {
        let plan = plan_case(
            &[p("foto.jpg")],
            &server(&["Foto.jpg"]),
            disk(&["foto.jpg"]),
            known(&["foto.jpg", "Foto.jpg"], false),
        );
        assert_eq!(plan.held, [p("Foto.jpg")]);
    }

    #[test]
    fn two_server_names_new_folder_keeps_the_first() {
        let plan = plan_case(&[], &server(&["a.jpg", "A.jpg"]), disk(&[]), nothing_known);
        assert_eq!(plan.paths, [p("A.jpg")]);
        assert_eq!(plan.hidden, [p("a.jpg")]);
    }

    #[test]
    fn two_server_names_keep_the_one_on_disk() {
        let plan = plan_case(
            &[p("a.jpg")],
            &server(&["a.jpg", "A.jpg"]),
            disk(&["a.jpg"]),
            known(&["a.jpg"], false),
        );
        assert_eq!(plan.paths, [p("a.jpg")]);
        assert_eq!(plan.hidden, [p("A.jpg")]);
        assert!(plan.renames.is_empty());
    }

    #[test]
    fn two_server_names_none_on_disk_spelling_is_held() {
        let plan = plan_case(
            &[p("FOTO.jpg")],
            &server(&["foto.jpg", "Foto.jpg"]),
            disk(&["FOTO.jpg"]),
            known(&["FOTO.jpg"], false),
        );
        assert!(plan.paths.is_empty() && plan.renames.is_empty());
        assert_eq!(plan.held.len(), 2);
    }

    #[test]
    fn server_deletes_the_hidden_twin_keeps_local_file() {
        // Server had `a.jpg` (on disk) and `A.jpg` (hidden). `A.jpg` is deleted; its delete
        // event asks for `A.jpg`, which NTFS finds as `a.jpg`. Before, that deleted `a.jpg`.
        let plan = plan_case(
            &[p("A.jpg")],
            &server(&["a.jpg"]),
            disk(&["a.jpg"]),
            known(&["a.jpg"], false),
        );
        assert_eq!(plan.paths, [p("a.jpg")]);
        assert!(plan.renames.is_empty() && plan.held.is_empty());
    }

    #[test]
    fn unicode_forms_stay_two_files() {
        let composed = "M\u{00FC}ller.txt";
        let decomposed = "Mu\u{0308}ller.txt";
        let plan = plan_case(
            &[p(composed)],
            &server(&[composed, decomposed]),
            disk(&["M\u{00FC}ller.txt"]),
            nothing_known,
        );
        let mut paths = plan.paths.clone();
        paths.sort();
        let mut expected = vec![p(composed), p(decomposed)];
        expected.sort();
        assert_eq!(paths, expected);
        assert!(plan.renames.is_empty() && plan.hidden.is_empty() && plan.held.is_empty());
    }

    #[test]
    fn plain_paths_are_untouched() {
        let plan = plan_case(
            &[p("x.txt"), p("gone.txt")],
            &server(&["x.txt", "new.txt"]),
            disk(&["x.txt"]),
            nothing_known,
        );
        let mut paths = plan.paths.clone();
        paths.sort();
        assert_eq!(paths, [p("gone.txt"), p("new.txt"), p("x.txt")]);
        assert!(plan.renames.is_empty() && plan.hidden.is_empty() && plan.held.is_empty());
    }

    #[test]
    fn local_only_requested_twice_is_planned_once() {
        let plan = plan_case(
            &[p("neu.txt"), p("Neu.txt")],
            &HashMap::new(),
            disk(&["Neu.txt"]),
            nothing_known,
        );
        assert_eq!(plan.paths, [p("Neu.txt")]);
    }

    #[test]
    fn twins_are_found_by_folded_name() {
        let twins = case_twins(["a.jpg", "A.jpg", "b.jpg", "M\u{00FC}ller", "Mu\u{0308}ller"].into_iter());
        assert_eq!(twins, HashSet::from([ntfs_fold("a.jpg")]));
    }

    #[test]
    fn ancestor_spelled_differently_on_disk_is_found() {
        let root = PathBuf::from(r"C:\S");
        // Disk: `C:\S\Photos\2025`. A sync under the server's `photos` must not run. Like
        // `on_disk_path`, the lookup returns the disk's spelling of the last name only.
        let on_disk = |path: &Path| {
            [p("Photos"), p(r"Photos\2025")]
                .into_iter()
                .find(|disk| same_on_ntfs(disk, path))
                .map(|disk| path.with_file_name(disk.file_name().unwrap()))
        };
        assert_eq!(
            case_mismatched_ancestor(&p(r"photos\2025"), &root, on_disk),
            Some(p("photos"))
        );
        assert_eq!(
            case_mismatched_ancestor(&p(r"Photos\2025"), &root, on_disk),
            None
        );
        // A folder missing on disk is no mismatch.
        assert_eq!(case_mismatched_ancestor(&p(r"Other\x"), &root, on_disk), None);
        assert_eq!(case_mismatched_ancestor(&root, &root, on_disk), None);
    }

    #[test]
    fn own_renames_match_once_and_only_their_target() {
        let renames = OwnRenames::default();
        renames.register(&p("foto.jpg"), &p("Foto.jpg"));
        assert!(!renames.matches(&p("foto.jpg"), &p("FOTO.jpg"), false));
        assert!(!renames.matches(&p("other.jpg"), &p("Foto.jpg"), false));
        assert!(renames.matches(&p("FOTO.JPG"), &p("Foto.jpg"), false));
        assert!(renames.matches(&p("foto.jpg"), &p("Foto.jpg"), true));
        assert!(!renames.matches(&p("foto.jpg"), &p("Foto.jpg"), false));
    }
}

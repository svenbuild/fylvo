#[cfg(windows)]
use crate::cfapi::{metadata::Metadata, placeholder_file::PlaceholderFile};
use crate::{
    drive::{
        local::{LocalFileInfo, PinState},
        case::{FILE_ID_KEY, case_mismatched_ancestor, disk_key},
        commands::MountCommand,
        guard::DeletionSide,
        mounts::Mount,
        placeholder::CrPlaceholder,
        utils::{local_path_to_cr_uri, remote_path_to_local_relative_path},
    },
    inventory::{ConflictState, FileMetadata, MetadataEntry},
    tasks::TaskPayload,
};
use anyhow::{Context, Result};
use chrono::{DateTime, Utc};
use cloudreve_api::{
    ApiError,
    api::explorer::ExplorerApiExt,
    error::ErrorCode,
    models::{
        explorer::{FileResponse, file_type, metadata},
        uri::CrUri,
    },
};
use notify_debouncer_full::notify::event::{
    AccessKind, CreateKind, EventKind, ModifyKind, RemoveKind, RenameMode,
};
use notify_debouncer_full::{DebouncedEvent, notify::Event};
#[cfg(windows)]
use nt_time::FileTime;
#[cfg(windows)]
use std::ffi::OsString;
use std::{
    collections::{HashMap, HashSet},
    fmt, fs, io,
    path::{Path, PathBuf},
    time::SystemTime,
};
use tokio::task;
use uuid::Uuid;

#[cfg(windows)]
pub fn cloud_file_to_placeholder(
    file: &FileResponse,
    _local_path: &PathBuf,
    remote_path: &CrUri,
) -> Result<PlaceholderFile> {
    let file_uri = CrUri::new(&file.path)?;
    let relative_path = remote_path_to_local_relative_path(&file_uri, &remote_path)?;
    tracing::trace!(target: "drive::sync", file_uri = %file_uri.to_string(), remote_path = %remote_path.to_string(), relative_path = %relative_path.to_string_lossy(), "Relative path");
    let primary_entity = OsString::from(file.primary_entity.as_ref().unwrap_or(&String::new()));
    // Remove leading slash if presented

    // Parse RFC time string to unix timestamp
    let created_at =
        FileTime::from_unix_time(file.created_at.parse::<DateTime<Utc>>()?.timestamp())?;
    let last_modified =
        FileTime::from_unix_time(file.updated_at.parse::<DateTime<Utc>>()?.timestamp())?;

    tracing::trace!(target: "drive::sync::cloud_file_to_placeholder", relative_path = %relative_path.to_string_lossy(), "Relative path");

    Ok(PlaceholderFile::new(relative_path)
        .metadata(
            match file.file_type == file_type::FOLDER {
                true => Metadata::directory(),
                false => Metadata::file(),
            }
            .size(file.size as u64)
            .changed(last_modified)
            .written(last_modified)
            .created(created_at),
        )
        .mark_in_sync()
        .overwrite()
        .blob(primary_entity.into_encoded_bytes()))
}

pub fn cloud_file_to_metadata_entry(
    file: &FileResponse,
    drive_id: &Uuid,
    local_path: &PathBuf,
) -> Result<MetadataEntry> {
    let mut local_path = local_path.clone();
    local_path.push(file.name.clone());
    let local_path_str = local_path.to_str();
    if local_path_str.is_none() {
        tracing::error!(
            target: "drive::mounts",
            local_path = %local_path.display(),
            error = "Failed to convert local path to string"
        );
        return Err(anyhow::anyhow!("Failed to convert local path to string"));
    }

    // Parse RFC time string to unix timestamp
    let created_at = file.created_at.parse::<DateTime<Utc>>()?.timestamp();
    let last_modified = file.updated_at.parse::<DateTime<Utc>>()?.timestamp();

    Ok(MetadataEntry::new(
        drive_id.clone(),
        local_path_str.unwrap(),
        file.file_type == file_type::FOLDER,
    )
    .with_created_at(created_at)
    .with_updated_at(last_modified)
    .with_permissions(file.permission.as_ref().unwrap_or(&String::new()).clone())
    .with_shared(file.shared.unwrap_or(false))
    .with_size(file.size)
    .with_etag(
        file.primary_entity
            .as_ref()
            .unwrap_or(&String::new())
            .clone(),
    )
    .with_metadata(metadata_with_file_id(file)))
}

/// Fylvo: the server's metadata of a file plus its file ID, so a later case-only rename can
/// tell whether the local file is the same object (see `case.rs`).
pub fn metadata_with_file_id(file: &FileResponse) -> HashMap<String, String> {
    let mut metadata = file.metadata.clone().unwrap_or_default();
    metadata.insert(FILE_ID_KEY.to_string(), file.id.clone());
    metadata
}

pub fn is_symbolic_link(file: &FileResponse) -> bool {
    return file.metadata.is_some()
        && file
            .metadata
            .as_ref()
            .unwrap()
            .get(metadata::SHARE_REDIRECT)
            .is_some();
}

pub type GroupedFsEvents = HashMap<EventKind, Vec<Event>>;

const REMOTE_PAGE_SIZE: i32 = 1000;

/// Groups filesystem events by their first-level EventKind.
///
/// This function groups events into a HashMap where the key is the first-level EventKind
/// (normalized to use ::Any for nested variants) and the value is a vector of events.
///
/// # Arguments
/// * `events` - A vector of DebouncedEvent to be grouped
///
/// # Returns
/// A HashMap mapping EventKind to Vec<DebouncedEvent>
pub fn group_fs_events(events: Vec<DebouncedEvent>) -> GroupedFsEvents {
    let mut grouped: GroupedFsEvents = HashMap::new();

    for event in events {
        let normalized_kind = normalize_event_kind(&event.kind);
        grouped
            .entry(normalized_kind)
            .or_insert_with(Vec::new)
            .push(event.event);
    }

    grouped
}

/// Normalizes an EventKind to its first-level representation.
///
/// This helper function converts all nested EventKind variants to use their ::Any variant,
/// effectively grouping by the first level only. This can be extended to support deeper
/// level matching by adding parameters for match depth or specific variant matching.
///
/// # Arguments
/// * `kind` - The EventKind to normalize
///
/// # Returns
/// A normalized EventKind representing the first level only
fn normalize_event_kind(kind: &EventKind) -> EventKind {
    match kind {
        EventKind::Any => EventKind::Any,
        EventKind::Access(_) => EventKind::Access(AccessKind::Any),
        EventKind::Create(_) => EventKind::Create(CreateKind::Any),
        EventKind::Modify(modify_kind) => match modify_kind {
            ModifyKind::Name(rename_mode) => match rename_mode {
                RenameMode::Both => EventKind::Modify(ModifyKind::Name(RenameMode::Both)),
                _ => EventKind::Modify(ModifyKind::Any),
            },
            _ => EventKind::Modify(ModifyKind::Any),
        },
        EventKind::Remove(_) => EventKind::Remove(RemoveKind::Any),
        EventKind::Other => EventKind::Other,
    }
}

/// Determines how deep a sync operation should traverse for a given path list.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SyncMode {
    /// Sync only the provided path entries.
    PathOnly,
    /// Sync the provided path entries and their first-level children.
    PathAndFirstLayer,
    /// Sync the provided path entries and every descendant.
    FullHierarchy,
}

const CONFLICT_PREFIX: &str = "__conflict__";

#[allow(dead_code)]
#[derive(Debug, Clone)]
enum SyncAction {
    CreatePlaceholderAndInventory {
        path: PathBuf,
        remote: FileResponse,
    },
    // Update inventory and placehodler metadata, conver to placehodler if it's not one
    UpdateInventoryFromRemote {
        path: PathBuf,
        remote: FileResponse,
        invalidate_all: bool,
    },
    QueueUpload {
        path: PathBuf,
        reason: UploadReason,
    },
    QueueDownload {
        path: PathBuf,
        remote: FileResponse,
    },
    DeleteLocalAndInventory {
        path: PathBuf,
        skip_if_not_empty: bool,
    },
    CreateRemoteFolderIfExist {
        path: PathBuf,
    },
    RenameLocalWithConflict {
        original: PathBuf,
        renamed: PathBuf,
    },
}

#[derive(Debug, Clone, Copy)]
enum UploadReason {
    RemoteMismatch,
    RemoteMissing,
    /// Fylvo: changed here from the version the server still has (an earlier upload failed).
    NotUploaded,
}

#[derive(Debug, Clone, Copy)]
enum WalkReason {
    ModePropagation,
    DiffTriggered,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum WalkTiming {
    Immediate,
    Deferred,
}

#[derive(Debug, Clone)]
struct WalkRequest {
    path: PathBuf,
    mode: SyncMode,
    reason: WalkReason,
    timing: WalkTiming,
}

#[derive(Default)]
struct SyncPlan {
    actions: Vec<SyncAction>,
    walk_requests: Vec<WalkRequest>,
}

// Debug print for SyncPlan
impl fmt::Debug for SyncPlan {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        writeln!(
            f,
            "SyncPlan ({} actions, {} walks):",
            self.actions.len(),
            self.walk_requests.len()
        )?;

        for (i, action) in self.actions.iter().enumerate() {
            writeln!(f, "  [{}] {:?}", i, action)?;
        }

        for (i, walk) in self.walk_requests.iter().enumerate() {
            writeln!(f, "  [W{}] {:?}", i, walk)?;
        }

        Ok(())
    }
}

#[derive(Debug)]
struct SyncErrorEntry {
    path: PathBuf,
    error: anyhow::Error,
}

#[derive(Debug)]
struct SyncAggregateError {
    context: String,
    entries: Vec<SyncErrorEntry>,
}

impl SyncAggregateError {
    fn new(context: impl Into<String>) -> Self {
        Self {
            context: context.into(),
            entries: Vec::new(),
        }
    }

    fn push<E>(&mut self, path: PathBuf, error: E)
    where
        E: Into<anyhow::Error>,
    {
        self.entries.push(SyncErrorEntry {
            path,
            error: error.into(),
        });
    }

    fn is_empty(&self) -> bool {
        self.entries.is_empty()
    }

    fn into_result(self) -> Result<()> {
        if self.is_empty() {
            Ok(())
        } else {
            Err(self.into())
        }
    }
}

impl fmt::Display for SyncAggregateError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        writeln!(
            f,
            "{} encountered {} error(s):",
            self.context,
            self.entries.len()
        )?;
        for entry in &self.entries {
            writeln!(f, "- {}: {}", entry.path.display(), entry.error)?;
        }
        Ok(())
    }
}

impl std::error::Error for SyncAggregateError {}

// fn local_has_pending_changes(local: &LocalFileInfo, _inventory: Option<&FileMetadata>) -> bool {
//     !local.is_placeholder() || !local.in_sync() ||

//     // if let (Some(last_modified), Some(entry)) = (local.last_modified, inventory) {
//     //     if let Some(last_modified_secs) = system_time_to_unix_secs(last_modified) {
//     //         return last_modified_secs > entry.updated_at;
//     //     }
//     // }
// }

#[allow(dead_code)]
fn system_time_to_unix_secs(time: SystemTime) -> Option<i64> {
    match time.duration_since(SystemTime::UNIX_EPOCH) {
        Ok(duration) => Some(duration.as_secs() as i64),
        Err(err) => {
            let duration = err.duration();
            Some(-(duration.as_secs() as i64))
        }
    }
}

pub(crate) fn generate_conflict_path(path: &Path) -> PathBuf {
    let timestamp = Utc::now().format("%Y%m%d%H%M%S");
    let stem = path
        .file_stem()
        .and_then(|value| value.to_str())
        .unwrap_or("item");
    let ext = path.extension().and_then(|value| value.to_str());
    let mut new_name = format!("{}{}_{}", CONFLICT_PREFIX, timestamp, stem);
    if let Some(ext) = ext {
        new_name.push('.');
        new_name.push_str(ext);
    }
    // Fylvo (Linux): a name may have 255 bytes; a long one keeps its start and extension.
    if cfg!(target_os = "linux") && new_name.len() > 255 {
        let ext = ext.map(|e| format!(".{e}")).filter(|e| e.len() < 32).unwrap_or_default();
        let mut cut = 255 - ext.len();
        while !new_name.is_char_boundary(cut) {
            cut -= 1;
        }
        new_name = format!("{}{ext}", &new_name[..cut.min(new_name.len())]);
        new_name.truncate(255);
    }
    let mut conflict_path = path.to_path_buf();
    conflict_path.set_file_name(new_name);
    conflict_path
}

fn next_child_mode(mode: SyncMode) -> SyncMode {
    match mode {
        SyncMode::FullHierarchy => SyncMode::FullHierarchy,
        SyncMode::PathAndFirstLayer => SyncMode::PathOnly,
        SyncMode::PathOnly => SyncMode::PathOnly,
    }
}

/// Result of collecting child targets, including pre-fetched remote file info.
struct CollectChildResult {
    /// All child paths (union of local and remote).
    paths: Vec<PathBuf>,
    /// Pre-fetched remote file info keyed by local path.
    remote_files: HashMap<PathBuf, FileResponse>,
}

impl Mount {
    /// Syncs a list of local paths by grouping them under their parent directories.
    pub async fn sync_paths(&self, local_paths: Vec<PathBuf>, mode: SyncMode) -> Result<()> {
        let _sync_guard = self.sync_lock.lock().await;

        if local_paths.is_empty() {
            tracing::debug!(target: "drive::sync", id = %self.id, "No paths provided for sync");
            return Ok(());
        }

        let mut grouped: HashMap<PathBuf, Vec<PathBuf>> = HashMap::new();

        for path in local_paths {
            let parent = path
                .parent()
                .map(|p| p.to_path_buf())
                .unwrap_or_else(|| path.clone());
            grouped.entry(parent).or_default().push(path);
        }

        let mut aggregate_error = SyncAggregateError::new(format!("Mount {} sync_paths", self.id));

        for (parent, paths) in grouped.iter() {
            if let Err(err) = self.sync_group(parent, paths, mode, None).await {
                let target_path = paths.first().cloned().unwrap_or_else(|| parent.clone());
                aggregate_error.push(target_path, err);
            }
        }

        aggregate_error.into_result()
    }

    async fn sync_group(
        &self,
        parent: &PathBuf,
        paths: &[PathBuf],
        mode: SyncMode,
        prefetched_remote_files: Option<HashMap<PathBuf, FileResponse>>,
    ) -> Result<()> {
        tracing::info!(
            target: "drive::sync",
            id = %self.id,
            parent = %parent.display(),
            paths = paths.len(),
            mode = ?mode,
            prefetched = prefetched_remote_files.is_some(),
            "Queued grouped sync"
        );

        let mut aggregate_error = SyncAggregateError::new(format!(
            "Mount {} sync_group({})",
            self.id,
            parent.display()
        ));

        // For sync root, directly walk to descendants
        let sync_root = {
            let config = self.config.read().await;
            config.sync_path.clone()
        };
        if paths.len() == 1 && paths[0] == sync_root {
            tracing::debug!(
                target: "drive::sync",
                id = %self.id,
                parent = %parent.display(),
                "Syncing sync root"
            );
            self.process_walk_requests(
                vec![WalkRequest {
                    path: sync_root,
                    mode,
                    reason: WalkReason::ModePropagation,
                    timing: WalkTiming::Immediate,
                }],
                &mut aggregate_error,
            )
            .await;
            return aggregate_error.into_result();
        }

        // Fylvo: a folder on the way spelled differently on disk (only case): the server
        // folder of one spelling must not be compared with the local folder of the other.
        if let Some(ancestor) =
            case_mismatched_ancestor(parent, &sync_root, LocalFileInfo::on_disk_path)
        {
            tracing::info!(
                target: "drive::sync",
                id = %self.id,
                parent = %parent.display(),
                ancestor = %ancestor.display(),
                "Skipping sync, a folder is spelled differently on disk"
            );
            return Ok(());
        }

        // Fylvo: nothing behind a symlink or in another file system inside the folder is synced.
        #[cfg(target_os = "linux")]
        let paths: &[PathBuf] = {
            use crate::drive::local_linux::is_foreign_below;
            if is_foreign_below(parent, &sync_root) {
                return Ok(());
            }
            &paths
                .iter()
                .filter(|path| !is_foreign_below(path, &sync_root))
                .cloned()
                .collect::<Vec<_>>()
        };

        let remote_files = match prefetched_remote_files {
            Some(files) => files,
            None => self.fetch_remote_file_infos(parent, paths).await?,
        };
        // Fylvo: a skipped path must not come back through the server's list.
        #[cfg(target_os = "linux")]
        let remote_files: HashMap<PathBuf, FileResponse> = remote_files
            .into_iter()
            .filter(|(path, _)| !crate::drive::local_linux::is_foreign_below(path, &sync_root))
            .collect();
        tracing::debug!(
            target: "drive::sync",
            id = %self.id,
            parent = %parent.display(),
            requested = paths.len(),
            fetched = remote_files.len(),
            "Fetched remote metadata for sync group"
        );
        tracing::trace!("{:?}", remote_files);

        // Fylvo: names that are one file on NTFS (only upper/lower case differs) become one
        // path here, renamed locally to the server's spelling where that is safe.
        let (paths, case_errors) = self.resolve_case(paths, &remote_files).await;
        for (path, err) in case_errors {
            aggregate_error.push(path, err);
        }
        let paths = paths.as_slice();

        let local_files = self.fetch_local_file_infos(paths).await?;
        tracing::debug!(
            target: "drive::sync",
            id = %self.id,
            parent = %parent.display(),
            locals = local_files.len(),
            "Fetched local metadata for sync group"
        );
        tracing::trace!("{:?}", local_files);

        let inventory_files = self.fetch_inventory_entries(paths).await?;
        tracing::trace!("{:?}", inventory_files);

        let plan = self.build_sync_plan(
            parent,
            mode,
            paths,
            &remote_files,
            &local_files,
            &inventory_files,
        ).await;
        let plan = self.hold_mass_local_deletion(plan);

        tracing::debug!(
            target: "drive::sync",
            id = %self.id,
            parent = %parent.display(),
            actions = plan.actions.len(),
            walks = plan.walk_requests.len(),
            "Planned sync actions"
        );
        tracing::trace!(target: "drive::sync", plan = ?plan, "Planned actions detail");

        let SyncPlan {
            actions,
            walk_requests,
        } = plan;
        let (immediate_walks, deferred_walks): (Vec<_>, Vec<_>) = walk_requests
            .into_iter()
            .partition(|request| request.timing == WalkTiming::Immediate);

        self.process_walk_requests(immediate_walks, &mut aggregate_error)
            .await;

        if let Err(err) = self
            .process_sync_plan_actions_list(&actions, &mut aggregate_error)
            .await
        {
            aggregate_error.push(parent.clone(), err);
        }

        self.process_walk_requests(deferred_walks, &mut aggregate_error)
            .await;
        aggregate_error.into_result()
    }

    async fn process_sync_plan_actions_list(
        &self,
        actions: &[SyncAction],
        aggregate_error: &mut SyncAggregateError,
    ) -> Result<()> {
        let (drive_id, sync_root) = {
            let config = self.config.read().await;
            (Uuid::parse_str(&config.id)?, config.sync_path.clone())
        };

        // Fylvo: when a step for a path fails (for example the old local copy could not be
        // moved away), skip the later steps for that path instead of replacing it anyway.
        let mut failed: HashSet<PathBuf> = HashSet::new();
        for action in actions {
            if failed.contains(action.path()) {
                continue;
            }
            let errors_before = aggregate_error.entries.len();
            self.process_action(action, &sync_root, &drive_id, aggregate_error)
                .await;
            if aggregate_error.entries.len() > errors_before {
                failed.insert(action.path().clone());
            }
        }

        Ok(())
    }

    /// Fylvo: the path changed here after it was planned: plan it again.
    /// Fylvo: whether the folder of `path` is the sync folder or has a row.
    #[cfg(target_os = "linux")]
    fn parent_tracked(&self, path: &Path) -> bool {
        let Some(parent) = path.parent() else {
            return true;
        };
        crate::drive::local_linux::sync_root_of(path).is_some_and(|root| root == parent)
            || self.has_row(parent)
    }

    /// Fylvo: whether the inventory has a row at exactly `path`.
    #[cfg(target_os = "linux")]
    fn has_row(&self, path: &Path) -> bool {
        path.to_str()
            .is_some_and(|text| self.inventory.query_by_path(text).ok().flatten().is_some())
    }

    fn plan_again(&self, path: &PathBuf) {
        tracing::info!(target: "drive::sync", id = %self.id, path = %path.display(), "Changed here meanwhile, planned again");
        let _ = self.command_tx.send(MountCommand::Sync {
            local_paths: vec![path.clone()],
            mode: SyncMode::PathOnly,
            user_initiated: false,
        });
    }

    async fn process_action(
        &self,
        action: &SyncAction,
        sync_root: &PathBuf,
        drive_id: &Uuid,
        aggregate_error: &mut SyncAggregateError,
    ) {
        match action {
            SyncAction::CreatePlaceholderAndInventory { path, remote } => {
                // Fylvo: Linux keeps every file local; a new file comes by its download, which
                // writes it and its row (`tasks/download.rs`). Folders are created below.
                // A row without a file: maybe the old name of a rename the watcher has not
                // reported yet. Wait for it before the old name comes back from the server.
                #[cfg(target_os = "linux")]
                if self.has_row(path)
                    && !crate::drive::watch_linux::rename_settled(path, self.parent_tracked(path), &self.command_tx)
                {
                    return;
                }
                #[cfg(target_os = "linux")]
                if remote.file_type != file_type::FOLDER {
                    if let Err(err) = self
                        .task_queue
                        .enqueue(TaskPayload::download(path.clone()))
                        .await
                    {
                        tracing::error!(target: "drive::sync", id = %self.id, path = %path.display(), error = ?err, "Failed to enqueue download task");
                        aggregate_error.push(path.clone(), anyhow::Error::from(err));
                    }
                    return;
                }
                let cr_placeholder =
                    CrPlaceholder::new(path.clone(), sync_root.clone(), drive_id.clone());
                // Fylvo: planned while nothing (or only a synced placeholder) was here. Something
                // with local content appeared since (Excel puts the new file in place a moment
                // after deleting the old one). Taking the server's entry for it would mark it in
                // sync with the server's size; plan the path again instead.
                if appeared_meanwhile(&cr_placeholder.local_file_info) {
                    self.plan_again(path);
                    return;
                }
                if let Err(err) = cr_placeholder
                    .with_remote_file(remote)
                    .commit(self.inventory.clone())
                {
                    tracing::error!(
                        target: "drive::sync",
                        id = %self.id,
                        path = %path.display(),
                        error = ?err,
                        "Failed to create placeholder and inventory"
                    );
                    aggregate_error.push(path.clone(), err);
                } else if cfg!(target_os = "linux") && remote.file_type == file_type::FOLDER {
                    // Fylvo: Linux keeps everything local, so a new folder gets its content at
                    // once (Windows fills a placeholder folder when it is opened).
                    let _ = self.command_tx.send(MountCommand::Sync {
                        local_paths: vec![path.clone()],
                        mode: SyncMode::FullHierarchy,
                        user_initiated: false,
                    });
                }
            }
            SyncAction::UpdateInventoryFromRemote {
                path,
                remote,
                invalidate_all,
            } => {
                let mut cr_placeholder =
                    CrPlaceholder::new(path.clone(), sync_root.clone(), drive_id.clone());
                // Fylvo: planned for a file in sync. Changed here since (or replaced by a new
                // file, as Excel saves), the server's version must not be taken for it: plan the
                // path again. Windows also refuses the update itself if the file left the in-sync
                // state in between.
                if !remote.file_type.eq(&file_type::FOLDER) {
                    if appeared_meanwhile(&cr_placeholder.local_file_info) {
                        self.plan_again(path);
                        return;
                    }
                    cr_placeholder = cr_placeholder.with_verify_in_sync();
                }
                if let Err(err) = cr_placeholder
                    .with_invalidate_all_range(*invalidate_all)
                    .with_remote_file(remote)
                    .commit(self.inventory.clone())
                {
                    // Fylvo: Windows refused it because the file changed right now: that is
                    // the guard working, not a failure. The path is planned again.
                    let now = LocalFileInfo::from_path(path).unwrap_or_else(|_| LocalFileInfo::missing());
                    if !remote.file_type.eq(&file_type::FOLDER) && appeared_meanwhile(&now) {
                        self.plan_again(path);
                        return;
                    }
                    tracing::error!(
                        target: "drive::sync",
                        id = %self.id,
                        path = %path.display(),
                        error = ?err,
                        "Failed to update inventory from remote"
                    );
                    aggregate_error.push(path.clone(), err);
                }
            }
            SyncAction::QueueUpload { path, reason } => {
                // A new file here: maybe the new name of a rename the watcher has not reported
                // yet. Wait for it before the file goes up a second time.
                #[cfg(target_os = "linux")]
                if matches!(reason, UploadReason::RemoteMissing) && !self.has_row(path) {
                    // A synced file moved here (its old name is gone): a move, not a new file.
                    if let Some(old) = self.moved_from(path) {
                        if let Err(e) = self.process_rename_linux(old, path.clone()).await {
                            tracing::warn!(target: "drive::sync", path = %path.display(), error = ?e, "Failed to handle the move");
                        }
                        return;
                    }
                    if !crate::drive::watch_linux::rename_settled(path, self.parent_tracked(path), &self.command_tx) {
                        return;
                    }
                }
                if matches!(reason, UploadReason::NotUploaded)
                    && crate::tasks::failed_recently(&path)
                {
                    tracing::debug!(target: "drive::sync", id = %self.id, path = %path.display(), "Upload failed a moment ago, tried again later");
                    return;
                }
                tracing::info!(
                    target: "drive::sync",
                    id = %self.id,
                    path = %path.display(),
                    reason = ?reason,
                    "Queueing upload task"
                );

                if let Err(err) = self.enqueue_upload(TaskPayload::upload(path.clone())).await {
                    tracing::error!(
                        target: "drive::sync",
                        id = %self.id,
                        path = %path.display(),
                        error = ?err,
                        "Failed to enqueue upload task"
                    );
                    aggregate_error.push(path.clone(), err);
                }
            }
            SyncAction::QueueDownload { path, remote:_ } => {
                tracing::info!(
                    target: "drive::sync",
                    id = %self.id,
                    path = %path.display(),
                    "Queueing download task"
                );

                // Cancel ongoing tasks
                let _ = self.task_queue.cancel_by_path(path.clone()).await;

                if let Err(err) = self
                    .task_queue
                    .enqueue(TaskPayload::download(path.clone()))
                    .await
                {
                    tracing::error!(
                        target: "drive::sync",
                        id = %self.id,
                        path = %path.display(),
                        error = ?err,
                        "Failed to enqueue download task"
                    );
                    aggregate_error.push(path.clone(), anyhow::Error::from(err));
                }
            }
            SyncAction::DeleteLocalAndInventory {
                path,
                skip_if_not_empty,
            } => {
                if *skip_if_not_empty {
                    // Check if folder is not empty
                    if let Ok(entries) = std::fs::read_dir(path) {
                        if entries.count() > 0 {
                            tracing::info!(
                                target: "drive::sync",
                                id = %self.id,
                                path = %path.display(),
                                "Folder is empty, skipping deletion"
                            );
                            return;
                        }
                    }
                }

                tracing::info!(
                    target: "drive::sync",
                    id = %self.id,
                    path = %path.display(),
                    "Deleting local file/folder and inventory entry"
                );

                // Fylvo: block the watcher event before it can happen, so the deletion is not
                // sent back to the server.
                self.event_blocker
                    .register_once(&EventKind::Remove(RemoveKind::Any), path.clone());
                let cr_placeholder =
                    CrPlaceholder::new(path.clone(), sync_root.clone(), drive_id.clone());
                if let Err(err) = cr_placeholder.delete_placeholder(self.inventory.clone()) {
                    tracing::error!(
                        target: "drive::sync",
                        id = %self.id,
                        path = %path.display(),
                        error = ?err,
                        "Failed to delete local file/folder and inventory entry"
                    );
                    // Nothing was removed, so a later deletion by the user must not be hidden.
                    if path.exists() {
                        self.event_blocker
                            .unregister_once(&EventKind::Remove(RemoveKind::Any), path);
                    }
                    aggregate_error.push(path.clone(), anyhow::Error::from(err));
                };
            }
            SyncAction::CreateRemoteFolderIfExist { path } => {
                if !path.exists() {
                    return;
                }
                #[cfg(target_os = "linux")]
                if !self.has_row(path)
                    && !crate::drive::watch_linux::rename_settled(path, self.parent_tracked(path), &self.command_tx)
                {
                    return;
                }
                tracing::info!(
                    target: "drive::sync",
                    id = %self.id,
                    path = %path.display(),
                    "Creating remote folder"
                );
                if let Err(err) = self.enqueue_upload(TaskPayload::upload(path.clone())).await {
                    tracing::error!(
                        target: "drive::sync",
                        id = %self.id,
                        path = %path.display(),
                        error = ?err,
                        "Failed to enqueue upload task"
                    );
                    aggregate_error.push(path.clone(), err);
                }
            }
            SyncAction::RenameLocalWithConflict { original, renamed } => {
                tracing::info!(
                    target: "drive::sync",
                    id = %self.id,
                    original = %original.display(),
                    renamed = %renamed.display(),
                    "Renaming local file to resolve conflict"
                );

                // Cancel tasks for the original path
                _ = self.task_queue.cancel_by_path(original.clone()).await;

                // Fylvo: never replaces another file (an older conflict copy).
                #[cfg(target_os = "linux")]
                crate::drive::watch_linux::register_own_move(original, renamed);
                let moved = crate::drive::utils::move_no_replace(original, renamed);
                #[cfg(target_os = "linux")]
                match &moved {
                    Err(_) => crate::drive::watch_linux::forget_own_move(original, renamed),
                    // The copy is a new file of this folder (on Windows its rename event does
                    // this); a read-only drive refuses it with its notice.
                    Ok(()) => {
                        if let Err(e) = self.enqueue_upload(TaskPayload::upload(renamed.clone())).await {
                            tracing::warn!(target: "drive::sync", path = %renamed.display(), error = ?e, "Failed to enqueue upload of the conflict copy");
                        }
                    }
                }
                if let Err(err) = moved {
                    tracing::error!(
                        target: "drive::sync",
                        id = %self.id,
                        original = %original.display(),
                        renamed = %renamed.display(),
                        error = ?err,
                        "Failed to rename local file"
                    );
                    aggregate_error.push(original.clone(), err);
                }
            }
        }
    }

    async fn fetch_local_file_infos(
        &self,
        paths: &[PathBuf],
    ) -> Result<HashMap<PathBuf, LocalFileInfo>> {
        if paths.is_empty() {
            return Ok(HashMap::new());
        }

        let targets: Vec<PathBuf> = paths.to_vec();
        let mut entries = HashMap::with_capacity(targets.len());
        for path in targets {
            let info = LocalFileInfo::from_path(&path)?;
            entries.insert(path, info);
        }

        Ok(entries)
    }

    async fn fetch_remote_file_infos(
        &self,
        parent: &PathBuf,
        paths: &[PathBuf],
    ) -> Result<HashMap<PathBuf, FileResponse>> {
        if paths.is_empty() {
            return Ok(HashMap::new());
        }

        let (remote_base, sync_root) = {
            let config = self.config.read().await;
            (config.remote_path.clone(), config.sync_path.clone())
        };

        // Fylvo: server names are matched the way NTFS compares them, so a name that differs
        // only in case is found too. Keyed by the server's own spelling.
        let wanted: HashSet<String> = paths
            .iter()
            .filter_map(|path| path.file_name())
            .map(|name| disk_key(&name.to_string_lossy()))
            .collect();

        let parent_remote_uri =
            local_path_to_cr_uri(parent.clone(), sync_root.clone(), remote_base.clone())
                .with_context(|| {
                    format!("failed to map parent {} to remote uri", parent.display())
                })?;
        let parent_uri_str = parent_remote_uri.to_string();

        let mut remote_entries: HashMap<PathBuf, FileResponse> =
            HashMap::with_capacity(paths.len());
        let mut previous_response = None;

        loop {
            let response = match self
                .cr_client
                .list_files_all(
                    previous_response.as_ref(),
                    parent_uri_str.as_str(),
                    REMOTE_PAGE_SIZE,
                )
                .await
            {
                Ok(resp) => resp,
                Err(ApiError::ApiError { code, .. })
                    if code == ErrorCode::ParentNotExist as i32 =>
                {
                    // Fylvo: the synced folder itself is gone on the server. Do not read
                    // that as "everything was deleted".
                    if *parent == sync_root {
                        anyhow::bail!("the synced folder is missing on the server");
                    }
                    // A missing subfolder reads as "everything in it was deleted", but only if
                    // the synced folder itself is still there.
                    if !self.sync_root_exists().await? {
                        anyhow::bail!("the synced folder is missing on the server");
                    }
                    // Nor if the folder exists on the server spelled in another case (its local
                    // rename is still pending).
                    if self.server_has_case_variant(parent).await? {
                        anyhow::bail!("the folder exists on the server in another case");
                    }
                    tracing::debug!(
                        target: "drive::sync",
                        id = %self.id,
                        parent = %parent.display(),
                        "Remote parent directory missing during fetch"
                    );
                    return Ok(HashMap::new());
                }
                Err(err) => {
                    return Err(err.into());
                }
            };

            // The whole folder is read: a second spelling of a name may come on a later page.
            for file in &response.res.files {
                if wanted.contains(&disk_key(&file.name)) {
                    remote_entries.insert(parent.join(&file.name), file.clone());
                }
            }

            let has_more = response.more;
            previous_response = Some(response);

            if !has_more {
                break;
            }
        }

        Ok(remote_entries)
    }

    /// Fylvo: whether the local folder `path`, missing on the server, is there under another
    /// upper/lower case spelling of its name or of a folder above it.
    async fn server_has_case_variant(&self, path: &Path) -> Result<bool> {
        let (remote_base, sync_root) = {
            let config = self.config.read().await;
            (config.remote_path.clone(), config.sync_path.clone())
        };
        let mut missing = path;
        // Go up to the first folder the server can list.
        while let (Some(parent), Some(name)) = (missing.parent(), missing.file_name()) {
            if !parent.starts_with(&sync_root) {
                return Ok(false);
            }
            let parent_uri =
                local_path_to_cr_uri(parent.to_path_buf(), sync_root.clone(), remote_base.clone())?
                    .to_string();
            let name = disk_key(&name.to_string_lossy());
            let mut previous_response = None;
            loop {
                let response = match self
                    .cr_client
                    .list_files_all(previous_response.as_ref(), &parent_uri, REMOTE_PAGE_SIZE)
                    .await
                {
                    Ok(response) => response,
                    Err(ApiError::ApiError { code, .. })
                        if code == ErrorCode::ParentNotExist as i32 =>
                    {
                        break;
                    }
                    Err(err) => return Err(err.into()),
                };
                if response.res.files.iter().any(|file| disk_key(&file.name) == name) {
                    return Ok(true);
                }
                if !response.more {
                    return Ok(false);
                }
                previous_response = Some(response);
            }
            missing = parent;
        }
        Ok(false)
    }

    async fn fetch_inventory_entries(
        &self,
        paths: &[PathBuf],
    ) -> Result<HashMap<PathBuf, FileMetadata>> {
        if paths.is_empty() {
            return Ok(HashMap::new());
        }

        let mut targets: Vec<String> = Vec::with_capacity(paths.len());
        for path in paths {
            match path.to_str() {
                Some(path_str) => targets.push(path_str.to_string()),
                None => {
                    tracing::warn!(
                        target: "drive::sync",
                        id = %self.id,
                        path = %path.display(),
                        "Unable to convert path to UTF-8 for inventory lookup"
                    );
                }
            }
        }

        if targets.is_empty() {
            return Ok(HashMap::new());
        }

        let inventory = self.inventory.clone();
        let entries = task::spawn_blocking(move || -> Result<HashMap<PathBuf, FileMetadata>> {
            let targets: Vec<&str> = targets.iter().map(String::as_str).collect();
            Ok(inventory
                .query_by_paths(&targets)?
                .into_iter()
                .map(|entry| (PathBuf::from(&entry.local_path), entry))
                .collect())
        })
        .await??;

        Ok(entries)
    }

    async fn build_sync_plan(
        &self,
        _parent: &PathBuf,
        mode: SyncMode,
        paths: &[PathBuf],
        remote_files: &HashMap<PathBuf, FileResponse>,
        local_files: &HashMap<PathBuf, LocalFileInfo>,
        inventory_entries: &HashMap<PathBuf, FileMetadata>,
    ) -> SyncPlan {
        let mut plan = SyncPlan::default();
        let matcher = self.ignore_matcher.read().await;
        let read_only = self.is_read_only();

        for path in paths {
            if matcher.is_match(path) {
                tracing::trace!(
                    target: "drive::sync",
                    id = %self.id,
                    path = %path.display(),
                    "Skipping ignored path in sync plan"
                );
                continue;
            }
            let local_info = local_files
                .get(path)
                .cloned()
                .unwrap_or_else(LocalFileInfo::missing);
            // Fylvo: a local deletion waits for the user's answer; do not bring it back yet.
            if !local_info.exists && self.is_deletion_held(DeletionSide::Server, path) {
                continue;
            }
            let remote = remote_files.get(path);
            let inventory = inventory_entries.get(path);
            plan_entry_actions(path, mode, remote, &local_info, inventory, read_only, &mut plan);
        }

        plan
    }

    /// Fylvo: hold the local deletions of this plan if they are a mass deletion. Returns the
    /// plan without them in that case.
    fn hold_mass_local_deletion(&self, plan: SyncPlan) -> SyncPlan {
        // Paths the user confirmed (or renamed on the server) pass; only the others are checked.
        let doomed: Vec<PathBuf> = doomed_local_paths(&plan)
            .into_iter()
            .filter(|p| !self.is_local_deletion_confirmed(std::slice::from_ref(p)))
            .collect();
        if doomed.is_empty() {
            return plan;
        }
        // Paths the user chose to keep stay, without asking again.
        let (kept, doomed): (Vec<PathBuf>, Vec<PathBuf>) =
            doomed.into_iter().partition(|p| self.is_kept_locally(p));
        let plan = without_paths(plan, &kept);
        // Many deletions at once (or within a short time) wait for the user's answer. Once a
        // deletion is held, the following ones wait too.
        if !doomed.is_empty() && self.hold_local_deletion_if_needed(&doomed) {
            return without_paths(plan, &doomed);
        }
        plan
    }

    async fn process_walk_requests(
        &self,
        requests: Vec<WalkRequest>,
        aggregate_error: &mut SyncAggregateError,
    ) {
        for walk in requests {
            match self.collect_child_targets(&walk.path).await {
                Ok(result) => {
                    if result.paths.is_empty() {
                        tracing::trace!(
                            target: "drive::sync",
                            id = %self.id,
                            path = %walk.path.display(),
                            timing = ?walk.timing,
                            "Skipping walk, no children discovered"
                        );
                        continue;
                    }

                    tracing::debug!(
                        target: "drive::sync",
                        id = %self.id,
                        directory = %walk.path.display(),
                        reason = ?walk.reason,
                        next_mode = ?walk.mode,
                        children = result.paths.len(),
                        timing = ?walk.timing,
                        "Walking child directory"
                    );

                    let prefetched = if result.remote_files.is_empty() {
                        None
                    } else {
                        Some(result.remote_files)
                    };
                    let child_future =
                        Box::pin(self.sync_group(&walk.path, &result.paths, walk.mode, prefetched));
                    if let Err(err) = child_future.await {
                        tracing::error!(
                            target: "drive::sync",
                            id = %self.id,
                            directory = %walk.path.display(),
                            error = %err,
                            timing = ?walk.timing,
                            "Failed to walk child directory"
                        );
                        aggregate_error.push(walk.path.clone(), err);
                    }
                }
                Err(err) => {
                    tracing::warn!(
                        target: "drive::sync",
                        id = %self.id,
                        directory = %walk.path.display(),
                        error = %err,
                        timing = ?walk.timing,
                        "Failed to enumerate child directory"
                    );
                    aggregate_error.push(walk.path.clone(), err);
                }
            }
        }
    }

    async fn collect_child_targets(&self, directory: &PathBuf) -> Result<CollectChildResult> {
        let dir_clone = directory.clone();
        let mut children = Vec::new();
        match fs::read_dir(&dir_clone) {
            Ok(entries) => {
                for entry in entries.flatten() {
                    children.push(entry.path());
                }
            }
            Err(err) if err.kind() == io::ErrorKind::NotFound => {}
            Err(err) => {
                return Err(err).context(format!(
                    "failed to enumerate local directory {}",
                    dir_clone.display()
                ));
            }
        };

        let (remote_children, remote_files) = self.list_remote_children(directory).await?;

        let matcher = self.ignore_matcher.read().await;
        let mut dedup: HashSet<PathBuf> = HashSet::new();
        for child in children.into_iter().chain(remote_children.into_iter()) {
            // Fylvo: a symlink or another file system here is never followed or synced, also not
            // a server entry of the same name (it would replace the link).
            #[cfg(target_os = "linux")]
            if crate::drive::local_linux::is_foreign(&child) {
                continue;
            }
            // Fylvo: a folder only on the server is a folder too (files-only ignore names).
            let ignored = match remote_files.get(&child) {
                Some(remote) if !child.exists() => {
                    matcher.is_match_as(&child, remote.file_type == file_type::FOLDER)
                }
                _ => matcher.is_match(&child),
            };
            if ignored {
                tracing::trace!(
                    target: "drive::sync",
                    id = %self.id,
                    path = %child.display(),
                    "Skipping ignored path during child collection"
                );
                continue;
            }
            dedup.insert(child);
        }
        drop(matcher);

        Ok(CollectChildResult {
            paths: dedup.into_iter().collect(),
            remote_files,
        })
    }

    /// Lists remote children and returns both the local paths and the file info map.
    async fn list_remote_children(
        &self,
        directory: &PathBuf,
    ) -> Result<(Vec<PathBuf>, HashMap<PathBuf, FileResponse>)> {
        let (remote_base, sync_root) = {
            let config = self.config.read().await;
            (config.remote_path.clone(), config.sync_path.clone())
        };

        let remote_dir_uri =
            match local_path_to_cr_uri(directory.clone(), sync_root.clone(), remote_base.clone()) {
                Ok(uri) => uri,
                Err(err) => {
                    tracing::warn!(
                        target: "drive::sync",
                        id = %self.id,
                        path = %directory.display(),
                        error = %err,
                        "Failed to map local directory to remote URI while walking"
                    );
                    return Ok((Vec::new(), HashMap::new()));
                }
            };
        let remote_dir_uri_str = remote_dir_uri.to_string();

        let remote_base_uri = match CrUri::new(&remote_base) {
            Ok(uri) => uri,
            Err(err) => {
                tracing::warn!(
                    target: "drive::sync",
                    id = %self.id,
                    remote_base = %remote_base,
                    error = %err,
                    "Failed to parse remote base URI while walking"
                );
                return Ok((Vec::new(), HashMap::new()));
            }
        };

        let mut previous_response = None;
        let mut children = Vec::new();
        let mut remote_files: HashMap<PathBuf, FileResponse> = HashMap::new();

        loop {
            let response = match self
                .cr_client
                .list_files_all(
                    previous_response.as_ref(),
                    remote_dir_uri_str.as_str(),
                    REMOTE_PAGE_SIZE,
                )
                .await
            {
                Ok(resp) => resp,
                Err(ApiError::ApiError { code, .. })
                    if code == ErrorCode::ParentNotExist as i32 =>
                {
                    // Fylvo: see fetch_remote_file_infos.
                    if *directory == sync_root {
                        anyhow::bail!("the synced folder is missing on the server");
                    }
                    if self.server_has_case_variant(directory).await? {
                        anyhow::bail!("the folder exists on the server in another case");
                    }
                    tracing::debug!(
                        target: "drive::sync",
                        id = %self.id,
                        directory = %directory.display(),
                        "Remote directory missing during walk"
                    );
                    return Ok((Vec::new(), HashMap::new()));
                }
                Err(err) => {
                    return Err(err.into());
                }
            };

            for file in &response.res.files {
                if is_symbolic_link(file) {
                    continue;
                }

                match CrUri::new(&file.path).and_then(|file_uri| {
                    remote_path_to_local_relative_path(&file_uri, &remote_base_uri)
                }) {
                    Ok(relative) => {
                        let mut local_path = sync_root.clone();
                        local_path.push(relative);
                        if local_path
                            .parent()
                            .map(|p| p == directory.as_path())
                            .unwrap_or(false)
                        {
                            children.push(local_path.clone());
                            remote_files.insert(local_path, file.clone());
                        }
                    }
                    Err(err) => {
                        tracing::warn!(
                            target: "drive::sync",
                            id = %self.id,
                            remote_path = %file.path,
                            error = %err,
                            "Failed to map remote child to local path"
                        );
                    }
                }
            }

            if !response.more {
                break;
            }

            previous_response = Some(response);
        }

        Ok((children, remote_files))
    }
}

/// Fylvo: the decisions of a sync pass, kept free of I/O so they can be tested.
/// Fylvo: on Linux every file is local and a download replaces the whole file, so a server
/// change of the date alone (same content, same etag) only updates the row. Windows keeps its
/// behavior (a pinned file is downloaded again).
const ONLY_DATE_CHANGED_IS_ROW_UPDATE: bool = cfg!(target_os = "linux");

fn plan_entry_actions(
    path: &PathBuf,
    mode: SyncMode,
    remote: Option<&FileResponse>,
    local: &LocalFileInfo,
    inventory: Option<&FileMetadata>,
    read_only: bool,
    plan: &mut SyncPlan,
) {
    match (remote, local.exists) {
        (Some(remote_entry), true) => plan_entry_with_remote_and_local(
            path,
            mode,
            remote_entry,
            local,
            inventory,
            read_only,
            plan,
        ),
        (Some(remote_entry), false) => {
            plan.actions
                .push(SyncAction::CreatePlaceholderAndInventory {
                    path: path.clone(),
                    remote: remote_entry.clone(),
                });
        }
        (None, true) => plan_entry_with_local_only(path, mode, local, plan),
        (None, false) => {}
    }
}

fn plan_entry_with_remote_and_local(
    path: &PathBuf,
    mode: SyncMode,
    remote: &FileResponse,
    local: &LocalFileInfo,
    inventory: Option<&FileMetadata>,
    read_only: bool,
    plan: &mut SyncPlan,
) {
    let remote_is_dir = remote.file_type == file_type::FOLDER;

    if local.is_directory != remote_is_dir {
        // Fylvo: only an unchanged online-only placeholder may simply go; anything else is
        // kept as a conflict copy.
        if local.is_placeholder() && local.partial_on_disk() && local.in_sync() {
            plan.actions.push(SyncAction::DeleteLocalAndInventory {
                path: path.clone(),
                skip_if_not_empty: false,
            });
        } else {
            plan.actions.push(SyncAction::RenameLocalWithConflict {
                original: path.clone(),
                renamed: generate_conflict_path(path),
            });
        }

        plan.actions
            .push(SyncAction::CreatePlaceholderAndInventory {
                path: path.clone(),
                remote: remote.clone(),
            });
        return;
    }

    let remote_etag = remote.primary_entity.as_deref().unwrap_or("");
    let etag_match = inventory
        .map(|entry| entry.etag == remote_etag)
        .unwrap_or(false);
    let modify_date_match = inventory
        .and_then(|entry| {
            remote
                .updated_at
                .parse::<DateTime<Utc>>()
                .ok()
                .map(|updated_at| updated_at.timestamp() == entry.updated_at)
        })
        .unwrap_or(false);

    // Fylvo: a folder row without the server's file ID (see `case.rs`) gets it now.
    let lacks_file_id =
        inventory.is_some_and(|entry| !entry.metadata.contains_key(FILE_ID_KEY));

    if remote_is_dir {
        if !etag_match || !modify_date_match || lacks_file_id {
            plan.actions.push(SyncAction::UpdateInventoryFromRemote {
                path: path.clone(),
                remote: remote.clone(),
                invalidate_all: false,
            });
        }
        maybe_enqueue_walk_for_directory(path, mode, local, false, false, plan);
        return;
    }

    if etag_match && !modify_date_match && ONLY_DATE_CHANGED_IS_ROW_UPDATE && local.in_sync() {
        plan.actions.push(SyncAction::UpdateInventoryFromRemote {
            path: path.clone(),
            remote: remote.clone(),
            invalidate_all: false,
        });
    } else if !etag_match || !modify_date_match {
        plan_file_actions(path, remote, local, inventory, read_only, plan);
    } else if local_change_not_uploaded(local, inventory, read_only) {
        // Fylvo: the server has the version this file was changed from, so the change is
        // still to be uploaded. Before, a failed upload (for example refused for a moment
        // during a case rename on the server) stayed undone until the file changed again.
        plan.actions.push(SyncAction::QueueUpload {
            path: path.clone(),
            reason: UploadReason::NotUploaded,
        });
    }
}

/// Whether something with local content is at a path the sync planned from a state without it:
/// to create from the server (only a synced placeholder in sync may be taken over), or to update
/// a file that was in sync.
fn appeared_meanwhile(local: &LocalFileInfo) -> bool {
    local.exists && !(local.is_placeholder() && local.in_sync())
}

/// Whether a local file with an inventory row holds a change that is not on the server: it is
/// no synced placeholder or not in sync. Not an online-only placeholder (no local bytes, so no
/// local change), not in a read-only drive (nothing can be uploaded there) and not while a
/// conflict waits for the user's choice.
fn local_change_not_uploaded(
    local: &LocalFileInfo,
    inventory: Option<&FileMetadata>,
    read_only: bool,
) -> bool {
    let Some(entry) = inventory else {
        return false;
    };
    !read_only
        && entry.conflict_state != Some(ConflictState::Pending)
        && (!local.is_placeholder() || (!local.in_sync() && !local.partial_on_disk()))
}

fn plan_entry_with_local_only(
    path: &PathBuf,
    mode: SyncMode,
    local: &LocalFileInfo,
    plan: &mut SyncPlan,
) {
    if !local.exists {
        return;
    }

    if local.is_directory {
        // Fylvo: only a placeholder folder that was never opened holds nothing local and can go
        // as a whole. Before, a normal folder (created here, not uploaded yet) was deleted
        // with all its files too.
        if local.is_placeholder() && !local.is_folder_populated() {
            plan.actions.push(SyncAction::DeleteLocalAndInventory {
                path: path.clone(),
                skip_if_not_empty: false,
            });
            return;
        }

        maybe_enqueue_walk_for_directory(path, mode, local, true, true, plan);
        if local.is_placeholder() {
            plan.actions.push(SyncAction::DeleteLocalAndInventory {
                path: path.clone(),
                skip_if_not_empty: true,
            });
        }
        plan.actions
            .push(SyncAction::CreateRemoteFolderIfExist { path: path.clone() });
        return;
    }

    if local.is_placeholder() && local.in_sync() {
        plan.actions.push(SyncAction::DeleteLocalAndInventory {
            path: path.clone(),
            skip_if_not_empty: false,
        });
        return;
    }

    plan.actions.push(SyncAction::QueueUpload {
        path: path.clone(),
        reason: UploadReason::RemoteMissing,
    });
}

fn plan_file_actions(
    path: &PathBuf,
    remote: &FileResponse,
    local: &LocalFileInfo,
    inventory: Option<&FileMetadata>,
    read_only: bool,
    plan: &mut SyncPlan,
) {
    if !local.is_placeholder() || !local.in_sync() {
        // Fylvo: in a read-only drive the local change can never be uploaded. Keep it as a
        // conflict copy and take the server's version, instead of skipping it forever.
        if read_only {
            plan.actions.push(SyncAction::RenameLocalWithConflict {
                original: path.clone(),
                renamed: generate_conflict_path(path),
            });
            plan.actions
                .push(SyncAction::CreatePlaceholderAndInventory {
                    path: path.clone(),
                    remote: remote.clone(),
                });
            return;
        }
        let conflicting =
            inventory.is_some_and(|inv| inv.conflict_state == Some(ConflictState::Pending));
        if !conflicting {
            plan.actions.push(SyncAction::QueueUpload {
                path: path.clone(),
                reason: UploadReason::RemoteMismatch,
            });
        }
        return;
    }

    let pinned = local.pinned();
    if pinned == PinState::Pinned {
        plan.actions.push(SyncAction::QueueDownload {
            path: path.clone(),
            remote: remote.clone(),
        });
    } else {
        plan.actions.push(SyncAction::UpdateInventoryFromRemote {
            path: path.clone(),
            remote: remote.clone(),
            invalidate_all: !local.partial_on_disk(),
        });
    }
}

fn maybe_enqueue_walk_for_directory(
    path: &PathBuf,
    parent_mode: SyncMode,
    local: &LocalFileInfo,
    force_diff: bool,
    immediate: bool,
    plan: &mut SyncPlan,
) {
    if !local.is_directory {
        return;
    }

    let timing = if immediate {
        WalkTiming::Immediate
    } else {
        WalkTiming::Deferred
    };

    if matches!(
        parent_mode,
        SyncMode::FullHierarchy | SyncMode::PathAndFirstLayer
    ) && (local.is_folder_populated() || !local.is_placeholder())
    {
        let mode = next_child_mode(parent_mode);
        insert_walk_request(path.clone(), mode, WalkReason::ModePropagation, timing, plan);
        return;
    }

    if force_diff && parent_mode == SyncMode::PathOnly {
        insert_walk_request(
            path.clone(),
            SyncMode::PathOnly,
            WalkReason::DiffTriggered,
            timing,
            plan,
        );
    }
}

fn insert_walk_request(
    path: PathBuf,
    mode: SyncMode,
    reason: WalkReason,
    timing: WalkTiming,
    plan: &mut SyncPlan,
) {
    if plan
        .walk_requests
        .iter()
        .any(|request| request.path == path && request.mode == mode)
    {
        return;
    }

    plan.walk_requests.push(WalkRequest {
        path,
        mode,
        reason,
        timing,
    });
}

/// Fylvo: paths this plan deletes on this PC. A deletion that is followed by a new
/// placeholder at the same path (file became folder or the other way) is a replacement.
fn doomed_local_paths(plan: &SyncPlan) -> Vec<PathBuf> {
    plan.actions
        .iter()
        .filter_map(|action| match action {
            SyncAction::DeleteLocalAndInventory { path, .. } => Some(path),
            _ => None,
        })
        .filter(|path| {
            !plan.actions.iter().any(|action| {
                matches!(action, SyncAction::CreatePlaceholderAndInventory { path: p, .. } if p == *path)
            })
        })
        .cloned()
        .collect()
}

/// Fylvo: the plan without any action or walk for the given paths.
fn without_paths(plan: SyncPlan, paths: &[PathBuf]) -> SyncPlan {
    let keep = |path: &PathBuf| !paths.contains(path);
    SyncPlan {
        actions: plan
            .actions
            .into_iter()
            .filter(|action| keep(action.path()))
            .collect(),
        walk_requests: plan
            .walk_requests
            .into_iter()
            .filter(|walk| keep(&walk.path))
            .collect(),
    }
}

impl SyncAction {
    fn path(&self) -> &PathBuf {
        match self {
            SyncAction::CreatePlaceholderAndInventory { path, .. }
            | SyncAction::UpdateInventoryFromRemote { path, .. }
            | SyncAction::QueueUpload { path, .. }
            | SyncAction::QueueDownload { path, .. }
            | SyncAction::DeleteLocalAndInventory { path, .. }
            | SyncAction::CreateRemoteFolderIfExist { path } => path,
            SyncAction::RenameLocalWithConflict { original, .. } => original,
        }
    }
}

#[cfg(test)]
mod tests {
    //! Fylvo: tests of the sync decisions (no Windows sync root needed).
    use super::*;
    use crate::drive::local::SyncState;

    const UPDATED: &str = "2026-11-19T10:00:00Z";

    fn local(is_directory: bool, state: Option<SyncState>) -> LocalFileInfo {
        LocalFileInfo {
            exists: true,
            is_directory,
            file_size: Some(10),
            last_modified: None,
            sync_state: state,
            pin_state: PinState::Unspecified,
        }
    }

    fn state(in_sync: bool, partial_on_disk: bool) -> Option<SyncState> {
        Some(SyncState {
            is_placeholder: true,
            in_sync,
            partial_on_disk,
        })
    }

    /// A normal file or folder, not a placeholder (created on this PC, not uploaded yet).
    fn normal(is_directory: bool) -> LocalFileInfo {
        local(is_directory, None)
    }
    /// A synced placeholder whose content is on this PC.
    fn hydrated_in_sync(is_directory: bool) -> LocalFileInfo {
        local(is_directory, state(true, false))
    }
    /// A synced placeholder that is online only (file) or never opened (folder).
    fn online_only(is_directory: bool) -> LocalFileInfo {
        local(is_directory, state(true, true))
    }
    /// A placeholder changed on this PC.
    fn modified() -> LocalFileInfo {
        local(false, state(false, false))
    }

    fn remote(is_folder: bool, etag: &str) -> FileResponse {
        FileResponse {
            file_type: if is_folder { file_type::FOLDER } else { file_type::FILE },
            created_at: UPDATED.to_string(),
            updated_at: UPDATED.to_string(),
            primary_entity: Some(etag.to_string()),
            ..Default::default()
        }
    }

    fn inventory(etag: &str) -> FileMetadata {
        FileMetadata {
            id: 1,
            drive_id: Uuid::nil(),
            is_folder: false,
            local_path: String::new(),
            created_at: 0,
            updated_at: UPDATED.parse::<DateTime<Utc>>().unwrap().timestamp(),
            etag: etag.to_string(),
            metadata: HashMap::new(),
            props: None,
            permissions: String::new(),
            shared: false,
            size: 10,
            conflict_state: None,
        }
    }

    fn plan_one(
        remote: Option<&FileResponse>,
        local: &LocalFileInfo,
        inventory: Option<&FileMetadata>,
        read_only: bool,
    ) -> SyncPlan {
        let mut plan = SyncPlan::default();
        let path = PathBuf::from(r"C:\S\x");
        plan_entry_actions(
            &path,
            SyncMode::PathOnly,
            remote,
            local,
            inventory,
            read_only,
            &mut plan,
        );
        plan
    }

    fn kinds(plan: &SyncPlan) -> Vec<&'static str> {
        plan.actions
            .iter()
            .map(|action| match action {
                SyncAction::CreatePlaceholderAndInventory { .. } => "create",
                SyncAction::UpdateInventoryFromRemote { .. } => "update",
                SyncAction::QueueUpload { .. } => "upload",
                SyncAction::QueueDownload { .. } => "download",
                SyncAction::DeleteLocalAndInventory { .. } => "delete",
                SyncAction::CreateRemoteFolderIfExist { .. } => "create_remote_folder",
                SyncAction::RenameLocalWithConflict { .. } => "conflict_copy",
            })
            .collect()
    }

    #[test]
    fn deleted_on_server_synced_file_is_deleted_here() {
        let plan = plan_one(None, &hydrated_in_sync(false), None, false);
        assert_eq!(kinds(&plan), ["delete"]);
    }

    #[test]
    fn local_only_new_or_changed_file_is_uploaded_never_deleted() {
        assert_eq!(kinds(&plan_one(None, &normal(false), None, false)), ["upload"]);
        assert_eq!(kinds(&plan_one(None, &modified(), None, false)), ["upload"]);
    }

    #[test]
    fn new_local_folder_is_uploaded_not_deleted() {
        // Before this fix the folder and all its files were deleted for good.
        let plan = plan_one(None, &normal(true), None, false);
        assert_eq!(kinds(&plan), ["create_remote_folder"]);
        assert_eq!(plan.walk_requests.len(), 1);
    }

    #[test]
    fn deleted_on_server_folder() {
        // Never opened: no local data, removed as a whole.
        assert_eq!(kinds(&plan_one(None, &online_only(true), None, false)), ["delete"]);
        // Opened: the content is checked one by one first, the folder goes only when empty.
        let plan = plan_one(None, &hydrated_in_sync(true), None, false);
        assert_eq!(kinds(&plan), ["delete", "create_remote_folder"]);
        assert!(matches!(
            plan.actions[0],
            SyncAction::DeleteLocalAndInventory { skip_if_not_empty: true, .. }
        ));
    }

    #[test]
    fn missing_here_but_on_server_comes_back() {
        // A lost local file is never deleted on the server by a sync pass.
        let missing = LocalFileInfo::missing();
        let plan = plan_one(Some(&remote(false, "e1")), &missing, None, false);
        assert_eq!(kinds(&plan), ["create"]);
    }

    #[test]
    fn local_change_is_uploaded_unless_read_only() {
        let server = remote(false, "e2");
        let known = inventory("e1");
        assert_eq!(
            kinds(&plan_one(Some(&server), &modified(), Some(&known), false)),
            ["upload"]
        );
        // Read-only drive: keep the local change as a copy, take the server's version.
        assert_eq!(
            kinds(&plan_one(Some(&server), &modified(), Some(&known), true)),
            ["conflict_copy", "create"]
        );
    }

    #[test]
    fn local_change_whose_upload_failed_is_uploaded_again() {
        // The server still has the version the file was changed from: the upload was refused
        // (40905 during a case rename on the server) or failed otherwise.
        let server = remote(false, "e1");
        let known = inventory("e1");
        assert_eq!(
            kinds(&plan_one(Some(&server), &modified(), Some(&known), false)),
            ["upload"]
        );
        assert_eq!(
            kinds(&plan_one(Some(&server), &normal(false), Some(&known), false)),
            ["upload"]
        );
        // Not an online-only file (no local bytes: only its metadata changed).
        let online_only_changed = local(false, state(false, true));
        assert!(plan_one(Some(&server), &online_only_changed, Some(&known), false)
            .actions
            .is_empty());
        // Not in a read-only drive, and not while a conflict waits for the user.
        assert!(plan_one(Some(&server), &modified(), Some(&known), true).actions.is_empty());
        let mut waiting = inventory("e1");
        waiting.conflict_state = Some(ConflictState::Pending);
        assert!(plan_one(Some(&server), &modified(), Some(&waiting), false).actions.is_empty());
    }

    #[test]
    fn a_file_that_appeared_after_planning_is_not_taken_for_the_servers() {
        // Excel put its new file in place after the sync saw the path empty.
        assert!(appeared_meanwhile(&normal(false)));
        assert!(appeared_meanwhile(&modified()));
        assert!(appeared_meanwhile(&normal(true)));
        // Still empty, or a placeholder the sync itself put there: created as planned.
        assert!(!appeared_meanwhile(&LocalFileInfo::missing()));
        assert!(!appeared_meanwhile(&hydrated_in_sync(false)));
        assert!(!appeared_meanwhile(&online_only(false)));
    }

    #[test]
    fn server_change_updates_synced_file() {
        let plan = plan_one(
            Some(&remote(false, "e2")),
            &hydrated_in_sync(false),
            Some(&inventory("e1")),
            false,
        );
        assert_eq!(kinds(&plan), ["update"]);
    }

    #[test]
    fn unchanged_file_needs_nothing() {
        let plan = plan_one(
            Some(&remote(false, "e1")),
            &hydrated_in_sync(false),
            Some(&inventory("e1")),
            false,
        );
        assert!(plan.actions.is_empty());
    }

    #[test]
    fn doomed_paths_skip_replacements_and_without_paths_removes_all_steps() {
        let a = PathBuf::from(r"C:\S\a");
        let b = PathBuf::from(r"C:\S\b");
        let mut plan = SyncPlan::default();
        // a: deleted on the server (folder, with a walk and a re-create step).
        plan.actions.push(SyncAction::DeleteLocalAndInventory {
            path: a.clone(),
            skip_if_not_empty: true,
        });
        plan.actions
            .push(SyncAction::CreateRemoteFolderIfExist { path: a.clone() });
        insert_walk_request(
            a.clone(),
            SyncMode::PathOnly,
            WalkReason::DiffTriggered,
            WalkTiming::Immediate,
            &mut plan,
        );
        // b: replaced (file became folder on the server), no deletion.
        plan.actions.push(SyncAction::DeleteLocalAndInventory {
            path: b.clone(),
            skip_if_not_empty: false,
        });
        plan.actions.push(SyncAction::CreatePlaceholderAndInventory {
            path: b.clone(),
            remote: remote(true, ""),
        });

        let doomed = doomed_local_paths(&plan);
        assert_eq!(doomed, vec![a.clone()]);

        let held = without_paths(plan, &doomed);
        assert!(held.actions.iter().all(|action| action.path() == &b));
        assert!(held.walk_requests.is_empty());
    }
}

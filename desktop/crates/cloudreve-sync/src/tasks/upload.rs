use std::{
    collections::HashMap,
    path::{Path, PathBuf},
    str::FromStr,
    sync::{Arc, Mutex},
    time::{Duration, Instant, SystemTime},
};


use crate::utils::toast::send_conflict_toast;
use crate::{
    drive::{placeholder::CrPlaceholder, utils::local_path_to_cr_uri},
    inventory::{ConflictState, FileMetadata, InventoryDb},
    tasks::queue::QueuedTask,
    uploader::{ProgressCallback, ProgressUpdate, UploadParams, Uploader, UploaderConfig},
};
use anyhow::{Context, Result};
use bytes::Bytes;
use cloudreve_api::{
    ApiError, Client,
    api::ExplorerApi,
    error::ErrorCode,
    models::explorer::{CreateFileService, FileResponse, FileUpdateService, file_type},
};
use dashmap::DashMap;
use tokio_util::sync::CancellationToken;
use tracing::{debug, info, warn};
use uuid::Uuid;

use super::types::TaskProgress;

/// Progress reporter that updates task progress in-memory via a DashMap reference.
/// Does NOT persist to inventory - only keeps in-memory for real-time queries.
pub struct InMemoryProgressReporter {
    task_id: String,
    progress_map: Arc<DashMap<String, TaskProgress>>,
}

impl InMemoryProgressReporter {
    pub fn new(task_id: String, progress_map: Arc<DashMap<String, TaskProgress>>) -> Self {
        Self {
            task_id,
            progress_map,
        }
    }
}

impl ProgressCallback for InMemoryProgressReporter {
    fn on_progress(&self, update: ProgressUpdate) {
        if let Some(mut entry) = self.progress_map.get_mut(&self.task_id) {
            entry.update_from_progress(&update);
        }
    }
}

pub struct UploadTask<'a> {
    inventory: Arc<InventoryDb>,
    cr_client: Arc<Client>,
    drive_id: &'a str,
    sync_path: PathBuf,
    remote_base: String,
    task: &'a QueuedTask,
    local_file: Option<CrPlaceholder>,
    inventory_meta: Option<FileMetadata>,
    cancel_token: CancellationToken,
    /// Reference to the in-memory progress map for real-time progress updates
    progress_map: Arc<DashMap<String, TaskProgress>>,
    /// Fylvo: size and last write time of the file when its upload started.
    started: Option<ContentState>,
    /// Fylvo: the file changed while it was uploaded; it needs another upload.
    changed_meanwhile: bool,
}

impl<'a> UploadTask<'a> {
    pub fn new(
        inventory: Arc<InventoryDb>,
        cr_client: Arc<Client>,
        drive_id: &'a str,
        task: &'a QueuedTask,
        sync_path: PathBuf,
        remote_base: String,
        progress_map: Arc<DashMap<String, TaskProgress>>,
    ) -> Self {
        Self {
            inventory,
            cr_client,
            drive_id,
            local_file: None,
            inventory_meta: None,
            task,
            sync_path,
            remote_base,
            cancel_token: CancellationToken::new(),
            progress_map,
            started: None,
            changed_meanwhile: false,
        }
    }

    /// Whether the file changed while it was uploaded (see `file_uploaded`).
    pub fn changed_meanwhile(&self) -> bool {
        self.changed_meanwhile
    }

    /// Set the cancellation token
    pub fn with_cancel_token(mut self, token: CancellationToken) -> Self {
        self.cancel_token = token;
        self
    }

    // Upload a local file/folder to cloud
    pub async fn execute(&mut self) -> Result<()> {
        // Get local file info
        let placeholder_file = CrPlaceholder::new(
            &self.task.payload.local_path,
            self.sync_path.clone(),
            Uuid::from_str(self.drive_id)?,
        );
        if !placeholder_file.local_file_info.exists {
            info!(
                target: "tasks::upload",
                task_id = %self.task.task_id,
                local_path = %self.task.payload.local_path_display(),
                "Local file does not exist, skipping upload"
            );
            return Ok(());
        }

        if placeholder_file.local_file_info.in_sync()
            && !placeholder_file.local_file_info.is_directory()
        {
            info!(
                target: "tasks::upload",
                task_id = %self.task.task_id,
                local_path = %self.task.payload.local_path_display(),
                "Local file is in sync, skipping upload"
            );
            return Ok(());
        }

        let is_directory = placeholder_file.local_file_info.is_directory;
        self.local_file = Some(placeholder_file);

        // Get inventory meta
        let path_str = self
            .task
            .payload
            .local_path
            .to_str()
            .context("failed to get local path as str")?;
        self.inventory_meta = self
            .inventory
            .query_by_path(path_str)
            .context("failed to get inventory meta")?;

        // clear file error state
        // Mark file as error state
        if let Err(e) = self
            .local_file
            .as_mut()
            .unwrap()
            .update_sync_error_state(false)
        {
            warn!(target: "tasks::upload", task_id = %self.task.task_id, local_path = %self.task.payload.local_path_display(), error = ?e, "Failed to clear sync error state");
        }

        // Fylvo: size and write time as the file has them now, read through a handle after the
        // error state is cleared, right before the content is read. The upload sends exactly
        // this size, and the change check in `file_uploaded` compares with it.
        let mut file_size = 0;
        if !is_directory {
            let Some(started) = content_state(&self.task.payload.local_path) else {
                let err = anyhow::anyhow!("the file cannot be read right now");
                return self.handle_error(Err(err)).await;
            };
            file_size = started.0;
            let info = &mut self.local_file.as_mut().unwrap().local_file_info;
            info.file_size = Some(started.0);
            info.last_modified = Some(started.1);
            self.started = Some(started);
        }

        // Handle empty files and directories separately
        let upload_res = match (
            is_directory,
            file_size == 0 && !self.task.payload.force_override,
            self.inventory_meta.is_none(),
        ) {
            (true, _, _) => self.create_empty_file_or_folder().await,
            (false, true, true) => self.create_empty_file_or_folder().await,
            (false, true, false) => self.clear_file_content().await,
            (false, false, _) => self.upload_file_with_uploader().await,
        };

        self.handle_error(upload_res).await
    }

    async fn handle_error(&mut self, r: Result<()>) -> Result<()> {
        let path = &self.task.payload.local_path;
        match r {
            Ok(()) => Ok(()),
            // Fylvo: cancelled for a rename, a deletion or a download: not a failure.
            Err(e) if self.cancel_token.is_cancelled() => Err(e),
            Err(e) => {
                // Fylvo: a refused name (40905) can pass with a case rename any moment, any
                // other failure waits before the sync tries the file again.
                if !crate::drive::case::is_name_case_taken(&e) {
                    note_failure(path);
                }
                if is_conflict_error(&e) {
                    warn!(
                        target: "tasks::upload",
                        task_id = %self.task.task_id,
                        local_path = %self.task.payload.local_path_display(),
                        "Conflict detected, server has newer version or object exists"
                    );

                    // Mark the file as conflicted in the inventory
                    let path_str = self.task.payload.local_path.to_str().unwrap_or_default();
                    if let Err(mark_err) = self
                        .inventory
                        .mark_as_conflicted(path_str, Some(ConflictState::Pending))
                    {
                        warn!(
                            target: "tasks::upload",
                            task_id = %self.task.task_id,
                            local_path = %self.task.payload.local_path_display(),
                            error = ?mark_err,
                            "Failed to mark file as conflicted"
                        );
                    }

                    // Send conflict toast
                    send_conflict_toast(
                        self.drive_id,
                        &self.task.payload.local_path,
                        self.inventory_meta
                            .as_ref()
                            .map(|meta| meta.id)
                            .unwrap_or(0),
                    )
                }

                // Fylvo: the server has the name in other upper/lower case. Tried again only
                // when the file or the sync changes it, never in a loop.
                crate::drive::case::report_name_case_taken(&self.task.payload.local_path, &e);

                // Mark file as error state
                if let Err(e) = self
                    .local_file
                    .as_mut()
                    .unwrap()
                    .update_sync_error_state(true)
                {
                    warn!(target: "tasks::upload", task_id = %self.task.task_id, local_path = %self.task.payload.local_path_display(), error = ?e, "Failed to update sync error state");
                }
                Err(e)
            }
        }
    }

    async fn clear_file_content(&mut self) -> Result<()> {
        info!(
            target: "tasks::upload",
            task_id = %self.task.task_id,
            local_path = %self.task.payload.local_path_display(),
            "Clearing file content with update request"
        );

        let uri = local_path_to_cr_uri(
            self.task.payload.local_path.clone(),
            self.sync_path.clone(),
            self.remote_base.clone(),
        )
        .context("failed to convert local path to cloudreve uri")?
        .to_string();
        let etag = self.inventory_meta.as_ref().unwrap().etag.clone();
        let res = self
            .cr_client
            .update_file(
                &FileUpdateService {
                    uri,
                    previous: Some(etag),
                },
                Bytes::new(),
            )
            .await;

        match res {
            Ok(file) => self.file_uploaded(&file),
            Err(e) => Err(e.into()),
        }
    }

    /// Upload a file using the new uploader module
    async fn upload_file_with_uploader(&mut self) -> Result<()> {
        let local_file = self.local_file.as_ref().unwrap();
        let file_size = local_file.local_file_info.file_size.unwrap_or(0);
        let is_new_file = self.inventory_meta.is_none();

        info!(
            target: "tasks::upload",
            task_id = %self.task.task_id,
            local_path = %self.task.payload.local_path_display(),
            file_size = file_size,
            "Starting file upload"
        );

        // Get remote URI
        let uri = local_path_to_cr_uri(
            self.task.payload.local_path.clone(),
            self.sync_path.clone(),
            self.remote_base.clone(),
        )
        .context("failed to convert local path to cloudreve uri")?
        .to_string();

        // If conflict state is set to Override, omit previous_version to force upload without version check
        let previous_version = if let Some(meta) = &self.inventory_meta {
            if matches!(meta.conflict_state, Some(ConflictState::Override)) {
                String::new() // Omit previous version when user chose to override
            } else {
                meta.etag.clone()
            }
        } else {
            String::new()
        };

        let params = UploadParams {
            local_path: self.task.payload.local_path.clone(),
            remote_uri: uri,
            file_size,
            mime_type: None, // Could be detected from file extension
            // Fylvo: a time before 1970 (valid on Windows) is not sent instead of a panic.
            last_modified: local_file
                .local_file_info
                .last_modified
                .and_then(|t| t.duration_since(SystemTime::UNIX_EPOCH).ok())
                .map(|t| t.as_millis() as i64),
            overwrite: !is_new_file || self.task.payload.force_override,
            previous_version,
            task_id: self.task.task_id.clone(),
            drive_id: self.drive_id.to_string(),
        };

        // Create uploader configuration
        let config = UploaderConfig::default();

        // Create uploader
        let uploader = Uploader::new(self.cr_client.clone(), self.inventory.clone(), config)
            .with_cancel_token(self.cancel_token.clone());

        // Create in-memory progress reporter (does not persist to inventory)
        let progress = InMemoryProgressReporter::new(
            self.task.task_id.clone(),
            Arc::clone(&self.progress_map),
        );

        // Execute upload
        let created = uploader
            .upload(params, progress)
            .await
            .context("failed to upload file")?;

        // Update local file placeholder status after successful upload
        self.finalize_upload(created).await?;

        Ok(())
    }

    /// Finalize upload by updating local file placeholder. `created` is the version the upload
    /// made, if the server named it.
    async fn finalize_upload(&mut self, created: Option<String>) -> Result<()> {
        // Get file info from server to confirm upload
        let uri = local_path_to_cr_uri(
            self.task.payload.local_path.clone(),
            self.sync_path.clone(),
            self.remote_base.clone(),
        )
        .context("failed to convert local path to cloudreve uri")?
        .to_string();

        let file_info = self
            .cr_client
            .get_file_info(&cloudreve_api::models::explorer::GetFileInfoService {
                uri: Some(uri),
                id: None,
                extended: None,
                folder_summary: None,
            })
            .await
            .context("failed to get file info after upload")?;

        // Fylvo: another device may have saved a newer version right after this upload. Its
        // version must not be taken for this file's content: that would mark the local file
        // in sync with bytes it does not have. Handled as a conflict instead.
        if !upload_is_current(created.as_deref(), &file_info) {
            return Err(anyhow::Error::new(ApiError::ApiError {
                code: ErrorCode::StaleVersion as i32,
                message: "another version was saved right after this upload".to_string(),
                error_detail: None,
                correlation_id: None,
                aggregated_errors: None,
            }));
        }

        self.file_uploaded(&file_info)
            .context("failed to commit uploaded file")?;
        Ok(())
    }

    async fn create_empty_file_or_folder(&mut self) -> Result<()> {
        info!(
            target: "tasks::upload",
            task_id = %self.task.task_id,
            local_path = %self.task.payload.local_path_display(),
            "Creating empty file/folder"
        );
        let local_file = &self.local_file.as_ref().unwrap().local_file_info;
        let uri = local_path_to_cr_uri(
            self.task.payload.local_path.clone(),
            self.sync_path.clone(),
            self.remote_base.clone(),
        )
        .context("failed to convert local path to cloudreve uri")?
        .to_string();

        debug!(target: "tasks::upload", task_id = %self.task.task_id, local_path = %self.task.payload.local_path_display(), uri = %uri, "Send test toast");

        // Create file in remote
        let res = self
            .cr_client
            .create_file(&CreateFileService {
                uri,
                file_type: if local_file.is_directory {
                    "folder".to_string()
                } else {
                    "file".to_string()
                },
                err_on_conflict: Some(!local_file.is_directory),
                metadata: None,
            })
            .await;
        match res {
            Ok(folder) => self.file_uploaded(&folder),
            Err(e) => Err(e.into()),
        }
    }

    fn file_uploaded(&mut self, file: &FileResponse) -> Result<()> {
        info!(
            target: "tasks::upload",
            task_id = %self.task.task_id,
            local_path = %self.task.payload.local_path_display(),
            "File uploaded"
        );

        self.local_file = Some(
            self.local_file
                .take()
                .unwrap()
                .with_mark_no_children(file.file_type == file_type::FOLDER)
                .with_remote_file(file),
        );

        // Fylvo: saved again while it was uploaded. Marking it in sync would take the newer
        // bytes for the uploaded version, and they would never reach the server. The server's
        // new version goes to the inventory only; the queue uploads the file again, a few
        // times in a row at most (a file written to all the time would else make versions
        // without end; after that the sync tries it again later).
        let path = &self.task.payload.local_path;
        if self.started.is_some() && content_state(path) != self.started {
            self.local_file
                .as_ref()
                .unwrap()
                .commit_inventory_only(&self.inventory)
                .context("failed to commit uploaded version")?;
            self.changed_meanwhile = true;
            info!(
                target: "tasks::upload",
                task_id = %self.task.task_id,
                local_path = %self.task.payload.local_path_display(),
                "File changed during the upload"
            );
            return Ok(());
        }

        // Fylvo: noted as just uploaded before it is marked in sync, so a delayed delete event
        // handled in between never takes the in-sync file for one the sync put back.
        forget_uploads(path);
        if let Err(err) = self.local_file.as_mut().unwrap().commit(self.inventory.clone()) {
            // Fylvo: the server has the new version either way. Without it in the inventory
            // the next upload would name the old one and conflict with this PC's own upload.
            let _ = self.local_file.as_ref().unwrap().commit_inventory_only(&self.inventory);
            return Err(err.context("failed to commit placeholder"));
        }

        self.local_file
            .as_mut()
            .unwrap()
            .update_sync_error_state(false)
            .context("failed to clear sync error state")?;
        Ok(())
    }
}

/// Size and last write time of a file, read through a handle (the folder entry that
/// `LocalFileInfo` reads may lag behind a writer that keeps the file open). Opening without
/// data access does not download an online-only file.
pub(crate) type ContentState = (u64, SystemTime);

pub(crate) fn content_state(path: &Path) -> Option<ContentState> {
    let meta = std::fs::metadata(path).ok()?;
    Some((meta.len(), meta.modified().ok()?))
}

/// How long the sync waits before it tries again a file whose upload failed.
const RETRY_FAILED_AFTER: Duration = Duration::from_secs(10 * 60);

/// How often in a row a file changed during its upload is uploaded again at once.
const MAX_UPLOADS_AGAIN: u32 = 3;

/// How long an upload refused with 40905 waits before it is tried again.
const NAME_TAKEN_RETRY: Duration = Duration::from_secs(90);

/// Recent upload trouble of a path.
#[derive(Default)]
struct Recent {
    failed_at: Option<Instant>,
    uploads_again: u32,
    name_retries: u32,
    uploaded_at: Option<Instant>,
}

/// By path, folded as NTFS compares names (a file may be asked for in another spelling).
static RECENT: Mutex<Option<HashMap<String, Recent>>> = Mutex::new(None);

fn with_recent<T>(path: &Path, f: impl FnOnce(&mut Recent) -> T) -> T {
    let mut recent = RECENT.lock().unwrap();
    let key = crate::drive::case::ntfs_fold(&path.to_string_lossy());
    f(recent.get_or_insert_with(HashMap::new).entry(key).or_default())
}

fn note_failure(path: &Path) {
    with_recent(path, |recent| recent.failed_at = Some(Instant::now()));
}

/// The upload of a path ended in sync: its trouble is forgotten, the time is kept for
/// `uploaded_recently`. Entries with nothing left to tell are dropped.
fn forget_uploads(path: &Path) {
    let mut recent = RECENT.lock().unwrap();
    let recent = recent.get_or_insert_with(HashMap::new);
    recent.retain(|_, entry| {
        entry.failed_at.is_some()
            || entry.uploads_again > 0
            || entry.name_retries > 0
            || entry.uploaded_at.is_some_and(|at| at.elapsed() < UPLOADED_RECENTLY)
    });
    recent.insert(
        crate::drive::case::ntfs_fold(&path.to_string_lossy()),
        Recent {
            uploaded_at: Some(Instant::now()),
            ..Default::default()
        },
    );
}

/// How long after its upload a file counts as just uploaded by this app.
const UPLOADED_RECENTLY: Duration = Duration::from_secs(60);

/// Whether this app uploaded `path` and marked it in sync less than a minute ago. A deletion
/// reported for it now is older than that upload (a file saved by replacement and uploaded by a
/// sync pass before the delayed delete event came in): the new version is not trashed.
pub(crate) fn uploaded_recently(path: &Path) -> bool {
    with_recent(path, |recent| {
        recent
            .uploaded_at
            .is_some_and(|at| at.elapsed() < UPLOADED_RECENTLY)
    })
}

/// When a file changed during its upload is uploaded again: at once a few times in a row,
/// then after `RETRY_FAILED_AFTER` each time (a file written to all the time makes a version
/// only that often).
pub(crate) fn upload_again_after(path: &Path) -> Duration {
    with_recent(path, |recent| {
        recent.uploads_again += 1;
        if recent.uploads_again <= MAX_UPLOADS_AGAIN {
            Duration::ZERO
        } else {
            // A sync pass waits as well (see `failed_recently`).
            recent.failed_at = Some(Instant::now());
            RETRY_FAILED_AFTER
        }
    })
}

/// When an upload the server refused with 40905 is tried again: a case rename on its way
/// resolves it (the app tries a waiting rename again after `case::RETRY_DELAY`). A few times
/// only: a real clash of two names stays as it is, with its message.
pub(crate) fn retry_name_taken_after(path: &Path) -> Option<Duration> {
    with_recent(path, |recent| {
        recent.name_retries += 1;
        (recent.name_retries <= MAX_UPLOADS_AGAIN).then_some(NAME_TAKEN_RETRY)
    })
}

/// Whether an upload of `path` failed less than `RETRY_FAILED_AFTER` ago. A sync pass does not
/// send such a file again yet: a refusal that does not pass (quota, size, rights) would else
/// send it on every pass. A change of the file uploads it at once, as before.
pub(crate) fn failed_recently(path: &Path) -> bool {
    with_recent(path, |recent| {
        recent
            .failed_at
            .is_some_and(|at| at.elapsed() < RETRY_FAILED_AFTER)
    })
}

/// Whether an upload failed because the server has another version than the one the file was
/// changed from (40076), or the name is taken (40004). The error may be wrapped in context.
pub(crate) fn is_conflict_error(error: &anyhow::Error) -> bool {
    error.chain().any(|cause| {
        matches!(cause.downcast_ref::<ApiError>(), Some(ApiError::ApiError { code, .. })
            if *code == ErrorCode::StaleVersion as i32 || *code == ErrorCode::ObjectExisted as i32)
    })
}

/// Whether the server's current version of a file is the one an upload created. Unknown when
/// the server did not name the created version (older server): taken as current, as before.
pub(crate) fn upload_is_current(created: Option<&str>, file: &FileResponse) -> bool {
    created.is_none_or(|created| file.primary_entity.as_deref() == Some(created))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn file(primary: Option<&str>) -> FileResponse {
        FileResponse {
            primary_entity: primary.map(str::to_string),
            ..Default::default()
        }
    }

    #[test]
    fn a_file_saved_during_the_upload_counts_as_changed() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("a.txt");
        std::fs::write(&path, "one").unwrap();
        let started = content_state(&path);
        assert!(started.is_some());
        assert_eq!(content_state(&path), started);
        // Written through a handle that stays open: seen before the writer closes it.
        let mut writer = std::fs::OpenOptions::new().append(true).open(&path).unwrap();
        std::io::Write::write_all(&mut writer, b" and more").unwrap();
        assert_ne!(content_state(&path), started);
        drop(writer);
        std::fs::remove_file(&path).unwrap();
        assert_eq!(content_state(&path), None);
    }

    #[test]
    fn a_failed_upload_waits_before_the_sync_tries_it_again() {
        let path = Path::new(r"C:\S\failed.txt");
        assert!(!failed_recently(path));
        note_failure(path);
        assert!(failed_recently(path));
        // The sync may ask with the server's spelling.
        assert!(failed_recently(Path::new(r"C:\S\Failed.TXT")));
        forget_uploads(path);
        assert!(!failed_recently(path));
    }

    #[test]
    fn a_file_just_uploaded_counts_as_such() {
        let path = Path::new(r"C:\S\Book.xlsx");
        assert!(!uploaded_recently(path));
        forget_uploads(path);
        assert!(uploaded_recently(path));
        assert!(uploaded_recently(Path::new(r"C:\S\book.XLSX")));
    }

    #[test]
    fn a_file_changed_during_each_upload_is_uploaded_again_at_once_a_few_times_only() {
        let path = Path::new(r"C:\S\busy.log");
        for _ in 0..MAX_UPLOADS_AGAIN {
            assert_eq!(upload_again_after(path), Duration::ZERO);
        }
        assert_eq!(upload_again_after(path), RETRY_FAILED_AFTER);
        assert!(failed_recently(path), "a sync pass waits as well");
        forget_uploads(path);
        assert_eq!(upload_again_after(path), Duration::ZERO);
        forget_uploads(path);
    }

    #[test]
    fn an_upload_refused_with_40905_is_tried_again_a_few_times_only() {
        let path = Path::new(r"C:\S\docs\b.txt");
        for _ in 0..MAX_UPLOADS_AGAIN {
            assert_eq!(retry_name_taken_after(path), Some(NAME_TAKEN_RETRY));
        }
        assert_eq!(retry_name_taken_after(path), None);
        forget_uploads(path);
    }

    #[test]
    fn a_version_saved_right_after_the_upload_is_not_taken_for_it() {
        assert!(upload_is_current(Some("e2"), &file(Some("e2"))));
        // Another device saved e3 after this upload made e2.
        assert!(!upload_is_current(Some("e2"), &file(Some("e3"))));
        assert!(!upload_is_current(Some("e2"), &file(None)));
        // An older server does not name the created version.
        assert!(upload_is_current(None, &file(Some("e3"))));
    }
}

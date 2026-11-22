//! Download task implementation for downloading remote files to local placeholders.
//!
//! This module provides a download task that:
//! - Downloads file content from remote server to a temporary location
//! - Tracks download progress with speed and ETA calculation
//! - Replaces the placeholder file content atomically when finished
//! - Uses CrPlaceholder to convert and mark the file as in-sync
//! - Only operates on hydrated placeholder files

#[cfg(windows)]
use std::{path::Path, str::FromStr};
use std::{
    path::PathBuf,
    sync::{
        Arc,
        atomic::{AtomicU64, Ordering},
    },
    time::{Duration, Instant},
};

use anyhow::{Context, Result};
use cloudreve_api::{Client, api::ExplorerApi, models::explorer::FileURLService};
use dashmap::DashMap;
use futures::StreamExt;
use tokio::io::AsyncWriteExt;
use tokio_util::sync::CancellationToken;
use tracing::{debug, info};
#[cfg(windows)]
use uuid::Uuid;

use crate::{
    drive::{local::LocalFileInfo, utils::local_path_to_cr_uri},
    inventory::{FileMetadata, InventoryDb},
    tasks::{
        queue::QueuedTask,
        upload::{ContentState, content_state},
    },
};

use super::types::TaskProgress;
#[cfg(windows)]
use crate::drive::placeholder::CrPlaceholder;

/// Fylvo: the Linux end of a download (new files and updates of normal local files).
#[cfg(target_os = "linux")]
#[path = "download_linux.rs"]
pub(crate) mod linux;

/// Progress tracker for download operations.
/// Uses atomic counters for thread-safe byte tracking and sliding window for speed calculation.
pub struct DownloadProgressTracker {
    /// Total file size in bytes
    total_size: u64,
    /// Downloaded bytes (atomic for thread safety)
    downloaded_bytes: AtomicU64,
    /// Speed calculation samples
    samples: std::sync::Mutex<Vec<(Instant, u64)>>,
    /// Window duration for speed calculation
    window_duration: Duration,
}

impl DownloadProgressTracker {
    pub fn new(total_size: u64) -> Self {
        Self {
            total_size,
            downloaded_bytes: AtomicU64::new(0),
            samples: std::sync::Mutex::new(Vec::with_capacity(32)),
            window_duration: Duration::from_secs(10),
        }
    }

    /// Add downloaded bytes
    pub fn add_bytes(&self, bytes: u64) {
        self.downloaded_bytes.fetch_add(bytes, Ordering::SeqCst);
    }

    /// Get current downloaded bytes
    pub fn downloaded(&self) -> u64 {
        self.downloaded_bytes.load(Ordering::SeqCst)
    }

    /// Calculate current speed and return progress update
    pub fn create_update(&self) -> DownloadProgressUpdate {
        let downloaded = self.downloaded();
        let now = Instant::now();

        // Calculate speed using sliding window
        let speed = {
            let mut samples = self.samples.lock().unwrap();
            samples.push((now, downloaded));

            // Remove old samples outside window
            let cutoff = now - self.window_duration;
            samples.retain(|(t, _)| *t >= cutoff);

            if samples.len() >= 2 {
                let (oldest_time, oldest_bytes) = samples.first().unwrap();
                let elapsed = now.duration_since(*oldest_time);
                if elapsed.as_millis() > 0 {
                    let bytes_diff = downloaded.saturating_sub(*oldest_bytes);
                    (bytes_diff as f64 / elapsed.as_secs_f64()) as u64
                } else {
                    0
                }
            } else {
                0
            }
        };

        let progress = if self.total_size > 0 {
            (downloaded as f64 / self.total_size as f64).clamp(0.0, 1.0)
        } else {
            1.0
        };

        let eta_seconds = if speed > 0 && downloaded < self.total_size {
            Some((self.total_size - downloaded) / speed)
        } else {
            None
        };

        DownloadProgressUpdate {
            total_size: self.total_size,
            downloaded,
            progress,
            speed_bytes_per_sec: speed,
            eta_seconds,
        }
    }
}

/// Progress update for downloads
#[derive(Debug, Clone)]
pub struct DownloadProgressUpdate {
    /// Total file size in bytes
    pub total_size: u64,
    /// Downloaded bytes so far
    pub downloaded: u64,
    /// Progress percentage (0.0 - 1.0)
    pub progress: f64,
    /// Download speed in bytes per second
    pub speed_bytes_per_sec: u64,
    /// Estimated time remaining in seconds
    pub eta_seconds: Option<u64>,
}

/// In-memory progress reporter for download tasks
pub struct InMemoryDownloadProgressReporter {
    task_id: String,
    progress_map: Arc<DashMap<String, TaskProgress>>,
}

impl InMemoryDownloadProgressReporter {
    pub fn new(task_id: String, progress_map: Arc<DashMap<String, TaskProgress>>) -> Self {
        Self {
            task_id,
            progress_map,
        }
    }

    /// Update progress from download progress update
    pub fn on_progress(&self, update: &DownloadProgressUpdate) {
        if let Some(mut entry) = self.progress_map.get_mut(&self.task_id) {
            entry.progress = update.progress;
            entry.processed_bytes = Some(update.downloaded as i64);
            entry.total_bytes = Some(update.total_size as i64);
            entry.speed_bytes_per_sec = update.speed_bytes_per_sec;
            entry.eta_seconds = update.eta_seconds;
        }
    }
}

/// Download task that downloads a file from remote to local placeholder
pub struct DownloadTask<'a> {
    inventory: Arc<InventoryDb>,
    cr_client: Arc<Client>,
    drive_id: &'a str,
    sync_path: PathBuf,
    remote_base: String,
    task: &'a QueuedTask,
    local_file_info: Option<LocalFileInfo>,
    inventory_meta: Option<FileMetadata>,
    /// Latest file info fetched from remote before download
    remote_file_info: Option<cloudreve_api::models::explorer::FileResponse>,
    /// Fylvo: size and write time of the local file when the download started.
    started: Option<ContentState>,
    /// Fylvo: the file was changed here, the download left it as it is.
    kept_local_change: bool,
    /// Fylvo (Linux): the local state when the download started (with the inode).
    #[cfg(target_os = "linux")]
    started_stamp: Option<crate::drive::local_linux::LocalStamp>,
    cancel_token: CancellationToken,
    progress_map: Arc<DashMap<String, TaskProgress>>,
}

impl<'a> DownloadTask<'a> {
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
            local_file_info: None,
            inventory_meta: None,
            remote_file_info: None,
            started: None,
            kept_local_change: false,
            #[cfg(target_os = "linux")]
            started_stamp: None,
            task,
            sync_path,
            remote_base,
            cancel_token: CancellationToken::new(),
            progress_map,
        }
    }

    /// Whether the file was changed here and the download left it as it is.
    pub fn kept_local_change(&self) -> bool {
        self.kept_local_change
    }

    /// Set the cancellation token
    #[allow(dead_code)]
    pub fn with_cancel_token(mut self, token: CancellationToken) -> Self {
        self.cancel_token = token;
        self
    }

    /// Execute the download task
    pub async fn execute(&mut self) -> Result<()> {
        // Get local file info
        let local_file_info = LocalFileInfo::from_path(&self.task.payload.local_path)
            .context("failed to get local file info")?;

        if !local_file_info.exists {
            // Fylvo: Linux keeps every file local: a file the server has and this folder not is
            // downloaded here (Windows creates an online-only placeholder instead).
            #[cfg(target_os = "linux")]
            {
                return self.download_and_replace().await;
            }
            #[cfg(not(target_os = "linux"))]
            {
                info!(
                    target: "tasks::download",
                    task_id = %self.task.task_id,
                    local_path = %self.task.payload.local_path_display(),
                    "Local file does not exist, skipping download"
                );
                return Ok(());
            }
        }
        // Fylvo: renamed meanwhile to another upper/lower case spelling; the sync decides
        // about it under the new spelling (see `drive/case.rs`).
        if crate::drive::case::spelled_differently_on_disk(
            &self.task.payload.local_path,
            &self.sync_path,
        ) {
            info!(
                target: "tasks::download",
                task_id = %self.task.task_id,
                local_path = %self.task.payload.local_path_display(),
                "Spelled differently on disk, skipping download"
            );
            return Ok(());
        }

        if local_file_info.is_directory {
            info!(
                target: "tasks::download",
                task_id = %self.task.task_id,
                local_path = %self.task.payload.local_path_display(),
                "Cannot download a directory, skipping"
            );
            return Ok(());
        }

        // Check if file is a placeholder and is hydrated (has content on disk)
        if !local_file_info.is_placeholder() {
            info!(
                target: "tasks::download",
                task_id = %self.task.task_id,
                local_path = %self.task.payload.local_path_display(),
                "File is not a placeholder, skipping download"
            );
            // Fylvo: a file put here with local content (for example saved by replacement):
            // it is uploaded (queueing this download cancelled an upload of the path).
            self.kept_local_change = true;
            return Ok(());
        }

        // partial_on_disk means the file content is NOT fully present locally
        // We need the file to be hydrated (NOT partial_on_disk) to replace its content
        if local_file_info.partial_on_disk() {
            info!(
                target: "tasks::download",
                task_id = %self.task.task_id,
                local_path = %self.task.payload.local_path_display(),
                "File is not fully hydrated, skipping download - file must be hydrated first"
            );
            return Ok(());
        }

        // Fylvo: changed here since the sync decided on this download: the upload of the change
        // meets the server's newer version and becomes a conflict, nothing is overwritten. The
        // queue uploads it (queueing this download cancelled an upload of the path).
        if !local_file_info.in_sync() {
            info!(
                target: "tasks::download",
                task_id = %self.task.task_id,
                local_path = %self.task.payload.local_path_display(),
                "File changed here, skipping download"
            );
            self.kept_local_change = true;
            return Ok(());
        }
        // Fylvo: size and write time now, compared again right before the content is replaced.
        self.started = Some(
            content_state(&self.task.payload.local_path)
                .context("the file cannot be read right now")?,
        );
        #[cfg(target_os = "linux")]
        {
            self.started_stamp =
                crate::drive::local_linux::LocalStamp::of_path(&self.task.payload.local_path);
        }

        self.local_file_info = Some(local_file_info);

        // Get inventory metadata - required for download
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

        // Fail if file is not in inventory - we need the metadata to download
        if self.inventory_meta.is_none() {
            anyhow::bail!(
                "File not found in inventory: {}. Cannot download without inventory metadata.",
                path_str
            );
        }

        // Perform the download
        self.download_and_replace().await
    }

    /// Download file from remote and replace local placeholder content
    async fn download_and_replace(&mut self) -> Result<()> {
        let local_path = &self.task.payload.local_path;
        info!(
            target: "tasks::download",
            task_id = %self.task.task_id,
            local_path = %self.task.payload.local_path_display(),
            "Starting file download"
        );

        // Get remote URI
        let uri = local_path_to_cr_uri(
            local_path.clone(),
            self.sync_path.clone(),
            self.remote_base.clone(),
        )
        .context("failed to convert local path to cloudreve uri")?
        .to_string();

        // Fetch latest file info from remote before download
        let file_info = self
            .cr_client
            .get_file_info(&cloudreve_api::models::explorer::GetFileInfoService {
                uri: Some(uri.clone()),
                id: None,
                extended: None,
                folder_summary: None,
            })
            .await
            .context("failed to get file info from remote")?;

        let file_size = file_info.size as u64;
        self.remote_file_info = Some(file_info);

        // Get download URL from server using inventory metadata for entity validation
        let mut request = FileURLService::default();
        request.uris.push(uri.clone());
        if self.remote_file_info.as_ref().map(|f|f.primary_entity.is_some()).unwrap_or(false) {
            request.entity = self.remote_file_info.as_ref().unwrap().primary_entity.clone();
        }

        let entity_url_res = self
            .cr_client
            .get_file_url(&request)
            .await
            .context("failed to get file url")?;

        let download_url = entity_url_res
            .urls
            .first()
            .context("no download URL in response")?
            .url
            .clone();

        debug!(
            target: "tasks::download",
            task_id = %self.task.task_id,
            download_url = %download_url,
            file_size = file_size,
            "Got download URL"
        );

        // Fylvo: the temp file lies next to the file, so `ReplaceFileW` works on any drive (it
        // cannot move across volumes). Its name starts with `~`, which the sync ignores. It is
        // removed however the task ends (also when it is cancelled or aborted).
        let temp = RemoveOnDrop(
            local_path
                .parent()
                .context("file without a parent folder")?
                .join(format!("~fylvo-download-{}.tmp", self.task.task_id)),
        );
        let temp_path = &temp.0;

        // Create progress tracker and reporter
        let tracker = Arc::new(DownloadProgressTracker::new(file_size));
        let reporter = InMemoryDownloadProgressReporter::new(
            self.task.task_id.clone(),
            Arc::clone(&self.progress_map),
        );

        // Download to temp file
        self.download_to_temp(&download_url, temp_path, tracker.clone(), &reporter)
            .await?;

        // Report final progress
        let final_update = tracker.create_update();
        reporter.on_progress(&final_update);

        // Fylvo (Linux): deleted or renamed on the server meanwhile: the fetched version is not
        // put here, the next sync decides about the path.
        #[cfg(target_os = "linux")]
        if !self.still_on_server(&uri).await {
            info!(
                target: "tasks::download",
                task_id = %self.task.task_id,
                local_path = %self.task.payload.local_path_display(),
                "Changed on the server during the download, not put in place"
            );
            return Ok(());
        }

        // Replace placeholder file with downloaded content and commit
        #[cfg(windows)]
        self.replace_and_commit_placeholder(temp_path)
            .context("failed to replace and commit placeholder")?;
        #[cfg(target_os = "linux")]
        self.place_download(temp_path)
            .context("failed to put the downloaded file in place")?;

        info!(
            target: "tasks::download",
            task_id = %self.task.task_id,
            local_path = %self.task.payload.local_path_display(),
            "Download completed successfully"
        );

        Ok(())
    }

    /// Download file content to a temporary file
    async fn download_to_temp(
        &self,
        url: &str,
        temp_path: &PathBuf,
        tracker: Arc<DownloadProgressTracker>,
        reporter: &InMemoryDownloadProgressReporter,
    ) -> Result<()> {
        let client = reqwest::Client::new();
        let response = client
            .get(url)
            .send()
            .await
            .context("failed to send download request")?;

        if !response.status().is_success() {
            anyhow::bail!("Download request failed with status: {}", response.status());
        }

        // Create temp file
        // Fylvo: created at once, not in the background: a cancel that removes the temp file
        // must not come before the file exists.
        let mut file = tokio::fs::File::from_std(
            std::fs::File::create(temp_path).context("failed to create temp file")?,
        );

        // Stream download with progress tracking
        let mut stream = response.bytes_stream();
        let mut last_report = Instant::now();
        const REPORT_INTERVAL: Duration = Duration::from_millis(100);

        while let Some(chunk_result) = stream.next().await {
            // Check for cancellation
            if self.cancel_token.is_cancelled() {
                anyhow::bail!("Download cancelled");
            }

            let chunk = chunk_result.context("failed to read chunk from stream")?;
            file.write_all(&chunk)
                .await
                .context("failed to write chunk to temp file")?;

            tracker.add_bytes(chunk.len() as u64);

            // Report progress at intervals to avoid too frequent updates
            if last_report.elapsed() >= REPORT_INTERVAL {
                let update = tracker.create_update();
                reporter.on_progress(&update);
                last_report = Instant::now();
            }
        }

        file.flush().await.context("failed to flush temp file")?;

        Ok(())
    }

    /// Replace the placeholder file content with the downloaded file and commit using CrPlaceholder
    #[cfg(windows)]
    fn replace_and_commit_placeholder(&mut self, temp_path: &PathBuf) -> Result<()> {
        let local_path = &self.task.payload.local_path;
        let remote_file_info = self
            .remote_file_info
            .as_ref()
            .expect("remote_file_info should be set");

        debug!(
            target: "tasks::download",
            task_id = %self.task.task_id,
            temp_path = %temp_path.display(),
            local_path = %local_path.display(),
            "Replacing placeholder content"
        );

        // Fylvo: a file kept on this device ("always keep") can be changed while its new
        // version downloads. Replacing it then would throw the change away: keep it instead.
        let in_sync = LocalFileInfo::from_path(local_path).is_ok_and(|info| info.in_sync());
        if !in_sync || !unchanged_since(self.started, local_path) {
            info!(
                target: "tasks::download",
                task_id = %self.task.task_id,
                local_path = %local_path.display(),
                "File changed during the download, keeping the local change"
            );
            self.kept_local_change = true;
            return Ok(());
        }

        // Use Windows ReplaceFileW for atomic replacement
        // This preserves file attributes and provides atomicity
        replace_with_backup(local_path, temp_path, &self.task.task_id)?;

        // Use CrPlaceholder to convert and mark as in-sync
        let drive_id = Uuid::from_str(self.drive_id).context("invalid drive ID")?;
        // Create CrPlaceholder and commit changes using the latest remote file info
        let mut cr_placeholder =
            CrPlaceholder::new(local_path.clone(), self.sync_path.clone(), drive_id)
                .with_remote_file(remote_file_info);

        cr_placeholder
            .commit(self.inventory.clone())
            .context("failed to commit placeholder")?;

        debug!(
            target: "tasks::download",
            task_id = %self.task.task_id,
            local_path = %local_path.display(),
            "Placeholder committed successfully"
        );

        Ok(())
    }
}

/// Put `temp` in the place of `local` with `ReplaceFileW` (keeps the file's attributes, one
/// step for other programs).
///
/// Fylvo: without a backup name, Windows renames the old file to a random name and deletes it,
/// and the sync would pass that rename and that deletion on to the server file. The backup name
/// starts with `~`, which the sync ignores; it is removed afterwards. There is no copy over the
/// file as a fallback: `ReplaceFileW` fails when another program has the file open, and a copy
/// would write over what that program saves (the sync downloads it again later). If Windows
/// moved the old file to the backup name but could not put the new one in place, the old one
/// goes back.
#[cfg(windows)]
fn replace_with_backup(local: &Path, temp: &Path, task_id: &str) -> Result<()> {
    use std::{ffi::OsStr, os::windows::ffi::OsStrExt};
    use windows::{
        Win32::Storage::FileSystem::{REPLACE_FILE_FLAGS, ReplaceFileW},
        core::PCWSTR,
    };

    let wide = |path: &Path| -> Vec<u16> {
        OsStr::new(path).encode_wide().chain(std::iter::once(0)).collect()
    };
    let backup = RemoveOnDrop(temp.with_file_name(format!("~fylvo-backup-{task_id}.tmp")));
    let (local_wide, temp_wide, backup_wide) = (wide(local), wide(temp), wide(&backup.0));
    let result = unsafe {
        ReplaceFileW(
            PCWSTR::from_raw(local_wide.as_ptr()),
            PCWSTR::from_raw(temp_wide.as_ptr()),
            PCWSTR::from_raw(backup_wide.as_ptr()),
            REPLACE_FILE_FLAGS(0),
            None,
            None,
        )
    };
    if let Err(err) = result {
        // Never over a file saved there meanwhile: then that file stays, and so does the backup.
        if !local.exists() && backup.0.exists() {
            if let Err(back) = crate::drive::utils::move_no_replace(&backup.0, local) {
                // The backup is the only local copy now: it stays (under its `~` name).
                std::mem::forget(backup);
                return Err(back.context("failed to put the local file back after a failed replace"));
            }
        }
        return Err(err).context("failed to replace the local file");
    }
    Ok(())
}

/// Fylvo: a file of the task itself, removed when the task ends in any way.
struct RemoveOnDrop(PathBuf);

impl Drop for RemoveOnDrop {
    fn drop(&mut self) {
        let _ = std::fs::remove_file(&self.0);
    }
}

/// Whether a file still has the size and write time it had at `started` (read through a
/// handle). Unknown counts as changed.
#[cfg(windows)]
fn unchanged_since(started: Option<ContentState>, path: &Path) -> bool {
    started.is_some() && content_state(path) == started
}

#[cfg(test)]
mod tests {
    #[allow(unused_imports)]
    use super::*;

    #[test]
    #[cfg(windows)]
    fn a_file_changed_during_the_download_is_not_replaced() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("kept.docx");
        std::fs::write(&path, "server version 1").unwrap();
        let started = content_state(&path);
        assert!(unchanged_since(started, &path));
        // The user saves while the server's version 2 downloads.
        let mut writer = std::fs::OpenOptions::new().append(true).open(&path).unwrap();
        std::io::Write::write_all(&mut writer, b", edited here").unwrap();
        assert!(!unchanged_since(started, &path));
        drop(writer);
        // Without a first reading nothing is replaced either.
        assert!(!unchanged_since(None, &path));
    }

    #[test]
    #[cfg(windows)]
    fn the_replace_leaves_no_file_with_a_random_name_behind() {
        let dir = tempfile::tempdir().unwrap();
        let local = dir.path().join("Book.xlsx");
        let temp = dir.path().join("~fylvo-download-t1.tmp");
        std::fs::write(&local, "old").unwrap();
        std::fs::write(&temp, "new").unwrap();
        replace_with_backup(&local, &temp, "t1").unwrap();
        assert_eq!(std::fs::read_to_string(&local).unwrap(), "new");
        // Only the file is left: no `Book.xlsx~RF….TMP` (passed on to the server as a rename
        // and a deletion), no temp file, no backup.
        let names: Vec<String> = std::fs::read_dir(dir.path())
            .unwrap()
            .map(|entry| entry.unwrap().file_name().to_string_lossy().to_string())
            .collect();
        assert_eq!(names, ["Book.xlsx"]);
    }

    #[test]
    #[cfg(windows)]
    fn a_failed_replace_keeps_the_file() {
        let dir = tempfile::tempdir().unwrap();
        let local = dir.path().join("Book.xlsx");
        std::fs::write(&local, "kept").unwrap();
        // The program that has the file open does not allow deleting it.
        let _open = {
            use std::os::windows::fs::OpenOptionsExt;
            std::fs::OpenOptions::new().read(true).share_mode(1).open(&local).unwrap()
        };
        let temp = dir.path().join("~fylvo-download-t2.tmp");
        std::fs::write(&temp, "new").unwrap();
        assert!(replace_with_backup(&local, &temp, "t2").is_err());
        assert_eq!(std::fs::read_to_string(&local).unwrap(), "kept");
    }
}

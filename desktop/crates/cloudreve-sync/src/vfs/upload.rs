//! Write back: closed changes are uploaded from a snapshot, conflicts become conflict copies.

use super::drive::{unix, Change, Drive, Node, Remote, ROOT};
use super::names;
use crate::tasks::upload::{is_conflict_error, upload_is_current};
use crate::uploader::{ProgressCallback, ProgressUpdate, UploadParams, Uploader, UploaderConfig};
use cloudreve_api::api::ExplorerApi;
use cloudreve_api::models::explorer::{FileResponse, GetFileInfoService};
use fuser::Errno;
use std::sync::atomic::Ordering;
use std::sync::Arc;
use std::time::{Duration, Instant};

/// Waits between failed uploads.
const BACKOFF: [u64; 5] = [60, 120, 300, 600, 1800];
const PARALLEL: usize = 2;

struct NoProgress;

impl ProgressCallback for NoProgress {
    fn on_progress(&self, _update: ProgressUpdate) {}
}

enum Outcome {
    /// The server holds this content now (as the file itself).
    Uploaded(FileResponse),
    /// Someone else's version is current; ours went to a conflict copy.
    ConflictCopy(FileResponse, String),
    /// Try again later.
    Retry(String),
}

/// Upload changes that are due, a few at a time, until the drive stops.
pub(crate) async fn run(drive: Arc<Drive>) {
    let running = Arc::new(tokio::sync::Semaphore::new(PARALLEL));
    loop {
        if drive.stopping.load(Ordering::SeqCst) {
            return;
        }
        if drive.is_online() {
            for ino in due(&drive) {
                let Ok(permit) = running.clone().try_acquire_owned() else { break };
                {
                    let mut tree = drive.tree();
                    match tree.nodes.get_mut(&ino) {
                        Some(node) if !node.uploading => node.uploading = true,
                        _ => continue,
                    }
                }
                let worker = drive.clone();
                drive.rt.spawn(async move {
                    let _permit = permit;
                    let _busy = crate::activity::enter().await;
                    upload(&worker, ino).await;
                    if let Some(node) = worker.tree().nodes.get_mut(&ino) {
                        node.uploading = false;
                    }
                    worker.upload_wake.notify_one();
                });
            }
        }
        tokio::select! {
            _ = drive.upload_wake.notified() => {}
            _ = tokio::time::sleep(Duration::from_secs(1)) => {}
        }
    }
}

fn due(drive: &Drive) -> Vec<u64> {
    let now = Instant::now();
    let tree = drive.tree();
    tree.nodes
        .iter()
        .filter(|(ino, node)| {
            node.parent != 0
                && !node.uploading
                && node.writers == 0
                && node.change.as_ref().is_some_and(|c| !c.local_only && c.due.is_some_and(|due| due <= now))
                && tree.path(**ino).is_some()
        })
        .map(|(ino, _)| *ino)
        .collect()
}

async fn upload(drive: &Arc<Drive>, ino: u64) {
    let outcome = attempt(drive, ino).await;
    let path = drive.tree().path(ino).unwrap_or_default();
    let finished = match outcome {
        Ok((Outcome::Uploaded(info), writes)) => {
            tracing::info!(target: "vfs", path = %path, "Uploaded");
            committed(drive, ino, &info, writes)
        }
        Ok((Outcome::ConflictCopy(info, copy), writes)) => {
            tracing::info!(target: "vfs", path = %path, copy = %copy, "Saved as a conflict copy");
            if !copy.is_empty() {
                crate::utils::toast::send_general_text_toast(
                    &t!("vfsConflictTitle"),
                    &t!("vfsConflictMessage", "name" => copy),
                );
            }
            reverted(drive, ino, &info, writes)
        }
        Ok((Outcome::Retry(reason), _)) | Err(reason) => {
            tracing::warn!(target: "vfs", path = %path, reason = %reason, "Upload failed, trying again later");
            let mut tree = drive.tree();
            if let Some(change) = tree.nodes.get_mut(&ino).and_then(|n| n.change.as_mut()) {
                let wait = BACKOFF[(change.attempts as usize).min(BACKOFF.len() - 1)];
                change.attempts += 1;
                change.failed = Some(reason);
                change.due = Some(Instant::now() + Duration::from_secs(wait));
            }
            return;
        }
    };
    if !finished {
        // Written again while it uploaded: upload again soon (no failure).
        let mut tree = drive.tree();
        if let Some(node) = tree.nodes.get_mut(&ino) {
            let closed = node.writers == 0;
            if let Some(change) = node.change.as_mut() {
                change.due = closed.then(|| Instant::now() + super::drive::UPLOAD_DELAY);
                change.failed = None;
            }
        }
    }
}

/// What the upload needs, read under the tree lock.
struct Job {
    path: String,
    remote: Option<Remote>,
    recovered: bool,
    cell: Arc<super::content::Cell>,
    mtime: i64,
}

fn job(drive: &Drive, ino: u64) -> Option<Job> {
    let tree = drive.tree();
    let node: &Node = tree.nodes.get(&ino)?;
    let change: &Change = node.change.as_ref()?;
    Some(Job {
        path: tree.path(ino)?,
        remote: node.remote.clone(),
        recovered: change.recovered,
        cell: change.cell.clone(),
        mtime: unix(node.mtime),
    })
}

/// Upload a snapshot of the change. Returns the outcome and the write count of the snapshot.
async fn attempt(drive: &Arc<Drive>, ino: u64) -> Result<(Outcome, u64), String> {
    let job = job(drive, ino).ok_or("the file is gone")?;
    // The parts never loaded come from the version the change started from.
    let remote_len = job.cell.lock().remote_len;
    if remote_len > 0 {
        drive
            .ensure_range(&job.cell, &job.path, 0, remote_len)
            .await
            .map_err(|e| format!("could not load the rest of the file ({e:?})"))?;
    }
    let staging = drive.info.data_dir.join("staging");
    std::fs::create_dir_all(&staging).map_err(|e| e.to_string())?;
    let snapshot = staging.join(uuid::Uuid::new_v4().simple().to_string());
    let (size, writes) = take_snapshot(drive, &job.cell, &snapshot).await?;
    let result = send(drive, &job, &snapshot, size).await;
    let _ = std::fs::remove_file(&snapshot);
    Ok((result?, writes))
}

/// Copy the content while no write can run (the gate), off the async workers.
pub(crate) async fn take_snapshot(drive: &Drive, cell: &Arc<super::content::Cell>, target: &std::path::Path) -> Result<(u64, u64), String> {
    let cell = cell.clone();
    let source = drive.contents.path_of(&cell.key());
    let target = target.to_path_buf();
    tokio::task::spawn_blocking(move || {
        let _gate = cell.gate.write().unwrap_or_else(|e| e.into_inner());
        let writes = cell.lock().writes;
        let size = std::fs::copy(&source, &target).map_err(|e| format!("snapshot: {e}"))?;
        Ok((size, writes))
    })
    .await
    .map_err(|e| e.to_string())?
}

async fn send(drive: &Arc<Drive>, job: &Job, snapshot: &std::path::Path, size: u64) -> Result<Outcome, String> {
    let uri = drive.uri_of(&job.path).map_err(|e| format!("{e:?}"))?;
    if job.recovered && job.remote.is_some() {
        // Changed and open when the app ended: maybe half written, never over the server file.
        return conflict_copy(drive, job, snapshot, size).await;
    }
    let result = upload_to(drive, &uri, snapshot, size, job.mtime, job.remote.as_ref()).await;
    match result {
        Ok(created) => {
            let info = file_info(drive, &uri).await?;
            if upload_is_current(created.as_deref(), &info) {
                Ok(Outcome::Uploaded(info))
            } else {
                // Another device saved right after: ours is an older version on the server.
                Ok(Outcome::ConflictCopy(info, String::new()))
            }
        }
        Err(e) if is_conflict_error(&e) => {
            let info = file_info(drive, &uri).await?;
            if same_content(drive, &uri, &info, snapshot, size).await {
                // A lost answer to our own upload, or the same bytes: no conflict.
                return Ok(Outcome::Uploaded(info));
            }
            conflict_copy(drive, job, snapshot, size).await
        }
        Err(e) => {
            if e.chain().any(|c| matches!(c.downcast_ref::<cloudreve_api::error::ApiError>(), Some(cloudreve_api::error::ApiError::RequestError(_)))) {
                drive.note_failure(Errno::ENETUNREACH);
            }
            Ok(Outcome::Retry(format!("{e:#}")))
        }
    }
}

async fn upload_to(
    drive: &Arc<Drive>,
    uri: &str,
    snapshot: &std::path::Path,
    size: u64,
    mtime: i64,
    base: Option<&Remote>,
) -> anyhow::Result<Option<String>> {
    let uploader = Uploader::new(drive.client.clone(), drive.inventory.clone(), UploaderConfig::default());
    let params = UploadParams {
        local_path: snapshot.to_path_buf(),
        remote_uri: uri.to_string(),
        file_size: size,
        mime_type: None,
        last_modified: Some(mtime * 1000),
        overwrite: base.is_some(),
        previous_version: base.map(|b| b.version.clone()).unwrap_or_default(),
        task_id: format!("vfs-{}", uuid::Uuid::new_v4().simple()),
        drive_id: drive.info.id.clone(),
    };
    uploader.upload(params, NoProgress).await
}

async fn file_info(drive: &Arc<Drive>, uri: &str) -> Result<FileResponse, String> {
    drive
        .client
        .get_file_info(&GetFileInfoService { uri: Some(uri.to_string()), id: None, extended: None, folder_summary: None })
        .await
        .map_err(|e| {
            drive.note_failure(super::drive::errno_of(&e));
            format!("{e}")
        })
}

/// Whether the server's current file has exactly the bytes of `snapshot`.
async fn same_content(drive: &Arc<Drive>, uri: &str, info: &FileResponse, snapshot: &std::path::Path, size: u64) -> bool {
    if info.size.max(0) as u64 != size || size > 64 * 1024 * 1024 {
        return false;
    }
    let mut request = cloudreve_api::models::explorer::FileURLService::default();
    request.uris.push(uri.to_string());
    request.entity = info.primary_entity.clone();
    let Ok(answer) = drive.client.get_file_url(&request).await else { return false };
    let Some(url) = answer.urls.first() else { return false };
    let Ok(Ok(response)) = tokio::time::timeout(Duration::from_secs(60), reqwest::get(&url.url)).await else {
        return false;
    };
    let Ok(Ok(server)) = tokio::time::timeout(Duration::from_secs(300), response.bytes()).await else {
        return false;
    };
    std::fs::read(snapshot).is_ok_and(|local| local == server.as_ref())
}

async fn conflict_copy(drive: &Arc<Drive>, job: &Job, snapshot: &std::path::Path, size: u64) -> Result<Outcome, String> {
    let name = job.path.rsplit('/').next().unwrap_or(&job.path);
    let copy = names::join(names::parent_of(&job.path), &names::conflict_name(name));
    let copy_uri = drive.uri_of(&copy).map_err(|e| format!("{e:?}"))?;
    match upload_to(drive, &copy_uri, snapshot, size, job.mtime, None).await {
        Ok(_) => {
            let uri = drive.uri_of(&job.path).map_err(|e| format!("{e:?}"))?;
            let info = file_info(drive, &uri).await?;
            Ok(Outcome::ConflictCopy(info, copy))
        }
        Err(e) => Ok(Outcome::Retry(format!("conflict copy: {e:#}"))),
    }
}

/// The upload is the server's current version: the local copy becomes its cached copy, unless
/// it was written after the snapshot (then it stays a change, continuing from the uploaded
/// version). Returns whether the change is done.
fn committed(drive: &Arc<Drive>, ino: u64, info: &FileResponse, writes: u64) -> bool {
    let version = info.primary_entity.clone().unwrap_or_default();
    let remote = Remote { id: info.id.clone(), version: version.clone() };
    let (path, old_key, cell) = {
        let mut tree = drive.tree();
        let path = tree.path(ino).unwrap_or_default();
        let Some(node) = tree.nodes.get_mut(&ino) else { return true };
        let Some(cell) = node.change.as_ref().map(|c| c.cell.clone()) else { return true };
        // Under the tree lock: a write starting a new change waits for this.
        match drive.contents.make_clean(&cell, &info.id, &version, writes) {
            Ok(Some(old_key)) => {
                node.change = None;
                node.remote = Some(remote);
                node.last_key = None;
                (path, old_key, cell)
            }
            Ok(None) => {
                node.remote = Some(remote);
                if let Some(change) = node.change.as_mut() {
                    change.recovered = false;
                }
                drop(tree);
                cell.lock().remote_len = 0;
                let _ = drive.persist_change(ino);
                return false;
            }
            Err(e) => {
                tracing::warn!(target: "vfs", error = %e, "Failed to keep the uploaded file in the cache");
                node.remote = Some(remote);
                drop(tree);
                cell.lock().remote_len = 0;
                let _ = drive.persist_change(ino);
                return false;
            }
        }
    };
    let row = cell.lock().row();
    if let Err(e) = drive.store.commit_upload(&path, &old_key, &row) {
        tracing::warn!(target: "vfs", error = %e, "Failed to record an upload");
    }
    drive.mark_stale(names::parent_of(&path));
    true
}

/// Our content went to a conflict copy (or is an older version): the file shows the server's
/// current version again, unless it was written after the snapshot (then it stays a change
/// from the old version, so the next upload makes another conflict copy). Returns whether the
/// change is done.
fn reverted(drive: &Arc<Drive>, ino: u64, info: &FileResponse, writes: u64) -> bool {
    let (path, cell) = {
        let mut tree = drive.tree();
        let path = tree.path(ino).unwrap_or_default();
        let Some(node) = tree.nodes.get_mut(&ino) else { return true };
        let Some(cell) = node.change.as_ref().map(|c| c.cell.clone()) else { return true };
        if !drive.contents.remove_if_unchanged(&cell, writes) {
            if let Some(change) = node.change.as_mut() {
                change.recovered = false;
            }
            return false;
        }
        node.change = None;
        node.remote = Some(Remote {
            id: info.id.clone(),
            version: info.primary_entity.clone().unwrap_or_default(),
        });
        node.size = info.size.max(0) as u64;
        node.last_key = None;
        (path, cell)
    };
    let _ = drive.store.replace_change(&[path.as_str()], None);
    let _ = drive.store.delete_content(&cell.key());
    drive.mark_stale(names::parent_of(&path));
    true
}

/// At start: put the recorded changes back into the tree. A change that was open when the app
/// ended is marked "recovered" (uploaded as a conflict copy). Leftover snapshots are removed.
pub(crate) fn restore(drive: &Arc<Drive>) {
    let staging = drive.info.data_dir.join("staging");
    if let Ok(entries) = std::fs::read_dir(&staging) {
        for entry in entries.flatten() {
            let path = entry.path().to_string_lossy().to_string();
            let _ = drive.inventory.batch_delete_upload_session_by_path(&[path.as_str()]);
            let _ = std::fs::remove_file(entry.path());
        }
    }
    let rows = drive.store.load_changes().unwrap_or_default();
    for row in rows {
        let Some(cell) = drive.contents.get(&row.key) else {
            tracing::warn!(target: "vfs", path = %row.path, "A recorded change has no content, dropped");
            let _ = drive.store.delete_change(&row.path);
            continue;
        };
        let parent_path = names::parent_of(&row.path).to_string();
        let name = row.path.rsplit('/').next().unwrap_or(&row.path).to_string();
        let parent = match ensure_folder(drive, &parent_path) {
            Some(parent) => parent,
            None => {
                tracing::warn!(target: "vfs", path = %row.path, "The folder of a change is unknown, kept for later");
                continue;
            }
        };
        let remote = match (&row.file_id, &row.base) {
            (Some(id), Some(base)) => Some(Remote { id: id.clone(), version: base.clone() }),
            _ => None,
        };
        let recovered = row.open != 0 && remote.is_some();
        let mut tree = drive.tree();
        let existing = tree.child(parent, &name);
        let ino = match existing {
            Some(ino) => ino,
            None => {
                tree.add(parent, Node::new(parent, &name, false))
            }
        };
        if let Some(node) = tree.nodes.get_mut(&ino) {
            node.remote = remote;
            // The file's own length: writes after the last record count.
            node.size = cell.len();
            node.mtime = std::time::UNIX_EPOCH + Duration::from_secs(row.mtime.max(0) as u64);
            node.change = Some(Change {
                cell,
                local_only: row.local_only != 0,
                due: Some(Instant::now() + super::drive::UPLOAD_DELAY),
                failed: None,
                attempts: 0,
                recovered,
            });
        }
    }
    drive.upload_wake.notify_one();
}

/// Make the folder at `path` part of the tree, from the store only (the mount must not wait
/// for the server). A folder the store does not know gets a made-up listing, listed from the
/// server before it is first answered.
fn ensure_folder(drive: &Arc<Drive>, path: &str) -> Option<u64> {
    let mut ino = ROOT;
    let mut walked = String::new();
    for part in std::iter::once("").chain(path.split('/').filter(|p| !p.is_empty())) {
        if !part.is_empty() {
            walked = names::join(&walked, part);
            let mut tree = drive.tree();
            ino = match tree.child(ino, part) {
                Some(child) => child,
                None => tree.add(ino, Node::new(ino, part, true)),
            };
        }
        let has_listing = drive.tree().nodes.get(&ino)?.listing.is_some();
        if !has_listing && !drive.load_stored_listing(ino, &walked) {
            let mut tree = drive.tree();
            tree.nodes.get_mut(&ino)?.listing = Some(super::drive::Listing {
                fresh_at: None,
                children: Default::default(),
                generation: 0,
                complete: false,
            });
        }
    }
    Some(ino)
}

//! Fylvo: protection of the user's files.
//!
//! - Mass deletion: when one pass would delete many entries (on the server or on this PC), the
//!   deletion is held until the user confirms it in the settings.
//! - Read-only drives: a shared folder the user may only read gets no upload attempts.
//!
//! Held deletions live in memory only. After a restart a held deletion on the server side is
//! undone by the full sync (the files come back from the server) and a held local deletion is
//! asked again.

use crate::drive::{
    commands::MountCommand, mounts::Mount, placeholder::CrPlaceholder, sync::SyncMode,
};
use crate::tasks::TaskPayload;
use crate::utils::toast;
use anyhow::Result;
use cloudreve_api::{
    api::ExplorerApi,
    boolset::Boolset,
    models::explorer::{ListFileService, navigator_capability},
};
use serde::Serialize;
use std::{
    path::{Path, PathBuf},
    sync::atomic::{AtomicU64, Ordering},
    time::{Duration, Instant},
};
use uuid::Uuid;

/// A pass that deletes at least this many entries is held.
const MASS_DELETE_COUNT: i64 = 50;
/// A pass that deletes at least `MASS_DELETE_SHARE_MIN` entries and at least
/// `MASS_DELETE_SHARE_PERCENT` percent of the drive's known entries is held too.
const MASS_DELETE_SHARE_MIN: i64 = 10;
const MASS_DELETE_SHARE_PERCENT: i64 = 30;
/// How long a confirmation (or a known server rename) lets local deletions under its paths pass.
const CONFIRMATION_VALID_FOR: Duration = Duration::from_secs(600);

/// Whether deleting `affected` of `total` known entries needs the user's confirmation.
pub fn is_mass_deletion(affected: i64, total: i64) -> bool {
    affected >= MASS_DELETE_COUNT
        || (affected >= MASS_DELETE_SHARE_MIN
            && affected * 100 >= total.max(affected) * MASS_DELETE_SHARE_PERCENT)
}

/// Where the files would be deleted.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum DeletionSide {
    /// Deleted on this PC, the deletion would go to the server.
    Server,
    /// Deleted on the server, the deletion would go to this PC.
    Local,
}

#[derive(Debug, Clone)]
pub struct HeldDeletion {
    pub paths: Vec<PathBuf>,
    pub count: i64,
    /// Changes whenever paths and count change, so a confirmation always matches what was
    /// shown.
    pub version: u64,
    /// The count matches the paths. An answer is refused while it does not.
    counted: bool,
    /// A confirmation is running; other answers are refused until it finished.
    resolving: bool,
}

/// Held deletions of one drive, one per side.
#[derive(Debug, Default)]
pub struct DeletionGuardState {
    server: Option<HeldDeletion>,
    local: Option<HeldDeletion>,
    /// Paths whose local deletion may pass (confirmed by the user, or renamed on the server).
    confirmed_local: Vec<(PathBuf, Instant)>,
    /// Paths the user chose to keep here for this run; not deleted and not asked about again.
    kept_local: Vec<PathBuf>,
}

impl DeletionGuardState {
    fn slot(&mut self, side: DeletionSide) -> &mut Option<HeldDeletion> {
        match side {
            DeletionSide::Server => &mut self.server,
            DeletionSide::Local => &mut self.local,
        }
    }

    /// Add paths to the hold of a side. Returns `None` if nothing changed, else whether a new
    /// hold started and all held paths, which then need a new count (`set_count`).
    fn add(&mut self, side: DeletionSide, paths: Vec<PathBuf>) -> Option<(bool, Vec<PathBuf>)> {
        let slot = self.slot(side);
        let started = slot.is_none();
        let held = slot.get_or_insert_with(|| HeldDeletion {
            paths: Vec::new(),
            count: 0,
            version: 0,
            counted: false,
            resolving: false,
        });
        let mut changed = false;
        for path in paths {
            // A path inside an already held folder is held already.
            if !is_under(&path, &held.paths) {
                held.paths.push(path);
                changed = true;
            }
        }
        if !changed {
            return None;
        }
        held.counted = false;
        Some((started, held.paths.clone()))
    }

    /// Store the count for the held paths and publish them under a new version, unless the
    /// paths changed meanwhile (then the newer call publishes).
    fn set_count(&mut self, side: DeletionSide, paths: &[PathBuf], count: i64) {
        if let Some(held) = self.slot(side).as_mut() {
            if held.paths == paths {
                held.count = count;
                held.counted = true;
                held.version = NEXT_VERSION.fetch_add(1, Ordering::SeqCst);
            }
        }
    }

    fn is_held(&mut self, side: DeletionSide, path: &Path) -> bool {
        self.slot(side)
            .as_ref()
            .is_some_and(|held| is_under(path, &held.paths))
    }

    fn is_kept(&self, path: &Path) -> bool {
        is_under(path, &self.kept_local)
    }

    fn is_confirmed(&mut self, paths: &[PathBuf], now: Instant) -> bool {
        self.confirmed_local
            .retain(|(_, at)| now.duration_since(*at) < CONFIRMATION_VALID_FOR);
        let confirmed: Vec<PathBuf> = self.confirmed_local.iter().map(|(p, _)| p.clone()).collect();
        paths.iter().all(|p| is_under(p, &confirmed))
    }

    fn allow(&mut self, paths: Vec<PathBuf>, now: Instant) {
        self.kept_local.retain(|kept| !is_under(kept, &paths));
        self.confirmed_local
            .extend(paths.into_iter().map(|p| (p, now)));
    }

    /// The held deletion the user answered, if it is still the one they saw. Claims it, so
    /// a second answer is refused until `release` or `unclaim`.
    fn answered(&mut self, side: DeletionSide, version: u64) -> Result<Option<HeldDeletion>> {
        match self.slot(side) {
            Some(held) if held.resolving => anyhow::bail!("this deletion is being handled"),
            Some(held) if held.version == version && held.counted => {
                held.resolving = true;
                Ok(Some(held.clone()))
            }
            Some(_) => anyhow::bail!("the held deletion changed, please look at it again"),
            None => Ok(None),
        }
    }

    /// Give up a claim without resolving anything (the answer failed).
    fn unclaim(&mut self, side: DeletionSide) {
        if let Some(held) = self.slot(side).as_mut() {
            held.resolving = false;
        }
    }

    /// Remove the resolved paths from the hold and end the claim; paths added meanwhile stay
    /// held. Returns the remaining paths if they need a new count.
    fn release(&mut self, side: DeletionSide, resolved: &[PathBuf]) -> Option<Vec<PathBuf>> {
        let slot = self.slot(side);
        let held = slot.as_mut()?;
        held.resolving = false;
        held.paths.retain(|p| !resolved.contains(p));
        if held.paths.is_empty() {
            *slot = None;
            return None;
        }
        held.counted = false;
        Some(held.paths.clone())
    }

    fn keep(&mut self, paths: &[PathBuf]) {
        self.kept_local.extend(paths.iter().cloned());
    }
}

/// What the settings page shows about a held deletion.
#[derive(Debug, Clone, Serialize)]
pub struct HeldDeletionInfo {
    pub side: DeletionSide,
    pub count: i64,
    pub version: u64,
}

static NEXT_VERSION: AtomicU64 = AtomicU64::new(1);

pub fn is_under(path: &Path, roots: &[PathBuf]) -> bool {
    roots.iter().any(|root| path.starts_with(root))
}

/// Read-only means the user may neither create, upload nor change files in the drive.
/// `None` when the server sent no capability set.
pub fn is_read_only_capability(capability: Option<&str>) -> Option<bool> {
    let set = Boolset::from_base64(capability?).ok()?;
    let writable = [
        navigator_capability::CREATE_FILE,
        navigator_capability::RENAME_FILE,
        navigator_capability::UPLOAD_FILE,
    ]
    .iter()
    .any(|bit| set.enabled(*bit as usize));
    Some(!writable)
}

impl Mount {
    /// Number of affected entries if deleting `paths` needs the user's confirmation.
    pub(crate) fn mass_deletion_count(&self, paths: &[PathBuf]) -> Option<i64> {
        let (affected, total) = self.count_deletion(paths);
        is_mass_deletion(affected, total).then_some(affected)
    }

    /// Entries at or under `paths`, and all entries of the drive. Without numbers from the
    /// database, every path counts as one and the drive as empty, which holds a large batch.
    fn count_deletion(&self, paths: &[PathBuf]) -> (i64, i64) {
        let strs: Vec<&str> = paths.iter().filter_map(|p| p.to_str()).collect();
        let counts = self
            .inventory
            .count_under_paths(&self.id, &strs)
            .and_then(|affected| Ok((affected, self.inventory.count_for_drive(&self.id)?)));
        counts.unwrap_or_else(|e| {
            tracing::error!(target: "drive::guard", id = %self.id, error = ?e, "Failed to count deletion");
            (paths.len() as i64, 0)
        })
    }

    /// Hold a deletion until the user decides. Adds to an already held deletion of that side.
    pub(crate) fn hold_deletion(&self, side: DeletionSide, paths: Vec<PathBuf>) {
        let Some((started, merged)) = self.deletion_guard.lock().unwrap().add(side, paths) else {
            return;
        };
        // Count outside the lock.
        let count = self.count_deletion(&merged).0;
        self.deletion_guard
            .lock()
            .unwrap()
            .set_count(side, &merged, count);
        tracing::warn!(target: "drive::guard", id = %self.id, side = ?side, count, "Mass deletion held");
        // One notification when a hold starts.
        if started {
            toast::send_mass_deletion_toast(&self.id, side, count);
        }
    }

    /// Whether a deletion of `path` on this side is held.
    pub(crate) fn is_deletion_held(&self, side: DeletionSide, path: &Path) -> bool {
        self.deletion_guard.lock().unwrap().is_held(side, path)
    }

    /// Whether the user chose to keep `path` here although it was deleted on the server.
    pub(crate) fn is_kept_locally(&self, path: &Path) -> bool {
        self.deletion_guard.lock().unwrap().is_kept(path)
    }

    pub(crate) fn has_server_deletion_held(&self) -> bool {
        self.deletion_guard.lock().unwrap().server.is_some()
    }

    /// Whether all `paths` may be deleted locally without asking.
    pub(crate) fn is_local_deletion_confirmed(&self, paths: &[PathBuf]) -> bool {
        self.deletion_guard
            .lock()
            .unwrap()
            .is_confirmed(paths, Instant::now())
    }

    /// Let local deletions under `paths` pass for a while: confirmed by the user, or known
    /// moves (a rename on the server).
    pub(crate) fn allow_local_deletion(&self, paths: Vec<PathBuf>) {
        self.deletion_guard
            .lock()
            .unwrap()
            .allow(paths, Instant::now());
    }

    pub fn held_deletions(&self) -> Vec<HeldDeletionInfo> {
        let state = self.deletion_guard.lock().unwrap();
        [
            (DeletionSide::Server, &state.server),
            (DeletionSide::Local, &state.local),
        ]
        .into_iter()
        .filter_map(|(side, held)| {
            held.as_ref().map(|h| HeldDeletionInfo {
                side,
                count: h.count,
                version: h.version,
            })
        })
        .collect()
    }

    /// The user's answer to a held deletion. `version` is the one the user saw; if more paths
    /// were added since, nothing happens and the user is asked to look again.
    pub async fn resolve_held_deletion(
        &self,
        side: DeletionSide,
        version: u64,
        confirm: bool,
    ) -> Result<()> {
        let Some(held) = self.deletion_guard.lock().unwrap().answered(side, version)? else {
            return Ok(());
        };
        tracing::info!(target: "drive::guard", id = %self.id, side = ?side, confirm, count = held.count, "Held deletion resolved");

        match (side, confirm) {
            (DeletionSide::Server, true) => {
                // Only what is surely still gone on this PC. The hold stays until the server
                // answered, so no sync brings the files back meanwhile.
                let gone: Vec<PathBuf> = held
                    .paths
                    .iter()
                    .filter(|p| matches!(p.try_exists(), Ok(false)))
                    .cloned()
                    .collect();
                let failed = match self.delete_on_server(gone).await {
                    Ok(failed) => failed,
                    Err(e) => {
                        self.deletion_guard.lock().unwrap().unclaim(side);
                        return Err(e);
                    }
                };
                // What the server refused stays held; the user can try again or restore it.
                let resolved: Vec<PathBuf> = held
                    .paths
                    .iter()
                    .filter(|p| !failed.contains(p))
                    .cloned()
                    .collect();
                self.release_held(side, &resolved);
                if !failed.is_empty() {
                    anyhow::bail!(
                        "{} entries could not be deleted on the server, they stay held",
                        failed.len()
                    );
                }
                Ok(())
            }
            (DeletionSide::Server, false) => {
                // Bring the files back from the server.
                self.release_held(side, &held.paths);
                self.request_sync(held.paths)
            }
            (DeletionSide::Local, true) => {
                self.allow_local_deletion(held.paths.clone());
                self.release_held(side, &held.paths);
                self.request_sync(held.paths)
            }
            (DeletionSide::Local, false) => {
                // Keep the local files for this run; they are not asked about again.
                self.deletion_guard.lock().unwrap().keep(&held.paths);
                self.release_held(side, &held.paths);
                Ok(())
            }
        }
    }

    /// Release resolved paths and count what stays held.
    fn release_held(&self, side: DeletionSide, resolved: &[PathBuf]) {
        let remaining = self.deletion_guard.lock().unwrap().release(side, resolved);
        if let Some(remaining) = remaining {
            let count = self.count_deletion(&remaining).0;
            self.deletion_guard
                .lock()
                .unwrap()
                .set_count(side, &remaining, count);
        }
    }

    fn request_sync(&self, paths: Vec<PathBuf>) -> Result<()> {
        self.command_tx.send(MountCommand::Sync {
            local_paths: paths,
            mode: SyncMode::PathOnly,
            user_initiated: false,
        })?;
        Ok(())
    }

    pub fn is_read_only(&self) -> bool {
        self.read_only.load(Ordering::SeqCst)
    }

    /// Ask the server what the user may do in this drive. Keeps the last known state if the
    /// server cannot be reached.
    pub async fn refresh_capabilities(&self) {
        let remote_path = self.config.read().await.remote_path.clone();
        let result = self
            .cr_client
            .list_files(&ListFileService {
                uri: remote_path,
                page: None,
                page_size: Some(10),
                order_by: None,
                order_direction: None,
                next_page_token: None,
            })
            .await;
        match result {
            Ok(list) => {
                if let Some(read_only) = is_read_only_capability(list.props.capability.as_deref()) {
                    let was = self.read_only.swap(read_only, Ordering::SeqCst);
                    if was != read_only {
                        tracing::info!(target: "drive::guard", id = %self.id, read_only, "Drive rights changed");
                    }
                }
            }
            Err(e) => {
                tracing::warn!(target: "drive::guard", id = %self.id, error = %e, "Failed to read drive rights");
            }
        }
    }

    /// The one way to queue an upload. A read-only drive gets none.
    pub(crate) async fn enqueue_upload(&self, payload: TaskPayload) -> Result<()> {
        if self.is_read_only() {
            self.refuse_read_only_change(&payload.local_path).await;
            return Ok(());
        }
        self.task_queue.enqueue(payload).await.map(|_| ())
    }

    /// A local change in a read-only drive: mark the file and tell the user once per run.
    pub(crate) async fn refuse_read_only_change(&self, path: &Path) {
        tracing::info!(target: "drive::guard", id = %self.id, path = %path.display(), "Read-only drive, change not uploaded");
        let (sync_root, drive_id) = {
            let config = self.config.read().await;
            (config.sync_path.clone(), Uuid::parse_str(&config.id).unwrap_or_default())
        };
        let placeholder = CrPlaceholder::new(path.to_path_buf(), sync_root, drive_id);
        if placeholder.local_file_info.exists {
            let _ = placeholder.update_sync_error_state(true);
        }
        if !self.read_only_notified.swap(true, Ordering::SeqCst) {
            let name = self.config.read().await.name.clone();
            toast::send_warning_toast(
                &t!("readOnlyDriveTitle"),
                &t!("readOnlyDriveMessage", "drive" => name),
            );
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use base64::{Engine, engine::general_purpose::STANDARD};

    #[test]
    fn mass_deletion_threshold() {
        // A few files never need a confirmation.
        assert!(!is_mass_deletion(1, 1));
        assert!(!is_mass_deletion(9, 9));
        // 10 or more entries when they are at least 30 % of the drive.
        assert!(is_mass_deletion(10, 30));
        assert!(!is_mass_deletion(10, 40));
        assert!(!is_mass_deletion(49, 1000));
        // 50 or more always.
        assert!(is_mass_deletion(50, 100_000));
        // An inventory that knows fewer entries than are deleted.
        assert!(is_mass_deletion(12, 0));
    }

    fn capability(bits: &[i32]) -> String {
        let mut data = vec![0u8; 4];
        for bit in bits {
            data[*bit as usize / 8] |= 1 << (*bit as usize % 8);
        }
        STANDARD.encode(data)
    }

    #[test]
    fn read_only_capability() {
        use navigator_capability::*;
        // Reader of a shared folder (see navigator.go shareNavigatorCapability).
        let reader = capability(&[DOWNLOAD_FILE, LIST_CHILDREN, GENERATE_THUMB, LOCK_FILE, INFO]);
        assert_eq!(is_read_only_capability(Some(&reader)), Some(true));
        // Editor and own drive.
        let editor = capability(&[CREATE_FILE, RENAME_FILE, UPLOAD_FILE, DOWNLOAD_FILE]);
        assert_eq!(is_read_only_capability(Some(&editor)), Some(false));
        // Taken from a running Fylvo server: an editor of a shared folder.
        assert_eq!(is_read_only_capability(Some("w8aZAQ==")), Some(false));
        // Nothing sent or garbage: unknown.
        assert_eq!(is_read_only_capability(None), None);
        assert_eq!(is_read_only_capability(Some("%%%")), None);
    }

    #[test]
    fn is_under_matches_whole_components() {
        let roots = vec![PathBuf::from(r"C:\S\a")];
        assert!(is_under(Path::new(r"C:\S\a"), &roots));
        assert!(is_under(Path::new(r"C:\S\a\b.txt"), &roots));
        assert!(!is_under(Path::new(r"C:\S\ab.txt"), &roots));
    }

    fn paths(list: &[&str]) -> Vec<PathBuf> {
        list.iter().map(PathBuf::from).collect()
    }

    #[test]
    fn hold_grows_and_changes_version() {
        let mut state = DeletionGuardState::default();
        let (started, merged) = state.add(DeletionSide::Server, paths(&[r"C:\S\a"])).unwrap();
        assert!(started);
        // Not answerable before the count is set.
        let unpublished = state.server.as_ref().unwrap().version;
        assert!(state.answered(DeletionSide::Server, unpublished).is_err());
        state.set_count(DeletionSide::Server, &merged, 1);
        let first = state.server.as_ref().unwrap().version;

        // A path inside a held folder is held already.
        assert!(state.add(DeletionSide::Server, paths(&[r"C:\S\a\x.txt"])).is_none());

        // The same path again changes nothing.
        assert!(state.add(DeletionSide::Server, paths(&[r"C:\S\a"])).is_none());

        // A new path joins the hold, no new notification, new version.
        let (started, merged) = state.add(DeletionSide::Server, paths(&[r"C:\S\b"])).unwrap();
        assert!(!started);
        assert_eq!(merged.len(), 2);
        state.set_count(DeletionSide::Server, &merged, 2);
        assert_ne!(state.server.as_ref().unwrap().version, first);

        // An answer to the old version is refused; nothing is released.
        assert!(state.answered(DeletionSide::Server, first).is_err());
        assert_eq!(state.server.as_ref().unwrap().paths.len(), 2);
    }

    #[test]
    fn release_keeps_paths_added_meanwhile() {
        let mut state = DeletionGuardState::default();
        let (_, merged) = state.add(DeletionSide::Local, paths(&[r"C:\S\a"])).unwrap();
        state.set_count(DeletionSide::Local, &merged, 1);
        let version = state.local.as_ref().unwrap().version;
        let answered = state.answered(DeletionSide::Local, version).unwrap().unwrap();
        // A second answer while the first one runs is refused.
        assert!(state.answered(DeletionSide::Local, version).is_err());
        // While the confirmed deletion runs, another path is held.
        state.add(DeletionSide::Local, paths(&[r"C:\S\b"]));
        let remaining = state.release(DeletionSide::Local, &answered.paths).unwrap();
        assert_eq!(remaining, paths(&[r"C:\S\b"]));
        // The rest needs a new count before it can be answered.
        let version = state.local.as_ref().unwrap().version;
        assert!(state.answered(DeletionSide::Local, version).is_err());
        state.set_count(DeletionSide::Local, &remaining, 1);
        assert!(state.release(DeletionSide::Local, &remaining).is_none());
        assert!(state.local.is_none());
    }

    #[test]
    fn held_path_stays_held_when_fewer_entries_are_left() {
        // The planner asks is_held before the threshold; a held path stays held.
        let mut state = DeletionGuardState::default();
        state.add(DeletionSide::Local, paths(&[r"C:\S\a", r"C:\S\b"]));
        assert!(state.is_held(DeletionSide::Local, Path::new(r"C:\S\a\file.txt")));
        assert!(!state.is_held(DeletionSide::Server, Path::new(r"C:\S\a")));
        assert!(!state.is_held(DeletionSide::Local, Path::new(r"C:\S\c")));
    }

    #[test]
    fn confirmation_is_not_used_up_and_expires() {
        let mut state = DeletionGuardState::default();
        let now = Instant::now();
        state.keep(&paths(&[r"C:\S\a"]));
        state.allow(paths(&[r"C:\S\a"]), now);
        // A confirmation ends "keep" for that path.
        assert!(!state.is_kept(Path::new(r"C:\S\a")));
        // Any number of passes may check it (an unrelated pass does not use it up).
        assert!(state.is_confirmed(&paths(&[r"C:\S\a\x.txt"]), now));
        assert!(state.is_confirmed(&paths(&[r"C:\S\a"]), now));
        assert!(!state.is_confirmed(&paths(&[r"C:\S\a", r"C:\S\b"]), now));
        // After the time limit it is gone.
        let later = now + CONFIRMATION_VALID_FOR + Duration::from_secs(1);
        assert!(!state.is_confirmed(&paths(&[r"C:\S\a"]), later));
    }
}

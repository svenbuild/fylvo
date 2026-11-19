//! Fylvo: protection of the user's files.
//!
//! - Mass deletion: when one pass would delete many entries (on the server or on this PC), the
//!   deletion is held until the user confirms it in the settings.
//! - Read-only drives: a shared folder the user may only read gets no upload attempts.
//!
//! Held deletions live in memory only. After a restart a held local deletion is undone by the
//! full sync (the files come back from the server) and a held server deletion is asked again.

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
    sync::atomic::Ordering,
};
use uuid::Uuid;

/// A pass that deletes at least this many entries is held.
const MASS_DELETE_COUNT: i64 = 50;
/// A pass that deletes at least `MASS_DELETE_SHARE_MIN` entries and at least
/// `MASS_DELETE_SHARE_PERCENT` percent of the drive's known entries is held too.
const MASS_DELETE_SHARE_MIN: i64 = 10;
const MASS_DELETE_SHARE_PERCENT: i64 = 30;

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
}

/// Held deletions of one drive, one per side.
#[derive(Debug, Default)]
pub struct DeletionGuardState {
    server: Option<HeldDeletion>,
    local: Option<HeldDeletion>,
    /// Paths whose local deletion the user confirmed; used by the next sync pass.
    confirmed_local: Vec<PathBuf>,
    /// Confirmed paths of the running sync pass.
    active_confirmed_local: Vec<PathBuf>,
    /// Paths the user chose to keep; no new notification for them in this run.
    kept_local: Vec<PathBuf>,
}

impl DeletionGuardState {
    fn slot(&mut self, side: DeletionSide) -> &mut Option<HeldDeletion> {
        match side {
            DeletionSide::Server => &mut self.server,
            DeletionSide::Local => &mut self.local,
        }
    }
}

/// What the settings page shows about a held deletion.
#[derive(Debug, Clone, Serialize)]
pub struct HeldDeletionInfo {
    pub side: DeletionSide,
    pub count: i64,
}

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
        let strs: Vec<&str> = paths.iter().filter_map(|p| p.to_str()).collect();
        let counts = self
            .inventory
            .count_under_paths(&self.id, &strs)
            .and_then(|affected| Ok((affected, self.inventory.count_for_drive(&self.id)?)));
        match counts {
            Ok((affected, total)) if is_mass_deletion(affected, total) => Some(affected),
            Ok(_) => None,
            Err(e) => {
                // Without numbers, hold a large batch to be safe.
                tracing::error!(target: "drive::guard", id = %self.id, error = ?e, "Failed to count deletion");
                (paths.len() as i64 >= MASS_DELETE_SHARE_MIN).then_some(paths.len() as i64)
            }
        }
    }

    /// Hold a deletion until the user decides. Adds to an already held deletion of that side.
    pub(crate) fn hold_deletion(&self, side: DeletionSide, paths: Vec<PathBuf>, count: i64) {
        // One notification when a hold starts, none for paths the user already chose to keep.
        let notify = {
            let mut state = self.deletion_guard.lock().unwrap();
            let kept = side == DeletionSide::Local
                && paths.iter().all(|p| is_under(p, &state.kept_local));
            match state.slot(side) {
                Some(held) => {
                    for path in paths {
                        if !held.paths.contains(&path) {
                            held.paths.push(path);
                        }
                    }
                    held.count = held.count.max(count);
                    false
                }
                slot => {
                    *slot = Some(HeldDeletion { paths, count });
                    !kept
                }
            }
        };
        tracing::warn!(target: "drive::guard", id = %self.id, side = ?side, count, "Mass deletion held");
        if notify {
            toast::send_mass_deletion_toast(&self.id, side, count);
        }
    }

    /// Whether a local deletion of `path` is held (so the sync must not bring it back yet).
    pub(crate) fn is_server_deletion_held(&self, path: &Path) -> bool {
        let state = self.deletion_guard.lock().unwrap();
        state
            .server
            .as_ref()
            .is_some_and(|held| is_under(path, &held.paths))
    }

    pub(crate) fn has_server_deletion_held(&self) -> bool {
        self.deletion_guard.lock().unwrap().server.is_some()
    }

    /// Whether the user confirmed the local deletion of all `paths` for the running pass.
    pub(crate) fn is_local_deletion_confirmed(&self, paths: &[PathBuf]) -> bool {
        let state = self.deletion_guard.lock().unwrap();
        paths
            .iter()
            .all(|p| is_under(p, &state.active_confirmed_local))
    }

    /// Mark local deletions as known moves (a rename on the server), so the guard lets them pass.
    pub(crate) fn allow_local_deletion(&self, paths: Vec<PathBuf>) {
        self.deletion_guard
            .lock()
            .unwrap()
            .confirmed_local
            .extend(paths);
    }

    /// Called at the start and the end of a sync pass.
    pub(crate) fn begin_sync_pass(&self) {
        let mut state = self.deletion_guard.lock().unwrap();
        let confirmed = std::mem::take(&mut state.confirmed_local);
        state.active_confirmed_local = confirmed;
    }

    pub(crate) fn end_sync_pass(&self) {
        self.deletion_guard
            .lock()
            .unwrap()
            .active_confirmed_local
            .clear();
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
            })
        })
        .collect()
    }

    /// The user's answer to a held deletion.
    pub async fn resolve_held_deletion(&self, side: DeletionSide, confirm: bool) -> Result<()> {
        let held = {
            let mut state = self.deletion_guard.lock().unwrap();
            let held = state.slot(side).take();
            if let (Some(held), DeletionSide::Local) = (&held, side) {
                if confirm {
                    state.confirmed_local.extend(held.paths.clone());
                } else {
                    state.kept_local.extend(held.paths.clone());
                }
            }
            held
        };
        let Some(held) = held else {
            return Ok(());
        };
        tracing::info!(target: "drive::guard", id = %self.id, side = ?side, confirm, count = held.count, "Held deletion resolved");

        match (side, confirm) {
            (DeletionSide::Server, true) => {
                // Only what is still gone on this PC.
                let paths: Vec<PathBuf> = held.paths.into_iter().filter(|p| !p.exists()).collect();
                self.delete_on_server(paths).await
            }
            // Bring the files back from the server, or run the confirmed local deletion.
            (DeletionSide::Server, false) | (DeletionSide::Local, true) => {
                self.command_tx.send(MountCommand::Sync {
                    local_paths: held.paths,
                    mode: SyncMode::PathOnly,
                    user_initiated: false,
                })?;
                Ok(())
            }
            // Keep the local files. Nothing to do.
            (DeletionSide::Local, false) => Ok(()),
        }
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
}

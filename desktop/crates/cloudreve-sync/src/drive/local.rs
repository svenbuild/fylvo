//! Fylvo: what the sync decisions know about a local file or folder, the same on every platform.
//!
//! Windows fills it from the Cloud Files placeholder state (`cfapi::placeholder`). Linux keeps
//! every file local and fills it from `lstat` plus the inventory row at exactly this path (see
//! `local_linux.rs` and `desktop/docs/linux-plan.md`).

use std::time::SystemTime;

/// The pin state of a placeholder.
///
/// [Read more
/// here](https://docs.microsoft.com/en-us/windows/win32/api/cfapi/ne-cfapi-cf_pin_state#remarks)
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum PinState {
    /// The platform could decide freely.
    Unspecified,
    /// The rest of the placeholder's data is hydrated. Any dehydration requests fail.
    Pinned,
    /// The rest of the placeholder's data is dehydrated.
    Unpinned,
    /// The placeholder will never sync to the cloud.
    Excluded,
    /// The placeholder will inherit the parent placeholder's pin state.
    Inherit,
}

/// The sync state of a tracked file or folder (a "placeholder" in Windows terms).
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct SyncState {
    /// Tracked by the sync: a placeholder on Windows, a file with a matching inventory row on
    /// Linux.
    pub is_placeholder: bool,
    /// Its content is the one the inventory has from the server.
    pub in_sync: bool,
    /// Not all of its content is on disk (Windows online-only files, never on Linux).
    pub partial_on_disk: bool,
}

#[derive(Debug, Clone)]
pub struct LocalFileInfo {
    pub exists: bool,
    pub is_directory: bool,
    pub file_size: Option<u64>,
    pub last_modified: Option<SystemTime>,
    pub sync_state: Option<SyncState>,
    pub pin_state: PinState,
}

impl LocalFileInfo {
    pub fn missing() -> Self {
        Self {
            exists: false,
            is_directory: false,
            file_size: None,
            last_modified: None,
            sync_state: None,
            pin_state: PinState::Unspecified,
        }
    }

    pub fn in_sync(&self) -> bool {
        self.sync_state.is_some_and(|state| state.in_sync)
    }

    pub fn is_placeholder(&self) -> bool {
        self.sync_state.is_some_and(|state| state.is_placeholder)
    }

    pub fn partial_on_disk(&self) -> bool {
        self.sync_state.is_some_and(|state| state.partial_on_disk)
    }

    pub fn pinned(&self) -> PinState {
        self.pin_state
    }

    pub fn is_directory(&self) -> bool {
        self.is_directory
    }

    pub fn is_folder_populated(&self) -> bool {
        self.is_placeholder() && self.is_directory && !self.partial_on_disk()
    }
}

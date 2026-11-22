//! Fylvo: drive letters exist only on Windows (`drive_letter.rs`). On Linux the setting is
//! always off and cannot be switched on; the settings page hides it.

use anyhow::Result;
use serde::Serialize;

use crate::drive::mounts::Mount;

/// What the settings show about a drive's letter.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Default)]
#[serde(rename_all = "snake_case")]
pub enum LetterState {
    /// Switched off.
    #[default]
    Off,
    /// The letter shows the sync folder.
    Active,
    /// The wished letter is used by something else.
    Conflict,
}

/// No letter is usable on Linux, so a stored letter (from a copied config) is ignored.
pub fn is_usable(_letter: char) -> bool {
    false
}

impl Mount {
    pub async fn set_drive_letter(&self, _enabled: bool, _letter: Option<char>) -> Result<LetterState> {
        anyhow::bail!("drive letters exist only on Windows")
    }

    pub async fn free_drive_letters(&self) -> Vec<char> {
        Vec::new()
    }

    pub(crate) async fn close_drive_letter(&self) {}

    pub fn drive_letter_state(&self) -> LetterState {
        LetterState::Off
    }

    pub async fn spawn_drive_letter_task(self: &std::sync::Arc<Self>) {}
}

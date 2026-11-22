//! The small state file `<dir>/state.json`: which update is being installed (so the next start
//! can tell success from failure) and which version failed last (not tried again for a day).

use crate::Version;
use serde::{Deserialize, Serialize};
use std::path::Path;

const STATE_FILE: &str = "state.json";
/// A version that failed to install is not tried again for this long.
pub const RETRY_FAILED_AFTER_SECS: u64 = 24 * 60 * 60;

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct State {
    #[serde(default)]
    pub installing: Option<Installing>,
    #[serde(default)]
    pub failed: Option<Failed>,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Installing {
    pub version: Version,
    pub from: Version,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Failed {
    pub version: Version,
    /// Unix time in seconds.
    pub at: u64,
}

/// What the start found out about the last update.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum StartOutcome {
    Nothing,
    Updated { from: Version, to: Version },
    Failed { version: Version },
}

impl State {
    /// Reads the state; a missing or broken file is an empty state.
    pub fn load(dir: &Path) -> State {
        std::fs::read(dir.join(STATE_FILE))
            .ok()
            .and_then(|raw| serde_json::from_slice(&raw).ok())
            .unwrap_or_default()
    }

    /// Writes the state in one step (temp file + rename).
    pub fn save(&self, dir: &Path) -> std::io::Result<()> {
        std::fs::create_dir_all(dir)?;
        let tmp = dir.join(".state.json.tmp");
        std::fs::write(&tmp, serde_json::to_vec(self)?)?;
        std::fs::rename(&tmp, dir.join(STATE_FILE))
    }

    /// Called once at start with the running version: ends a pending installation.
    pub fn on_start(&mut self, running: Version, now: u64) -> StartOutcome {
        let Some(installing) = self.installing.take() else {
            return StartOutcome::Nothing;
        };
        if running >= installing.version {
            self.failed = None;
            StartOutcome::Updated { from: installing.from, to: running }
        } else {
            self.failed = Some(Failed { version: installing.version, at: now });
            StartOutcome::Failed { version: installing.version }
        }
    }

    /// True if `version` failed less than a day ago and is not tried again yet.
    pub fn blocked(&self, version: Version, now: u64) -> bool {
        self.failed
            .as_ref()
            .is_some_and(|f| f.version == version && now.saturating_sub(f.at) < RETRY_FAILED_AFTER_SECS)
    }
}

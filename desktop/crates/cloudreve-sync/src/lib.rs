pub mod activity;
#[cfg(windows)]
pub mod cfapi;
pub mod config;
pub mod drive;
pub mod events;
pub mod inventory;
pub mod logging;
#[cfg(windows)]
pub mod shellext;
pub mod tasks;
pub mod uploader;
pub mod utils;
#[cfg(target_os = "linux")]
pub mod vfs;

// Re-export commonly used types
pub use config::{AppConfig, ConfigManager};
pub use drive::manager::{DriveInfo, DriveInfoStatus, DriveManager, StatusSummary, TaskWithProgress};
pub use drive::mounts::{Credentials, DriveConfig};
pub use events::{Event, EventBroadcaster};
pub use logging::{LogConfig, LogGuard};

/// User agent string for HTTP requests
pub const USER_AGENT: &str = concat!("fylvo-desktop/", env!("CARGO_PKG_VERSION"));

/// Folder in the user profile for config, database, logs and icons.
/// Not ".cloudreve", so Fylvo Desktop and the Store app keep separate state.
pub const DATA_DIR: &str = ".fylvo";

#[macro_use]
extern crate rust_i18n;

/// Fylvo: a Windows path of a test (`C:\\S\\a.txt`) as a path of the platform the tests run
/// on (`/S/a.txt` on Linux), so the same tests check both.
#[cfg(test)]
pub(crate) fn native(path: &str) -> &'static str {
    if cfg!(windows) {
        return Box::leak(path.to_string().into_boxed_str());
    }
    let unix = path.strip_prefix("C:").unwrap_or(path).replace('\\', "/");
    Box::leak(unix.into_boxed_str())
}

i18n!("../../locales", fallback = "en-US");

/// Initialize the application root path (Windows Package detection)
pub fn init_app_root() {
    utils::app::init_app_root();
}

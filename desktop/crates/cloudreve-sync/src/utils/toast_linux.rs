//! Fylvo: the notifications of `toast.rs` for Linux, sent over D-Bus to the desktop's
//! notification service. Same texts and the same settings as on Windows, without buttons: held
//! deletions and conflicts are handled in the app window.

use std::path::PathBuf;

use crate::config::ConfigManager;

const APP_NAME: &str = "Fylvo Desktop";
const ICON: &str = "fylvo-desktop";

/// Shows a notification. A failure (no notification service, for example in a bare session) is
/// logged and never stops the caller.
fn show(title: &str, message: &str, urgent: bool) {
    let mut notification = notify_rust::Notification::new();
    notification
        .appname(APP_NAME)
        .icon(ICON)
        .summary(title)
        .body(message);
    if urgent {
        notification.urgency(notify_rust::Urgency::Critical);
    }
    if let Err(e) = notification.show() {
        tracing::warn!(target: "toast", error = ?e, "Failed to show notification");
    }
}

pub fn send_general_text_toast(title: &str, message: &str) {
    show(title, message, false);
}

/// Send a notification about something the user should look at.
pub fn send_warning_toast(title: &str, message: &str) {
    show(title, message, false);
}

/// Fylvo: a mass deletion is held. Always shown (safety); the settings in the app window list
/// it for the user to confirm or cancel.
pub fn send_mass_deletion_toast(_drive_id: &str, side: crate::drive::guard::DeletionSide, count: i64) {
    use crate::drive::guard::DeletionSide;
    let (title, message) = match side {
        DeletionSide::Server => (
            t!("massDeleteServerTitle"),
            t!("massDeleteServerMessage", "count" => count),
        ),
        DeletionSide::Local => (
            t!("massDeleteLocalTitle"),
            t!("massDeleteLocalMessage", "count" => count),
        ),
    };
    show(&title, &message, true);
}

/// Send a notification for token expiry. Respects the notify_credential_expired setting.
pub fn send_token_expiry_toast(_drive_id: &str, title: &str, message: &str) {
    if let Some(config) = ConfigManager::try_get() {
        if !config.notify_credential_expired() {
            tracing::debug!(target: "toast", "Token expiry notification suppressed by config");
            return;
        }
    }
    show(title, message, false);
}

/// Send a notification for a file conflict. Respects the notify_file_conflict setting.
pub fn send_conflict_toast(_drive_id: &str, path: &PathBuf, _inventory_id: i64) {
    if let Some(config) = ConfigManager::try_get() {
        if !config.notify_file_conflict() {
            tracing::debug!(target: "toast", "Conflict notification suppressed by config");
            return;
        }
    }
    let name = path
        .file_name()
        .unwrap_or_default()
        .to_string_lossy()
        .to_string();
    show(&t!("conflictToastTitle"), &name, false);
}

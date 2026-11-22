//! Fylvo: installing an update on Linux (the general steps are in `updater.rs` and the crate
//! `fylvo-update`: find, download, check size, checksum and the release signature).
//!
//! - AppImage: the new file is put next to the running one, made executable and renamed over
//!   it (one step on the same file system); the new version is started after this one ends.
//! - rpm/deb: the package manager installs the file. `pkexec` lets the system ask for the
//!   admin password (Sven's choice: the app installs these updates itself).

use fylvo_update::LinuxFormat;
use std::path::{Path, PathBuf};
use std::process::Command;
use tauri::AppHandle;

/// The installed program of the rpm and deb packages.
const INSTALLED_PROGRAM: &str = "/usr/bin/fylvo-desktop";
/// The package name in rpm and dpkg.
const PACKAGE: &str = "fylvo-desktop";

/// The AppImage file this app runs from, if it runs from one.
fn appimage() -> Option<PathBuf> {
    std::env::var_os("APPIMAGE").map(PathBuf::from).filter(|p| p.is_file())
}

/// Whether a program answers `true` (exit code 0) for these arguments.
fn succeeds(program: &str, args: &[&str]) -> bool {
    Command::new(program)
        .args(args)
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .status()
        .is_ok_and(|status| status.success())
}

/// How this app was installed; `None` for a build run from source (no updates then).
pub fn format() -> Option<LinuxFormat> {
    if appimage().is_some() {
        return Some(LinuxFormat::AppImage);
    }
    let program = std::env::current_exe().ok()?;
    if program != Path::new(INSTALLED_PROGRAM) {
        return None;
    }
    if succeeds("rpm", &["-q", PACKAGE]) {
        Some(LinuxFormat::Rpm)
    } else if succeeds("dpkg-query", &["-W", PACKAGE]) {
        Some(LinuxFormat::Deb)
    } else {
        None
    }
}

/// The name the new AppImage gets next to the running one until it replaces it.
fn prepared_path(target: &Path) -> PathBuf {
    let mut name = target.file_name().unwrap_or_default().to_os_string();
    name.push(".fylvo-update");
    target.with_file_name(name)
}

/// Copies the checked download next to the running AppImage and makes it executable. Fails
/// early (before sync stops) if that folder cannot be written.
pub fn prepare_appimage(download: &Path) -> anyhow::Result<PathBuf> {
    use anyhow::Context;
    use std::os::unix::fs::PermissionsExt;
    let target = appimage().context("not running from an AppImage")?;
    let prepared = prepared_path(&target);
    std::fs::copy(download, &prepared)
        .with_context(|| format!("cannot write next to {}", target.display()))?;
    std::fs::set_permissions(&prepared, std::fs::Permissions::from_mode(0o755))?;
    Ok(prepared)
}

/// Puts the prepared AppImage in place of the running one (one rename).
pub fn replace_appimage(prepared: &Path) -> anyhow::Result<()> {
    use anyhow::Context;
    let target = appimage().context("not running from an AppImage")?;
    std::fs::rename(prepared, &target)
        .with_context(|| format!("cannot replace {}", target.display()))
}

/// The package manager command for a downloaded package (also an upgrade of the installed one).
fn install_command(format: LinuxFormat, package: &Path) -> Option<Vec<String>> {
    let file = package.to_string_lossy().to_string();
    match format {
        LinuxFormat::Rpm => Some(vec!["dnf".into(), "install".into(), "-y".into(), file]),
        LinuxFormat::Deb => Some(vec!["apt-get".into(), "install".into(), "-y".into(), file]),
        LinuxFormat::AppImage => None,
    }
}

/// Installs an rpm or deb with the package manager, the system asks for the admin password.
pub fn install_package(format: LinuxFormat, package: &Path) -> anyhow::Result<()> {
    let command = install_command(format, package)
        .ok_or_else(|| anyhow::anyhow!("not a package format"))?;
    let status = Command::new("pkexec").args(&command).status()?;
    if !status.success() {
        anyhow::bail!("the installation was cancelled or failed ({status})");
    }
    Ok(())
}

/// Starts the app again once this process has ended, then ends this process (started earlier,
/// the single-instance check would hand the new start to this process).
pub fn restart(app: &AppHandle) {
    let program = appimage()
        .or_else(|| std::env::current_exe().ok())
        .unwrap_or_else(|| PathBuf::from(INSTALLED_PROGRAM));
    let script = "while kill -0 \"$1\" 2>/dev/null; do sleep 1; done; exec \"$2\"";
    let started = Command::new("setsid")
        .args(["sh", "-c", script, "fylvo-restart"])
        .arg(std::process::id().to_string())
        .arg(&program)
        .stdin(std::process::Stdio::null())
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .spawn();
    match started {
        Ok(_) => app.exit(0),
        Err(e) => {
            tracing::error!(target: "updater", error = %e, "Failed to start the restart helper, restarting directly");
            app.restart();
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_prepared_appimage_lies_next_to_the_running_one() {
        assert_eq!(
            prepared_path(Path::new("/home/u/Apps/Fylvo Desktop.AppImage")),
            Path::new("/home/u/Apps/Fylvo Desktop.AppImage.fylvo-update")
        );
    }

    #[test]
    fn packages_go_to_their_package_manager() {
        let rpm = install_command(LinuxFormat::Rpm, Path::new("/tmp/a b.rpm")).unwrap();
        assert_eq!(rpm, ["dnf", "install", "-y", "/tmp/a b.rpm"]);
        let deb = install_command(LinuxFormat::Deb, Path::new("/tmp/x.deb")).unwrap();
        assert_eq!(deb, ["apt-get", "install", "-y", "/tmp/x.deb"]);
        assert!(install_command(LinuxFormat::AppImage, Path::new("/tmp/x")).is_none());
    }
}

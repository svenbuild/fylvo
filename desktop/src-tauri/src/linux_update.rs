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
    use std::os::unix::fs::OpenOptionsExt;
    let target = appimage().context("not running from an AppImage")?;
    let prepared = prepared_path(&target);
    // A leftover of an earlier try (or anything else under that name) goes first; removing a
    // link removes the link, not its target.
    match std::fs::remove_file(&prepared) {
        Err(e) if e.kind() != std::io::ErrorKind::NotFound => return Err(e.into()),
        _ => {}
    }
    let mut out = std::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o700)
        .open(&prepared)
        .with_context(|| format!("cannot write next to {}", target.display()))?;
    std::io::copy(&mut std::fs::File::open(download)?, &mut out)?;
    out.sync_all()?;
    out.set_permissions(std::fs::Permissions::from_mode(0o755))?;
    Ok(prepared)
}

/// Puts the prepared AppImage in place of the running one (one rename).
pub fn replace_appimage(prepared: &Path) -> anyhow::Result<()> {
    use anyhow::Context;
    let target = appimage().context("not running from an AppImage")?;
    std::fs::rename(prepared, &target)
        .with_context(|| format!("cannot replace {}", target.display()))
}

/// The package manager of a format (also upgrades the installed package).
fn package_manager(format: LinuxFormat) -> Option<&'static str> {
    match format {
        LinuxFormat::Rpm => Some("dnf install -y"),
        LinuxFormat::Deb => Some("apt-get install -y"),
        LinuxFormat::AppImage => None,
    }
}

/// Runs as root (through pkexec): copies the package into a fresh root-only folder, checks its
/// sha256 there against the signed release, and installs that copy. The user's download can
/// change after the app checked it; the copy cannot. Arguments: package, sha256, manager.
const ROOT_INSTALL: &str = r#"set -eu
dir=$(mktemp -d /var/tmp/fylvo-update.XXXXXX)
trap 'rm -rf "$dir"' EXIT
pkg="$dir/package.$4"
cp -- "$1" "$pkg"
[ "$(sha256sum "$pkg" | cut -c1-64)" = "$2" ] || { echo "checksum differs" >&2; exit 3; }
$3 "$pkg"
"#;

/// Installs an rpm or deb with the package manager, the system asks for the admin password.
pub fn install_package(format: LinuxFormat, package: &Path, sha256: &str) -> anyhow::Result<()> {
    let manager = package_manager(format).ok_or_else(|| anyhow::anyhow!("not a package format"))?;
    if sha256.len() != 64 || !sha256.bytes().all(|b| b.is_ascii_hexdigit()) {
        anyhow::bail!("unexpected checksum");
    }
    let ext = if format == LinuxFormat::Rpm { "rpm" } else { "deb" };
    let status = Command::new("pkexec")
        .args(["/bin/sh", "-c", ROOT_INSTALL, "fylvo-update"])
        .arg(package)
        .args([sha256, manager, ext])
        .status()?;
    if !status.success() {
        anyhow::bail!("the installation was cancelled or failed ({status})");
    }
    Ok(())
}

/// Starts the app again once this process has ended, then ends this process (started earlier,
/// the single-instance check would hand the new start to this process).
pub fn restart(app: &AppHandle) {
    // After an rpm/deb upgrade `current_exe` names the replaced file (" (deleted)"): a packaged
    // app restarts from its installed path.
    let packaged = std::env::current_exe()
        .is_ok_and(|exe| exe.to_string_lossy().starts_with(INSTALLED_PROGRAM));
    let program = appimage()
        .or_else(|| packaged.then(|| PathBuf::from(INSTALLED_PROGRAM)))
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
    fn the_root_step_installs_only_the_checked_copy() {
        // Run the script without root: "install" is `cat` into a log here.
        let dir = tempfile::tempdir().unwrap();
        let package = dir.path().join("a b.rpm");
        std::fs::write(&package, b"package").unwrap();
        let log = dir.path().join("log");
        let fake = dir.path().join("fake-manager");
        std::fs::write(&fake, format!("#!/bin/sh\ncat \"$1\" > '{}'\n", log.display())).unwrap();
        {
            use std::os::unix::fs::PermissionsExt;
            std::fs::set_permissions(&fake, std::fs::Permissions::from_mode(0o755)).unwrap();
        }
        let manager = fake.to_string_lossy().to_string();
        let sha = "1a4b2a6c3cfb5b0b6f40d1bc5e0b1c7ce7b9f73c0d4f5a7e6c2b1e5c0f0a9b8c"; // wrong
        let run = |sha: &str| {
            Command::new("/bin/sh")
                .args(["-c", ROOT_INSTALL, "fylvo-update"])
                .arg(&package)
                .args([sha, manager.as_str(), "rpm"])
                .env("TMPDIR", dir.path())
                .status()
                .unwrap()
        };
        assert!(!run(sha).success());
        assert!(!log.exists());
        let right = String::from_utf8(
            Command::new("sha256sum").arg(&package).output().unwrap().stdout,
        )
        .unwrap()[..64]
            .to_string();
        assert!(run(&right).success());
        assert_eq!(std::fs::read(&log).unwrap(), b"package");
    }
}

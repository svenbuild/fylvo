//! Fylvo: what the app does on a Linux desktop that Windows does through its package (MSIX):
//! starting with the session and the "Open online" entry in the file managers.
//!
//! Both are files in the user's home folder that point at the program. An AppImage moves with
//! every update, so they are written again at every start with the path of the running program.

use std::path::{Path, PathBuf};

/// The command line option the file manager entries pass with the selected path.
pub const VIEW_ONLINE_ARG: &str = "--view-online";

/// The program to start: the AppImage file itself when running from one (the path inside the
/// mounted image changes at every start), else the installed binary.
pub fn launcher() -> PathBuf {
    std::env::var_os("APPIMAGE")
        .map(PathBuf::from)
        .or_else(|| std::env::current_exe().ok())
        .unwrap_or_else(|| PathBuf::from("fylvo-desktop"))
}

fn home() -> Option<PathBuf> {
    std::env::var_os("HOME").map(PathBuf::from)
}

/// A path for an `Exec=` line of a `.desktop` file, as the Desktop Entry spec asks: quoted, with
/// `"`, `` ` ``, `$` and `\` escaped inside the quotes, `%` doubled (else a field code), then the
/// backslashes escaped once more for the string value. `None` for a path with a line break or
/// another control character, which no desktop file line can hold.
fn exec_quoted(path: &Path) -> Option<String> {
    let text = path.to_string_lossy();
    if text.chars().any(|c| c.is_control()) {
        return None;
    }
    let mut quoted = String::from("\"");
    for c in text.chars() {
        match c {
            '"' | '`' | '$' | '\\' => {
                quoted.push('\\');
                quoted.push(c);
            }
            '%' => quoted.push_str("%%"),
            _ => quoted.push(c),
        }
    }
    quoted.push('"');
    Some(quoted.replace('\\', "\\\\"))
}

/// A path for a POSIX shell script (single quotes).
fn shell_quoted(path: &Path) -> String {
    format!("'{}'", path.to_string_lossy().replace('\'', "'\\''"))
}

fn autostart_file() -> Option<PathBuf> {
    Some(home()?.join(".config/autostart/fylvo-desktop.desktop"))
}

fn autostart_entry(program: &Path) -> Option<String> {
    Some(format!(
        "[Desktop Entry]\nType=Application\nName=Fylvo Desktop\nExec={}\nIcon=fylvo-desktop\nTerminal=false\nX-GNOME-Autostart-enabled=true\n",
        exec_quoted(program)?
    ))
}

/// Whether the app starts with the session.
pub fn autostart_enabled() -> bool {
    autostart_file().is_some_and(|file| file.exists())
}

/// Switch starting with the session on or off.
pub fn set_autostart(enabled: bool) -> std::io::Result<()> {
    let file = autostart_file()
        .ok_or_else(|| std::io::Error::other("no home folder"))?;
    if enabled {
        let entry = autostart_entry(&launcher())
            .ok_or_else(|| std::io::Error::other("the program path cannot be written to a desktop file"))?;
        write_file(&file, &entry, false)
    } else {
        match std::fs::remove_file(&file) {
            Err(e) if e.kind() != std::io::ErrorKind::NotFound => Err(e),
            _ => Ok(()),
        }
    }
}

fn write_file(path: &Path, content: &str, executable: bool) -> std::io::Result<()> {
    use std::os::unix::fs::PermissionsExt;
    if let Some(parent) = path.parent() {
        std::fs::create_dir_all(parent)?;
    }
    if std::fs::read_to_string(path).is_ok_and(|old| old == content) {
        return Ok(());
    }
    std::fs::write(path, content)?;
    let mode = if executable { 0o755 } else { 0o644 };
    std::fs::set_permissions(path, std::fs::Permissions::from_mode(mode))
}

/// GNOME Files (Nautilus) runs scripts from this folder for the selected files.
fn nautilus_script(program: &Path) -> String {
    format!(
        "#!/bin/sh\n# Fylvo Desktop: open the selected files in the Fylvo web app.\nprintf '%s' \"$NAUTILUS_SCRIPT_SELECTED_FILE_PATHS\" | while IFS= read -r path || [ -n \"$path\" ]; do\n  [ -n \"$path\" ] && {} {} \"$path\"\ndone\n",
        shell_quoted(program),
        VIEW_ONLINE_ARG
    )
}

/// Dolphin (KDE) reads service menus from this folder.
fn dolphin_menu(program: &Path, label: &str) -> Option<String> {
    Some(format!(
        "[Desktop Entry]\nType=Service\nMimeType=all/all;inode/directory;\nActions=fylvoViewOnline;\nX-KDE-Priority=TopLevel\n\n[Desktop Action fylvoViewOnline]\nName={label}\nIcon=fylvo-desktop\nExec={} {} %f\n",
        exec_quoted(program)?,
        VIEW_ONLINE_ARG
    ))
}

/// Write the file manager entries and refresh the autostart entry, with the path of the
/// running program. Failures are logged; the app works without them.
pub fn install_desktop_integration(label: &str) {
    let Some(home) = home() else {
        return;
    };
    let program = launcher();
    let Some(dolphin) = dolphin_menu(&program, label) else {
        tracing::warn!(target: "main", path = %program.display(), "Program path with a line break, no file manager entries");
        return;
    };
    let files = [
        (
            home.join(".local/share/nautilus/scripts").join(label),
            nautilus_script(&program),
            true,
        ),
        (
            home.join(".local/share/kio/servicemenus/fylvo-desktop.desktop"),
            dolphin,
            true,
        ),
    ];
    for (path, content, executable) in files {
        if let Err(e) = write_file(&path, &content, executable) {
            tracing::warn!(target: "main", path = %path.display(), error = %e, "Failed to install file manager entry");
        }
    }
    // A label of an older language stays behind otherwise.
    remove_old_nautilus_scripts(&home.join(".local/share/nautilus/scripts"), label);
    if autostart_enabled() {
        if let Err(e) = set_autostart(true) {
            tracing::warn!(target: "main", error = %e, "Failed to refresh the autostart entry");
        }
    }
}

fn remove_old_nautilus_scripts(folder: &Path, label: &str) {
    let Ok(entries) = std::fs::read_dir(folder) else {
        return;
    };
    for entry in entries.flatten() {
        let ours = std::fs::read_to_string(entry.path())
            .is_ok_and(|text| text.starts_with("#!/bin/sh\n# Fylvo Desktop:"));
        if ours && entry.file_name() != label {
            let _ = std::fs::remove_file(entry.path());
        }
    }
}

/// The path after `--view-online` in a command line, if any.
pub fn view_online_path(argv: &[String]) -> Option<PathBuf> {
    let at = argv.iter().position(|arg| arg == VIEW_ONLINE_ARG)?;
    argv.get(at + 1).map(PathBuf::from)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn paths_are_quoted_for_desktop_files_and_shell() {
        let odd = Path::new("/home/a b/it's \"$x\".AppImage");
        // Inside the quotes `\"` and `\$`, then every backslash doubled for the string value.
        assert_eq!(exec_quoted(odd).unwrap(), "\"/home/a b/it's \\\\\"\\\\$x\\\\\".AppImage\"");
        assert_eq!(shell_quoted(odd), "'/home/a b/it'\\''s \"$x\".AppImage'");
        assert_eq!(exec_quoted(Path::new("/a/100%.AppImage")).unwrap(), "\"/a/100%%.AppImage\"");
        assert_eq!(exec_quoted(Path::new("/a/b\nExec=evil")), None);
    }

    #[test]
    fn view_online_path_is_read_from_the_command_line() {
        let argv = vec!["fylvo-desktop".to_string(), VIEW_ONLINE_ARG.to_string(), "/home/u/Fylvo/a b.txt".to_string()];
        assert_eq!(view_online_path(&argv), Some(PathBuf::from("/home/u/Fylvo/a b.txt")));
        assert_eq!(view_online_path(&["fylvo-desktop".to_string()]), None);
    }

    #[test]
    fn the_nautilus_script_passes_every_selected_path() {
        let dir = tempfile::tempdir().unwrap();
        let log = dir.path().join("log");
        let fake = dir.path().join("fake program");
        write_file(
            &fake,
            &format!("#!/bin/sh\necho \"$1|$2\" >> {}\n", shell_quoted(&log)),
            true,
        )
        .unwrap();
        let script = dir.path().join("script");
        write_file(&script, &nautilus_script(&fake), true).unwrap();
        let status = std::process::Command::new(&script)
            .env("NAUTILUS_SCRIPT_SELECTED_FILE_PATHS", "/a/one.txt\n/a/two words.txt")
            .status()
            .unwrap();
        assert!(status.success());
        assert_eq!(
            std::fs::read_to_string(&log).unwrap(),
            "--view-online|/a/one.txt\n--view-online|/a/two words.txt\n"
        );
    }
}

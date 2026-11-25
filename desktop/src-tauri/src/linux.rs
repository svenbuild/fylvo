//! Fylvo: what the app does on a Linux desktop that Windows does through its package (MSIX):
//! starting with the session and the "Open online" entry in the file managers.
//!
//! Both are files in the user's home folder that point at the program. An AppImage moves with
//! every update, so they are written again at every start with the path of the running program.

use std::path::{Path, PathBuf};

/// The command line options the file manager entries pass with the selected path.
pub const VIEW_ONLINE_ARG: &str = "--view-online";
/// Fylvo: keep a folder of a virtual drive on this computer / free its space again.
pub const PIN_ARG: &str = "--pin";
pub const UNPIN_ARG: &str = "--unpin";

/// One file manager entry: its label and the option it passes.
pub struct Entry {
    pub label: String,
    pub arg: &'static str,
}

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
fn nautilus_script(program: &Path, arg: &str) -> String {
    format!(
        "#!/bin/sh\n# Fylvo Desktop: pass the selected files to the app ({arg}).\nprintf '%s' \"$NAUTILUS_SCRIPT_SELECTED_FILE_PATHS\" | while IFS= read -r path || [ -n \"$path\" ]; do\n  [ -n \"$path\" ] && {} {} \"$path\"\ndone\n",
        shell_quoted(program),
        arg
    )
}

/// Dolphin (KDE) reads service menus from this folder.
fn dolphin_menu(program: &Path, entries: &[Entry]) -> Option<String> {
    let program = exec_quoted(program)?;
    let id = |arg: &str| format!("fylvo{}", arg.trim_start_matches('-').replace('-', ""));
    let ids: Vec<String> = entries.iter().map(|e| id(e.arg)).collect();
    let mut menu = format!(
        "[Desktop Entry]\nType=Service\nMimeType=all/all;inode/directory;\nActions={};\nX-KDE-Priority=TopLevel\n",
        ids.join(";")
    );
    for entry in entries {
        let label = entry.label.replace(['\n', '\r'], " ");
        menu.push_str(&format!(
            "\n[Desktop Action {}]\nName={label}\nIcon=fylvo-desktop\nExec={program} {} %f\n",
            id(entry.arg),
            entry.arg
        ));
    }
    Some(menu)
}

/// Write the file manager entries and refresh the autostart entry, with the path of the
/// running program. Failures are logged; the app works without them.
pub fn install_desktop_integration(entries: &[Entry]) {
    let Some(home) = home() else {
        return;
    };
    let program = launcher();
    let Some(dolphin) = dolphin_menu(&program, entries) else {
        tracing::warn!(target: "main", path = %program.display(), "Program path with a line break, no file manager entries");
        return;
    };
    let scripts = home.join(".local/share/nautilus/scripts");
    let labels: Vec<String> = entries.iter().map(|e| e.label.replace('/', "-")).collect();
    let mut files: Vec<(PathBuf, String, bool)> = entries
        .iter()
        .zip(&labels)
        .map(|(e, label)| (scripts.join(label), nautilus_script(&program, e.arg), true))
        .collect();
    files.push((home.join(".local/share/kio/servicemenus/fylvo-desktop.desktop"), dolphin, true));
    for (path, content, executable) in files {
        if let Err(e) = write_file(&path, &content, executable) {
            tracing::warn!(target: "main", path = %path.display(), error = %e, "Failed to install file manager entry");
        }
    }
    // Labels of an older language stay behind otherwise.
    remove_old_nautilus_scripts(&scripts, &labels);
    if autostart_enabled() {
        if let Err(e) = set_autostart(true) {
            tracing::warn!(target: "main", error = %e, "Failed to refresh the autostart entry");
        }
    }
}

fn remove_old_nautilus_scripts(folder: &Path, labels: &[String]) {
    let Ok(entries) = std::fs::read_dir(folder) else {
        return;
    };
    for entry in entries.flatten() {
        let ours = std::fs::read_to_string(entry.path())
            .is_ok_and(|text| text.starts_with("#!/bin/sh\n# Fylvo Desktop:"));
        let name = entry.file_name().to_string_lossy().to_string();
        if ours && !labels.contains(&name) {
            let _ = std::fs::remove_file(entry.path());
        }
    }
}

/// A file manager command in a command line: the option and the path after it.
pub fn file_command(argv: &[String]) -> Option<(&'static str, PathBuf)> {
    [VIEW_ONLINE_ARG, PIN_ARG, UNPIN_ARG].into_iter().find_map(|option| {
        let at = argv.iter().position(|arg| arg == option)?;
        argv.get(at + 1).map(|path| (option, PathBuf::from(path)))
    })
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
    fn file_commands_are_read_from_the_command_line() {
        let argv = vec!["fylvo-desktop".to_string(), VIEW_ONLINE_ARG.to_string(), "/home/u/Fylvo/a b.txt".to_string()];
        assert_eq!(file_command(&argv), Some((VIEW_ONLINE_ARG, PathBuf::from("/home/u/Fylvo/a b.txt"))));
        let argv = vec!["fylvo-desktop".to_string(), PIN_ARG.to_string(), "/home/u/Fylvo/Fotos".to_string()];
        assert_eq!(file_command(&argv), Some((PIN_ARG, PathBuf::from("/home/u/Fylvo/Fotos"))));
        assert_eq!(file_command(&["fylvo-desktop".to_string()]), None);
    }

    #[test]
    fn the_dolphin_menu_has_one_action_per_entry() {
        let entries = [
            Entry { label: "Fylvo: View online".into(), arg: VIEW_ONLINE_ARG },
            Entry { label: "Fylvo: Free up space".into(), arg: UNPIN_ARG },
        ];
        let menu = dolphin_menu(Path::new("/opt/fylvo"), &entries).unwrap();
        assert!(menu.contains("Actions=fylvoviewonline;fylvounpin;"), "{menu}");
        assert!(menu.contains("Exec=\"/opt/fylvo\" --unpin %f"), "{menu}");
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
        write_file(&script, &nautilus_script(&fake, VIEW_ONLINE_ARG), true).unwrap();
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

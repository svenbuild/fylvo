//! Programs that read every file they see: desktop search indexers and thumbnailers. Browsing
//! a folder must not make them load the whole server, so the drive refuses them content and
//! folders it has not loaded yet. Best effort, by program name.

use std::collections::HashMap;
use std::sync::Mutex;
use std::time::{Duration, Instant};

/// How long a process's answer is kept (process ids are reused, but not that fast).
const REMEMBER: Duration = Duration::from_secs(10);

/// Whether the process that made a FUSE request (`pid` is the thread id the kernel reports) is
/// an indexer or thumbnailer.
pub(crate) fn is_scanner(pid: u32) -> bool {
    static SEEN: Mutex<Option<HashMap<u32, (bool, Instant)>>> = Mutex::new(None);
    let tgid = thread_group(pid).unwrap_or(pid);
    if let Ok(mut seen) = SEEN.lock() {
        let seen = seen.get_or_insert_with(HashMap::new);
        if let Some((answer, at)) = seen.get(&tgid) {
            if at.elapsed() < REMEMBER {
                return *answer;
            }
        }
        let answer = classify_process(tgid);
        if seen.len() > 512 {
            seen.retain(|_, (_, at)| at.elapsed() < REMEMBER);
        }
        seen.insert(tgid, (answer, Instant::now()));
        answer
    } else {
        classify_process(tgid)
    }
}

/// The process a thread belongs to (`Tgid` in `/proc/<tid>/status`).
fn thread_group(tid: u32) -> Option<u32> {
    let status = std::fs::read_to_string(format!("/proc/{tid}/status")).ok()?;
    status
        .lines()
        .find_map(|line| line.strip_prefix("Tgid:"))
        .and_then(|value| value.trim().parse().ok())
}

fn classify_process(pid: u32) -> bool {
    let comm = std::fs::read_to_string(format!("/proc/{pid}/comm")).unwrap_or_default();
    let cmdline = std::fs::read(format!("/proc/{pid}/cmdline"))
        .map(|raw| String::from_utf8_lossy(&raw).replace('\0', " "))
        .unwrap_or_default();
    classify(comm.trim(), &cmdline)
}

/// `comm` is the program name the kernel keeps (cut to 15 characters).
pub(crate) fn classify(comm: &str, cmdline: &str) -> bool {
    const PREFIXES: &[&str] = &[
        "tracker-",
        "localsearch",
        "baloo_file",
        "gdk-pixbuf-thum",
        "totem-video-thu",
        "ffmpegthumbnail",
        "evince-thumbnai",
        "tumblerd",
    ];
    if PREFIXES.iter().any(|prefix| comm.starts_with(prefix)) || comm.contains("thum") {
        return true;
    }
    // KDE runs thumbnailers in a generic worker; only its command line names the job.
    (comm.starts_with("kioworker") || comm.starts_with("kioslave")) && cmdline.contains("thumbnail")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn indexers_and_thumbnailers_are_recognized() {
        for comm in [
            "tracker-extract",
            "tracker-miner-f",
            "localsearch-3",
            "localsearch-ext",
            "baloo_file",
            "baloo_file_extr",
            "gdk-pixbuf-thum",
            "totem-video-thu",
            "ffmpegthumbnail",
            "evince-thumbnai",
            "gnome-epub-thum",
            "tumblerd",
        ] {
            assert!(classify(comm, ""), "{comm}");
        }
        assert!(classify("kioworker", "/usr/lib/kf6/kioworker /usr/lib/qt6/plugins/kf6/kio/thumbnail.so thumbnail"));
        for comm in ["nautilus", "dolphin", "cp", "vlc", "libreoffice", "bash", "gnome-shell"] {
            assert!(!classify(comm, ""), "{comm}");
        }
        assert!(!classify("kioworker", "/usr/lib/kf6/kioworker /usr/lib/qt6/plugins/kf6/kio/file.so file"));
    }

    #[test]
    fn this_test_is_no_scanner() {
        assert!(!is_scanner(std::process::id()));
    }
}

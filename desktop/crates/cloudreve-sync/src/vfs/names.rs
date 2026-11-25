//! Names in a drive that stay on this computer, and names the drive refuses.

use globset::{Glob, GlobSet, GlobSetBuilder};
use std::path::Path;
use std::sync::OnceLock;

/// Temp files of editors: written next to the file while it is saved or open, never meant for
/// the server. Only names no normal file has (not `*~` or `*.part`, a real file of that name
/// must not stay here forever).
const LOCAL_ONLY: &[&str] = &[
    ".goutputstream-*",
    ".~lock.*#",
    ".*.sw[a-z]",
    "*.kate-swp",
    ".#*",
    "4913",
    "lu*.tmp",
];

fn local_only_set() -> &'static GlobSet {
    static SET: OnceLock<GlobSet> = OnceLock::new();
    SET.get_or_init(|| {
        let mut builder = GlobSetBuilder::new();
        for pattern in LOCAL_ONLY {
            builder.add(Glob::new(pattern).expect("valid pattern"));
        }
        builder.build().expect("valid patterns")
    })
}

/// Whether a file of this name is kept on this computer only (never uploaded).
pub(crate) fn is_local_only(name: &str) -> bool {
    local_only_set().is_match(name)
}

/// File managers create `.Trash-<uid>` at the top of a mount to trash files there. Refused, so
/// they offer to delete instead (which puts the file into the server's trash).
pub(crate) fn is_trash_name(name: &str) -> bool {
    name == ".Trash" || name.starts_with(".Trash-")
}

/// The name of a conflict copy of `name`, as the sync names them.
pub(crate) fn conflict_name(name: &str) -> String {
    crate::drive::sync::generate_conflict_path(Path::new(name))
        .file_name()
        .map(|n| n.to_string_lossy().to_string())
        .unwrap_or_else(|| format!("conflict_{name}"))
}

/// Whether `path` is `base` or below it (by whole components, "a/bc" is not below "a/b").
/// Both are relative to the drive root, "" is the root.
pub(crate) fn is_below(path: &str, base: &str) -> bool {
    base.is_empty()
        || path == base
        || (path.starts_with(base) && path.as_bytes().get(base.len()) == Some(&b'/'))
}

/// `path` with `old` (a prefix by whole components) replaced by `new`.
pub(crate) fn replace_prefix(path: &str, old: &str, new: &str) -> String {
    if path == old {
        new.to_string()
    } else if new.is_empty() {
        path[old.len() + 1..].to_string()
    } else if old.is_empty() {
        format!("{new}/{path}")
    } else {
        format!("{new}{}", &path[old.len()..])
    }
}

/// Join a relative folder path and a name.
pub(crate) fn join(parent: &str, name: &str) -> String {
    if parent.is_empty() {
        name.to_string()
    } else {
        format!("{parent}/{name}")
    }
}

/// The folder part of a relative path ("" for a name at the root).
pub(crate) fn parent_of(path: &str) -> &str {
    path.rfind('/').map(|at| &path[..at]).unwrap_or("")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn editor_temp_names_stay_local() {
        for name in [
            ".goutputstream-ABC123",
            ".~lock.report.odt#",
            ".notes.txt.swp",
            ".notes.txt.swx",
            "notes.txt.kate-swp",
            ".#notes.txt",
            "4913",
            "lu12345abc.tmp",
        ] {
            assert!(is_local_only(name), "{name}");
        }
        for name in ["notes.txt", "backup~", "film.part", "lunch.tmp.txt", "swap.sw", "49130"] {
            assert!(!is_local_only(name), "{name}");
        }
    }

    #[test]
    fn trash_folders_are_recognized() {
        assert!(is_trash_name(".Trash"));
        assert!(is_trash_name(".Trash-1000"));
        assert!(!is_trash_name("Trash"));
        assert!(!is_trash_name(".Trashcan2"));
    }

    #[test]
    fn conflict_names_keep_the_extension() {
        let name = conflict_name("report.odt");
        assert!(name.ends_with("_report.odt"), "{name}");
        assert_ne!(name, "report.odt");
    }

    #[test]
    fn paths_are_compared_by_components() {
        assert!(is_below("a/b/c", "a/b"));
        assert!(is_below("a/b", "a/b"));
        assert!(!is_below("a/bc", "a/b"));
        assert!(is_below("anything", ""));
        assert_eq!(replace_prefix("a/b/c", "a/b", "x"), "x/c");
        assert_eq!(replace_prefix("a/b", "a/b", "x/y"), "x/y");
        assert_eq!(replace_prefix("a/b", "a", ""), "b");
        assert_eq!(join("", "a"), "a");
        assert_eq!(join("a", "b"), "a/b");
        assert_eq!(parent_of("a/b/c"), "a/b");
        assert_eq!(parent_of("a"), "");
    }
}

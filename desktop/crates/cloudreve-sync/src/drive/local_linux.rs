//! Fylvo: the Linux side of the shared `LocalFileInfo`.
//!
//! On Linux every synced file is a normal local file. Whether it is tracked and in sync comes
//! from the inventory row at exactly this path: the row keeps the local state it was synced with
//! in metadata key `fylvo:local` (`<inode>:<size>:<mtime ns>`, folders `<inode>`; no device
//! number, btrfs subvolumes and disks can get other numbers after a reboot). A renamed or rewritten file does not match and counts as changed here,
//! the same as a file that is no placeholder on Windows. See `desktop/docs/linux-plan.md`.

use crate::drive::local::{LocalFileInfo, PinState, SyncState};
use crate::inventory::{FileMetadata, InventoryDb};
use anyhow::{Context, Result};
use std::os::unix::fs::MetadataExt;
use std::path::{Path, PathBuf};
use std::sync::{Arc, RwLock};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

/// Inventory metadata key with the local state a row was synced with.
pub const LOCAL_STATE_KEY: &str = "fylvo:local";

static INVENTORY: RwLock<Option<Arc<InventoryDb>>> = RwLock::new(None);

/// Set at start, before any drive is mounted (again by a new drive manager, as in tests).
pub fn set_inventory(inventory: Arc<InventoryDb>) {
    *INVENTORY.write().unwrap_or_else(|e| e.into_inner()) = Some(inventory);
}

/// The sync folders of the mounted drives (Linux has no system-wide registry of sync folders).
static SYNC_ROOTS: std::sync::RwLock<Vec<PathBuf>> = std::sync::RwLock::new(Vec::new());

pub fn register_sync_root(root: &Path) {
    let mut roots = SYNC_ROOTS.write().unwrap_or_else(|e| e.into_inner());
    if !roots.iter().any(|known| known == root) {
        roots.push(root.to_path_buf());
    }
}

pub fn unregister_sync_root(root: &Path) {
    let mut roots = SYNC_ROOTS.write().unwrap_or_else(|e| e.into_inner());
    roots.retain(|known| known != root);
}

/// The sync folder `path` lies in (the folder itself counts), if any.
pub fn sync_root_of(path: &Path) -> Option<PathBuf> {
    let roots = SYNC_ROOTS.read().unwrap_or_else(|e| e.into_inner());
    roots.iter().find(|root| path.starts_with(root)).cloned()
}

/// Fylvo: the file systems a sync folder may be on: only those whose inode numbers stay the
/// same (the local state in the inventory is keyed by them). FAT, exFAT, NTFS through FUSE and
/// network shares are refused.
pub fn check_file_system(folder: &Path) -> Result<()> {
    use std::ffi::CString;
    use std::os::unix::ffi::OsStrExt;
    const SUPPORTED: &[(i64, &str)] = &[
        (0xEF53, "ext4"),
        (0x9123683E, "btrfs"),
        (0x58465342, "xfs"),
        (0xF2F52010, "f2fs"),
    ];
    let path = CString::new(folder.as_os_str().as_bytes())?;
    let mut stat: libc::statfs = unsafe { std::mem::zeroed() };
    if unsafe { libc::statfs(path.as_ptr(), &mut stat) } != 0 {
        return Err(std::io::Error::last_os_error())
            .with_context(|| format!("cannot read the file system of {}", folder.display()));
    }
    let kind = stat.f_type as i64;
    if SUPPORTED.iter().any(|(magic, _)| *magic == kind) {
        return Ok(());
    }
    anyhow::bail!("{}", t!("linuxFileSystemNotSupported", "folder" => folder.display()))
}

/// Mount points listed by the kernel (also bind mounts on the same device), read at most every
/// few seconds (a walk asks for every folder).
fn mount_points() -> Vec<PathBuf> {
    static CACHE: std::sync::Mutex<Option<(std::time::Instant, Vec<PathBuf>)>> =
        std::sync::Mutex::new(None);
    let mut cache = CACHE.lock().unwrap_or_else(|e| e.into_inner());
    if let Some((at, points)) = cache.as_ref() {
        if at.elapsed() < Duration::from_secs(5) {
            return points.clone();
        }
    }
    let points = read_mount_points();
    *cache = Some((std::time::Instant::now(), points.clone()));
    points
}

fn read_mount_points() -> Vec<PathBuf> {
    let Ok(text) = std::fs::read_to_string("/proc/self/mountinfo") else {
        return Vec::new();
    };
    text.lines()
        .filter_map(|line| line.split(' ').nth(4))
        // Spaces and other special characters are written as octal escapes.
        .map(|field| PathBuf::from(field.replace("\\040", " ").replace("\\011", "\t")))
        .collect()
}

/// Fylvo: whether the sync must leave `path` alone: a symlink (never followed, so nothing
/// outside the sync folder is read or changed) or another file system mounted inside the
/// folder (its inode numbers are not comparable). Logged once per path.
pub fn is_foreign(path: &Path) -> bool {
    let Ok(meta) = std::fs::symlink_metadata(path) else {
        return false;
    };
    let foreign = if meta.file_type().is_symlink() {
        true
    } else if meta.is_dir() {
        let other_device = path
            .parent()
            .and_then(|parent| std::fs::symlink_metadata(parent).ok())
            .is_some_and(|parent| parent.dev() != meta.dev());
        other_device || mount_points().iter().any(|mount| mount == path)
    } else {
        false
    };
    if foreign {
        static LOGGED: std::sync::Mutex<Vec<PathBuf>> = std::sync::Mutex::new(Vec::new());
        let mut logged = LOGGED.lock().unwrap_or_else(|e| e.into_inner());
        if !logged.iter().any(|known| known == path) {
            tracing::warn!(target: "drive::local", path = %path.display(), "Symlink or mount point in the sync folder, not synced");
            logged.push(path.to_path_buf());
        }
    }
    foreign
}

/// Fylvo: whether `path` or a folder on the way to it (below `root`) is a symlink or another
/// file system. Paths from the server or from events are checked with this before anything is
/// read or written there, so the sync never reaches outside the sync folder.
pub fn is_foreign_below(path: &Path, root: &Path) -> bool {
    path.ancestors()
        .take_while(|ancestor| *ancestor != root && ancestor.starts_with(root))
        .any(is_foreign)
}

/// The local state of a file or folder as stored in `fylvo:local`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct LocalStamp {
    pub inode: u64,
    /// Size and mtime in nanoseconds; `None` for folders.
    pub content: Option<(u64, i128)>,
}

impl LocalStamp {
    /// The state of `path` itself (symlinks are not followed), or `None` if nothing is there or
    /// it is a symlink.
    pub fn of_path(path: &Path) -> Option<Self> {
        let meta = std::fs::symlink_metadata(path).ok()?;
        Self::of_metadata(&meta)
    }

    pub fn of_metadata(meta: &std::fs::Metadata) -> Option<Self> {
        if meta.file_type().is_symlink() {
            return None;
        }
        let content = (!meta.is_dir()).then(|| {
            let mtime_ns = meta.mtime() as i128 * 1_000_000_000 + meta.mtime_nsec() as i128;
            (meta.size(), mtime_ns)
        });
        Some(Self {
            inode: meta.ino(),
            content,
        })
    }

    pub fn encode(&self) -> String {
        match self.content {
            Some((size, mtime_ns)) => format!("{}:{}:{}", self.inode, size, mtime_ns),
            None => self.inode.to_string(),
        }
    }

    pub fn decode(text: &str) -> Option<Self> {
        let parts: Vec<&str> = text.split(':').collect();
        match parts.as_slice() {
            [inode] => Some(Self {
                inode: inode.parse().ok()?,
                content: None,
            }),
            [inode, size, mtime_ns] => Some(Self {
                inode: inode.parse().ok()?,
                content: Some((size.parse().ok()?, mtime_ns.parse().ok()?)),
            }),
            _ => None,
        }
    }

    /// The same file or folder (same inode).
    pub fn same_object(&self, other: &Self) -> bool {
        self.inode == other.inode
    }
}

/// The stored local state of an inventory row, if any.
pub fn stored_stamp(row: &FileMetadata) -> Option<LocalStamp> {
    row.metadata
        .get(LOCAL_STATE_KEY)
        .and_then(|text| LocalStamp::decode(text))
}

/// The sync state of a file or folder whose current state is `now`, given the row at its path.
pub fn sync_state(now: &LocalStamp, row: Option<&FileMetadata>) -> Option<SyncState> {
    let stored = row.and_then(stored_stamp)?;
    if !stored.same_object(now) {
        return None;
    }
    Some(SyncState {
        is_placeholder: true,
        in_sync: stored.content == now.content,
        partial_on_disk: false,
    })
}

fn system_time(mtime_ns: i128) -> Option<SystemTime> {
    if mtime_ns < 0 {
        return None;
    }
    let secs = (mtime_ns / 1_000_000_000) as u64;
    let nanos = (mtime_ns % 1_000_000_000) as u32;
    Some(UNIX_EPOCH + Duration::new(secs, nanos))
}

impl LocalFileInfo {
    /// Builds the state from what is on disk now and the given inventory row of this path.
    pub fn from_stamp(now: &LocalStamp, row: Option<&FileMetadata>) -> Self {
        Self {
            exists: true,
            is_directory: now.content.is_none(),
            file_size: now.content.map(|(size, _)| size),
            last_modified: now.content.and_then(|(_, mtime_ns)| system_time(mtime_ns)),
            sync_state: sync_state(now, row),
            // Everything is local: server changes are always downloaded.
            pin_state: PinState::Pinned,
        }
    }

    /// The state of `path`. A symlink counts as missing: the sync never follows or uploads one.
    pub fn from_path(path: &Path) -> Result<Self> {
        let Some(now) = LocalStamp::of_path(path) else {
            return Ok(Self::missing());
        };
        let inventory = INVENTORY.read().unwrap_or_else(|e| e.into_inner()).clone();
        let row = match (inventory, path.to_str()) {
            (Some(inventory), Some(text)) => inventory.query_by_path(text)?,
            _ => None,
        };
        Ok(Self::from_stamp(&now, row.as_ref()))
    }

    /// The path itself if something is there. Linux names are case sensitive, so another
    /// spelling never counts as the same file (the case logic plans every spelling on its own).
    pub fn on_disk_path(path: &Path) -> Option<PathBuf> {
        LocalStamp::of_path(path).map(|_| path.to_path_buf())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;

    fn row_with(stamp: Option<&LocalStamp>) -> FileMetadata {
        let mut metadata = HashMap::new();
        if let Some(stamp) = stamp {
            metadata.insert(LOCAL_STATE_KEY.to_string(), stamp.encode());
        }
        FileMetadata {
            id: 1,
            drive_id: uuid::Uuid::nil(),
            local_path: String::new(),
            is_folder: false,
            created_at: 0,
            updated_at: 0,
            size: 0,
            etag: "e".into(),
            metadata,
            props: None,
            permissions: String::new(),
            shared: false,
            conflict_state: None,
        }
    }

    #[test]
    fn stamp_round_trips() {
        let file = LocalStamp { inode: 77, content: Some((12, 1_700_000_000_123_456_789)) };
        let folder = LocalStamp { inode: 78, content: None };
        assert_eq!(LocalStamp::decode(&file.encode()), Some(file));
        assert_eq!(LocalStamp::decode(&folder.encode()), Some(folder));
        assert_eq!(LocalStamp::decode("garbage"), None);
    }

    #[test]
    fn a_synced_unchanged_file_is_a_placeholder_in_sync() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("a.txt");
        std::fs::write(&path, b"hello").unwrap();
        let stamp = LocalStamp::of_path(&path).unwrap();
        let info = LocalFileInfo::from_stamp(&stamp, Some(&row_with(Some(&stamp))));
        assert!(info.is_placeholder() && info.in_sync() && !info.partial_on_disk());
        assert_eq!(info.pinned(), PinState::Pinned);
    }

    #[test]
    fn an_edit_in_place_is_not_in_sync() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("a.txt");
        std::fs::write(&path, b"hello").unwrap();
        let synced = LocalStamp::of_path(&path).unwrap();
        std::fs::write(&path, b"hello world").unwrap();
        let now = LocalStamp::of_path(&path).unwrap();
        let info = LocalFileInfo::from_stamp(&now, Some(&row_with(Some(&synced))));
        assert!(info.is_placeholder() && !info.in_sync());
    }

    #[test]
    fn a_save_by_rename_is_not_a_placeholder() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("a.txt");
        std::fs::write(&path, b"hello").unwrap();
        let synced = LocalStamp::of_path(&path).unwrap();
        let temp = dir.path().join(".goutputstream-X");
        std::fs::write(&temp, b"hello").unwrap();
        std::fs::rename(&temp, &path).unwrap();
        let now = LocalStamp::of_path(&path).unwrap();
        let info = LocalFileInfo::from_stamp(&now, Some(&row_with(Some(&synced))));
        assert!(!info.is_placeholder() && !info.in_sync());
    }

    #[test]
    fn a_renamed_file_has_no_row_and_is_not_a_placeholder() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("b.txt");
        std::fs::write(&path, b"hello").unwrap();
        let now = LocalStamp::of_path(&path).unwrap();
        assert!(!LocalFileInfo::from_stamp(&now, None).is_placeholder());
        // A row without a local state (written from the server only) does not count either.
        assert!(!LocalFileInfo::from_stamp(&now, Some(&row_with(None))).is_placeholder());
    }

    #[test]
    fn a_folder_is_tracked_by_identity_only() {
        let dir = tempfile::tempdir().unwrap();
        let folder = dir.path().join("f");
        std::fs::create_dir(&folder).unwrap();
        let synced = LocalStamp::of_path(&folder).unwrap();
        std::fs::write(folder.join("child"), b"x").unwrap();
        let now = LocalStamp::of_path(&folder).unwrap();
        let info = LocalFileInfo::from_stamp(&now, Some(&row_with(Some(&synced))));
        assert!(info.is_directory && info.is_placeholder() && info.in_sync());
        assert!(info.is_folder_populated());
    }

    #[test]
    fn a_symlink_on_the_way_is_found() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().join("root");
        let outside = dir.path().join("outside");
        std::fs::create_dir_all(root.join("real")).unwrap();
        std::fs::create_dir(&outside).unwrap();
        std::os::unix::fs::symlink(&outside, root.join("link")).unwrap();
        assert!(is_foreign_below(&root.join("link/sub/a.txt"), &root));
        assert!(is_foreign_below(&root.join("link"), &root));
        assert!(!is_foreign_below(&root.join("real/sub/a.txt"), &root));
        assert!(!is_foreign_below(&root, &root));
    }

    #[test]
    fn a_symlink_counts_as_missing() {
        let dir = tempfile::tempdir().unwrap();
        let target = dir.path().join("target");
        std::fs::create_dir(&target).unwrap();
        let link = dir.path().join("link");
        std::os::unix::fs::symlink(&target, &link).unwrap();
        assert!(!LocalFileInfo::from_path(&link).unwrap().exists);
        assert_eq!(LocalStamp::of_path(&link), None);
    }
}

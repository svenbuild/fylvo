//! Fylvo: the end of a download on Linux, where every synced file is a normal local file.
//!
//! - A new file: the temp file gets the server's date, its state goes into the row, then it is
//!   renamed into place without replacing anything that appeared meanwhile (then the row as it
//!   was comes back and the path is planned again). A crash between row and rename leaves a row
//!   without file, which the next sync downloads again.
//! - An update: the old file is moved aside under an ignored backup name, the new one is renamed
//!   in, then the row is written. The backup is removed only if it still has the synced state
//!   and no process of the user can still write to it; otherwise it becomes a conflict copy.
//!
//! See `desktop/docs/linux-plan.md`.

use super::DownloadTask;
use crate::drive::local_linux::LocalStamp;
use crate::drive::placeholder::CrPlaceholder;
use crate::drive::utils::move_no_replace;
use crate::inventory::MetadataEntry;
use anyhow::{Context, Result};
use std::os::unix::fs::MetadataExt;
use std::path::{Path, PathBuf};
use std::str::FromStr;
use tracing::info;
use uuid::Uuid;

/// Prefix of the name an old file gets while its new version is put in place. Ignored by the
/// sync (`~*.tmp`); the original name follows, so a backup left by a crash can be put back.
pub(crate) const BACKUP_PREFIX: &str = "~fylvo-backup-";

impl DownloadTask<'_> {
    /// Linux: put the downloaded `temp` at the task's path (new file or update) and write the row.
    pub(super) fn place_download(&mut self, temp: &Path) -> Result<()> {
        let local = self.task.payload.local_path.clone();
        let remote = self
            .remote_file_info
            .clone()
            .expect("remote_file_info should be set");
        if let Ok(updated) = chrono::DateTime::parse_from_rfc3339(&remote.updated_at) {
            let time = std::time::SystemTime::from(updated);
            std::fs::File::options()
                .write(true)
                .open(temp)
                .and_then(|file| file.set_modified(time))
                .context("failed to set the date of the downloaded file")?;
        }
        let stamp = LocalStamp::of_path(temp).context("the downloaded file is gone")?;
        let drive_id = Uuid::from_str(self.drive_id).context("invalid drive ID")?;
        let placeholder = || {
            CrPlaceholder::new(local.clone(), self.sync_path.clone(), drive_id)
                .with_remote_file(&remote)
                .with_local_stamp(stamp)
        };
        let path_text = local.to_str().context("path is not UTF-8")?.to_string();

        if self.started.is_none() {
            // A new file: row first, then the file (see the module comment).
            let old_row = self.inventory.query_by_path(&path_text)?;
            placeholder().commit_downloaded(&self.inventory)?;
            if let Err(err) = move_no_replace(temp, &local) {
                match old_row {
                    Some(row) => {
                        self.inventory.upsert(&MetadataEntry::from(&row))?;
                    }
                    None => {
                        self.inventory.batch_delete_by_path(vec![path_text.as_str()])?;
                    }
                }
                info!(target: "tasks::download", local_path = %local.display(), error = ?err, "Something appeared at the path meanwhile, keeping it");
                self.kept_local_change = true;
            }
            return Ok(());
        }

        // An update: changed here since the start, the local change is kept.
        let synced = LocalStamp::of_path(&local);
        let in_sync = crate::drive::local::LocalFileInfo::from_path(&local)
            .is_ok_and(|info| info.in_sync());
        if !in_sync || synced != self.started_stamp {
            info!(target: "tasks::download", local_path = %local.display(), "File changed during the download, keeping the local change");
            self.kept_local_change = true;
            return Ok(());
        }
        let backup = backup_path(&local);
        move_no_replace(&local, &backup).context("failed to move the local file aside")?;
        if let Err(err) = move_no_replace(temp, &local) {
            if let Err(back) = move_no_replace(&backup, &local) {
                return Err(back.context("failed to put the local file back after a failed replace"));
            }
            return Err(err).context("failed to replace the local file");
        }
        placeholder()
            .commit(self.inventory.clone())
            .context("failed to commit the downloaded file")?;
        settle_backup(&backup, &local, self.started_stamp);
        Ok(())
    }
}

/// The backup name of `local`, in the same folder.
fn backup_path(local: &Path) -> PathBuf {
    let name = local
        .file_name()
        .map(|name| name.to_string_lossy().to_string())
        .unwrap_or_default();
    local.with_file_name(format!("{BACKUP_PREFIX}{name}.tmp"))
}

/// The original path of a backup name, if it is one.
pub(crate) fn original_of_backup(backup: &Path) -> Option<PathBuf> {
    let name = backup.file_name()?.to_str()?;
    let original = name.strip_prefix(BACKUP_PREFIX)?.strip_suffix(".tmp")?;
    (!original.is_empty()).then(|| backup.with_file_name(original))
}

/// Remove the backup if nothing can still change it, else keep it as a conflict copy.
fn settle_backup(backup: &Path, local: &Path, synced: Option<LocalStamp>) {
    let unchanged = || LocalStamp::of_path(backup) == synced && synced.is_some();
    if unchanged() && !may_be_written(backup) && unchanged() {
        if let Err(err) = std::fs::remove_file(backup) {
            tracing::warn!(target: "tasks::download", path = %backup.display(), error = ?err, "Failed to remove the backup");
        }
        return;
    }
    keep_as_conflict_copy(backup, local);
}

/// Give a backup a conflict name next to `local` (never under the ignored backup name, so it is
/// uploaded like any new file).
pub(crate) fn keep_as_conflict_copy(backup: &Path, local: &Path) {
    let copy = crate::drive::sync::generate_conflict_path(local);
    match move_no_replace(backup, &copy) {
        Ok(()) => info!(target: "tasks::download", path = %copy.display(), "Kept the replaced local file as a conflict copy"),
        Err(err) => tracing::warn!(target: "tasks::download", path = %backup.display(), error = ?err, "Failed to keep the backup as a conflict copy, it stays under its backup name"),
    }
}

/// Whether a process of this user may still write to `path`: a handle open for writing or a
/// shared writable mapping. Read-only handles (indexers, viewers) do not count. Unknown counts
/// as yes. Other users' processes cannot write to the user's files (root is out of scope).
pub(crate) fn may_be_written(path: &Path) -> bool {
    let Ok(target) = std::fs::symlink_metadata(path) else {
        return false;
    };
    let uid = unsafe { libc::getuid() };
    let Ok(procs) = std::fs::read_dir("/proc") else {
        return true;
    };
    for proc_dir in procs.flatten() {
        let pid_dir = proc_dir.path();
        let is_pid = pid_dir
            .file_name()
            .and_then(|name| name.to_str())
            .is_some_and(|name| name.bytes().all(|b| b.is_ascii_digit()));
        if !is_pid {
            continue;
        }
        match std::fs::metadata(&pid_dir) {
            Ok(meta) if meta.uid() == uid => {}
            _ => continue,
        }
        match process_may_write(&pid_dir, &target) {
            Some(false) => {}
            // Ended meanwhile: its handles are gone.
            None if !pid_dir.exists() => {}
            _ => return true,
        }
    }
    false
}

/// For one process: `Some(true)` if it may write to the file, `Some(false)` if not, `None` if
/// its entries could not be read.
fn process_may_write(pid_dir: &Path, target: &std::fs::Metadata) -> Option<bool> {
    for fd in std::fs::read_dir(pid_dir.join("fd")).ok()?.flatten() {
        // Following the link gives the open file itself, matched by device and inode.
        let Ok(open) = std::fs::metadata(fd.path()) else {
            continue;
        };
        if open.dev() != target.dev() || open.ino() != target.ino() {
            continue;
        }
        let info = std::fs::read_to_string(pid_dir.join("fdinfo").join(fd.file_name())).ok()?;
        let flags = info
            .lines()
            .find_map(|line| line.strip_prefix("flags:"))
            .and_then(|value| u32::from_str_radix(value.trim(), 8).ok())?;
        if flags & (libc::O_WRONLY as u32 | libc::O_RDWR as u32) != 0 {
            return Some(true);
        }
    }
    let maps = std::fs::read_to_string(pid_dir.join("maps")).ok()?;
    for line in maps.lines() {
        let fields: Vec<&str> = line.split_whitespace().collect();
        if let [_, perms, _, _, inode, ..] = fields.as_slice() {
            let shared_writable = perms.contains('w') && perms.contains('s');
            if shared_writable && inode.parse::<u64>().ok() == Some(target.ino()) {
                return Some(true);
            }
        }
    }
    Some(false)
}

/// At start: leftovers of a crash in `folder` and below. Unfinished downloads are removed; a
/// backup goes back to its name if that is free, else it becomes a conflict copy (never deleted).
pub(crate) fn clean_leftovers(folder: &Path) {
    let Ok(entries) = std::fs::read_dir(folder) else {
        return;
    };
    for entry in entries.flatten() {
        let path = entry.path();
        let Ok(meta) = std::fs::symlink_metadata(&path) else {
            continue;
        };
        if meta.is_dir() {
            if !crate::drive::local_linux::is_foreign(&path) {
                clean_leftovers(&path);
            }
            continue;
        }
        let name = entry.file_name().to_string_lossy().to_string();
        if name.starts_with("~fylvo-download-") && name.ends_with(".tmp") {
            let _ = std::fs::remove_file(&path);
        } else if let Some(original) = original_of_backup(&path) {
            if move_no_replace(&path, &original).is_err() {
                keep_as_conflict_copy(&path, &original);
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn backup_names_lead_back_to_the_file() {
        let local = Path::new("/S/Docs/Book.xlsx");
        let backup = backup_path(local);
        assert_eq!(backup, Path::new("/S/Docs/~fylvo-backup-Book.xlsx.tmp"));
        assert_eq!(original_of_backup(&backup).as_deref(), Some(local));
        assert_eq!(original_of_backup(Path::new("/S/x.tmp")), None);
    }

    #[test]
    fn a_file_open_for_writing_may_be_written_a_reader_does_not_count() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("a.txt");
        std::fs::write(&path, b"x").unwrap();
        assert!(!may_be_written(&path));
        let reader = std::fs::File::open(&path).unwrap();
        assert!(!may_be_written(&path));
        let writer = std::fs::OpenOptions::new().append(true).open(&path).unwrap();
        assert!(may_be_written(&path));
        drop(writer);
        drop(reader);
        assert!(!may_be_written(&path));
    }

    #[test]
    fn leftovers_are_put_back_or_kept_never_deleted() {
        let dir = tempfile::tempdir().unwrap();
        let sub = dir.path().join("sub");
        std::fs::create_dir(&sub).unwrap();
        std::fs::write(sub.join("~fylvo-download-t1.tmp"), b"half").unwrap();
        std::fs::write(sub.join("~fylvo-backup-free.txt.tmp"), b"old").unwrap();
        std::fs::write(sub.join("~fylvo-backup-taken.txt.tmp"), b"old").unwrap();
        std::fs::write(sub.join("taken.txt"), b"new").unwrap();
        clean_leftovers(dir.path());
        let mut names: Vec<String> = std::fs::read_dir(&sub)
            .unwrap()
            .map(|e| e.unwrap().file_name().to_string_lossy().to_string())
            .collect();
        names.sort();
        assert_eq!(names.len(), 3, "{names:?}");
        assert!(names.contains(&"free.txt".to_string()));
        assert!(names.contains(&"taken.txt".to_string()));
        assert!(names.iter().any(|n| n.starts_with("__conflict__") && n.ends_with("_taken.txt")));
        assert_eq!(std::fs::read(sub.join("taken.txt")).unwrap(), b"new");
    }
}

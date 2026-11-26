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
        if crate::drive::local_linux::is_foreign_below(&local, &self.sync_path) {
            anyhow::bail!("the folder is behind a symlink or mount point now");
        }
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
            use std::os::unix::fs::PermissionsExt;
            let umask = std::fs::read_to_string("/proc/self/status")
                .ok()
                .and_then(|status| {
                    status
                        .lines()
                        .find_map(|line| line.strip_prefix("Umask:"))
                        .and_then(|value| u32::from_str_radix(value.trim(), 8).ok())
                })
                .unwrap_or(0o022);
            std::fs::set_permissions(temp, std::fs::Permissions::from_mode(0o666 & !umask))
                .context("failed to set the access rights")?;
            // A new file: row first, then the file (see the module comment).
            let old_row = self.inventory.query_by_path(&path_text)?;
            placeholder().commit_downloaded(&self.inventory)?;
            own_move(temp, &local);
            if let Err(err) = move_no_replace(temp, &local) {
                forget(temp, &local);
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
        // The new version keeps the access rights of the file it replaces.
        let rights = std::fs::symlink_metadata(&local)
            .context("the local file is gone")?
            .permissions();
        std::fs::set_permissions(temp, rights).context("failed to keep the access rights")?;
        let backup = backup_path(&local);
        own_move(&local, &backup);
        if let Err(err) = move_no_replace(&local, &backup) {
            forget(&local, &backup);
            return Err(err).context("failed to move the local file aside");
        }
        own_move(temp, &local);
        if let Err(err) = move_no_replace(temp, &local) {
            forget(temp, &local);
            own_move(&backup, &local);
            if let Err(back) = move_no_replace(&backup, &local) {
                forget(&backup, &local);
                return Err(back.context("failed to put the local file back after a failed replace"));
            }
            return Err(err).context("failed to replace the local file");
        }
        placeholder()
            .commit(self.inventory.clone())
            .context("failed to commit the downloaded file")?;
        self.conflict_copy = settle_backup(&backup, &local, self.started_stamp);
        Ok(())
    }
}

/// Longest file name a backup may carry the original name in (Linux allows 255 bytes; the
/// prefix and suffix need 18 more).
const MAX_NAME_IN_BACKUP: usize = 230;

fn own_move(from: &Path, to: &Path) {
    crate::drive::watch_linux::register_own_move(from, to);
}

fn forget(from: &Path, to: &Path) {
    crate::drive::watch_linux::forget_own_move(from, to);
}

/// The backup name of `local`, in the same folder. A very long name does not fit: then the
/// backup gets a short unique name and, if a crash leaves it, it is kept as a conflict copy.
fn backup_path(local: &Path) -> PathBuf {
    let name = local
        .file_name()
        .map(|name| name.to_string_lossy().to_string())
        .unwrap_or_default();
    if name.len() > MAX_NAME_IN_BACKUP {
        let unique = uuid::Uuid::new_v4().simple().to_string();
        return local.with_file_name(format!("{BACKUP_PREFIX}#{unique}.tmp"));
    }
    local.with_file_name(format!("{BACKUP_PREFIX}{name}.tmp"))
}

impl DownloadTask<'_> {
    /// Whether the server still has the file this download fetched (same file ID, same
    /// version). A file deleted or renamed on the server while it downloaded is not put here.
    pub(super) async fn still_on_server(&self, uri: &str) -> bool {
        use cloudreve_api::api::ExplorerApi;
        let Some(fetched) = self.remote_file_info.as_ref() else {
            return false;
        };
        match self
            .cr_client
            .get_file_info(&cloudreve_api::models::explorer::GetFileInfoService {
                uri: Some(uri.to_string()),
                id: None,
                extended: None,
                folder_summary: None,
            })
            .await
        {
            Ok(now) => now.id == fetched.id && now.primary_entity == fetched.primary_entity,
            Err(_) => false,
        }
    }
}

/// The original path of a backup name, if it is one.
pub(crate) fn original_of_backup(backup: &Path) -> Option<PathBuf> {
    let name = backup.file_name()?.to_str()?;
    let original = name.strip_prefix(BACKUP_PREFIX)?.strip_suffix(".tmp")?;
    // `#…`: the original name was too long to keep (see `backup_path`).
    (!original.is_empty() && !original.starts_with('#')).then(|| backup.with_file_name(original))
}

/// Remove the backup if nothing can still change it, else keep it as a conflict copy.
/// Remove the backup if nothing can still change it, else keep it as a conflict copy (returned,
/// its upload is asked for by the caller).
fn settle_backup(backup: &Path, local: &Path, synced: Option<LocalStamp>) -> Option<PathBuf> {
    let unchanged = || LocalStamp::of_path(backup) == synced && synced.is_some();
    if unchanged() && !may_be_written(backup) && unchanged() {
        if let Err(err) = std::fs::remove_file(backup) {
            tracing::warn!(target: "tasks::download", path = %backup.display(), error = ?err, "Failed to remove the backup");
        }
        return None;
    }
    keep_as_conflict_copy(backup, local)
}

/// Give a backup a conflict name next to `local` (never under the ignored backup name, so it is
/// uploaded like any new file). An own move: the watcher may merge it with the move to the
/// backup name, so the upload is asked for explicitly (or by the first sync at start).
pub(crate) fn keep_as_conflict_copy(backup: &Path, local: &Path) -> Option<PathBuf> {
    let mut copy = crate::drive::sync::generate_conflict_path(local);
    if copy.file_name().is_some_and(|name| name.len() > 255) {
        // Too long for Linux (255 bytes): a short conflict name with the extension.
        let ext = local
            .extension()
            .map(|e| format!(".{}", e.to_string_lossy()))
            .filter(|e| e.len() < 32)
            .unwrap_or_default();
        let short = uuid::Uuid::new_v4().simple().to_string();
        copy = local.with_file_name(format!("__conflict__{}{ext}", &short[..12]));
    }
    own_move(backup, &copy);
    match move_no_replace(backup, &copy) {
        Ok(()) => {
            info!(target: "tasks::download", path = %copy.display(), "Kept the replaced local file as a conflict copy");
            Some(copy)
        }
        Err(err) => {
            forget(backup, &copy);
            tracing::warn!(target: "tasks::download", path = %backup.display(), error = ?err, "Failed to keep the backup as a conflict copy, it stays under its backup name");
            None
        }
    }
}

/// Whether a process of this user may still write to `path`: a handle open for writing or a
/// shared writable mapping. Read-only handles (indexers, viewers) do not count. Unknown counts
/// as yes, except entries the kernel hides from us (permission denied): those belong to
/// processes that are not dumpable or hold capabilities (`systemd --user`, `ssh-agent`, the
/// compositor), never to an editor, and every real desktop has some. Other users' processes
/// cannot write to the user's files (root is out of scope).
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
    let hidden = |e: &std::io::Error| e.kind() == std::io::ErrorKind::PermissionDenied;
    let fds = match std::fs::read_dir(pid_dir.join("fd")) {
        Ok(fds) => fds,
        Err(e) if hidden(&e) => return Some(false),
        Err(_) => return None,
    };
    for fd in fds.flatten() {
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
    let maps = match std::fs::read_to_string(pid_dir.join("maps")) {
        Ok(maps) => maps,
        Err(e) if hidden(&e) => return Some(false),
        Err(_) => return None,
    };
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
    // Only a real folder: a sync folder replaced by a symlink while the app was off is not
    // followed (the walk below never follows links either).
    if !std::fs::symlink_metadata(folder).is_ok_and(|m| m.is_dir()) {
        return;
    }
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
            // An unfinished download; the name alone does not prove it, so to the trash, not
            // deleted for good.
            let _ = crate::drive::utils::move_to_recycle_bin(&path);
        } else if name.starts_with(BACKUP_PREFIX) && name.ends_with(".tmp") {
            match original_of_backup(&path) {
                Some(original) => {
                    if move_no_replace(&path, &original).is_err() {
                        let _ = keep_as_conflict_copy(&path, &original);
                    }
                }
                None => {
                    let _ = keep_as_conflict_copy(&path, &path);
                }
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
        let long = format!("/S/{}.txt", "n".repeat(240));
        let backup = backup_path(Path::new(&long));
        assert!(backup.file_name().unwrap().len() < 100);
        assert_eq!(original_of_backup(&backup), None);
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
        // SAFETY: tests of this module touch no other environment variable.
        unsafe { std::env::set_var("XDG_DATA_HOME", dir.path().join("data")) };
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

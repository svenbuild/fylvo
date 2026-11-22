//! Fylvo: `CrPlaceholder` on Linux. Synced files and folders are normal ones; what Windows
//! keeps in the placeholder (tracked, in sync) is the `fylvo:local` key of the inventory row.
//!
//! Write rules for the key (`desktop/docs/linux-plan.md`):
//! 1. A new key comes only from a state known to equal the server's content
//!    (`with_local_stamp`: download temp file, upload start) or from a folder on disk.
//! 2. Otherwise the key of the existing row is carried over only if the file still matches it
//!    and the server's content is unchanged (same etag). Else the row is written without key,
//!    so the file counts as changed here.
//! 3. `commit_inventory_only` never writes a key.

use super::CrPlaceholder;
use crate::drive::local_linux::{LOCAL_STATE_KEY, LocalStamp, stored_stamp};
use crate::drive::utils::move_to_recycle_bin;
use crate::inventory::{FileMetadata, InventoryDb, MetadataEntry};
use anyhow::{Context, Result};
use std::sync::Arc;

impl CrPlaceholder {
    /// The local state that is known to hold the server's content of this row.
    pub fn with_local_stamp(mut self, stamp: LocalStamp) -> Self {
        self.local_stamp = Some(stamp);
        self
    }

    /// The key to store with the new row, by the write rules above.
    fn key_for(&self, new_row: &FileMetadata, old_row: Option<&FileMetadata>) -> Option<LocalStamp> {
        if let Some(stamp) = self.local_stamp {
            return Some(stamp);
        }
        let now = LocalStamp::of_path(&self.local_path)?;
        if new_row.is_folder {
            // A folder has no content of its own: the folder at this path is the synced one.
            return now.content.is_none().then_some(now);
        }
        let old_row = old_row?;
        let stored = stored_stamp(old_row)?;
        (stored == now && old_row.etag == new_row.etag).then_some(now)
    }

    /// Store the row of this path. A missing folder is created; a missing file is never
    /// created here: on Linux files come only from a finished download.
    pub fn commit(&mut self, inventory: Arc<InventoryDb>) -> Result<()> {
        let mut file_meta = self
            .file_meta
            .clone()
            .context("File metadata is not set")?;
        let path_text = self
            .local_path
            .to_str()
            .context("failed to convert path to string")?;

        if !self.local_file_info.exists {
            if !file_meta.is_folder {
                anyhow::bail!("on Linux a file is created by its download, not as a placeholder");
            }
            std::fs::create_dir(&self.local_path).context("failed to create local folder")?;
        }

        let old_row = inventory
            .query_by_path(path_text)
            .context("failed to read inventory row")?;
        file_meta.metadata.remove(LOCAL_STATE_KEY);
        if let Some(stamp) = self.key_for(&file_meta, old_row.as_ref()) {
            file_meta
                .metadata
                .insert(LOCAL_STATE_KEY.to_string(), stamp.encode());
        }
        inventory
            .upsert(&MetadataEntry::from(&file_meta))
            .context("failed to upsert inventory")?;
        Ok(())
    }

    /// Store the row of a downloaded file that is not in place yet (the download renames it in
    /// right after). The key is the state of the downloaded file (`with_local_stamp`).
    pub fn commit_downloaded(&self, inventory: &InventoryDb) -> Result<()> {
        let mut file_meta = self
            .file_meta
            .clone()
            .context("File metadata is not set")?;
        let stamp = self
            .local_stamp
            .context("the downloaded file's state is not set")?;
        file_meta
            .metadata
            .insert(LOCAL_STATE_KEY.to_string(), stamp.encode());
        inventory
            .upsert(&MetadataEntry::from(&file_meta))
            .context("failed to upsert inventory")?;
        Ok(())
    }

    /// Remove the local file or folder and its inventory entry. Nothing is deleted for good: a
    /// file goes to the freedesktop trash, a folder is removed only when it is empty. The row
    /// goes first (a file restored from the trash is then a new file) and comes back if the
    /// file could not be moved.
    pub fn delete_placeholder(&self, inventory: Arc<InventoryDb>) -> Result<()> {
        let path_text = self
            .local_path
            .to_str()
            .context("failed to convert path to string")?;
        let old_row = inventory
            .query_by_path(path_text)
            .context("failed to read inventory row")?;
        inventory
            .batch_delete_by_path(vec![path_text])
            .context("failed to delete from inventory")?;

        let info = &self.local_file_info;
        if !info.exists {
            return Ok(());
        }
        // Changed here since the sync planned the deletion: kept, without its row, so it counts
        // as a new local file (uploaded again) instead of going to the trash.
        if !info.is_directory && !(info.is_placeholder() && info.in_sync()) {
            tracing::info!(target: "drive::placeholder", path = %self.local_path.display(), "Changed here, kept instead of moving it to the trash");
            return Ok(());
        }
        let removed = if info.is_directory {
            std::fs::remove_dir(&self.local_path).context("local folder is not empty, it was kept")
        } else {
            move_to_recycle_bin(&self.local_path)
        };
        if let Err(err) = removed {
            if let Some(row) = old_row {
                if let Err(e) = inventory.upsert(&MetadataEntry::from(&row)) {
                    tracing::error!(target: "drive::placeholder", path = %self.local_path.display(), error = %e, "Failed to restore inventory row");
                }
            }
            return Err(err);
        }
        Ok(())
    }

    /// Windows shows a sync error on the file in Explorer; Linux has no such marker.
    pub fn update_sync_error_state(&self, _set_error: bool) -> Result<()> {
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::drive::local::LocalFileInfo;
    use std::collections::HashMap;

    fn row(path: &std::path::Path, etag: &str, is_folder: bool) -> FileMetadata {
        FileMetadata {
            id: 0,
            drive_id: uuid::Uuid::nil(),
            local_path: path.to_string_lossy().to_string(),
            is_folder,
            created_at: 0,
            updated_at: 0,
            size: 0,
            etag: etag.into(),
            metadata: HashMap::new(),
            props: None,
            permissions: String::new(),
            shared: false,
            conflict_state: None,
        }
    }

    fn placeholder(path: &std::path::Path, meta: FileMetadata) -> CrPlaceholder {
        CrPlaceholder::new(path, path.parent().unwrap().to_path_buf(), uuid::Uuid::nil())
            .with_file_meta(meta)
    }

    #[test]
    fn key_rules() {
        let dir = tempfile::tempdir().unwrap();
        let file = dir.path().join("a.txt");
        std::fs::write(&file, b"hello").unwrap();
        let now = LocalStamp::of_path(&file).unwrap();

        // A known state wins.
        let p = placeholder(&file, row(&file, "e1", false)).with_local_stamp(now);
        assert_eq!(p.key_for(&row(&file, "e1", false), None), Some(now));

        // Without one, no row before: no key.
        let p = placeholder(&file, row(&file, "e1", false));
        assert_eq!(p.key_for(&row(&file, "e1", false), None), None);

        // Carried over only for an unchanged file and the same server content.
        let mut old = row(&file, "e1", false);
        old.metadata.insert(LOCAL_STATE_KEY.into(), now.encode());
        assert_eq!(p.key_for(&row(&file, "e1", false), Some(&old)), Some(now));
        assert_eq!(p.key_for(&row(&file, "e2", false), Some(&old)), None);
        std::fs::write(&file, b"hello, changed").unwrap();
        let p = placeholder(&file, row(&file, "e1", false));
        assert_eq!(p.key_for(&row(&file, "e1", false), Some(&old)), None);

        // A folder on disk is tracked by itself.
        let folder = dir.path().join("f");
        std::fs::create_dir(&folder).unwrap();
        let p = placeholder(&folder, row(&folder, "", true));
        assert_eq!(
            p.key_for(&row(&folder, "", true), None),
            LocalStamp::of_path(&folder)
        );
    }

    #[test]
    fn a_missing_file_is_never_created_and_a_missing_folder_is() {
        let dir = tempfile::tempdir().unwrap();
        let inventory = Arc::new(InventoryDb::with_path(dir.path().join("meta.db")).unwrap());

        let file = dir.path().join("new.txt");
        let mut p = placeholder(&file, row(&file, "e1", false));
        assert!(p.commit(inventory.clone()).is_err());
        assert!(!file.exists());

        let folder = dir.path().join("new folder");
        let mut p = placeholder(&folder, row(&folder, "", true));
        p.commit(inventory.clone()).unwrap();
        assert!(folder.is_dir());
        let stored = inventory.query_by_path(folder.to_str().unwrap()).unwrap().unwrap();
        let info = LocalFileInfo::from_stamp(&LocalStamp::of_path(&folder).unwrap(), Some(&stored));
        assert!(info.is_placeholder() && info.in_sync());
    }
}

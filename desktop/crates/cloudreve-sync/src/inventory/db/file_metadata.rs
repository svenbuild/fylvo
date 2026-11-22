use super::InventoryDb;
use crate::inventory::{
    ConflictState, FileMetadata, MetadataEntry,
};
use anyhow::{Context, Result};
use diesel::prelude::*;
use diesel::sql_types::Text;
use std::collections::HashMap;
use uuid::Uuid;

use crate::inventory::schema::file_metadata::{self, dsl as file_metadata_dsl};

impl InventoryDb {
    pub fn batch_insert(&self, entries: &[MetadataEntry]) -> Result<()> {
        if entries.is_empty() {
            return Ok(());
        }

        let rows: Vec<NewFileMetadata> = entries
            .iter()
            .map(NewFileMetadata::try_from)
            .collect::<Result<_>>()?;

        let mut conn = self.connection()?;
        diesel::insert_into(file_metadata::table)
            .values(&rows)
            .execute(&mut conn)
            .context("Failed to batch insert inventory metadata")?;
        Ok(())
    }

    pub fn nuke_drive(&self, drive: &str) -> Result<()> {
        let mut conn = self.connection()?;
        diesel::delete(
            file_metadata_dsl::file_metadata.filter(file_metadata_dsl::drive_id.eq(drive)),
        )
        .execute(&mut conn)
        .context("Failed to delete inventory rows for drive")?;
        Ok(())
    }

    /// Insert a new file metadata entry
    pub fn insert(&self, entry: &MetadataEntry) -> Result<usize> {
        let mut conn = self.connection()?;
        let new_entry = NewFileMetadata::try_from(entry)?;
        diesel::insert_into(file_metadata::table)
            .values(&new_entry)
            .execute(&mut conn)
            .context("Failed to insert inventory metadata")
    }

    /// Update an existing file metadata entry by local path
    pub fn update(&self, entry: &MetadataEntry) -> Result<bool> {
        let mut conn = self.connection()?;
        let changeset = FileMetadataChangeset::from_entry(entry)?;
        let rows_affected = diesel::update(
            file_metadata_dsl::file_metadata
                .filter(file_metadata_dsl::local_path.eq(&entry.local_path)),
        )
        .set(changeset)
        .execute(&mut conn)
        .context("Failed to update inventory metadata")?;
        Ok(rows_affected > 0)
    }

    /// Insert or update a file metadata entry (upsert based on local_path)
    pub fn upsert(&self, entry: &MetadataEntry) -> Result<usize> {
        let mut conn = self.connection()?;
        let insert_data = NewFileMetadata::try_from(entry)?;
        let update_data = FileMetadataChangeset::from_entry(entry)?;

        diesel::insert_into(file_metadata::table)
            .values(&insert_data)
            .on_conflict(file_metadata::local_path)
            .do_update()
            .set(update_data)
            .execute(&mut conn)
            .context("Failed to upsert inventory metadata")
    }

    /// Query file metadata by local path
    pub fn query_by_path(&self, path: &str) -> Result<Option<FileMetadata>> {
        let mut conn = self.connection()?;
        let row = file_metadata_dsl::file_metadata
            .filter(file_metadata_dsl::local_path.eq(path))
            .first::<FileMetadataRow>(&mut conn)
            .optional()
            .context("Failed to query inventory metadata by path")?;

        row.map(FileMetadata::try_from).transpose()
    }

    /// Query file metadata by id
    pub fn query_by_id(&self, id: i64) -> Result<Option<FileMetadata>> {
        let mut conn = self.connection()?;
        let row = file_metadata_dsl::file_metadata
            .filter(file_metadata_dsl::id.eq(id))
            .first::<FileMetadataRow>(&mut conn)
            .optional()
            .context("Failed to query inventory metadata by id")?;

        row.map(FileMetadata::try_from).transpose()
    }

    /// Batch delete file metadata by local path
    pub fn batch_delete_by_path(&self, paths: Vec<&str>) -> Result<bool> {
        if paths.is_empty() {
            return Ok(false);
        }

        let affected = {
            let mut conn = self.connection()?;
            (&mut *conn)
                .transaction::<i64, diesel::result::Error, _>(|tx_conn| {
                    let mut total: i64 = 0;
                    for path in &paths {
                        // Fylvo: the path and everything under it. A plain prefix compare, so `_`
                        // and `%` in names are no wildcards, with the Windows separator.
                        let prefix = descendant_prefix(path);
                        total += diesel::sql_query(
                            "DELETE FROM file_metadata WHERE local_path = ? \
                             OR substr(local_path, 1, length(?)) = ?",
                        )
                        .bind::<Text, _>(*path)
                        .bind::<Text, _>(&prefix)
                        .bind::<Text, _>(&prefix)
                        .execute(tx_conn)? as i64;
                    }
                    Ok(total)
                })
                .context("Failed to batch delete inventory metadata")?
        }; // conn is dropped here, releasing it back to the pool

        // Delete upload sessions - now safe to acquire a new connection
        self.batch_delete_upload_session_by_path(&paths)?;
        Ok(affected > 0)
    }

    /// Get total count of entries in the database
    pub fn count(&self) -> Result<i64> {
        let mut conn = self.connection()?;
        file_metadata_dsl::file_metadata
            .count()
            .get_result(&mut conn)
            .context("Failed to count inventory metadata")
    }

    /// Fylvo: number of entries of one drive.
    pub fn count_for_drive(&self, drive_id: &str) -> Result<i64> {
        let mut conn = self.connection()?;
        file_metadata_dsl::file_metadata
            .filter(file_metadata_dsl::drive_id.eq(drive_id))
            .count()
            .get_result(&mut conn)
            .context("Failed to count inventory metadata of drive")
    }

    /// Fylvo: number of distinct entries of one drive at or under any of the paths. A path
    /// without an entry counts as one, so a deletion is never counted as zero.
    pub fn count_under_paths(&self, drive_id: &str, paths: &[&str]) -> Result<i64> {
        #[derive(QueryableByName)]
        struct CountRow {
            #[diesel(sql_type = diesel::sql_types::BigInt)]
            n: i64,
        }

        let mut conn = self.connection()?;
        let mut total = 0;
        for path in outermost_paths(paths) {
            let prefix = descendant_prefix(path);
            // Fylvo: Windows ignores case in paths, Linux does not.
            let query = if cfg!(windows) {
                "SELECT COUNT(*) AS n FROM file_metadata WHERE drive_id = ? \
                 AND (local_path = ? COLLATE NOCASE \
                 OR substr(local_path, 1, length(?)) = ? COLLATE NOCASE)"
            } else {
                "SELECT COUNT(*) AS n FROM file_metadata WHERE drive_id = ? \
                 AND (local_path = ? OR substr(local_path, 1, length(?)) = ?)"
            };
            let row: CountRow = diesel::sql_query(query)
            .bind::<Text, _>(drive_id)
            .bind::<Text, _>(path)
            .bind::<Text, _>(&prefix)
            .bind::<Text, _>(&prefix)
            .get_result(&mut conn)
            .context("Failed to count inventory metadata under path")?;
            total += row.n.max(1);
        }
        Ok(total)
    }

    /// Fylvo: whether any entry is stored at `path` or under it, spelled exactly like that.
    pub fn has_rows_at_or_under(&self, path: &str) -> Result<bool> {
        #[derive(QueryableByName)]
        struct CountRow {
            #[diesel(sql_type = diesel::sql_types::BigInt)]
            n: i64,
        }

        let mut conn = self.connection()?;
        let prefix = descendant_prefix(path);
        let row: CountRow = diesel::sql_query(
            "SELECT COUNT(*) AS n FROM file_metadata WHERE local_path = ? \
             OR substr(local_path, 1, length(?)) = ?",
        )
        .bind::<Text, _>(path)
        .bind::<Text, _>(&prefix)
        .bind::<Text, _>(&prefix)
        .get_result(&mut conn)
        .context("Failed to count inventory metadata under path")?;
        Ok(row.n > 0)
    }

    /// Clear all entries from the database
    pub fn clear(&self) -> Result<()> {
        let mut conn = self.connection()?;
        diesel::delete(file_metadata::table)
            .execute(&mut conn)
            .context("Failed to clear inventory metadata")?;
        Ok(())
    }

    /// Rename or move a file/folder and update all its descendants.
    /// Uses two UPDATE queries: one for the exact path, one for descendants.
    /// Only replaces the prefix portion to avoid issues with duplicate path segments.
    ///
    /// Returns the number of rows updated.
    pub fn rename_path(&self, old_path: &str, new_path: &str) -> Result<usize> {
        if old_path == new_path {
            return Ok(0);
        }

        let mut conn = self.connection()?;
        let old_prefix = descendant_prefix(old_path);
        let new_prefix = descendant_prefix(new_path);

        let total = (&mut *conn)
            .transaction::<usize, diesel::result::Error, _>(|tx_conn| {
                let exact = diesel::update(
                    file_metadata_dsl::file_metadata
                        .filter(file_metadata_dsl::local_path.eq(old_path)),
                )
                .set((file_metadata_dsl::local_path.eq(new_path),))
                .execute(tx_conn)?;

                let descendants = diesel::sql_query(
                    "UPDATE file_metadata \
                     SET local_path = ? || substr(local_path, length(?) + 1) \
                     WHERE substr(local_path, 1, length(?)) = ?",
                )
                .bind::<Text, _>(&new_prefix)
                .bind::<Text, _>(&old_prefix)
                .bind::<Text, _>(&old_prefix)
                .bind::<Text, _>(&old_prefix)
                .execute(tx_conn)?;

                Ok(exact + descendants)
            })
            .context("Failed to rename metadata path")?;

        Ok(total)
    }

    /// Mark a file as conflicted by setting its conflict_state.
    /// Pass `None` to clear the conflict state.
    ///
    /// Returns true if a row was updated.
    pub fn mark_as_conflicted(&self, path: &str, state: Option<ConflictState>) -> Result<bool> {
        let mut conn = self.connection()?;
        let state_str = state.map(|s| s.as_str().to_string());
        let rows_affected = diesel::update(
            file_metadata_dsl::file_metadata.filter(file_metadata_dsl::local_path.eq(path)),
        )
        .set(file_metadata_dsl::conflict_state.eq(state_str))
        .execute(&mut conn)
        .context("Failed to update conflict state")?;
        Ok(rows_affected > 0)
    }
}

// =========================================================================
// Row Types
// =========================================================================

#[derive(Queryable)]
struct FileMetadataRow {
    id: i64,
    drive_id: String,
    is_folder: bool,
    local_path: String,
    created_at: i64,
    updated_at: i64,
    etag: String,
    metadata: String,
    props: Option<String>,
    permissions: String,
    shared: bool,
    size: i64,
    conflict_state: Option<String>,
}

#[derive(Insertable)]
#[diesel(table_name = file_metadata)]
struct NewFileMetadata {
    drive_id: String,
    is_folder: bool,
    local_path: String,
    created_at: i64,
    updated_at: i64,
    etag: String,
    metadata: String,
    props: Option<String>,
    permissions: String,
    shared: bool,
    size: i64,
    conflict_state: Option<String>,
}

#[derive(AsChangeset)]
#[diesel(table_name = file_metadata)]
struct FileMetadataChangeset {
    drive_id: String,
    is_folder: bool,
    updated_at: i64,
    etag: String,
    metadata: String,
    props: Option<String>,
    permissions: String,
    shared: bool,
    size: i64,
    /// Use Option<Option<String>> so that:
    /// - Some(None) explicitly sets conflict_state to NULL
    /// - Some(Some(value)) sets it to a value
    conflict_state: Option<Option<String>>,
}

impl TryFrom<FileMetadataRow> for FileMetadata {
    type Error = anyhow::Error;

    fn try_from(row: FileMetadataRow) -> Result<Self> {
        let metadata_map: HashMap<String, String> =
            serde_json::from_str(&row.metadata).context("Failed to deserialize metadata column")?;
        let props_value = match row.props {
            Some(json) => {
                Some(serde_json::from_str(&json).context("Failed to deserialize props column")?)
            }
            None => None,
        };
        let conflict_state = row
            .conflict_state
            .as_deref()
            .and_then(ConflictState::from_str);

        Ok(FileMetadata {
            id: row.id,
            drive_id: Uuid::parse_str(&row.drive_id).context("Failed to parse drive_id column")?,
            is_folder: row.is_folder,
            local_path: row.local_path,
            created_at: row.created_at,
            updated_at: row.updated_at,
            etag: row.etag,
            metadata: metadata_map,
            props: props_value,
            permissions: row.permissions,
            shared: row.shared,
            size: row.size,
            conflict_state,
        })
    }
}

impl TryFrom<&MetadataEntry> for NewFileMetadata {
    type Error = anyhow::Error;

    fn try_from(entry: &MetadataEntry) -> Result<Self> {
        Ok(Self {
            drive_id: entry.drive_id.to_string(),
            is_folder: entry.is_folder,
            local_path: entry.local_path.clone(),
            created_at: entry.created_at,
            updated_at: entry.updated_at,
            etag: entry.etag.clone(),
            metadata: serde_json::to_string(&entry.metadata)
                .context("Failed to serialize metadata map")?,
            props: entry
                .props
                .as_ref()
                .map(|p| serde_json::to_string(p))
                .transpose()
                .context("Failed to serialize props field")?,
            permissions: entry.permissions.clone(),
            shared: entry.shared,
            size: entry.size,
            conflict_state: entry.conflict_state.map(|s| s.as_str().to_string()),
        })
    }
}

impl FileMetadataChangeset {
    fn from_entry(entry: &MetadataEntry) -> Result<Self> {
        Ok(Self {
            drive_id: entry.drive_id.to_string(),
            is_folder: entry.is_folder,
            updated_at: entry.updated_at,
            etag: entry.etag.clone(),
            metadata: serde_json::to_string(&entry.metadata)
                .context("Failed to serialize metadata map")?,
            props: entry
                .props
                .as_ref()
                .map(|p| serde_json::to_string(p))
                .transpose()
                .context("Failed to serialize props field")?,
            permissions: entry.permissions.clone(),
            shared: entry.shared,
            size: entry.size,
            // Use Some(...) to always update the column, even when clearing to NULL
            conflict_state: Some(entry.conflict_state.map(|s| s.as_str().to_string())),
        })
    }
}

/// Fylvo: the prefix every descendant of `path` starts with.
fn descendant_prefix(path: &str) -> String {
    format!(
        "{}{}",
        path.trim_end_matches(std::path::MAIN_SEPARATOR),
        std::path::MAIN_SEPARATOR
    )
}

/// Fylvo: the paths that are not under another path of the list, without duplicates.
fn outermost_paths<'a>(paths: &[&'a str]) -> Vec<&'a str> {
    let mut sorted: Vec<&str> = paths.to_vec();
    sorted.sort_by_key(|p| p.len());
    let mut result: Vec<&str> = Vec::new();
    for path in sorted {
        let covered = result
            .iter()
            .any(|outer| *outer == path || path.starts_with(&descendant_prefix(outer)));
        if !covered {
            result.push(path);
        }
    }
    result
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::native;

    fn db() -> (tempfile::TempDir, InventoryDb) {
        let dir = tempfile::tempdir().unwrap();
        let db = InventoryDb::with_path(dir.path().join("meta.db")).unwrap();
        (dir, db)
    }

    fn add(db: &InventoryDb, drive: Uuid, path: &str) {
        db.insert(&MetadataEntry::new(drive, path, false)).unwrap();
    }

    #[test]
    fn count_under_paths_counts_descendants_once() {
        let (_dir, db) = db();
        let drive = Uuid::new_v4();
        let other = Uuid::new_v4();
        for path in [
            native(r"C:\S\a"),
            native(r"C:\S\a\1.txt"),
            native(r"C:\S\a\b\2.txt"),
            native(r"C:\S\ab.txt"),
            native(r"C:\S\a_x\3.txt"),
        ] {
            add(&db, drive, path);
        }
        add(&db, other, native(r"C:\T\a\1.txt"));
        let id = drive.to_string();

        assert_eq!(db.count_for_drive(&id).unwrap(), 5);
        // "a" and its two children; "ab.txt" and "a_x" are siblings, not children.
        assert_eq!(db.count_under_paths(&id, &[native(r"C:\S\a")]).unwrap(), 3);
        // A nested path is not counted twice.
        assert_eq!(
            db.count_under_paths(&id, &[native(r"C:\S\a"), native(r"C:\S\a\1.txt")]).unwrap(),
            3
        );
        // Windows ignores case in paths, so does the count. Linux does not.
        let other_case = db.count_under_paths(&id, &[native(r"C:\s\A")]).unwrap();
        assert_eq!(other_case, if cfg!(windows) { 3 } else { 1 });
        // A path without entry counts as one.
        assert_eq!(db.count_under_paths(&id, &[native(r"C:\S\new.txt")]).unwrap(), 1);
    }

    #[test]
    fn case_only_rename_moves_descendants_and_refuses_existing_rows() {
        let (_dir, db) = db();
        let drive = Uuid::new_v4();
        for path in [native(r"C:\S\Fotos"), native(r"C:\S\Fotos\a.jpg"), native(r"C:\S\Fotos\b\c.jpg")] {
            add(&db, drive, path);
        }

        db.rename_path(native(r"C:\S\Fotos"), native(r"C:\S\fotos")).unwrap();
        assert!(db.query_by_path(native(r"C:\S\fotos\b\c.jpg")).unwrap().is_some());
        assert!(db.query_by_path(native(r"C:\S\Fotos\a.jpg")).unwrap().is_none());

        assert!(db.has_rows_at_or_under(native(r"C:\S\fotos")).unwrap());
        assert!(!db.has_rows_at_or_under(native(r"C:\S\Fotos")).unwrap());

        // Rows under the new spelling exist already: refused as a whole, nothing moved.
        add(&db, drive, native(r"C:\S\Fotos\a.jpg"));
        assert!(db.has_rows_at_or_under(native(r"C:\S\Fotos")).unwrap());
        assert!(!db.has_rows_at_or_under(native(r"C:\S\Foto")).unwrap());
        assert!(db.rename_path(native(r"C:\S\fotos"), native(r"C:\S\Fotos")).is_err());
        assert!(db.query_by_path(native(r"C:\S\fotos\b\c.jpg")).unwrap().is_some());
    }

    #[test]
    fn batch_delete_and_rename_use_literal_windows_prefixes() {
        let (_dir, db) = db();
        let drive = Uuid::new_v4();
        for path in [native(r"C:\S\a_b"), native(r"C:\S\a_b\1.txt"), native(r"C:\S\axb\2.txt")] {
            add(&db, drive, path);
        }

        db.rename_path(native(r"C:\S\a_b"), native(r"C:\S\c")).unwrap();
        assert!(db.query_by_path(native(r"C:\S\c\1.txt")).unwrap().is_some());
        assert!(db.query_by_path(native(r"C:\S\axb\2.txt")).unwrap().is_some());

        db.batch_delete_by_path(vec![native(r"C:\S\c")]).unwrap();
        assert!(db.query_by_path(native(r"C:\S\c\1.txt")).unwrap().is_none());
        assert!(db.query_by_path(native(r"C:\S\axb\2.txt")).unwrap().is_some());
    }
}

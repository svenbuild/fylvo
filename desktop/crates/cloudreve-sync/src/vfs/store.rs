//! `index.db` of one drive: the known tree (for offline use and fast starts), the content
//! store, local changes not uploaded yet, and pinned folders. Paths are relative to the drive
//! root with `/`, the root is "".

use anyhow::{Context, Result};
use diesel::connection::SimpleConnection;
use diesel::prelude::*;
use diesel::sql_types::{BigInt, Binary, Integer, Nullable, Text};
use std::path::Path;
use std::sync::Mutex;

const SCHEMA: &str = "
PRAGMA busy_timeout = 5000;
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
CREATE TABLE IF NOT EXISTS entries (
    path TEXT PRIMARY KEY NOT NULL,
    parent TEXT NOT NULL,
    name TEXT NOT NULL,
    is_dir INTEGER NOT NULL,
    size BIGINT NOT NULL,
    mtime BIGINT NOT NULL,
    file_id TEXT NOT NULL,
    version TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS entries_parent ON entries(parent);
CREATE TABLE IF NOT EXISTS listed (
    path TEXT PRIMARY KEY NOT NULL,
    at BIGINT NOT NULL
);
CREATE TABLE IF NOT EXISTS content (
    key TEXT PRIMARY KEY NOT NULL,
    file_id TEXT NOT NULL,
    version TEXT NOT NULL,
    remote_len BIGINT NOT NULL,
    chunks BLOB NOT NULL,
    last_used BIGINT NOT NULL,
    changed INTEGER NOT NULL,
    path TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS changes (
    path TEXT PRIMARY KEY NOT NULL,
    key TEXT NOT NULL,
    file_id TEXT,
    base TEXT,
    local_only INTEGER NOT NULL,
    open INTEGER NOT NULL,
    size BIGINT NOT NULL,
    mtime BIGINT NOT NULL
);
CREATE TABLE IF NOT EXISTS pins (
    path TEXT PRIMARY KEY NOT NULL
);
CREATE TABLE IF NOT EXISTS meta (
    key TEXT PRIMARY KEY NOT NULL,
    value TEXT NOT NULL
);
";

#[derive(Debug, Clone, PartialEq, QueryableByName)]
pub(crate) struct EntryRow {
    #[diesel(sql_type = Text)]
    pub name: String,
    #[diesel(sql_type = Integer)]
    pub is_dir: i32,
    #[diesel(sql_type = BigInt)]
    pub size: i64,
    #[diesel(sql_type = BigInt)]
    pub mtime: i64,
    #[diesel(sql_type = Text)]
    pub file_id: String,
    #[diesel(sql_type = Text)]
    pub version: String,
}

#[derive(Debug, Clone, PartialEq, QueryableByName)]
pub(crate) struct ContentRow {
    #[diesel(sql_type = Text)]
    pub key: String,
    #[diesel(sql_type = Text)]
    pub file_id: String,
    #[diesel(sql_type = Text)]
    pub version: String,
    #[diesel(sql_type = BigInt)]
    pub remote_len: i64,
    #[diesel(sql_type = Binary)]
    pub chunks: Vec<u8>,
    #[diesel(sql_type = BigInt)]
    pub last_used: i64,
    #[diesel(sql_type = Integer)]
    pub changed: i32,
    #[diesel(sql_type = Text)]
    pub path: String,
}

#[derive(Debug, Clone, PartialEq, QueryableByName)]
pub(crate) struct ChangeRow {
    #[diesel(sql_type = Text)]
    pub path: String,
    #[diesel(sql_type = Text)]
    pub key: String,
    #[diesel(sql_type = Nullable<Text>)]
    pub file_id: Option<String>,
    #[diesel(sql_type = Nullable<Text>)]
    pub base: Option<String>,
    #[diesel(sql_type = Integer)]
    pub local_only: i32,
    #[diesel(sql_type = Integer)]
    pub open: i32,
    #[diesel(sql_type = BigInt)]
    pub size: i64,
    #[diesel(sql_type = BigInt)]
    pub mtime: i64,
}

#[derive(QueryableByName)]
struct OneText {
    #[diesel(sql_type = Text)]
    value: String,
}

#[derive(QueryableByName)]
struct OneBig {
    #[diesel(sql_type = BigInt)]
    value: i64,
}

pub(crate) struct Store {
    conn: Mutex<SqliteConnection>,
}

/// Escape `%`, `_` and `\` for a `LIKE ... ESCAPE '\'` prefix match.
fn like_prefix(path: &str) -> String {
    let mut escaped = String::with_capacity(path.len() + 2);
    for c in path.chars() {
        if matches!(c, '%' | '_' | '\\') {
            escaped.push('\\');
        }
        escaped.push(c);
    }
    escaped.push_str("/%");
    escaped
}

impl Store {
    pub(crate) fn open(path: &Path) -> Result<Self> {
        let url = path.to_str().context("store path is not UTF-8")?;
        let mut conn = SqliteConnection::establish(url).context("open the drive index")?;
        conn.batch_execute(SCHEMA).context("prepare the drive index")?;
        Ok(Self {
            conn: Mutex::new(conn),
        })
    }

    fn with<T>(&self, f: impl FnOnce(&mut SqliteConnection) -> QueryResult<T>) -> Result<T> {
        let mut conn = self.conn.lock().unwrap_or_else(|e| e.into_inner());
        Ok(f(&mut conn)?)
    }

    // ---- known tree ----

    /// Replace the stored listing of folder `parent` (`at` in Unix seconds).
    pub(crate) fn save_listing(&self, parent: &str, entries: &[EntryRow], at: i64) -> Result<()> {
        self.with(|c| {
            c.transaction(|c| {
                diesel::sql_query("DELETE FROM entries WHERE parent = ?")
                    .bind::<Text, _>(parent)
                    .execute(c)?;
                for e in entries {
                    diesel::sql_query(
                        "INSERT OR REPLACE INTO entries (path, parent, name, is_dir, size, mtime, file_id, version) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
                    )
                    .bind::<Text, _>(super::names::join(parent, &e.name))
                    .bind::<Text, _>(parent)
                    .bind::<Text, _>(&e.name)
                    .bind::<Integer, _>(e.is_dir)
                    .bind::<BigInt, _>(e.size)
                    .bind::<BigInt, _>(e.mtime)
                    .bind::<Text, _>(&e.file_id)
                    .bind::<Text, _>(&e.version)
                    .execute(c)?;
                }
                diesel::sql_query("INSERT OR REPLACE INTO listed (path, at) VALUES (?, ?)")
                    .bind::<Text, _>(parent)
                    .bind::<BigInt, _>(at)
                    .execute(c)?;
                Ok(())
            })
        })
    }

    /// The stored listing of a folder, if it was ever listed.
    pub(crate) fn load_listing(&self, parent: &str) -> Result<Option<Vec<EntryRow>>> {
        self.with(|c| {
            let listed: Vec<OneBig> = diesel::sql_query("SELECT at AS value FROM listed WHERE path = ?")
                .bind::<Text, _>(parent)
                .load(c)?;
            if listed.first().is_none_or(|row| row.value < 0) {
                return Ok(None);
            }
            let rows: Vec<EntryRow> = diesel::sql_query(
                "SELECT name, is_dir, size, mtime, file_id, version FROM entries WHERE parent = ?",
            )
            .bind::<Text, _>(parent)
            .load(c)?;
            Ok(Some(rows))
        })
    }

    /// Forget the stored tree at and below `path` (after a rename or deletion; listed again
    /// when needed).
    pub(crate) fn forget_tree(&self, path: &str) -> Result<()> {
        let below = like_prefix(path);
        self.with(|c| {
            for table in ["entries", "listed"] {
                diesel::sql_query(format!(
                    "DELETE FROM {table} WHERE path = ? OR path LIKE ? ESCAPE '\\'"
                ))
                .bind::<Text, _>(path)
                .bind::<Text, _>(&below)
                .execute(c)?;
            }
            Ok(())
        })
    }

    // ---- content ----

    pub(crate) fn save_content(&self, row: &ContentRow) -> Result<()> {
        self.with(|c| insert_content(c, row))
    }

    pub(crate) fn delete_content(&self, key: &str) -> Result<()> {
        self.with(|c| {
            diesel::sql_query("DELETE FROM content WHERE key = ?")
                .bind::<Text, _>(key)
                .execute(c)
                .map(|_| ())
        })
    }

    pub(crate) fn load_contents(&self) -> Result<Vec<ContentRow>> {
        self.with(|c| {
            diesel::sql_query(
                "SELECT key, file_id, version, remote_len, chunks, last_used, changed, path FROM content",
            )
            .load(c)
        })
    }

    // ---- local changes ----

    #[cfg(test)]
    pub(crate) fn save_change(&self, row: &ChangeRow) -> Result<()> {
        self.with(|c| insert_change(c, row))
    }

    /// A change and its content's chunk map in one transaction.
    pub(crate) fn save_change_and_content(&self, change: &ChangeRow, content: &ContentRow) -> Result<()> {
        self.with(|c| {
            c.transaction(|c| {
                insert_change(c, change)?;
                insert_content(c, content)
            })
        })
    }

    /// Remove the changes at `paths` and record `row` (with its content), in one transaction
    /// (a rename never leaves a change without its row).
    pub(crate) fn replace_change(&self, paths: &[&str], row: Option<(&ChangeRow, &ContentRow)>) -> Result<()> {
        self.with(|c| {
            c.transaction(|c| {
                for path in paths {
                    diesel::sql_query("DELETE FROM changes WHERE path = ?")
                        .bind::<Text, _>(*path)
                        .execute(c)?;
                }
                if let Some((change, content)) = row {
                    insert_change(c, change)?;
                    insert_content(c, content)?;
                }
                Ok(())
            })
        })
    }

    /// After an upload: the change (the row of that content, not a newer one at the same path)
    /// is gone, its content is the cached copy now.
    pub(crate) fn commit_upload(&self, path: &str, old_key: &str, content: &ContentRow) -> Result<()> {
        self.with(|c| {
            c.transaction(|c| {
                diesel::sql_query("DELETE FROM changes WHERE path = ? AND key = ?")
                    .bind::<Text, _>(path)
                    .bind::<Text, _>(old_key)
                    .execute(c)?;
                diesel::sql_query("DELETE FROM content WHERE key = ?")
                    .bind::<Text, _>(old_key)
                    .execute(c)?;
                insert_content(c, content)
            })
        })
    }

    /// Remove the change at `path` only if it is still the one of content `key`.
    pub(crate) fn delete_change_of(&self, path: &str, key: &str) -> Result<()> {
        self.with(|c| {
            diesel::sql_query("DELETE FROM changes WHERE path = ? AND key = ?")
                .bind::<Text, _>(path)
                .bind::<Text, _>(key)
                .execute(c)
                .map(|_| ())
        })
    }

    pub(crate) fn delete_change(&self, path: &str) -> Result<()> {
        self.with(|c| {
            diesel::sql_query("DELETE FROM changes WHERE path = ?")
                .bind::<Text, _>(path)
                .execute(c)
                .map(|_| ())
        })
    }

    /// A folder or file moved from `old` to `new`: its changes move with it (read and written
    /// in one transaction).
    pub(crate) fn move_changes(&self, old: &str, new: &str) -> Result<()> {
        if old.is_empty() {
            return Ok(());
        }
        self.with(|c| {
            c.transaction(|c| {
                let rows: Vec<ChangeRow> = diesel::sql_query(
                    "SELECT path, key, file_id, base, local_only, open, size, mtime FROM changes",
                )
                .load(c)?;
                for row in rows.iter().filter(|r| super::names::is_below(&r.path, old)) {
                    diesel::sql_query("DELETE FROM changes WHERE path = ?")
                        .bind::<Text, _>(&row.path)
                        .execute(c)?;
                    let moved = ChangeRow { path: super::names::replace_prefix(&row.path, old, new), ..row.clone() };
                    insert_change(c, &moved)?;
                }
                Ok(())
            })
        })
    }

    pub(crate) fn load_changes(&self) -> Result<Vec<ChangeRow>> {
        self.with(|c| {
            diesel::sql_query(
                "SELECT path, key, file_id, base, local_only, open, size, mtime FROM changes",
            )
            .load(c)
        })
    }

    // ---- pins and small values ----

    pub(crate) fn add_pin(&self, path: &str) -> Result<()> {
        self.with(|c| {
            diesel::sql_query("INSERT OR IGNORE INTO pins (path) VALUES (?)")
                .bind::<Text, _>(path)
                .execute(c)
                .map(|_| ())
        })
    }

    pub(crate) fn remove_pin(&self, path: &str) -> Result<()> {
        self.with(|c| {
            diesel::sql_query("DELETE FROM pins WHERE path = ?")
                .bind::<Text, _>(path)
                .execute(c)
                .map(|_| ())
        })
    }

    pub(crate) fn pins(&self) -> Result<Vec<String>> {
        self.with(|c| {
            let rows: Vec<OneText> = diesel::sql_query("SELECT path AS value FROM pins ORDER BY path").load(c)?;
            Ok(rows.into_iter().map(|r| r.value).collect())
        })
    }

    pub(crate) fn set_meta(&self, key: &str, value: &str) -> Result<()> {
        self.with(|c| {
            diesel::sql_query("INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)")
                .bind::<Text, _>(key)
                .bind::<Text, _>(value)
                .execute(c)
                .map(|_| ())
        })
    }

    pub(crate) fn meta(&self, key: &str) -> Result<Option<String>> {
        self.with(|c| {
            let rows: Vec<OneText> = diesel::sql_query("SELECT value FROM meta WHERE key = ?")
                .bind::<Text, _>(key)
                .load(c)?;
            Ok(rows.into_iter().next().map(|r| r.value))
        })
    }
}

fn insert_change(c: &mut SqliteConnection, row: &ChangeRow) -> QueryResult<()> {
    diesel::sql_query(
        "INSERT OR REPLACE INTO changes (path, key, file_id, base, local_only, open, size, mtime) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
    )
    .bind::<Text, _>(&row.path)
    .bind::<Text, _>(&row.key)
    .bind::<Nullable<Text>, _>(row.file_id.as_deref())
    .bind::<Nullable<Text>, _>(row.base.as_deref())
    .bind::<Integer, _>(row.local_only)
    .bind::<Integer, _>(row.open)
    .bind::<BigInt, _>(row.size)
    .bind::<BigInt, _>(row.mtime)
    .execute(c)
    .map(|_| ())
}

fn insert_content(c: &mut SqliteConnection, row: &ContentRow) -> QueryResult<()> {
    diesel::sql_query(
        "INSERT OR REPLACE INTO content (key, file_id, version, remote_len, chunks, last_used, changed, path) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
    )
    .bind::<Text, _>(&row.key)
    .bind::<Text, _>(&row.file_id)
    .bind::<Text, _>(&row.version)
    .bind::<BigInt, _>(row.remote_len)
    .bind::<Binary, _>(&row.chunks)
    .bind::<BigInt, _>(row.last_used)
    .bind::<Integer, _>(row.changed)
    .bind::<Text, _>(&row.path)
    .execute(c)
    .map(|_| ())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn entry(name: &str, is_dir: bool) -> EntryRow {
        EntryRow {
            name: name.into(),
            is_dir: is_dir as i32,
            size: 5,
            mtime: 1_700_000_000,
            file_id: format!("id-{name}"),
            version: "v1".into(),
        }
    }

    #[test]
    fn the_store_keeps_listings_changes_and_pins() {
        let dir = tempfile::tempdir().unwrap();
        let store = Store::open(&dir.path().join("index.db")).unwrap();
        assert_eq!(store.load_listing("").unwrap(), None);
        store.save_listing("", &[entry("a", true), entry("b.txt", false)], 1).unwrap();
        store.save_listing("a", &[entry("c_d.txt", false)], 1).unwrap();
        store.save_listing("a_b", &[entry("x", false)], 1).unwrap();
        assert_eq!(store.load_listing("").unwrap().unwrap().len(), 2);
        // A rename forgets the tree below, but not a sibling whose name starts the same.
        store.forget_tree("a").unwrap();
        assert_eq!(store.load_listing("a").unwrap(), None);
        assert!(store.load_listing("a_b").unwrap().is_some());

        let change = ChangeRow {
            path: "a/c_d.txt".into(),
            key: "w-1".into(),
            file_id: Some("id".into()),
            base: Some("v1".into()),
            local_only: 0,
            open: 1,
            size: 3,
            mtime: 2,
        };
        store.save_change(&change).unwrap();
        store.move_changes("a", "z/a").unwrap();
        let changes = store.load_changes().unwrap();
        assert_eq!(changes.len(), 1);
        assert_eq!(changes[0].path, "z/a/c_d.txt");
        store.delete_change("z/a/c_d.txt").unwrap();
        assert!(store.load_changes().unwrap().is_empty());

        store.add_pin("a").unwrap();
        store.add_pin("a").unwrap();
        assert_eq!(store.pins().unwrap(), vec!["a".to_string()]);
        store.remove_pin("a").unwrap();
        assert!(store.pins().unwrap().is_empty());

        let content = ContentRow {
            key: "c-id-v1".into(),
            file_id: "id".into(),
            version: "v1".into(),
            remote_len: 10,
            chunks: vec![1],
            last_used: 3,
            changed: 0,
            path: "b.txt".into(),
        };
        store.save_content(&content).unwrap();
        assert_eq!(store.load_contents().unwrap(), vec![content]);
        store.delete_content("c-id-v1").unwrap();
        assert!(store.load_contents().unwrap().is_empty());

        store.set_meta("quota", "1/2").unwrap();
        assert_eq!(store.meta("quota").unwrap().as_deref(), Some("1/2"));
    }
}

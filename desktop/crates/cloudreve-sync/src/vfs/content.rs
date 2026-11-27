//! The content store of one drive: one sparse file per cached server version
//! (`c-<file id>-<version>`) or per changed file (`w-<uuid>`), plus which chunks are loaded.

use super::chunks::{chunk_count, ChunkMap, CHUNK};
use super::store::ContentRow;
use anyhow::{Context, Result};
use std::collections::HashMap;
use std::fs::{File, OpenOptions};
use std::os::unix::fs::{FileExt, OpenOptionsExt};
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex, MutexGuard};

pub(crate) fn now_secs() -> i64 {
    chrono::Utc::now().timestamp()
}

fn safe(part: &str) -> String {
    part.chars()
        .map(|c| if c.is_ascii_alphanumeric() || c == '_' || c == '-' { c } else { '_' })
        .collect()
}

/// The key of the cached copy of one server version.
pub(crate) fn clean_key(file_id: &str, version: &str) -> String {
    format!("c-{}-{}", safe(file_id), safe(version))
}

#[derive(Debug, Clone)]
pub(crate) struct CellState {
    pub key: String,
    /// The server file and version whose bytes fill chunks that are not loaded yet.
    pub file_id: String,
    pub version: String,
    /// Bytes `0..remote_len` come from that version; beyond it the local file is the truth.
    pub remote_len: u64,
    pub chunks: ChunkMap,
    pub last_used: i64,
    /// Open handles (never evicted while open).
    pub open: u32,
    /// Holds a local change not uploaded yet (never evicted).
    pub changed: bool,
    /// The last known path in the drive (pins are found by path).
    pub path: String,
    /// Dropped from the store (evicted or replaced); handles that still have it keep reading.
    pub dropped: bool,
    /// Counts writes and truncations (in memory), so an upload knows whether the file changed
    /// after its snapshot.
    pub writes: u64,
    /// Counts changes of the chunk map; `saved_map` is the count the store has. A write may
    /// only take the fast way (not recording anything) while both are equal: otherwise a crash
    /// could later fill a written chunk with the server's bytes.
    pub map_changes: u64,
    pub saved_map: u64,
    /// The change of this content has its row in the store (fast writes only then).
    pub recorded: bool,
}

impl CellState {
    pub(crate) fn missing_runs(&self, start: u64, end: u64) -> Vec<(u64, u64)> {
        let end = end.min(self.remote_len);
        if end <= start {
            return Vec::new();
        }
        let (first, last) = super::chunks::chunks_of(start, end);
        self.chunks.missing_runs(first, last)
    }

    pub(crate) fn is_complete(&self) -> bool {
        self.missing_runs(0, self.remote_len).is_empty()
    }

    /// Bytes of server content loaded (what the cache limit counts).
    pub(crate) fn loaded_bytes(&self) -> u64 {
        (self.chunks.count_below(chunk_count(self.remote_len)) * CHUNK).min(self.remote_len)
    }

    pub(crate) fn row(&self) -> ContentRow {
        ContentRow {
            key: self.key.clone(),
            file_id: self.file_id.clone(),
            version: self.version.clone(),
            remote_len: self.remote_len as i64,
            chunks: self.chunks.as_bytes().to_vec(),
            last_used: self.last_used,
            changed: self.changed as i32,
            path: self.path.clone(),
        }
    }
}

/// One content file. Reads use `pread` without a lock; writes and the filling of chunks hold
/// `state` while they write, so a chunk loaded from the server never lands over a local write.
/// Writes also hold `gate` (shared); an upload snapshot holds it alone while it copies, so it
/// never sees half a write and never holds `state` for long (other code takes `state` under the
/// tree lock).
pub(crate) struct Cell {
    pub file: File,
    pub state: Mutex<CellState>,
    pub gate: std::sync::RwLock<()>,
    /// One loader per file at a time (two reads of the same part share it).
    pub fetch: tokio::sync::Mutex<()>,
}

impl Cell {
    fn new(file: File, state: CellState) -> Self {
        Self { file, state: Mutex::new(state), gate: Default::default(), fetch: Default::default() }
    }

    pub(crate) fn lock(&self) -> MutexGuard<'_, CellState> {
        self.state.lock().unwrap_or_else(|e| e.into_inner())
    }

    pub(crate) fn key(&self) -> String {
        self.lock().key.clone()
    }

    pub(crate) fn len(&self) -> u64 {
        self.file.metadata().map(|m| m.len()).unwrap_or(0)
    }

    pub(crate) fn read_at(&self, offset: u64, size: u32) -> std::io::Result<Vec<u8>> {
        let len = self.len();
        if offset >= len {
            return Ok(Vec::new());
        }
        let size = (size as u64).min(len - offset) as usize;
        let mut buf = vec![0u8; size];
        let mut done = 0;
        while done < size {
            let n = self.file.read_at(&mut buf[done..], offset + done as u64)?;
            if n == 0 {
                break;
            }
            done += n;
        }
        buf.truncate(done);
        Ok(buf)
    }
}

pub(crate) struct Contents {
    dir: PathBuf,
    cells: Mutex<HashMap<String, Arc<Cell>>>,
}

fn open_file(path: &Path, create: bool) -> std::io::Result<File> {
    OpenOptions::new()
        .read(true)
        .write(true)
        .create(create)
        .mode(0o600)
        .open(path)
}

impl Contents {
    /// The store in `dir`, with the cells of `rows` whose files still exist. Returns the keys
    /// of rows without a file (to remove).
    pub(crate) fn load(dir: PathBuf, rows: Vec<ContentRow>) -> Result<(Self, Vec<String>)> {
        std::fs::create_dir_all(&dir).context("create the content folder")?;
        let mut cells = HashMap::new();
        let mut lost = Vec::new();
        for row in rows {
            match open_file(&dir.join(&row.key), false) {
                Ok(file) => {
                    let state = CellState {
                        key: row.key.clone(),
                        file_id: row.file_id,
                        version: row.version,
                        remote_len: row.remote_len.max(0) as u64,
                        chunks: ChunkMap::from_bytes(row.chunks),
                        last_used: row.last_used,
                        open: 0,
                        changed: row.changed != 0,
                        path: row.path,
                        dropped: false,
                        writes: 0,
                        map_changes: 0,
                        saved_map: 0,
                        recorded: true,
                    };
                    cells.insert(row.key, Arc::new(Cell::new(file, state)));
                }
                Err(e) => {
                    if row.changed != 0 {
                        tracing::warn!(target: "vfs", key = %row.key, error = %e, "A changed cached file cannot be opened, its change is dropped");
                    }
                    lost.push(row.key)
                }
            }
        }
        // Files no row knows (a crash between creating and recording) are removed.
        if let Ok(entries) = std::fs::read_dir(&dir) {
            for entry in entries.flatten() {
                let name = entry.file_name().to_string_lossy().to_string();
                if !cells.contains_key(&name) {
                    let _ = std::fs::remove_file(entry.path());
                }
            }
        }
        Ok((Self { dir, cells: Mutex::new(cells) }, lost))
    }

    fn map(&self) -> MutexGuard<'_, HashMap<String, Arc<Cell>>> {
        self.cells.lock().unwrap_or_else(|e| e.into_inner())
    }

    pub(crate) fn get(&self, key: &str) -> Option<Arc<Cell>> {
        self.map().get(key).cloned()
    }

    pub(crate) fn all(&self) -> Vec<Arc<Cell>> {
        self.map().values().cloned().collect()
    }

    /// The cached copy of a server version, created empty (sparse) if there is none. With
    /// `open`, counted as open before it is returned, so eviction cannot take it meanwhile.
    pub(crate) fn clean(&self, file_id: &str, version: &str, size: u64, path: &str, open: bool) -> Result<Arc<Cell>> {
        let key = clean_key(file_id, version);
        let mut map = self.map();
        if let Some(cell) = map.get(&key) {
            let mut state = cell.lock();
            state.path = path.to_string();
            if open {
                state.open += 1;
            }
            drop(state);
            return Ok(cell.clone());
        }
        let file = open_file(&self.dir.join(&key), true).context("create a cache file")?;
        file.set_len(size).context("size a cache file")?;
        let state = CellState {
            key: key.clone(),
            file_id: file_id.to_string(),
            version: version.to_string(),
            remote_len: size,
            chunks: ChunkMap::default(),
            last_used: now_secs(),
            open: open as u32,
            changed: false,
            path: path.to_string(),
            dropped: false,
            writes: 0,
            map_changes: 0,
            saved_map: 0,
            recorded: false,
        };
        let cell = Arc::new(Cell::new(file, state));
        map.insert(key, cell.clone());
        Ok(cell)
    }

    /// An empty file for a new local file.
    pub(crate) fn new_local(&self, path: &str) -> Result<Arc<Cell>> {
        let key = format!("w-{}", uuid::Uuid::new_v4().simple());
        let file = open_file(&self.dir.join(&key), true).context("create a file")?;
        let state = CellState {
            key: key.clone(),
            file_id: String::new(),
            version: String::new(),
            remote_len: 0,
            chunks: ChunkMap::default(),
            last_used: now_secs(),
            open: 0,
            changed: true,
            path: path.to_string(),
            dropped: false,
            writes: 0,
            map_changes: 0,
            saved_map: 0,
            recorded: false,
        };
        let cell = Arc::new(Cell::new(file, state));
        self.map().insert(key, cell.clone());
        Ok(cell)
    }

    /// Turn the cached copy of a server version into the copy of a local change (a new key, so
    /// a later open of that server version gets its own copy). Returns the old key.
    pub(crate) fn make_changed(&self, cell: &Arc<Cell>) -> Result<Option<String>> {
        let mut map = self.map();
        let mut state = cell.lock();
        if state.changed {
            return Ok(None);
        }
        let old = state.key.clone();
        let new = format!("w-{}", uuid::Uuid::new_v4().simple());
        std::fs::rename(self.dir.join(&old), self.dir.join(&new)).context("prepare a changed file")?;
        map.remove(&old);
        state.key = new.clone();
        state.changed = true;
        state.recorded = false;
        drop(state);
        map.insert(new, cell.clone());
        Ok(Some(old))
    }

    /// After an upload: the changed copy is now the cached copy of the new server version
    /// (complete). Only if nothing was written since the snapshot (`writes`); `None` if it was.
    /// Returns the old key.
    pub(crate) fn make_clean(&self, cell: &Arc<Cell>, file_id: &str, version: &str, writes: u64) -> Result<Option<String>> {
        let mut map = self.map();
        let mut state = cell.lock();
        if state.writes != writes {
            return Ok(None);
        }
        let old = state.key.clone();
        let new = clean_key(file_id, version);
        if let Some(other) = map.remove(&new) {
            other.lock().dropped = true;
        }
        std::fs::rename(self.dir.join(&old), self.dir.join(&new)).context("keep the uploaded file")?;
        map.remove(&old);
        let len = cell.len();
        state.key = new.clone();
        state.file_id = file_id.to_string();
        state.version = version.to_string();
        state.remote_len = len;
        state.chunks.clear();
        state.chunks.set_range(0, chunk_count(len));
        state.changed = false;
        drop(state);
        map.insert(new, cell.clone());
        Ok(Some(old))
    }

    /// Remove a changed cell after its content went to a conflict copy, only if nothing was
    /// written since the snapshot. Returns whether it was removed.
    pub(crate) fn remove_if_unchanged(&self, cell: &Arc<Cell>, writes: u64) -> bool {
        let mut map = self.map();
        let mut state = cell.lock();
        if state.writes != writes {
            return false;
        }
        if map.get(&state.key).is_some_and(|c| Arc::ptr_eq(c, cell)) {
            map.remove(&state.key);
        }
        state.dropped = true;
        let _ = std::fs::remove_file(self.dir.join(&state.key));
        true
    }

    /// Remove a cell and its file. Handles that have it keep their open file.
    pub(crate) fn remove(&self, cell: &Arc<Cell>) {
        let mut map = self.map();
        let mut state = cell.lock();
        if map.get(&state.key).is_some_and(|c| Arc::ptr_eq(c, cell)) {
            map.remove(&state.key);
        }
        state.dropped = true;
        let _ = std::fs::remove_file(self.dir.join(&state.key));
    }

    /// Remove `key` if nothing uses it (eviction). Returns the bytes freed.
    pub(crate) fn evict(&self, key: &str) -> Option<u64> {
        let mut map = self.map();
        let cell = map.get(key)?.clone();
        let mut state = cell.lock();
        if state.open > 0 || state.changed {
            return None;
        }
        let Ok(_busy) = cell.fetch.try_lock() else {
            return None;
        };
        let freed = state.loaded_bytes();
        state.dropped = true;
        map.remove(key);
        let _ = std::fs::remove_file(self.dir.join(key));
        Some(freed)
    }

    pub(crate) fn path_of(&self, key: &str) -> PathBuf {
        self.dir.join(key)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_cached_copy_becomes_a_change_and_then_the_new_version() {
        let dir = tempfile::tempdir().unwrap();
        let (contents, lost) = Contents::load(dir.path().join("content"), Vec::new()).unwrap();
        assert!(lost.is_empty());
        let cell = contents.clean("f1", "v1", 3 * CHUNK, "a.txt", true).unwrap();
        assert_eq!(cell.len(), 3 * CHUNK);
        assert_eq!(cell.lock().open, 1);
        // The same version is the same cell.
        let again = contents.clean("f1", "v1", 3 * CHUNK, "a.txt", false).unwrap();
        assert!(Arc::ptr_eq(&cell, &again));
        assert_eq!(cell.lock().missing_runs(0, 3 * CHUNK), vec![(0, 3)]);

        let old = contents.make_changed(&cell).unwrap().unwrap();
        assert_eq!(old, clean_key("f1", "v1"));
        assert!(contents.get(&old).is_none());
        assert!(cell.lock().changed);
        // A changed copy is never evicted, also when nobody has it open.
        cell.lock().open = 0;
        assert!(contents.evict(&cell.key()).is_none());

        cell.file.set_len(10).unwrap();
        // A write after the snapshot keeps the change.
        cell.lock().writes = 1;
        assert_eq!(contents.make_clean(&cell, "f1", "v2", 0).unwrap(), None);
        assert!(cell.lock().changed);
        contents.make_clean(&cell, "f1", "v2", 1).unwrap().unwrap();
        let state = cell.lock().clone();
        assert_eq!(state.key, clean_key("f1", "v2"));
        assert!(state.is_complete());
        assert_eq!(state.remote_len, 10);
        assert_eq!(contents.evict(&state.key), Some(10));
        assert!(contents.get(&state.key).is_none());
    }

    #[test]
    fn rows_without_files_and_files_without_rows_are_cleaned() {
        let dir = tempfile::tempdir().unwrap();
        let folder = dir.path().join("content");
        std::fs::create_dir_all(&folder).unwrap();
        std::fs::write(folder.join("w-stray"), b"x").unwrap();
        std::fs::write(folder.join("c-a-b"), b"x").unwrap();
        let row = |key: &str| ContentRow {
            key: key.into(),
            file_id: "a".into(),
            version: "b".into(),
            remote_len: 1,
            chunks: vec![1],
            last_used: 0,
            changed: 0,
            path: "x".into(),
        };
        let (contents, lost) = Contents::load(folder.clone(), vec![row("c-a-b"), row("c-gone")]).unwrap();
        assert_eq!(lost, vec!["c-gone".to_string()]);
        assert!(contents.get("c-a-b").is_some());
        assert!(!folder.join("w-stray").exists());
    }
}

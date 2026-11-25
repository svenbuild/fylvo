//! Which parts of a cached file are loaded: one bit per 1 MiB chunk.

/// Size of one chunk.
pub(crate) const CHUNK: u64 = 1 << 20;
/// Read-ahead after the end of a read that has to load from the server.
pub(crate) const READ_AHEAD: u64 = 4 * CHUNK;

#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub(crate) struct ChunkMap {
    bits: Vec<u8>,
}

impl ChunkMap {
    pub(crate) fn from_bytes(bytes: Vec<u8>) -> Self {
        Self { bits: bytes }
    }

    pub(crate) fn as_bytes(&self) -> &[u8] {
        &self.bits
    }

    pub(crate) fn get(&self, chunk: u64) -> bool {
        let (byte, bit) = ((chunk / 8) as usize, chunk % 8);
        self.bits.get(byte).is_some_and(|b| b & (1 << bit) != 0)
    }

    pub(crate) fn set(&mut self, chunk: u64) {
        let (byte, bit) = ((chunk / 8) as usize, chunk % 8);
        if self.bits.len() <= byte {
            self.bits.resize(byte + 1, 0);
        }
        self.bits[byte] |= 1 << bit;
    }

    /// Mark chunks `first..last` as loaded.
    pub(crate) fn set_range(&mut self, first: u64, last: u64) {
        for chunk in first..last {
            self.set(chunk);
        }
    }

    pub(crate) fn clear(&mut self) {
        self.bits.clear();
    }

    /// Number of loaded chunks below `limit` (in chunks).
    pub(crate) fn count_below(&self, limit: u64) -> u64 {
        (0..limit).filter(|c| self.get(*c)).count() as u64
    }

    /// Runs of missing chunks `[first, last)` among `first..last`.
    pub(crate) fn missing_runs(&self, first: u64, last: u64) -> Vec<(u64, u64)> {
        let mut runs = Vec::new();
        let mut start = None;
        for chunk in first..last {
            match (self.get(chunk), start) {
                (false, None) => start = Some(chunk),
                (true, Some(s)) => {
                    runs.push((s, chunk));
                    start = None;
                }
                _ => {}
            }
        }
        if let Some(s) = start {
            runs.push((s, last));
        }
        runs
    }
}

/// Number of chunks needed for `len` bytes.
pub(crate) fn chunk_count(len: u64) -> u64 {
    len.div_ceil(CHUNK)
}

/// The chunks that hold bytes `start..end` (`end` exclusive), as `first..last`.
pub(crate) fn chunks_of(start: u64, end: u64) -> (u64, u64) {
    if end <= start {
        return (start / CHUNK, start / CHUNK);
    }
    (start / CHUNK, end.div_ceil(CHUNK))
}

/// The chunks a write of `start..end` overlaps without covering them fully (only those must be
/// loaded first; fully overwritten chunks need nothing from the server).
pub(crate) fn partly_written(start: u64, end: u64, remote_len: u64) -> Vec<u64> {
    let (first, last) = chunks_of(start, end.min(remote_len));
    (first..last)
        .filter(|chunk| {
            let chunk_start = chunk * CHUNK;
            let chunk_end = ((chunk + 1) * CHUNK).min(remote_len);
            start > chunk_start || end < chunk_end
        })
        .collect()
}

/// Whether a `Content-Range` header answers a request for bytes `start..=end` of a file of
/// `total` bytes.
pub(crate) fn content_range_matches(header: &str, start: u64, end: u64, total: u64) -> bool {
    let Some(rest) = header.trim().strip_prefix("bytes ") else {
        return false;
    };
    let Some((range, size)) = rest.split_once('/') else {
        return false;
    };
    let Some((first, last)) = range.split_once('-') else {
        return false;
    };
    first.trim().parse::<u64>().ok() == Some(start)
        && last.trim().parse::<u64>().ok() == Some(end)
        && (size.trim() == "*" || size.trim().parse::<u64>().ok() == Some(total))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn missing_chunks_come_in_runs() {
        let mut map = ChunkMap::default();
        map.set(1);
        map.set_range(4, 6);
        assert_eq!(map.missing_runs(0, 8), vec![(0, 1), (2, 4), (6, 8)]);
        assert_eq!(map.missing_runs(4, 6), vec![]);
        assert_eq!(map.count_below(8), 3);
        let copy = ChunkMap::from_bytes(map.as_bytes().to_vec());
        assert_eq!(copy, map);
    }

    #[test]
    fn chunks_of_a_range() {
        assert_eq!(chunks_of(0, 1), (0, 1));
        assert_eq!(chunks_of(CHUNK - 1, CHUNK + 1), (0, 2));
        assert_eq!(chunks_of(CHUNK, 2 * CHUNK), (1, 2));
        assert_eq!(chunk_count(0), 0);
        assert_eq!(chunk_count(CHUNK + 1), 2);
    }

    #[test]
    fn only_partly_written_chunks_need_loading() {
        let remote = 3 * CHUNK + 10;
        // Covers chunk 1 fully, chunk 0 and 2 partly.
        assert_eq!(partly_written(CHUNK - 5, 2 * CHUNK + 5, remote), vec![0, 2]);
        // A write at the start of the last, short chunk that covers its server part fully.
        assert_eq!(partly_written(3 * CHUNK, 3 * CHUNK + 20, remote), Vec::<u64>::new());
        // Beyond the server's bytes nothing is loaded.
        assert_eq!(partly_written(5 * CHUNK, 5 * CHUNK + 1, remote), Vec::<u64>::new());
    }

    #[test]
    fn content_range_must_match_the_request() {
        assert!(content_range_matches("bytes 0-99/1000", 0, 99, 1000));
        assert!(content_range_matches("bytes 100-199/*", 100, 199, 1000));
        assert!(!content_range_matches("bytes 0-999/1000", 0, 99, 1000));
        assert!(!content_range_matches("bytes 0-99/2000", 0, 99, 1000));
        assert!(!content_range_matches("0-99/1000", 0, 99, 1000));
    }
}

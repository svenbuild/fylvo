# Fylvo Desktop for Linux: virtual drive (files on demand)

Status: revision 3 (after rat rounds 1 and 2), built 2026-11-25 (see "As built" at the end).
Branch `fylvo/linux-virtual-drive`. User guide: `desktop/LINUX.md`.

## Goal

Next to the full folder sync (unchanged), a drive can be added as a **virtual drive**: the app
mounts the server folder at a local folder (for example `~/Fylvo`) with FUSE. Names, sizes and
dates show at once; content is loaded when a program reads it and kept in a cache with a size
limit. Folders can be marked "always offline". Without a connection only cached and offline
files are there. Linux has nothing built in for placeholders (the fanotify pre-content hooks of
kernel 6.14 are unfinished and not used).

Out of scope: Windows (has placeholders), changing the mode of an existing drive (remove and
add again), status emblems, thumbnails from the server, symlinks, hard links, xattrs
(`ENOTSUP` at once), a stopped app process (`SIGSTOP`: like any FUSE file system, callers wait
until it continues; kernels from 6.14 can set a FUSE request timeout).

## Building blocks

- Crate `fuser` 0.19 (pure Rust, no libfuse; mounts through `fusermount3` of the `fuse3`
  package, which the rpm and deb now depend on). Linux only.
- Module `crates/cloudreve-sync/src/vfs/` (Linux only): the `fuser::Filesystem`, the known tree
  (inodes), the content store (chunk map, eviction), write back and conflicts, pins, caller
  detection, request deadlines.
- Store per drive: `~/.fylvo/drives/<drive id>/` with `index.db` (SQLite through the `diesel`
  the inventory already uses, own file, raw SQL), `content/` (cached and changed files),
  `staging/` (upload snapshots). App data, not `~/.cache`: changed files must survive.

## Integration with the drive manager

- `DriveConfig` gets `mode: "sync" | "drive"` (serde default `sync`). A drive-mode `Mount` is
  created as today (credentials and refresh, capacity, UI lists), but `start()` mounts the FUSE
  file system instead of the folder checks and the watcher; the manager does not start the
  remote event processor for it (the drive subscribes to server events itself). The command
  processor drops every sync command for a drive-mode mount (debug log): the sync engine never
  walks a FUSE mount.
- Adding a drive or sync folder is refused when it lies inside another drive or sync folder or
  contains one (both directions, all modes). The sync walker already skips mount points.
- **Finding the drive of a path** (`--pin`, "View online", notifications) compares drive-mode
  mount roots as plain path prefixes (component-wise), never `canonicalize()` (which would
  enter the mount). "View online" takes file or folder from `index.db`.
- `has_pending_work` (updater gate) is true while a drive has changed files not uploaded yet
  or files open for writing.
- **Removing a drive:** unmount first (`-u -z`), wait for the session to end (no write can come
  in after that), then copy changes not uploaded (not the temp names) to
  `~/Fylvo not uploaded/<drive name>/<path>` and say so, then delete the store. The mount stays
  registered until the unmount succeeded.

## Requests never block, never hang

- Every request that may wait (listing, content, upload, server change, SQLite) is answered
  from a tokio task; fuser replies may be sent from any thread.
- **Deadlines:** each waiting request is registered with an absolute deadline (metadata 20 s,
  content 60 s). A watchdog thread (not tokio) answers `EIO` to every overdue request; whoever
  takes the reply first answers, the other side does nothing. So even a stuck task or lock
  cannot freeze a caller longer than its deadline. Work is bounded (4 parallel content fetches
  and 8 metadata requests per drive); a server change whose request already expired is not
  started. A server change that timed out is reconciled by listing again.
- **Offline:** a failed request triggers a probe (`/api/v4/site/ping`, 5 s). If the probe fails
  the drive is offline: requests that need the server fail at once (`ENETUNREACH`) until a
  probe every 30 s succeeds. `statfs` answers from the stored quota, never waits.
- **Crash:** when the app process ends in any way, the kernel ends the connection, callers get
  `ENOTCONN` at once (no hang). A small helper process (`sh`, reading a pipe the app holds,
  started with `setsid`, the mount point as an argument) runs `fusermount3 -u -z "$1"` when the
  pipe closes, so no dead mount is left. (fuser's `auto_unmount` needs `user_allow_other` in
  `/etc/fuse.conf`, off by default, so it is not used.) At start a dead mount at the mount
  point is removed with `fusermount3 -u -z` too.
- Quit and drive removal unmount with `-u -z` (a terminal in the folder would make a plain
  unmount fail); open readers then get `ENOTCONN`.

## Reading

- **Listing:** `list_files_all` (all pages) of a folder, with one shared request per folder
  (single flight). Kept in memory and in `index.db`. A listing older than 60 s is answered at
  once and refreshed in the background. Server events mark folders stale.
- **Lookup** (`cd`, `stat`, `open` may come before `readdir`): answered from the parent's
  listing; a parent never listed is listed first (single flight, same deadline). A name missing
  from a fresh listing is `ENOENT` at once (programs probe many names). Offline with an unknown
  parent: `ENETUNREACH`.
- **Kernel cache:** entry, attribute and negative entry TTL 1 s; `keep_cache` on open only if
  the version is the one already cached; `notify_inval_inode` when a file's version changes,
  sent from one dedicated thread. Each open handle is bound to one content generation.
- **Changed and local-only files** take size and mtime from the local copy; listings and server
  events never replace them.
- **Content:** a cached file is `content/<file id>-<version>` (sparse) plus a map of loaded
  1 MiB chunks. Missing chunks are fetched with an HTTP `Range` request, with a fixed 4 MiB
  read-ahead. The download URL is always requested **for that version** (`entity`, as
  `tasks/download.rs` does) and reused until its `expires`; the answer must be `206` with the
  matching `Content-Range` and length, else nothing is marked loaded. Two reads of the same
  chunk share one request. When the server says the version is gone, the copy is stale. A
  pinned file keeps its last complete version until the new one is complete.
- **Attributes:** size and mtime from the server, files `0644`, folders `0755`, owner the user.

## Writing (write back after close)

Writes go to the local copy and are uploaded after the file is closed (as rclone's write cache,
onedriver).

- **Open** for writing loads nothing. A write loads only the chunks it partly covers; the rest
  is filled in the background before the upload snapshot. `O_TRUNC`, truncate and new files
  need nothing.
- **Durable:** at the first write the file is recorded in `index.db` as changed and open
  (path, file id, base version), before the write is answered. `fsync` syncs the content file
  and the record. Local write errors are answered to `write`/`flush` (a `release` error cannot
  reach `close()`).
- **Upload:** 2 s after the last handle closes (a new open restarts the wait). The content is
  first copied to `staging/<uuid>` (`std::fs::copy`, a reflink on btrfs/xfs) together with a
  change counter, under the file's write lock (no write can land half in the snapshot); the
  upload reads only the snapshot (a unique path, so no stale upload session can be reused).
  After the upload, the existing `upload_is_current` check confirms that the returned version
  is current; it becomes the base version. If the change counter moved meanwhile, the file
  stays changed (with the new base) and uploads again. Before a retry, an upload session left
  for that snapshot in the inventory is compared with the server's current version: if the
  lost answer was a success, it is taken as such (no conflict copy of our own upload).
  Leftover snapshots and their session rows are removed at start.
  - Existing file: `overwrite=true`, `previous_version` = base version.
  - New file: `overwrite=false`.
  - `StaleVersion` (someone else saved) or `ObjectExisted` (someone created that name): the
    local content is uploaded as a conflict copy (the sync's conflict naming,
    `overwrite=false`), the file shows the server version, a notification says so.
  - The server file was deleted meanwhile: the server creates it new (the client sends no
    `previous_required`).
  - Network failure: retried 1, 2, 5, 10, 30 min, and when the drive is online again.
- **Crash while open:** at start a file recorded as "changed and open" is uploaded as a
  conflict copy ("recovered"), never over the server file; the file shows the server version.
- **Changed files are never evicted** and survive restarts and updates.
- **Disk reserve:** if free space on the store's disk is below 2 GB (snapshot copies included),
  writes and new fetches answer `ENOSPC` after evicting what can go.
- **Local-only names** (never uploaded, persisted in `index.db`, survive restarts): a short
  built-in list of editor temp names only: `.goutputstream-*`, `.~lock.*#`, `.*.sw?`,
  `*.kate-swp`, `.#*`, `4913`, `lu*.tmp`. Not `*~` or `*.part` (a normal file of that name must
  not stay local forever). The drive's own ignore patterns are not used in drive mode.
- **Trash:** creating `.Trash` or `.Trash-<uid>` at the mount root is refused (`EPERM`), so the
  file manager offers to delete; deletion goes to the server trash.
- **Rename / move:**
  1. Local-only source (new, not uploaded, or temp name): local rename. If the target is a
     server file, the target keeps its server identity and gets the new content (changed, a
     new version on upload). This is the save of gedit, LibreOffice, Kate.
  2. Server file or folder to an unused name: a rename in the same folder, or a move keeping
     the name, on the server, after checking that the server file ID at the source path is
     still the one in the tree (the same millisecond gap as the sync's documented limit; the
     server has no conditional rename). A move with a new name answers `EXDEV` (`mv` then
     copies and deletes). A timeout re-lists both parents to find out what happened.
  3. Server source onto an existing target: `EEXIST` (no trash-first; "move and replace" in a
     file manager fails, documented).
  4. A file whose upload is running (also a new file's first upload) is renamed or deleted only
     after the upload finished (within the deadline, else `EBUSY`), then as case 2.

  Vim/neovim save by renaming `file` to `file~`: that works (rename, new upload, delete), but
  the new content is a new file on the server, not a version (documented: `backupcopy=yes`).
- **Delete:** server trash after the same ID check; local-only: dropped. **mkdir:** on the
  server at once.
- **Offline:** reading cached files, editing them, new files and case 1 work. Changes of the
  server tree (case 2, delete of server entries, mkdir) answer `ENETUNREACH` at once.
- **chmod, chown, utimens:** accepted and ignored; a new mtime of a changed file is sent with
  the upload.
- **Read-only drives** (the existing read-only state of the mount, once known): open for
  writing, create, truncate, rename, delete and mkdir answer `EROFS`. Before it is known,
  changes are accepted and a refused upload shows as a failed change. Server permission errors
  map to `EACCES`.

## Cache

- One limit for all drives together, default 20 GB, 1 to 1000 GB, general settings (Linux).
  Counted: loaded chunks of files that are neither pinned nor changed.
- Eviction: least recently used first; open, changed and pinned files are never evicted. Runs
  before a fetch would cross the limit. The limit is soft for open files: they may exceed it
  and are evicted after closing; the hard stop is the disk reserve.

## Always offline

- File manager entries (as "View online" today): "Fylvo: Always keep on this device" and
  "Fylvo: Free up space", calling `fylvo-desktop --pin <path>` / `--unpin <path>`.
- A pinned folder or file is loaded fully in the background and kept current at start, after
  server events and after the event subscription reconnects. "Ready" only when all is loaded.
- Settings list the pinned folders per drive with a remove button. Pinned content does not
  count against the limit and is never evicted.

## Programs that read everything

- The caller's process (`Tgid` from `/proc/<tid>/status`, then `comm` and the command line) is
  checked: `tracker-*`, `localsearch-*`, `baloo_file*`, thumbnailers (`gdk-pixbuf-thum*`,
  `totem-video-thu*`, `ffmpegthumbnail*`, `evince-thumbnai*`, `gnome-*-thumbnailer`, KDE
  `kioworker` with `thumbnail` in its command line). For them: reading a file that is not fully
  cached and listing a folder never listed fail at once (`EACCES`). Best effort.
- Consequence (documented): no thumbnails for files not cached; GNOME remembers a failed
  thumbnail, so it may not appear later either.
- Uncached reads are limited to 4 parallel fetches per drive, so an unknown scanner cannot
  flood the connection.
- File system type `fuse.fylvo`, `fsname` `fylvo`.

## UI

- Add drive (Linux): "Keep a full copy (sync folder)" or "Load files when opened (drive)".
  Drive: suggested folder `~/Fylvo` (empty or new; created).
- Settings per drive: mode, cache in use, pinned folders and size, changes waiting (and
  failed), "Free up space".
- Tray: "N changes waiting" while uploads are pending.
- General settings (Linux): cache limit in GB. Texts German and English. `LINUX.md` section.

## Tests

- Unit (Linux): chunk map and range checks (206, `Content-Range`, length), eviction order and
  protection, local-only names, caller detection, rename cases 1 to 4, deadline watchdog,
  conflict names, crash recovery records, store round trip, overlap check.
- End to end (ignored by default, as `server_e2e_linux.rs`), WSL Ubuntu with `/dev/fuse`,
  local Fylvo server: mount, `ls`, `stat` before `readdir`, `cat`, partial read loads only its
  range, new file and edit uploaded (versions on the server), gedit-style save keeps one file
  with versions, mkdir/rename/delete, conflict copy (server changed meanwhile), write during
  upload (snapshot), first write into a big uncached file, pin loads all, small limit evicts,
  server stopped: cached read works, uncached fails at once, `ls` answers at once; offline edit
  uploaded later; app killed: no dead mount, restart mounts again; changed-and-open file after
  kill becomes a recovered copy; drive removal with waiting changes keeps them.
- Windows: `cargo test` and `cargo check` unchanged (only the `mode` field and the overlap
  check are shared).

## As built (2026-11-25)

Deviations and additions, decided while building and testing:

- **Setup in one click:** on Linux the add dialog has the drive mode selected and the folder
  `~/Fylvo` (or `~/Fylvo 2`, ...) filled in; after sign-in only "Finish" is left. The first start
  without drives opens the dialog by itself (as before). Settings show "Also as drive" while only
  sync folders exist. The mount point is added to the GTK bookmarks (side bar of GNOME Files).
- **Speed:** the mount never waits for the server (recorded changes come back from the store);
  the top folder and up to 16 subfolders of every listed folder are listed ahead; listings are
  answered from memory with `readdirplus` (attributes in the same answer); reads answer as soon
  as their own chunks are there and read ahead 8 MiB in the background (also while reading from
  the cache, so streaming does not stop); kernel read-ahead and writes 1 MiB.
- **Listings vs. local changes:** each folder listing has a generation counter; a server listing
  asked for before a local change of that folder is dropped (it would undo the change).
- **Rename over a file that is still uploading** waits for that upload (gedit saving twice).
- **Vim:** no identity transfer (`*~` is not a local-only name); documented.
- **Removal** copies unsent changes to `~/Fylvo not uploaded/<drive name>` after unmounting.

Tests: 18 unit tests (`vfs::`), 2 end-to-end tests against a local server with a real FUSE mount
(`vfs/e2e.rs`: reading, partial reads, new files, edits, gedit save, mkdir/rename/move/delete,
`EXDEV` for move plus rename, conflict copy, cache limit, pinned folder, offline listing and
reading, offline edit kept on removal). By hand in WSL Ubuntu (kernel 6.6): `kill -9` of the app
leaves no mount (the helper unmounts), a restart mounts again; with the server stopped
(`SIGSTOP`) listed folders answer in 2 ms, a folder never listed fails after the 20 s deadline.
Speed against a local server: mount 0.4 s, top folder 17 ms, prefetched subfolder 6 ms, 400 files
96 ms the first time and 33 ms after, 200 MB not cached 177 MB/s.

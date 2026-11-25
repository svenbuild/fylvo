# Fylvo Desktop for Linux: virtual drive (files on demand)

Status: plan, revision 2 (after rat round 1), 2026-11-25. Branch `fylvo/linux-virtual-drive`.

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
- Module `crates/cloudreve-sync/src/vfs/` (Linux only): `fs.rs` (the `fuser::Filesystem`),
  `tree.rs` (inodes, known tree), `content.rs` (content store, chunk map, eviction),
  `upload.rs` (write back, conflicts), `pins.rs`, `callers.rs` (indexer detection),
  `deadline.rs` (request deadlines).
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
- Paths from the file manager (`--pin`) are matched to a drive by the stored mount root
  (string prefix), never by `canonicalize()` (which would enter the mount).
- `has_pending_work` (updater gate) is true while a drive has changed files not uploaded yet
  or files open for writing.
- **Removing a drive** with changes not uploaded: the files are moved to
  `~/Fylvo not uploaded/<drive name>/<path>` first, and the app says so. Then unmount, delete
  the store.

## Requests never block, never hang

- Every request that may wait (listing, content, upload, server change, SQLite) is answered
  from a tokio task; fuser replies may be sent from any thread.
- **Deadlines:** each waiting request is registered with an absolute deadline (metadata 20 s,
  content 60 s). A watchdog thread (not tokio) answers `EIO` to every overdue request; whoever
  takes the reply first answers, the other side does nothing. So even a stuck task or lock
  cannot freeze a caller longer than its deadline.
- **Offline:** a failed request triggers a probe (`/api/v4/site/ping`, 5 s). If the probe fails
  the drive is offline: requests that need the server fail at once (`ENETUNREACH`) until a
  probe every 30 s succeeds.
- **Crash:** when the app process ends in any way, the kernel ends the connection, callers get
  `ENOTCONN` at once (no hang). A small helper process (`sh`, reading a pipe the app holds)
  runs `fusermount3 -u -z` when the pipe closes, so no dead mount is left. (fuser's
  `auto_unmount` needs `user_allow_other` in `/etc/fuse.conf`, off by default, so it is not
  used.) At start a dead mount at the mount point is removed with `fusermount3 -u -z` too.
- Quit and drive removal unmount cleanly.

## Reading

- **Listing:** `list_files_all` (all pages) of a folder, with one shared request per folder
  (single flight). Kept in memory and in `index.db`. A listing older than 60 s is answered at
  once and refreshed in the background. Server events mark folders stale.
- **Lookup** (`cd`, `stat`, `open` may come before `readdir`): answered from the parent's
  listing; a parent never listed is listed first (single flight, same deadline). A name missing
  from a fresh listing is `ENOENT` at once (programs probe many names). Offline with an unknown
  parent: `ENETUNREACH`.
- **Kernel cache:** entry, attribute and negative entry TTL 1 s; `keep_cache` on open only if
  the version is the one already cached; `notify_inval_inode` when a file's version changes.
- **Content:** a cached file is `content/<file id>-<version>` (sparse) plus a map of loaded
  1 MiB chunks. Missing chunks are fetched with an HTTP `Range` request, with a fixed 4 MiB
  read-ahead. The download URL is always requested **for that version** (`entity`, as
  `tasks/download.rs` does); the answer must be `206` with the matching `Content-Range` and
  length, else nothing is marked loaded. Two reads of the same chunk share one request. When
  the server says the version is gone, the copy is stale. A pinned file keeps its last complete
  version until the new one is complete.
- **Attributes:** size and mtime from the server, files `0644`, folders `0755`, owner the user.
  `statfs` reports the server quota.

## Writing (write back after close)

Writes go to the local copy and are uploaded after the file is closed (as rclone's write cache,
onedriver).

- **Open** for writing loads nothing yet. The first `write` (or a truncate to a size > 0) of a
  file that is not fully cached loads the whole file first (that caller waits, nobody else);
  `O_TRUNC`, truncate to 0 and new files need nothing.
- **Durable:** at the first write the file is recorded in `index.db` as changed and open
  (path, file id, base version), before the write is answered. `fsync` syncs the content file
  and the record. Local write errors are answered to `write`/`flush` (a `release` error cannot
  reach `close()`).
- **Upload:** 2 s after the last handle closes (a new open restarts the wait). The content is
  first copied to `staging/<uuid>` (`std::fs::copy`, a reflink on btrfs/xfs) together with a
  change counter; the upload reads only the snapshot (a unique path, so no stale upload
  session can be reused). After the upload, the existing `upload_is_current` check confirms
  that the returned version is current; the copy is then the new server version, unless the
  change counter moved (then it uploads again).
  - Existing file: `overwrite=true`, `previous_version` = base version.
  - New file: `overwrite=false`.
  - `StaleVersion` (someone else saved) or `ObjectExisted` (someone created that name): the
    local content is uploaded as a conflict copy (the sync's conflict naming, `overwrite=false`),
    the file shows the server version, a notification says so.
  - The server file was deleted meanwhile (`UploadTargetGone` / not found): uploaded as new.
  - Network failure: retried 1, 2, 5, 10, 30 min, and when the drive is online again.
- **Crash while open:** at start a file recorded as "changed and open" is uploaded as a
  conflict copy ("recovered"), never over the server file; the file shows the server version.
- **Changed files are never evicted** and survive restarts and updates.
- **Disk reserve:** if free space on the store's disk is below 2 GB, writes and new fetches
  answer `ENOSPC` (after evicting what can go).
- **Local-only names** (never uploaded, persisted in `index.db`, survive restarts): a short
  built-in list of editor and download temp names: `.goutputstream-*`, `.~lock.*#`, `.*.sw?`,
  `*.kate-swp`, `.#*`, `4913`, `lu*.tmp`, `*.part`, `*~`, `.fuse_hidden*`. The drive's own
  ignore patterns are not used in drive mode.
- **Trash:** creating `.Trash` or `.Trash-<uid>` at the mount root is refused (`EPERM`), so the
  file manager offers to delete; deletion goes to the server trash.
- **Rename / move:**
  1. Local-only source (new, not uploaded, or temp name): local rename. If the target is a
     server file, the target keeps its server identity and gets the new content (changed, a
     new version on upload). This is the save of gedit, LibreOffice, Kate, browsers (`*.part`).
  2. Server file renamed to a local-only name (vim/neovim `file` -> `file~`): local only; the
     temp name keeps the server identity, the original path is free. A new file at the original
     path then takes that identity (new version on upload). Deleting the temp name afterwards
     is local. Deleting it while no file took its identity, or renaming it to a normal name,
     is the server delete or rename.
  3. Server file or folder to an unused name: server rename/move (folder of target and name),
     after checking that the server file ID at the source path is still the one in the tree.
     A running upload of that file is cancelled and queued again for the new path. A timeout
     re-lists both parents to find out what happened.
  4. Server source onto an existing target (not case 1 or 2): `EEXIST`. No trash-first.
- **Delete:** server trash after the same ID check; local-only: dropped. **mkdir:** on the
  server at once.
- **Offline:** reading cached files, editing them, new files, cases 1 and 2 work. Changes of
  the server tree (cases 3 and 4, delete of server entries, mkdir) answer `ENETUNREACH` at once.
- **chmod, chown, utimens:** accepted and ignored; a new mtime of a changed file is sent with
  the upload.
- **Read-only drives** (the existing read-only state of the mount): writes answer `EROFS` at
  open, before an editor thinks it saved. Server permission errors map to `EACCES`.

## Cache

- One limit for all drives together, default 20 GB, 1 to 1000 GB, general settings (Linux).
  Counted: loaded chunks of files that are neither pinned nor changed.
- Eviction: least recently used first; open, changed and pinned files are never evicted. Runs
  before a fetch would cross the limit. A single file larger than the limit may still be read
  (it is evicted after it is closed).
- Settings per drive: cache in use, pinned size, changes waiting (and failed), "Free up space".

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
- Settings per drive: mode, cache use, pinned folders, waiting changes, "Free up space".
- Tray: "N changes waiting" while uploads are pending.
- General settings (Linux): cache limit in GB. Texts German and English. `LINUX.md` section.

## Tests

- Unit (Linux): chunk map and range checks (206, `Content-Range`, length), eviction order and
  protection, local-only names, caller detection, rename cases 1 to 4, vim save, deadline
  watchdog, conflict names, crash recovery records, store round trip, overlap check.
- End to end (ignored by default, as `server_e2e_linux.rs`), WSL Ubuntu with `/dev/fuse`,
  local Fylvo server: mount, `ls`, `stat` before `readdir`, `cat`, partial read loads only its
  range, new file and edit uploaded (versions on the server), gedit-style and vim-style save
  keep one file with versions, mkdir/rename/delete, conflict copy (server changed meanwhile),
  write during upload (snapshot), pin loads all, small limit evicts, server stopped: cached
  read works, uncached fails at once, `ls` answers at once; offline edit uploaded later; app
  killed: no dead mount, restart mounts again; changed-and-open file after kill becomes a
  recovered copy; drive removal with waiting changes keeps them.
- Windows: `cargo test` and `cargo check` unchanged (only the `mode` field and the overlap
  check are shared).

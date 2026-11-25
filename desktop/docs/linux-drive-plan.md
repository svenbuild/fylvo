# Fylvo Desktop for Linux: virtual drive (files on demand)

Status: plan, revision 1, 2026-11-25. Branch `fylvo/linux-virtual-drive`.

## Goal

Next to the full folder sync (unchanged), a drive can be added as a **virtual drive**: the app
mounts the server folder at a local folder (for example `~/Fylvo`) with FUSE. Names, sizes and
dates show at once; content is loaded when a program reads it and kept in a cache with a size
limit. Folders can be marked "always offline". Without a connection only cached and offline
files are there. Linux has nothing built in for placeholders (the fanotify pre-content hooks of
kernel 6.14 are unfinished and not used).

Out of scope: Windows (has placeholders), changing the mode of an existing drive (remove and
add again), file manager status emblems, server thumbnails in the file manager.

## Building blocks

- Crate `fuser` without `libfuse` linking (mounts through the `fusermount3` binary of the
  `fuse3` package, present on Fedora, Ubuntu, Debian, openSUSE). Only for Linux.
- Module `crates/cloudreve-sync/src/vfs/` (Linux only):
  - `fs.rs`: the `fuser::Filesystem`. Never blocks its loop: every request that may wait
    (listing, content, upload, server change) is answered from a tokio task (fuser replies can
    be sent from any thread).
  - `tree.rs`: inodes and the known tree (in memory, persisted in `index.db`).
  - `cache.rs`: content cache (sparse files plus a chunk map), eviction.
  - `writeback.rs`: uploads of changed files, conflicts.
  - `pins.rs`: "always offline" folders.
  - `index.db` per drive: SQLite via the `rusqlite` version that shares `libsqlite3-sys 0.28`
    (bundled) with the existing inventory. Tables: `entries` (known tree), `content` (cached
    files), `pins`. Kept apart from the sync inventory on purpose: no planner code sees it.
- Data: `~/.cache/fylvo/drives/<drive id>/` (content, `index.db`). The cache path is not inside
  the mount.

## Integration with the drive manager

`DriveConfig` gets `mode: "sync" | "drive"` (serde default `sync`, so old configs stay sync
drives). A drive-mode `Mount` is created as today (credentials, refresh, props, capacity, UI
lists keep working), but `start()` mounts the FUSE file system instead of checking the folder
and starting the watcher, and the manager does not start the remote event processor for it.
The command processor drops every sync command (`Sync`, `ProcessFsEvents`, `FetchData`,
`FetchPlaceholders`, `Rename`...) for a drive-mode mount with a debug log; that is the safety net
that the sync engine never walks a FUSE mount. `remove_drive` unmounts and deletes the cache.
`shutdown` unmounts.

## Reading

- **Listing:** `list_files_all` of a folder when it is first opened. The result is kept in
  memory and in `entries`; after 60 s a listing is still answered at once from memory and
  refreshed in the background (stale while revalidate). Server events (the existing SSE
  subscription, `subscribe_file_events` on the drive root) mark folders stale. `lookup` never
  asks the server on its own: a name not in a known listing is `ENOENT` at once (programs probe
  many names that do not exist). A folder never listed is listed with a 15 s timeout.
- **Content:** a cached file is a sparse file `content/<file id>` plus a map of loaded 1 MiB
  chunks. A read of missing chunks fetches them with an HTTP `Range` request on the download
  URL (`get_file_url`, kept until it expires, fetched again on 403/410). Sequential reads grow
  a read-ahead up to 16 MiB; a random read fetches only what it needs (a video player seeking,
  a thumbnailer reading a header). Two reads of the same chunk share one request. The cached
  copy belongs to one server version (`primary_entity`); a new version on the server makes the
  old copy stale (dropped once no handle has it open). Timeout: 30 s without progress, then
  `EIO`.
- **Attributes:** size and mtime from the server, files `0644`, folders `0755`, owner the user.
  `statfs` reports the server quota (capacity props) so file managers show free space.

## Writing (write back on close)

Writes go to the cached copy and are uploaded after the file is closed. As rclone's write
cache and onedriver do.

- **Open for writing** (without `O_TRUNC`) loads the whole file first; `O_TRUNC` and new files
  need nothing. The program waits, the file system does not.
- **Close** of the last handle of a changed file queues its upload 2 s later (a new open
  restarts the wait). Uploads use the existing `Uploader` with `overwrite` and
  `previous_version` = the version the copy was made from (empty for new files). Afterwards
  the copy is marked as the new server version.
- **Changed files are never evicted** and survive a restart (`content.dirty`, with the path and
  base version). Upload failures are retried with backoff (1 min, up to 30 min); offline edits
  are uploaded when the connection is back.
- **Conflict** (server answers 40076 `StaleVersion`, someone else saved meanwhile): the local
  content is uploaded as a conflict copy next to it, named like the sync's conflict copies; the
  file itself shows the server version; a notification says so. Server file deleted meanwhile:
  uploaded as new.
- **Temp names** (the Linux ignore list of `drive/ignore.rs`: `.goutputstream-*`, `*~`,
  `.*.swp`, `.~lock.*#`, `lu*.tmp`, `4913`, ...) and the drive's own ignore patterns stay local:
  never uploaded, kept only in memory and cache, dropped at unmount.
- **Rename / move:**
  - A source that is only local (new, not uploaded yet, or a temp name): local rename. Over an
    existing server file the target keeps its server identity and gets the new content
    (dirty): the "write temp, rename over" save of gedit, LibreOffice, Kate works offline
    too and makes a new version, not a new file.
  - A server file or folder: rename/move on the server (with 15 s timeout), then in the tree.
    An existing target is first moved to the server trash (POSIX rename replaces). A pending
    upload follows the new path.
- **Delete:** server trash (files and empty folders); a local-only file is just dropped.
- **mkdir:** created on the server at once.
- **Offline:** reading cached files, editing them, creating new files and the rename-over save
  work; operations that change the server tree (mkdir, delete, rename of server entries) fail
  with `ENETUNREACH` ("Network is unreachable") at once.
- **chmod, chown, utimens:** accepted and ignored (`cp -p` must not fail); a new mtime of a
  changed file is uploaded as its date.
- **Read-only shares:** server permission errors map to `EACCES`.

## Cache

- Limit: one setting for all drives, default 20 GB, 1 to 1000 GB, in the general settings
  (Linux only). Counted: loaded chunks of files that are not pinned and not dirty.
- Eviction: least recently used first, files open right now and dirty or pinned files are
  never evicted. Runs after each fetch that grew the cache, and at start.
- "Free up space" button per drive: drops all evictable content.
- Shown in the settings: used / limit per drive.

## Always offline

- File manager entries (as "View online" today: Nautilus script, Dolphin service menu):
  "Fylvo: Always keep on this device" and "Fylvo: Free up space", calling
  `fylvo-desktop --pin <path>` / `--unpin <path>`, forwarded to the running app.
- A pinned folder (or file) is loaded fully in the background and kept current: after server
  events, at start and every 30 min, changed and new files below it are loaded.
- The settings list the pinned folders per drive with a remove button.
- Pinned content does not count against the limit and is never evicted.

## Programs that read everything

- **Indexers and thumbnailers** (by `/proc/<pid>/comm` and the command line of the caller of
  a FUSE request): `tracker-*`, `localsearch-*`, `baloo_file*`, thumbnailers
  (`gdk-pixbuf-thum*`, `totem-video-thu*`, `ffmpegthumbnail*`, `evince-thumbnai*`,
  `gnome-*-thumbnailer`, KDE `kioworker` running `thumbnail.so`). For them, reading a file that
  is not fully cached fails at once with `EACCES`, so browsing a folder does not load it.
  Cached files work for them (thumbnails appear once a file was opened).
- The mount reports file system type `fuse.fylvo` and `fsname` `fylvo`.

## Never freeze the file manager

- The FUSE loop never waits on the network (see above). Every network wait has a timeout.
- Offline state: after a connection error the drive is offline for 30 s (probe, then again);
  meanwhile requests that need the server fail at once instead of waiting.
- Mount options: `auto_unmount` (`fusermount3` unmounts when the app ends, also after a crash
  or `kill -9`), `default_permissions`, `noatime`, `fsname=fylvo`, `subtype=fylvo`.
- At start a dead mount at the mount point (`ENOTCONN`) is removed with `fusermount3 -u -z`.
- App quit and drive removal unmount; open files then fail with `ENOTCONN` (as any network
  file system).

## UI

- Add drive (Linux): a choice "Keep a full copy (sync folder)" / "Load files when opened
  (drive)". Drive: suggested folder `~/Fylvo` (empty or not existing; created).
- Settings per drive: mode; for drives: cache use, "Free up space", pinned folders.
- General settings (Linux): cache limit in GB.
- Texts German and English. `LINUX.md` gets a short section.

## Tests

- Unit (Linux): chunk map and range planning, read-ahead, eviction order and protection,
  temp-name classification, caller classification, conflict name, rename rules (local-only
  over server file), offline errors, index persistence round trip.
- End to end (ignored by default, like `server_e2e_linux.rs`), against a local Fylvo server in
  WSL Ubuntu with `/dev/fuse`: mount, `ls`, `cat`, partial read of a large file only loads its
  range, new file and edit uploaded (versions on the server), gedit-style save (temp + rename
  over), mkdir/rename/delete, conflict copy, pin folder loads all, small limit evicts, server
  stopped: cached file reads, uncached fails at once, `ls` answers at once; `kill -9` of the
  app leaves no dead mount (auto_unmount); restart with a dead mount mounts again.
- Windows: `cargo test` and `cargo check` unchanged (no Windows code touched besides the
  `mode` field).

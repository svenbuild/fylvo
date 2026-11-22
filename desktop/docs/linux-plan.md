# Fylvo Desktop for Linux: plan

Status: revision 3, 2026-11-22. Branch `fylvo/desktop-linux`.

## Decisions (Sven, 2026-11-22)

- Target: Fedora first, but as many distributions as possible. Desktop unknown: support GNOME and KDE.
- Files: **all local**. A sync folder is a normal folder with full copies (like Nextcloud on Linux). No FUSE, no files on demand.
- Packages: **AppImage** (updates itself silently) plus **.rpm** and **.deb**; for these the app installs updates itself after the system asks for the admin password (pkexec). Built on Ubuntu 22.04 (glibc 2.35).
- File manager: a right-click entry "open online", **no status emblems**. Status is shown by the tray icon and the app window.
- Tests on a real desktop: allowed (VM on D:). Hyper-V is not enabled on the PC and enabling it needs a reboot, so the desktop runs inside an own Fedora WSL distro (GNOME and KDE sessions on a virtual display, screenshots for checking).

## Why not the upstream `feat/linux` branch

It implements FUSE (files on demand), which Sven did not choose, is untested, and predates all Fylvo changes to the core. No code is taken.

## Core idea: same planner, local state from the inventory

`LocalFileInfo` becomes **one plain shared struct** (`exists`, `is_directory`, `file_size`, `last_modified`, `placeholder`, `in_sync`, `partial_on_disk`, `pin`) with today's methods, moved from `cfapi` to `drive/local.rs`. Each platform fills it:

- **Windows:** from the Cloud Files placeholder state, exactly as now. No behavior change.
- **Linux:** from `lstat` plus the inventory row **at exactly this path**. The row keeps the local state it was synced with in metadata key `fylvo:local` = `<inode>:<size>:<mtime ns>` (folders: `<inode>`). No schema migration.

| Method | Linux |
| --- | --- |
| `is_placeholder()` | row at this path with `fylvo:local`, inode matches |
| `in_sync()` | inode, size and mtime match |
| `partial_on_disk()` | false |
| `pinned()` | Pinned |
| `is_folder_populated()` | true for a tracked folder |

A renamed path has no row, so it is never "synced and deleted on the server". Edits in place change size or mtime; saves by rename or rewrite give a new inode. Both are uploaded, as on Windows.

### Write rules for `fylvo:local` (Linux)

`upsert` replaces the whole metadata, so the Linux `CrPlaceholder::commit` applies one rule:

1. A new key comes only from: the stat of a download's temp file (the inode survives the rename), the upload's `started` state (now with inode, via `symlink_metadata`), or a folder the app created or found on disk at that path.
2. Otherwise the key of the existing row is carried over **only if** the current `lstat` still matches it and the server content is unchanged (same etag). If not, the row is written without key, which makes the file "changed here" (upload, or conflict).
3. `commit_inventory_only` writes no key (the file changed during the upload).
4. Linux planner difference: "same etag, other date" on a file in sync is a row update with the key carried over, not a download (Windows keeps its current behavior; the switch is a constant per platform).

## Platform layer

Windows-only code (`cfapi`, `shellext`, `drive_letter`, `callback`, crate `win32_notif`, crates `windows`, `widestring`, `nt-time`) compiles only on Windows. Small OS functions get a Windows and a Linux body behind the same name:

- recycle bin: Windows recycle bin / freedesktop trash (`trash` crate); on Linux the row is removed first (a restored file is new) and put back if the trash move fails;
- notifications: Windows toasts / D-Bus (`notify-rust`), same texts, no action buttons (the app window shows held deletions and conflicts);
- `notify_shell_change`, `update_sync_error_state`: as now / no-op;
- `move_no_replace`: `MoveFileExW` / `renameat2(RENAME_NOREPLACE)`;
- case fold (`ntfs_fold` uses `LCMapStringEx`): portable fold on Linux, tested against `fold_is_like_windows`;
- path to server URI and ignore matching: on Linux `\` is a valid name character; such names are skipped with a notice (the server cannot hold them), never split.

All Windows tests (83) stay green, `build-msix.ps1 -Arch x64` builds. The planner tests use the shared struct and run on both systems.

## Linux sync behavior

**Watcher.** One inotify watcher for all Linux sync folders, events routed by path prefix, so a move between two sync folders arrives as one paired rename. Before any filtering, a bare `Name(From)` (move out, also "Move to Trash") becomes Remove and a bare `Name(To)` becomes Create; then the existing event blockers and the ignore list apply. A queue overflow or rescan event triggers a full sync; hitting the watch limit is shown as an error with the fix. Symlinks are skipped (lstat, walker does not follow, one-time notice); deliberate path swaps by other programs on the same account are out of scope.

**Renames seen after the fact** (`process_fs_modify_name_event`, a separate Linux handler, no event blockers):

| Case | Action |
| --- | --- |
| From has a row, To is new, both in this folder | server rename or move, then `inventory.rename_path`; on refusal (read-only, 40905, other owner) move back with `move_no_replace` and the Windows notice; on "not found" upload To |
| From ignored or without row (gedit `.goutputstream-*`, LibreOffice `lu*.tmp`, own `.~fylvo-dl-*`) | change of To (upload or own download) |
| To ignored, From has a row (vim `file~`, `.Trash-1000` at a mount top) | Remove of From (guards apply; the existing "saved by replacement" grace keeps vim saves safe) |
| To had a row (rename over an existing file) | change of To (upload with `previous`) plus Remove of From |
| From in sync folder A, To in folder B | B writable: Remove in A (server trash, guards) plus upload in B. B read-only: move back and the Windows notice |
| Folder moved in (bare To) | full sync of that folder (uploads the whole tree) |

**Server to PC.**

- New file: download to a temp file in the target folder (`.~fylvo-dl-*`, ignored, leftovers removed at start), set mtime, stat, write the row, then `renameat2(RENAME_NOREPLACE)`. Something appeared meanwhile: temp file dropped, row removed, path planned again. Crash between row and rename: a row without file is downloaded again (a missing file counts as deleted only when the watcher reports it).
- Update of an existing file: the existing `replace_with_backup` order (move aside to `~fylvo-backup-*`, compare with the stored state, rename the new file in without replace, write the row, compare the backup again before removing it; a changed backup becomes a conflict copy). At start, a leftover backup is renamed back if the path is free, else kept as conflict copy, never deleted. Crash between rename and row: the new file looks changed, its upload meets 40076 and a conflict copy with identical content appears (rare, nothing lost).
- Server rename (SSE): Linux moves the local file or folder (`move_no_replace`, then `rename_path`) instead of delete plus create; if that fails, the current delete plus create.
- Server deletion: freedesktop trash, guarded as on Windows. The planned trash action re-reads the local state first and runs only if the file is still in sync.

**Case.** Linux compares paths exactly everywhere (planner, task queue, deletion holds); the fold is used only to predict server collisions. `plan_case` plans every disk twin under its own path and compares only the exact name with the server entry; a case variant is used only when the exact name is missing and exactly one variant exists. The second twin gets 40905 with the existing message and retry limit.

**Adding a drive.** Refused for a non-empty local folder (existing rule) and for filesystems without stable inodes (allowed: ext4, btrfs, xfs, f2fs; refused: vfat, exfat, ntfs-3g fuse, network shares). Warned when the server folder is larger than the free space.

**Ignore list**, Linux only: `.Trash-*`, `.goutputstream-*`, `*~`, `.*.sw?`, `4913`, `*.kate-swp`, `.#*`, `.directory`, `.nfs*`, `.fuse_hidden*`, `.~fylvo-dl-*`, `lu*.tmp` and `*.part` after checking what LibreOffice and KIO really write, `.~lock.*#`.

**App not running.** A rename while off: new path uploaded, old path comes back from the server (a duplicate, nothing lost). A deletion while off comes back, as on Windows. Both noted for Sven.

## App shell (Tauri)

- Tray icon via AppIndicator. Fedora GNOME needs the "AppIndicator" extension; without it, starting the app from the menu opens its window, and the guide names the extension.
- Autostart: `.desktop` file in `~/.config/autostart`, same setting as on Windows.
- `fylvo://`: `x-scheme-handler/fylvo` in the `.desktop` file (rpm, deb) or registered at first start (AppImage).
- Settings hide Windows-only options (drive letter, Explorer).
- File manager: "Open online" for GNOME Files (script) and Dolphin (service menu), installed by the app, calling `fylvo-desktop --view-online <path>`, forwarded to the running app by the single-instance plugin.
- Look and texts as on Windows (German and English).

## Updates

Built on the shared crate `desktop/crates/fylvo-update` and server API from the Windows update thread (`/api/v4/fylvo/desktop/linux-x64/latest`), once that is in `main`. Linux part only:

- AppImage: download next to `$APPIMAGE`, verify (sha256 plus the authenticity check the shared crate adopts), make executable, rename over the old file, restart when no sync runs, notify afterwards.
- rpm/deb: download, verify, `pkexec dnf install` / `pkexec apt install` of the file when no sync runs, restart, notify. The app knows its format from a build flag.
- Publish: `scripts/publish-desktop.sh <file> linux-x64`; the server holds one file per platform, so `linux-x64` gets the AppImage and the rpm/deb need their own platform names (`linux-x64-rpm`, `linux-x64-deb`), to agree with the Windows thread.

## Stages (each runnable and tested on its own)

1. **Build setup** (done): own WSL distro `FylvoBuild` (Ubuntu 22.04) on `D:\FylvoLinux`, Rust, Node, Tauri dependencies. Windows baseline 83 tests green.
2. **Shared `LocalFileInfo` and cfg gating; compiles and tests on Linux.** No Linux sync yet. Windows 83 tests green; the planner tests run on both.
3. **Linux sync, server to PC only.** No server writes at all (no uploads, no watcher deletes or renames). Mount start, write rules, new-file download, update with backup, server rename, server deletion to trash, case lookup, symlinks, ignore list, overflow rescan, filesystem and free space checks. Tests against a local Fylvo server in WSL (own data dir, port 5290, ext4). rat review.
4. **Linux sync, both ways.** Shared watcher, rename table above, uploads with inode, mass deletion both ways, read-only, conflicts, 40905 twins. Tests: rename and folder move during a sync pass, refused rename, Move to Trash, folder moved in, move between two sync folders (writable and read-only), case twins, symlink, gedit and LibreOffice save, update while the file is open, restart. rat review.
5. **App on Linux:** tray, window, autostart, deep link, notifications, file manager entry, Windows-only settings hidden. Checked in a Fedora WSL distro with GNOME and KDE sessions.
6. **Packages:** AppImage, rpm, deb. Early checks: blank WebKit window on new Mesa (`WEBKIT_DISABLE_DMABUF_RENDERER=1`), libfuse2 for the AppImage runtime. Install test on Fedora 44 and Ubuntu.
7. **Updates**, after the Windows thread's crate is in `main`. rat review.
8. **Manual test pass** and report with install guide and manual tests for Sven.

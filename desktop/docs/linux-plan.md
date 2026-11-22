# Fylvo Desktop for Linux: plan

Status: revision 2, 2026-11-22. Branch `fylvo/desktop-linux`.

## Decisions (Sven, 2026-11-22)

- Target: Fedora first, but as many distributions as possible. Desktop unknown: support GNOME and KDE.
- Files: **all local**. A sync folder is a normal folder with full copies (like Nextcloud on Linux). No FUSE, no files on demand.
- Packages: **AppImage** (runs everywhere, updates itself) plus **.deb** and **.rpm** (Fedora). Built on Ubuntu 22.04 (glibc 2.35).
- File manager: a right-click entry "open online", **no status emblems**. Status is shown by the tray icon and the app window.

## Why not the upstream `feat/linux` branch

It implements FUSE (files on demand), which Sven did not choose, is untested, and predates all Fylvo changes to the core. No code is taken.

## Core idea: same planner, local state from the inventory

The planner (`drive/sync.rs`) decides by `LocalFileInfo`. It becomes **one plain shared struct** (`exists`, `is_directory`, `file_size`, `last_modified`, `placeholder`, `in_sync`, `partial_on_disk`, `pin`) with the same methods as today, moved out of `cfapi` into `drive/local.rs`. Each platform fills it:

- **Windows:** from the Cloud Files placeholder state, exactly as now (`cfapi::placeholder` builds it). No behavior change.
- **Linux:** from `lstat` plus the inventory row **at exactly this path**. The row stores the local state it was synced with as metadata key `fylvo:local` = `<inode>:<size>:<mtime ns>` (folders: `<inode>`). No schema migration.

| Method | Linux |
| --- | --- |
| `is_placeholder()` | a row at this path with `fylvo:local` whose inode matches |
| `in_sync()` | inode, size and mtime match |
| `partial_on_disk()` | false |
| `pinned()` | Pinned (server changes are downloaded) |
| `is_folder_populated()` | true for a tracked folder |

Consequences, all matching the Windows semantics:

- Edit in place: size or mtime differ, not in sync, uploaded.
- Save by rename or delete and rewrite (LibreOffice, gedit, vim): new inode, "not a placeholder", the path Windows already handles.
- A renamed path has no row: it is never "synced and deleted on the server", so nothing is trashed by mistake. After a server rename, `inventory.rename_path` moves the rows of the whole subtree.
- A file restored from the trash has no row (rows are deleted with the file): uploaded as new.
- `fylvo:local` is written only where the content is known to equal the server's: after a download (stat of the temp file before it is renamed into place, inode survives the rename), after an upload (the state recorded when the upload started, `upload.rs` `started`; nothing touches mtime afterwards), when a folder is created or confirmed.

The inventory is one database; the Linux side reads it through a process-wide handle set at start. Symlinks: `lstat` everywhere on Linux, symlinks are skipped with a one-time notice (no following, no loops).

## Platform layer

Windows-only code (`cfapi`, `shellext`, `drive_letter`, `callback`, crate `win32_notif`, the `windows`, `widestring`, `nt-time` crates) compiles only on Windows. Small OS functions get a Windows and a Linux body behind the same name, in `drive/utils.rs` or a new `platform` file where the Windows body is long:

- recycle bin: Windows recycle bin / freedesktop trash (`trash` crate), the inventory row is removed first so a restored file is new;
- notifications: Windows toasts / D-Bus notifications (`notify-rust`), same texts, no action buttons (the app window shows held deletions and conflicts);
- `notify_shell_change`, `update_sync_error_state`: Windows as now / no-op;
- `move_no_replace`: `MoveFileExW` / `renameat2(RENAME_NOREPLACE)`;
- case fold (`ntfs_fold` uses `LCMapStringEx`): a portable fold on Linux, tested against the existing `fold_is_like_windows` cases;
- `CrPlaceholder::commit` and `delete_placeholder`: the cfapi part stays for Windows; on Linux commit = `mkdir` for a new folder plus the row with `fylvo:local`.

Windows behavior does not change: same code paths, only moved behind the same names. All Windows tests (83) stay green, `build-msix.ps1 -Arch x64` still builds. The planner tests build the shared struct and run on both systems.

## Linux sync behavior

- **New file from the server.** Windows creates an online-only placeholder; Linux downloads. A new download path in `tasks/download.rs`: write to a temp file in the target folder (name `.~fylvo-dl-*`, ignored, leftovers removed at start), set mtime, stat, write the row with that state, then `renameat2(RENAME_NOREPLACE)` into place. If something appeared at the path meanwhile, the temp file is dropped, the row removed and the path planned again. A crash between row and rename leaves a row without file: the next pass sees "on the server, missing here" and downloads again (a missing file is only taken as a local deletion when the watcher reports it).
- **Server update of an existing file.** The existing `replace_with_backup` order: move the file aside (`~fylvo-backup-*`), compare it with the stored state, rename the temp file in, write the row, keep a changed backup as conflict copy. A crash between rename and row makes the new file look changed; it is uploaded with `previous` and the server keeps one identical extra version (rare, nothing lost).
- **Renames and moves** are seen after the fact (`process_fs_modify_name_event`). A separate Linux handler: server rename or move, then `inventory.rename_path`; no event blockers (inotify never sends the Remove/Create they wait for). If the server refuses (read-only, 40905, other owner), the file is moved back with `move_no_replace` and the Windows notice is shown: the Windows veto, after the fact.
- **Unmatched moves.** inotify reports a move out of the folder (also "Move to Trash" in GNOME Files and Dolphin) as a bare `Name(From)`, and a move in as a bare `Name(To)`. On Linux these become Remove and Create, so the mass deletion guard and the server trash apply. Before a bare From is treated as a deletion it waits 3 seconds; if a bare To with the same inode arrives in another sync folder meanwhile, it is a move between two sync folders: the file is moved back and the Windows notice ("copy instead") is shown, same guarantee as Windows.
- **Case.** The exact name always wins. A case variant on disk is used only when the exact name is missing and exactly one variant exists. Local twins (`a.txt` and `A.txt`) are each planned under their own path; the server takes one, the other gets 40905 with the existing message and retry limit.
- **Deletion from the server:** the file goes to the freedesktop trash, guarded as on Windows.
- **Watcher limits.** A queue overflow or rescan event triggers a full sync of the drive; hitting the inotify watch limit is shown as an error with the fix.
- **Adding a drive:** refused for a non-empty local folder (existing rule), warned when the server folder is larger than the free disk space.
- **Ignore list** gains Linux names: `.Trash-*`, `.goutputstream-*`, `*~`, `.*.sw?`, `4913`, `*.kate-swp`, `.#*`, `.directory`, `.nfs*`, `.~fylvo-dl-*`, `.~lock.*#` (LibreOffice lock files, if not covered yet).
- **App not running.** A rename while the app is off gives a new upload at the new path and the old path comes back from the server (a duplicate, nothing lost). A deletion while off comes back, as on Windows. Both noted for Sven.

## App shell (Tauri)

- Tray icon via AppIndicator. Fedora GNOME has no tray without the "AppIndicator" extension: then starting the app from the menu opens its window, and the guide names the extension.
- Autostart: `.desktop` file in `~/.config/autostart`, same setting as on Windows.
- `fylvo://`: `x-scheme-handler/fylvo` in the `.desktop` file (deb, rpm) or registered at first start (AppImage).
- Settings hide Windows-only options (drive letter, Explorer integration).
- File manager: "Open online" for GNOME Files (script) and Dolphin (service menu), installed by the app, calling `fylvo-desktop --view-online <path>`, forwarded to the running app by the single-instance plugin.
- Look and texts as on Windows (German and English).

## Updates

On top of the shared crate `desktop/crates/fylvo-update` and the server API from the Windows update thread (`/api/v4/fylvo/desktop/linux-x64/latest`). Linux part only:

- **AppImage:** download next to `$APPIMAGE`, verify (sha256 and whatever authenticity check the shared crate adopts; proposal to the Windows thread: one ed25519 signature for all platforms), make executable, rename over the old file, restart when no sync runs, notify afterwards.
- **.deb / .rpm:** see open question 1.
- Publish: `scripts/publish-desktop.sh <file> linux-x64` with a Linux check (ELF with AppImage magic, embedded `.desktop` with our id).

## Stages (each runnable and tested on its own)

1. **Build setup:** own WSL distro `FylvoBuild` (Ubuntu 22.04) on `D:\FylvoLinux`, Rust, Node, Tauri dependencies. Windows baseline: 83 tests green.
2. **Shared `LocalFileInfo` and cfg gating; compiles and tests on Linux.** No Linux sync yet. Windows: 83 tests green plus the planner tests on the shared struct; Linux: the same planner tests plus inventory state tests.
3. **Linux sync, server to PC (read-only mirror).** Mount start without cfapi, new-file download, update with backup, server deletion to trash, case lookup, symlinks skipped, ignore list, overflow rescan, free space warning. Uploads switched off. Tests against a local Fylvo server in WSL (own data dir, port 5290, ext4 inside WSL, not `/mnt/c`). rat review.
4. **Linux sync, both ways.** Uploads, renames after the fact with move back on refusal, unmatched moves, move between sync folders, mass deletion both ways, read-only, conflicts, 40905 twins. Tests: rename, folder rename and move during a sync pass, refused rename (read-only, 40905), Move to Trash from the file manager, folder moved in, case twins, symlink, update while the file is open, LibreOffice save, restart. rat review.
5. **App on Linux:** tray, window, autostart, deep link, notifications, file manager entry, Windows-only settings hidden. Check in WSLg, then tray, menu and autostart in a desktop VM (see question 2).
6. **Packages:** AppImage, .rpm, .deb. Early checks: blank WebKit window on new Mesa (`WEBKIT_DISABLE_DMABUF_RENDERER=1`), libfuse2 for the AppImage runtime. Install test on Fedora 44 and Ubuntu.
7. **Updates** on `fylvo-update` once it is in `main`. rat review.
8. **Manual test pass** on real Linux, report with install guide and manual tests for Sven.

## Open questions for Sven

1. Updates for .deb/.rpm: (a) the app only says "new version, download here", or (b) the app installs it after asking for the admin password. Recommendation: (a); the AppImage updates itself silently.
2. A desktop VM for the tests (Fedora Workstation in Hyper-V, about 3 GB download, about 25 GB on D:, deleted at the end)?

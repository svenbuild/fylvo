# Fylvo Desktop for Linux: plan

Status: draft, 2026-11-22. Branch `fylvo/desktop-linux`.

## Decisions (Sven, 2026-11-22)

- Target: Fedora first, but as many distributions as possible. Desktop unknown: support GNOME and KDE.
- Files: **all local**. A sync folder is a normal folder with full copies (like Nextcloud on Linux). No FUSE, no files on demand.
- Packages: **AppImage** (runs everywhere, updates itself) plus **.deb** and **.rpm** (Fedora). Built on Ubuntu 22.04 (glibc 2.35) so it runs on older systems too.
- File manager: a right-click menu (open online, sync now), **no status emblems** on files. Status is shown by the tray icon and the app window.

## Why not the upstream `feat/linux` branch

It implements FUSE (files on demand), which Sven did not choose, is untested, and is based on the core before all Fylvo changes (guard, case, conditional uploads). We take ideas only (the cfg layout of the dependencies), no code.

## Core idea: same planner, different "local state"

The shared planner (`drive/sync.rs`) decides by `LocalFileInfo`: `exists`, `is_directory`, `is_placeholder()`, `in_sync()`, `partial_on_disk()`, `pinned()`, `is_folder_populated()`. On Windows these come from the Cloud Files placeholder state. On Linux they come from a **sync stamp** in an extended attribute `user.fylvo.sync` on each synced file and folder:

`v1:<drive uuid>:<inode>:<size>:<mtime ns>` (folders: `v1:<drive uuid>:<inode>`)

| Windows | Linux (all local) |
| --- | --- |
| `is_placeholder()` | a stamp with the drive of the folder the path is in |
| `in_sync()` | stamp matches inode, size and mtime now |
| `partial_on_disk()` | always false (everything is local) |
| `pinned()` | always Pinned (server changes are downloaded) |
| `is_folder_populated()` | true for a stamped folder |

This copies the Windows semantics exactly:

- Editing a file in place changes size or mtime: not in sync, the planner uploads.
- Saving by delete and rewrite or by rename (LibreOffice, gedit, vim) gives a new inode without stamp: "not a placeholder", the same path Windows already handles (replacement save, `upload_replaced`).
- A file moved from another sync folder carries the other drive's id: not a placeholder here, so it is uploaded, never taken for "synced and deleted on the server".
- `cp -a` copies keep the xattr but get a new inode: not in sync.

The stamp is written where Windows calls "mark in sync" (`CrPlaceholder::commit`, `mark_path_in_sync`, download commit). When adding a sync folder, the app checks that the filesystem keeps user xattrs (ext4, btrfs, xfs yes; FAT, exFAT, some network shares no) and refuses with a clear message otherwise.

Which drive a path belongs to comes from a small registry of mounted sync roots (also used for `is_in_cloud_sync_root`).

## Platform layer

New module `cloudreve-sync/src/platform/` with one file per OS, behind `cfg(target_os)`:

- `LocalFileInfo` (Windows: re-export of the cfapi type; Linux: stamp based, same method names). The planner and its tests stay shared.
- Placeholder operations used by `CrPlaceholder`: create folder, mark in sync, convert, delete, set error state. Linux: `mkdir`, write stamp, remove stamp, no-op.
- Creating a file from the server: Windows makes an online-only placeholder; Linux enqueues a full download that writes to a temp file next to the target, renames it into place (no overwrite of anything that appeared meanwhile) and stamps it. The inventory row is written only after the file is on disk, so a half-done download never looks like "deleted here".
- Recycle bin: Windows recycle bin / freedesktop trash (`trash` crate, also on other mounts via `.Trash-<uid>`).
- Notifications: Windows toasts / D-Bus notifications (`notify-rust`) with the same texts; actions (open settings for held deletions, resolve conflict) as notification buttons where the desktop supports them, otherwise the app window shows them.
- Shell refresh: Windows `SHChangeNotify` / no-op.
- Windows-only modules (`cfapi`, `shellext`, `drive_letter`, `callback`, crate `win32_notif`, the `windows` crate) are compiled only on Windows.

Windows must not change behavior: the Windows code paths stay as they are, only moved behind the same functions. All Windows tests stay green and `build-msix.ps1 -Arch x64` still builds.

## Linux-specific sync behavior

- **Renames and moves.** Windows asks the app before a rename (cfapi callback) and can refuse it. Linux cannot refuse; the app sees the finished rename from the watcher (inotify). It then renames or moves on the server like the Windows callback does, and updates the inventory. If the server refuses (40905, read-only, conflict), the next sync treats the new path as a new file and the old one as locally deleted, both with the usual guards (upload, server trash). A move into another sync folder becomes "deleted here" plus "new file there": the server gets the deletion into its trash and the copy is uploaded, no data is lost.
- **Case.** Linux filesystems are case sensitive, the server refuses names that differ only in case (40905). A local `a.txt` next to `A.txt` uploads one; the other stays local with the existing message and is not retried endlessly. A case-only rename on the server renames the local file (the existing `case.rs` logic, with the on-disk lookup done case-insensitively on Linux, since NTFS did that for us).
- **Deletion.** From the PC: watcher, mass deletion guard, server trash (unchanged shared code). From the server: local file goes to the freedesktop trash, guarded the same way.
- **App not running.** As on Windows: a file deleted while the app was off comes back from the server (the planner cannot tell "deleted" from "never downloaded" without the watcher). Documented, same as Windows.
- **Initial sync** of a non-empty local folder: refused, same as the existing Windows rule (the upstream fix for non-empty folders).

## App shell (Tauri)

- Tray icon via AppIndicator (KDE, Ubuntu, most desktops). Fedora GNOME shows no tray without the "AppIndicator" extension: the app then opens its window when started from the menu, and the guide names the extension.
- Autostart: `.desktop` file in `~/.config/autostart` (setting in the app, on by default like Windows).
- `fylvo://` links: `x-scheme-handler/fylvo` in the `.desktop` file (deb, rpm) or registered at first start (AppImage).
- Settings hide Windows-only options (drive letter, Explorer).
- File manager menu: GNOME Files script entries and a Dolphin service menu, installed by the app, calling `fylvo-desktop --view-online <path>` / `--sync-now <path>`, forwarded to the running app by the single-instance plugin.
- Look and texts as on Windows (German and English), no new UI.

## Updates

Build on the shared update crate from the Windows update thread (`desktop/crates/fylvo-update`: ask, compare, download, verify sha256, schedule) and the server API (`/api/v4/fylvo/desktop/:platform/latest`, platform `linux-x64`). Linux part:

- **AppImage** (main format): download next to the running file (`$APPIMAGE`), verify, set executable, rename over the old file (atomic on the same filesystem), restart when no sync runs; notify after the update.
- **.deb / .rpm**: the package manager owns the files; open question for Sven (see below).
- Authenticity: coordinate with the Windows thread. Proposal: an ed25519 signature made by the publish script, embedded in `release.json`, checked by the app with a public key compiled in, before installing. The server's sha256 alone only proves the download is what the server has.
- Publish: `scripts/publish-desktop.sh <file> linux-x64`, with a Linux check that the file is a real Fylvo AppImage (ELF, AppImage magic, embedded `.desktop` with our id).

## Stages (each runnable and tested on its own)

1. **Build setup** (done in part): own WSL distro `FylvoBuild` (Ubuntu 22.04) on `D:\FylvoLinux`, Rust, Node, Tauri dependencies. Baseline Windows tests.
2. **Platform layer, compiles on Linux.** cfg gating, `platform` module, stamp based `LocalFileInfo`, trash, notifications. `cargo test` green on Windows (same count) and on Linux (shared planner tests run on both; new stamp tests on Linux).
3. **Linux sync.** Root registry, mount start without cfapi, full download on create, renames from the watcher, case lookup. Tests: unit tests plus the existing server e2e tests extended for Linux, against a local Fylvo server in WSL (own data dir, port 5290). rat review.
4. **App on Linux.** Tray, window, autostart, deep link, notifications, file manager menus, Windows-only settings hidden. Manual check in WSLg; tray, menus and autostart in a Fedora desktop VM.
5. **Packages.** AppImage, .deb, .rpm from the build distro. Install test on Fedora 44 and Ubuntu.
6. **Updates** on top of `fylvo-update` once it is in `main`. rat review.
7. **Manual test pass** under real Linux (list below) and the report with install guide and manual tests for Sven.

## Tests under Linux (adapted from the Windows manual tests)

Delete here and in the web (trash on both sides), mass deletion both ways (50 files), read-only shared folder, big file with interruption (network cut, app killed), conflict (both sides edited), case-only rename both ways and two names differing only in case locally, save with LibreOffice (lock file `.~lock.*#` ignored, replacement save uploaded once), move between two sync folders, rename and move inside a folder, app off while files change, no connection to upstream servers (network log), pairing via `fylvo://`.

## Open questions for Sven

1. Updates for .deb/.rpm: (a) the app only says "new version, download here" and the package manager installs, or (b) the app installs it itself after asking for the admin password. Recommendation: (a), and use the AppImage if you want silent updates.
2. Is a desktop VM for the tests OK (Fedora Workstation in Hyper-V, about 3 GB download, about 25 GB on D:, deleted at the end)?

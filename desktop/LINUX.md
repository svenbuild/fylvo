# Fylvo Desktop on Linux (preview)

Fylvo Desktop shows your Fylvo storage as a drive that loads files when they are opened, or keeps
folders on your Linux computer in sync as full copies (see "Drive or full copy"). Technical
design: [docs/linux-drive-plan.md](docs/linux-drive-plan.md) (drive) and
[docs/linux-plan.md](docs/linux-plan.md) (sync folders).

## Which package?

| Package | For | Updates |
| --- | --- | --- |
| `Fylvo Desktop_<version>_amd64.AppImage` | any Linux (a single file) | silent, the app replaces itself |
| `Fylvo Desktop-<version>-1.x86_64.rpm` | Fedora, openSUSE | the app installs it, the system asks for the admin password |
| `Fylvo Desktop_<version>_amd64.deb` | Ubuntu, Debian, Mint | as rpm |
| `desktop/nix/package.nix` | NixOS | with the system (`nixos-rebuild`) |

On Fedora, the `.rpm` is the best choice (shows up in the app menu, `fylvo://` links work right away).

## Install

**rpm (Fedora):**
```
sudo dnf install "./Fylvo Desktop-0.3.0-1.x86_64.rpm"
```
**deb (Ubuntu, Debian):**
```
sudo apt install "./Fylvo Desktop_0.3.0_amd64.deb"
```
**AppImage:** put the file into a folder you can write to (for example `~/Applications`, otherwise
updates fail), make it executable (`chmod +x`, or in the file manager's properties) and start it.
Ubuntu needs `sudo apt install libfuse2` once for AppImages.

**NixOS:** use the package in `nix/package.nix`, not the AppImage. `appimage-run` starts
AppImages in a sandbox that cannot mount the drive (no setuid `fusermount3`) and where links may
not open in the browser. In `configuration.nix`:
```nix
environment.systemPackages = [
  (pkgs.callPackage "${pkgs.fetchFromGitHub {
    owner = "svenbuild"; repo = "fylvo"; rev = "<commit>"; hash = "<hash>";
  }}/desktop/nix/package.nix" { })
];
```
Nix updates it (a new `rev`), the app does not update itself.

Then start Fylvo Desktop. The first time it opens "Add drive" by itself: enter the Fylvo address
and sign in in the browser. Everything else is filled in: click **Finish**.

## Drive or full copy

When you add a drive you choose how the files are on this computer:

- **Load files when opened (drive, recommended):** the Fylvo storage appears as the folder
  `~/Fylvo` (also in the side bar of the file manager). All files and folders are shown at once,
  a file is only downloaded when a program opens it. Opened files are kept in a cache of 20 GB
  (settings, "Drives"); when it is full, the files opened longest ago are removed first. Changes
  are uploaded a few seconds after the file is closed.
- **Keep a full copy (sync folder):** every file is downloaded and kept here, as before.

Good to know about the drive:

- **Offline:** right-click a folder, "Fylvo: Always keep on this device". It is downloaded and
  kept current. "Fylvo: Free up space" undoes it. Without a connection only files that were
  opened before or are kept offline are shown; edits are uploaded when the connection is back.
- **Thumbnails and search:** the file manager shows no thumbnails for files that were never
  opened, and desktop search does not index the drive (both would download everything).
- **Conflicts:** if someone else changed a file while you edited it, your version is saved next
  to it as a conflict copy and a notification says so.
- **Removing the drive** with changes that never reached the server keeps them in
  `~/Fylvo not uploaded`.
- **Moving onto an existing file** ("replace" in the file manager) puts the replaced file into
  the server's trash.
- **Not supported:** symbolic links, hard links, moving and renaming in one step (`mv` falls
  back to copy and delete). Vim saves a new file instead of a new version unless
  `:set backupcopy=yes`.
- Needs the `fuse3` package (the rpm and deb install it; for the AppImage it is part of every
  common desktop distribution).

## Good to know

- **Tray icon on GNOME:** GNOME shows no tray icons without an extension. Install
  "AppIndicator and KStatusNotifierItem Support" (`sudo dnf install gnome-shell-extension-appindicator`,
  sign out and in). Without it, starting Fylvo Desktop again from the menu opens its window.
- **Right-click "Fylvo: View online":** under "Scripts" in GNOME Files, directly in the menu in
  Dolphin. The app adds the entries when it starts.
- **Autostart:** in the app's settings (creates `~/.config/autostart/fylvo-desktop.desktop`).
- **Supported file systems:** ext4, btrfs, xfs, f2fs. USB sticks with FAT/exFAT and network shares
  are refused (the properties the app uses to detect changes are not stable there).
- **Very many folders:** Linux limits how many folders a program can watch. If the app says "Not
  all changes are noticed":
  `echo fs.inotify.max_user_watches=524288 | sudo tee /etc/sysctl.d/90-fylvo.conf && sudo sysctl --system`
- **Symlinks** and mounted drives inside a sync folder are never synced.
- **Trash:** what is deleted on the server goes to the Linux trash, what you delete here goes to
  the server's trash. From 50 deletions (or 10 when that is 30 % of the drive) the app asks first.
- **Moving between two sync folders** is undone (as on Windows). Copying works.

## Known limits

- If the app is not running while you delete or rename a file, the old file comes back from the
  server at the next start (after a rename you have both names). Same as on Windows.
- A program that overwrites a file in place and keeps its size and modification time is not
  noticed. Same as on Windows.
- A whole folder moved into a folder that was just created is deleted on the server (trash) and
  uploaded again; shares and versions stay with the old one. Moves of single files are detected.
- Between the check "is it still the same file on the server" and the deletion on the server
  there are milliseconds; the server has no conditional delete.

## Publishing updates (maintainer)

Build the packages on Linux (`desktop/src-tauri`, `cargo tauri build`). Publish them with a
signature; the private ed25519 key has no password and never goes into the repository
(`FYLVO_LINUX_KEY` points to it):
```
scripts/publish-desktop.sh "Fylvo Desktop_0.3.1_amd64.AppImage" linux-x64-appimage
scripts/publish-desktop.sh "Fylvo Desktop-0.3.1-1.x86_64.rpm" linux-x64-rpm
scripts/publish-desktop.sh "Fylvo Desktop_0.3.1_amd64.deb" linux-x64-deb
```
The script runs on Linux or in Git Bash (needs `openssl` 3). The version is in
`src-tauri/tauri.conf.json`. The app checks 2 minutes after the start and then every 6 hours,
installs only when nothing syncs, and says "Updated" after the restart.

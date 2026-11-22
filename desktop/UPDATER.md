# Fylvo Desktop: updates from the app (plan)

Status: plan, 2026-11-22. Branch `fylvo/desktop-update`.

## Decisions by Sven (2026-11-22)

1. The Windows package is **signed** with an own certificate (subject `CN=Fylvo`, the
   publisher in `AppxManifest.xml`, so the package family stays the same). Each PC trusts the
   certificate once (admin, `LocalMachine\TrustedPeople`), then the MSIX installs by double
   click and Developer Mode is no longer needed. Windows itself checks the signature of every
   update and rolls back a failed installation.
2. Updates install **silently**, but only while no sync task runs. The app restarts for a few
   seconds.
3. Settings: shown version, switch "Automatically update" (on), button "Check now".
4. A Windows notification after an update ("updated to X"); on failure a notification with
   the reason, the old version keeps running.

## Server (Go)

Shared code instead of a copy: `service/fylvo/fylvo_release.go` gets the generic part of
today's `fylvo_android_release.go` (read `release.json`, check checksum pattern and size,
open `<sha256>.<ext>` with Lstat/regular-file check, checksum cached once per file). Android
keeps its JSON fields and routes unchanged; its file becomes a thin wrapper.

New, signed-in users only (`auth` group in `routers/router.go`, like Android):

- `GET /api/v4/fylvo/desktop/:platform/latest` -> `{"version","sha256","size","file_name"}`
- `GET /api/v4/fylvo/desktop/:platform/package/:sha256` -> the file, only under the
  published checksum, `Content-Disposition` with `file_name`.

`platform` is one of a fixed list: `windows-x64`, `windows-arm64`, `linux-x64`,
`linux-arm64` (anything else: 404, never used in a path). Folder:
`<data>/desktop/<platform>/<sha256>.pkg` and `release.json`. `version`: `^\d{1,5}(\.\d{1,5}){0,3}$`;
`file_name`: `^[A-Za-z0-9._-]{1,80}$`; size limit 500 MB. Nothing published or broken: code
404 like Android (the app treats it as "no update").

Tests: table tests for the shared store (missing, broken json, wrong size, wrong checksum,
symlink, unknown platform), existing Android tests stay green, controller test for both routes.

## Publishing

`scripts/publish-desktop.sh <file> <platform>` (Git Bash on the PC, ssh to the NAS), same
steps as `publish-android.sh`: check the package, compare with the published version, copy
to `.upload-*`, check the checksum on the NAS, rename, write `release.json` last, remove
older packages. Package check per platform in its own function; Windows:

- `AppxManifest.xml` from the zip: `Name="Fylvo.Desktop"`, `Publisher="CN=Fylvo"`,
  `ProcessorArchitecture` matches the platform, version.
- Signature: `signtool verify /pa` passes and the signer certificate thumbprint equals the
  Fylvo certificate (pinned in the script).
- Version strictly higher than the published one (part by part).

Linux adds its own check function (other thread).

## Signing

- New certificate made once with `New-SelfSignedCertificate` (code signing EKU only, not a
  CA, `CN=Fylvo`, valid 30 years). Kept like the Android key: PFX in
  `C:\Users\alex\Documents\Projects\Fylvo-Signing\`, password from Sven (the password manager), public
  `.cer` in the repo (`desktop/package/fylvo-desktop.cer`) with its thumbprint.
- `build-msix.ps1 -Sign` signs with `signtool sign /fd SHA256 /f <pfx>`; password from the
  environment (`FYLVO_SIGN_PASSWORD`), never on the command line in logs.
- `desktop/install.ps1` (replaces the Developer Mode part of `install-local.ps1`): asks for
  admin once, imports the `.cer` into `LocalMachine\TrustedPeople` if missing, removes a
  development registration of `Fylvo.Desktop` (data in `%USERPROFILE%\.fylvo` stays), then
  `Add-AppxPackage` the signed MSIX. `install-local.ps1` stays for unsigned dev builds.

## App (Rust)

New crate `desktop/crates/fylvo-update`, small hooks into existing files only:

- `lib.rs` (all platforms): `Release` type, `PLATFORM` from `cfg`, version parse/compare,
  `check(client)` (the `latest` route), `download(client, release, dir)` to
  `<dir>/<sha256>.part`, stream with size limit = `release.size`, SHA-256, rename only when
  both match. `Updater` loop: first check 2 minutes after start, then every 6 hours, and on
  "Check now". Before installing it asks a hook `is_busy()`; while busy it waits and asks again
  every 5 minutes (at most 24 hours, then the next round). Writes a small state file
  `~/.fylvo/update/state.json` (`{"installing": "<version>", "from": "<version>"}`) before the
  install so the next start can tell success from failure.
- `windows.rs` (`cfg(windows)`): read `AppxManifest.xml` from the downloaded zip (crate `zip`)
  and check name, publisher, architecture and version (= release version and higher than the
  running one); then `RegisterApplicationRestart`, then
  `PackageManager::AddPackageByUriAsync` with `ForceTargetAppShutdown`. Windows checks the
  signature, stages, stops the app, registers, restarts it. If staging fails (bad signature,
  broken file) the app is still running and reports the error.
- Which server: the drives' own signed-in clients (`Mount::cr_client`), first one that
  answers; no drive means no update check. `cloudreve-api` gets one small method for an
  authenticated raw GET (stream).
- `is_busy()` in `src-tauri`: any running or queued task in any drive (`task_queue`), any
  running hydration (counter in the fetch-data callback, small change), or a pending held
  deletion question.
- Graceful stop before install: `DriveManager::shutdown()` and `persist()` like on exit, then
  install. If `AddPackage` fails after that, the app restarts itself (`app.restart()`), so it
  is never left without running sync.
- Start: if `state.json` says `installing` and the running version equals it: toast "updated";
  if it is still the old version: toast "update failed", and the same version is not tried
  again for 24 hours. Old files in `~/.fylvo/update/` are removed.
- Settings (`AboutSection.tsx` or `GeneralSection.tsx`): version, switch, "Check now", last
  result line. Config: `AppConfig.auto_update: bool` (default true). Tauri commands
  `get_update_status`, `check_for_update`, `set_auto_update`. Texts German and English.

## Attacker view

The trust chain: package signed with the Fylvo key and checked by Windows; publisher fixed to
`CN=Fylvo`; the app only installs a package whose manifest names `Fylvo.Desktop` and a higher
version, downloaded over https from the user's own signed-in server, with matching size and
checksum.

- Someone who controls the server or the NAS data folder can only offer packages; without the
  private key Windows rejects them. They can withhold updates (no harm beyond staying old) or
  offer an old signed package: refused by the "higher version" check.
- A man in the middle: https; plain http server addresses are not used for updates.
- A public CA certificate with subject exactly `CN=Fylvo` is not realistic (public code
  signing certificates carry O, L, C). Optional extra: pin the signer thumbprint in the app
  (parse the package signature); left out unless the review says it is needed.
- Local malware as the same user can already run code; out of scope. The download folder is
  in the user profile.
- The private key is the crown jewel: kept off the repo, password in the password manager, copy on the
  NAS like the Android key.

## Tests

- Go: store, controller, Android unchanged.
- Rust: version compare, platform string, download (size too big, wrong checksum, cut off ->
  no file left), manifest check (wrong name, publisher, arch, lower version), state file
  logic, busy waiting. The existing 83 tests stay green.
- Real run on this PC: local server from this branch on port 5290, own data folder; install a
  signed 0.2.1, publish 0.2.2 locally, update runs; failures: broken file, wrong checksum,
  unsigned / wrongly signed package, cut download: old version keeps running. Finally the
  newest version is installed and connected to nothing.

## Order

1. Spike: self update with `AddPackageByUriAsync` + `ForceTargetAppShutdown` +
   `RegisterApplicationRestart` from inside the packaged app, and switching the dev
   registration to the signed package (keeps `~/.fylvo`). If the restart does not work, a
   fallback: the app starts the installation through a scheduled task outside the package.
2. Server and publish script.
3. App crate, settings, texts.
4. Tests, real run, rat on the code.

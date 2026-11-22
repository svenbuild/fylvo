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
`<data>/desktop/<platform>/<sha256>.pkg` and `release.json`. `version`: 1 to 4 dot separated
numbers, each 0..65535 (missing parts count as 0, so `0.2.2` = `0.2.2.0`); `file_name`:
`^[A-Za-z0-9._-]{1,80}$`; size limit 500 MB. Errors like Android: HTTP 200 with the usual JSON
`code`; nothing published or broken is code 404 (the app treats it as "no update"), not signed
in is the usual 401 code. The Linux thread confirms its platform keys before the list is
fixed (a key would name the format if Linux ever ships two, e.g. `linux-x64-appimage`).

Tests: table tests for the shared store (missing, broken json, wrong size, wrong checksum,
symlink, unknown platform), existing Android tests stay green, controller test for both routes.

## Publishing

`scripts/publish-desktop.sh <file> <platform>` (Git Bash on the PC, ssh to the NAS), same
steps as `publish-android.sh`: check the package, compare with the published version, copy
to `.upload-*`, check the checksum on the NAS, rename, write `release.json` last, remove
older packages. Package check per platform in its own function; Windows:

- `AppxManifest.xml` from the zip: `Name="Fylvo.Desktop"`, `Publisher="CN=Fylvo"`,
  `ProcessorArchitecture` matches the platform, version.
- Signature: `Get-AuthenticodeSignature` on the .msix reports no hash mismatch and the signer
  thumbprint equals the Fylvo certificate (pinned in the script). `signtool verify /pa` is not
  used: it wants a trusted root, the certificate is only in TrustedPeople.
- Version strictly higher than the published one (part by part).

Linux adds its own check function (other thread).

## Signing

- New certificate made once with `New-SelfSignedCertificate` (code signing EKU only, not a
  CA, `CN=Fylvo`, valid 30 years). Kept like the Android key: PFX in
  `C:\Users\alex\Documents\Projects\Fylvo-Signing\`, password from Sven (the password manager), public
  `.cer` in the repo (`desktop/package/fylvo-desktop.cer`) with its thumbprint.
- The password lives only in the password manager (and in the session that signs), never on the NAS or
  in the repo.
- `build-msix.ps1 -Sign` signs with `signtool sign /fd SHA256 /f <pfx>`; password from the
  environment (`FYLVO_SIGN_PASSWORD`), never on the command line in logs.
- `desktop/install.ps1` (first install and the one-time move away from Developer Mode), runs
  as the normal user:
  1. Check the MSIX first: signature without hash mismatch, signer thumbprint pinned, manifest
     name/publisher. Nothing is touched before that.
  2. Import the `.cer` into `LocalMachine\TrustedPeople` if missing (only this step runs
     elevated, in its own process).
  3. If a development registration exists: stop the app, `Remove-AppxPackage
     -PreserveApplicationData`, `Add-AppxPackage` the signed MSIX; if that fails, register the
     old folder again (`Add-AppxPackage -Register`). The old folder
     `%LOCALAPPDATA%\Programs\FylvoDesktop` is deleted only after the signed install works
     (with the `Test-FylvoFolder` check of today's script). Data in `%USERPROFILE%\.fylvo`
     stays.
  4. `-ForceUpdateFromAnyVersion` switch for a manual repair with an older good package.
- Every published MSIX is also kept on the PC
  (`C:\Users\alex\Documents\Projects\Fylvo-Desktop\`), so a version that crashes at start
  can be replaced by hand with the last good one (the NAS keeps only the newest).
- `install-local.ps1` stays for unsigned dev builds.

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
- `windows.rs` (`cfg(windows)`), in this order:
  1. `PackageManager::StagePackageAsync` while sync keeps running. Windows checks signature,
     publisher and files here; almost every error (broken file, unsigned, wrong key) ends here
     and is only reported.
  2. Ask `is_busy()` again; if busy, wait (the staged package stays, step 1 is cheap next time).
  3. Stop sync exactly like "Quit" in the tray (`DriveManager::shutdown()` + `persist()`). An
     update is never more harmful than quitting the app. If `persist()` fails: no install,
     `app.restart()`.
  4. `RegisterApplicationRestart("--updated")` (the app has run for more than 60 seconds by
     then; "Check now" waits for that too), then `AddPackageAsync` with
     `ForceTargetAppShutdown`. This only registers the staged package. If it fails while the
     app still runs: `app.restart()`.
  The app does not parse the package itself: Windows refuses other publishers and lower
  versions; the app only compares the release version with its own
  (`Package::Current().Id().Version()`, four u16 parts).
- `persist()` writes atomically (temp file + rename), a small fix that also helps normal quits.
- Which server: the drives' own signed-in clients (`Mount::cr_client`), in order, first one
  that has a release; no drive means no update check. The same client downloads (token
  refresh included). Only `https` base URLs, or `http` to a loopback address (local tests);
  a response whose final URL is not https (redirect) is refused. If the checksum disappears
  between `latest` and the download (a new release), `latest` is asked once more.
  `cloudreve-api` gets one small method for an authenticated raw GET (stream).
- "Check now" works also with the switch off (the switch only controls automatic checks). It
  installs right away when idle, otherwise it shows "waiting for sync to finish".
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
  private key and its password Windows rejects them (a copy of the PFX may sit on the NAS
  like the Android key, its password never does). They can withhold updates (no harm beyond staying old) or
  offer an old signed package: refused by the "higher version" check.
- A man in the middle: https; plain http only for loopback addresses; the signature still
  protects even then.
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

## Known limits

- If registration fails after Windows has closed the app (rare: staging already passed), the
  app may stay closed until the next logon (startup task) or a manual start. Tested in the
  spike; a fallback (scheduled task) is built only if the spike shows it is needed.
- A version that installs fine and then crashes at start cannot heal itself: manual repair
  with the kept good MSIX and `install.ps1 -ForceUpdateFromAnyVersion`.
- Installing from a file URI needs Windows 10 2004 or newer; the manifest minimum is raised to
  10.0.19041 (2004+ also has sideloading on by default).

## Order

1. Spike (partly done 2026-11-22: unsigned refused, signed installs after trusting the
   certificate, a dev registration must be removed first, family name unchanged). Still to
   test: self update from inside the app incl. restart, a failure after the app was closed,
   the move with existing placeholders and autostart.
2. Server and publish script.
3. App crate, settings, texts.
4. Tests, real run, rat on the code.

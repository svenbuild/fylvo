# Fylvo Desktop: updates from the app (plan)

Status: built and tested, 2026-11-22. "Results" at the end says what was tested.

## Decisions by the maintainer (2026-11-22)

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

## Who checks what

| Check | Publish script | App | Windows |
| --- | --- | --- | --- |
| Signature valid, signer thumbprint = Fylvo certificate | yes | | signature + trust |
| Package name / publisher / architecture | yes | family name + architecture | publisher = signer |
| Version higher than published / running | yes | yes (manifest version = release version > running) | refuses lower for the same family |
| Size and SHA-256 | writes them | yes | |

## Publishing

`scripts/publish-desktop.sh <file> <platform>` (Git Bash on the PC, ssh to the NAS), same
steps as `publish-android.sh`: check the package, compare with the published version, copy
to `.upload-*`, check the checksum on the NAS, rename, write `release.json` last, remove
older packages. Package check per platform in its own function; Windows:

- `AppxManifest.xml` from the zip: `Name="Fylvo.Desktop"`, `Publisher="CN=Fylvo"`,
  `ProcessorArchitecture` matches the platform. The version in `release.json` is the manifest
  `Identity Version`, never a script argument.
- Signature: `Get-AuthenticodeSignature` must report `Valid` (the PC that publishes trusts the
  certificate) and the signer thumbprint must equal the Fylvo certificate (pinned in the
  script). Anything else stops.
- Version strictly higher than the published one (four parts).
- A copy of every published MSIX goes to `<your release folder>\`,
  for a manual repair (the NAS keeps only the newest).

Linux adds its own check function (other thread).

## Signing

- Certificate made 2026-11-22 with `New-SelfSignedCertificate -Type Custom -KeyUsage
  DigitalSignature`, EKU code signing, basic constraints "not a CA", `CN=Fylvo`, RSA 3072,
  valid until 2056. PFX in `<your signing folder>\fylvo-desktop.pfx`,
  public `.cer` in `desktop/package/fylvo-desktop.cer`, thumbprint
  `16A089077B5D78CCAA8996BD51EE523CF0264D79`.
- The password lives only in a password manager (and in the session that signs), never on the NAS or
  in the repo.
- `build-msix.ps1 -SignPasswordFile <file>` (and `-SignPfx <path>`): reads the password from that file, imports the PFX into `CurrentUser\My` for the moment of signing,
  `signtool sign /fd SHA256 /sha1 <thumbprint>`, removes it again in `finally`. The password
  never appears in process arguments.
- `desktop/install.ps1` (first install and the one-time move away from Developer Mode), runs
  as the normal user:
  1. Check the MSIX first: signer thumbprint pinned, no `HashMismatch`/`NotSigned`; before the
     certificate is trusted the only accepted non-`Valid` state is "root not trusted"; after
     the import (step 2) it must be `Valid`. Manifest name/publisher. Nothing is touched
     before that.
  2. Import the `.cer` into `LocalMachine\TrustedPeople` if missing (only this step runs
     elevated, in its own process).
  3. If a development registration exists: stop the app, `Remove-AppxPackage
     -PreserveApplicationData`, `Add-AppxPackage` the signed MSIX; if that fails, register the
     old folder again (`Add-AppxPackage -Register`). The old folder
     `%LOCALAPPDATA%\Programs\FylvoDesktop` is deleted only after the signed install works
     (with the `Test-FylvoFolder` check of today's script). Data in `%USERPROFILE%\.fylvo`
     stays.
  4. `-ForceUpdateFromAnyVersion` switch for a manual repair with an older good package.
- `install-local.ps1` stays for unsigned dev builds.

## App (Rust)

New crate `desktop/crates/fylvo-update`, small hooks into existing files only. The updater is
off when the app runs without package identity (`cargo tauri dev`).

- `lib.rs` (all platforms): `Release` type, `PLATFORM` from `cfg`, version parse/compare
  (four u16 parts, missing = 0), `check()` (the `latest` route; the answer is checked before
  anything is written: `sha256` exactly 64 lowercase hex, `size` 1 byte to 500 MB, version
  parses), `download()` to
  `<dir>/<sha256>.part`, stream with size limit = `release.size`, SHA-256, rename to
  `<sha256>.msix` (extension per platform) only when both match. If the checksum is gone
  between `latest` and the download (a new release), `latest` is asked once more.
- Requests: the updater uses its own `reqwest` client with redirects turned off and the
  access token from the drive's client (`cloudreve-api` gets two small accessors: base URL and
  a fresh access token, refresh included). Only `https` base URLs, or `http` to a loopback
  address (local tests).
- Which server: the drives' signed-in clients (`Mount::cr_client`), in order, first one that
  has a release; no drive means no update check.
- `Updater` loop: first check 2 minutes after start, then every 6 hours, and on "Check now".
  While busy it waits and asks again every 5 minutes (at most 24 hours, then the next round).
  State file `~/.fylvo/update/state.json` (`{"installing": "<version>", "from": "<version>"}`)
  written before the install, so the next start can tell success from failure.
- `windows.rs` (`cfg(windows)`), in this order:
  1. Read the package identity with the Windows packaging API (`IAppxFactory` ->
     `CreatePackageReader` -> `GetManifest` -> `GetPackageId`, no own zip/XML code). Require
     family name = `Package::Current().Id().FamilyName()`, architecture = the running one,
     version = release version and higher than `Package::Current().Id().Version()`.
  2. `PackageManager::StagePackageAsync` while sync keeps running. Windows checks signature,
     trust and files here; almost every error ends here and is only reported.
  3. Final idle check and stop are one step through a gate in `cloudreve-sync`
     (`activity.rs`): one mutex holds the count of running work and a "closed" flag.
     `close_if_idle()` closes only when nothing runs and no task is queued; after that, new
     work waits at the gate. Then sync stops exactly like "Quit" in the tray
     (`DriveManager::shutdown()` + `persist()`). If busy: the gate stays open, wait (the
     staged package stays). If `persist()` fails: no install, restart.
  4. `RegisterApplicationRestart` (the app has run for more than 60 seconds by then; "Check
     now" waits for that too), then `RegisterPackageByFullNameAsync(<full name from step 1>,
     ForceTargetApplicationShutdown)`: registers exactly the staged package. (The spike checks
     this call against `AddPackageByUriAsync` with `LimitToExistingPackages`; whichever works
     from inside the app is used, both only update the installed family.) If it fails while
     the app still runs: restart through `app.restart()`; a flag makes the second
     `shutdown()` on exit do nothing.
- Busy means: any running or queued task in any drive (`task_queue`), work counted by the gate
  (an RAII guard taken in each handler that `process_commands` spawns: rename, sync,
  placeholders, hydration, filesystem events, rename completed; and around each remote event
  batch in `listen_remote_events`), or a pending held deletion question. What can still be
  cut off is the same as when the user quits the app.
- `persist()` writes atomically (temp file + rename), a small fix that also helps normal quits.
- "Check now" works also with the switch off (the switch only controls automatic checks). It
  installs right away when idle, otherwise it shows "waiting for sync to finish".
- Start: if `state.json` says `installing` and the running version equals it: toast "updated";
  if it is still the old version: toast "update failed", and `state.json` keeps
  `{"failed": "<version>", "at": <time>}` so the same version is not tried again for 24
  hours (also the "last result" line in settings). Only downloaded packages and `.part` files
  in `~/.fylvo/update/` are removed, never `state.json`; a package that was staged but never
  registered is left to Windows (known limit).
- Settings (`AboutSection.tsx` or `GeneralSection.tsx`): version, switch, "Check now", last
  result line. Config: `AppConfig.auto_update: bool` (default true; the struct already loads
  missing fields with its defaults). Tauri commands `get_update_status`, `check_for_update`,
  `set_auto_update`. Texts German and English.

## Attacker view

- Someone who controls the server or the NAS data folder can offer any file. The app installs
  only a package of its own family (`Fylvo.Desktop` + the publisher `CN=Fylvo` hash) with a
  higher version, and Windows only accepts it signed by a certificate the PC trusts with
  subject exactly `CN=Fylvo`: in practice only the Fylvo key. A package of another family
  (signed with some public certificate) is refused by the app before staging. They can
  withhold updates (no harm beyond staying old) or offer an old signed package: refused.
- A copy of the PFX may sit on the NAS like the Android key; its password never does.
- A man in the middle: https; plain http only for loopback; the signature still protects.
- Local malware as the same user can already run code; out of scope.

## Tests

- Go: store, controller, Android unchanged.
- Rust: version compare, platform string, download (size too big, wrong checksum, cut off ->
  no file left), identity check (other family, other architecture, lower or other version),
  state file logic, busy waiting, URL rule (https / loopback http). The existing 83 tests stay
  green.
- Real run on this PC: local server from this branch on port 5290, own data folder; install a
  signed older version, publish a newer one locally, update runs; failures: broken file, wrong
  checksum, unsigned and foreign family package, cut download, registration failing: the old
  version keeps running. Finally the newest version is installed and connected to nothing.

## Known limits

- If registration fails after Windows has closed the app (rare: staging already passed), the
  app may stay closed until the next logon (startup task) or a manual start. Tested in the
  spike; a fallback (scheduled task) is built only if the spike shows it is needed.
- A version that installs fine and then crashes at start cannot heal itself: manual repair
  with the kept good MSIX and `install.ps1 -ForceUpdateFromAnyVersion`.
- A staged but never registered package stays until Windows cleans it up.
- Installing needs Windows 10 2004 or newer; the manifest minimum is raised to 10.0.19041.

## Order

1. Spike (partly done 2026-11-22: unsigned refused, signed installs after trusting the
   certificate, a dev registration must be removed first, family name unchanged,
   `Get-AuthenticodeSignature` says `Valid` once trusted). Still to test: self update from
   inside the app incl. restart, a failure after the app was closed, the move with existing
   placeholders and autostart. The spike must show that the chosen register call activates
   exactly the checked package and that the app comes back by itself.
2. Server and publish script.
3. App crate, settings, texts.
4. Tests, real run, rat on the code.

## Results (2026-11-22, on the PC, local server on port 5290)

- Move from Developer Mode: `install.ps1` replaced a dev registration of 0.2.0 with the signed
  0.2.1; a drive with a placeholder (not downloaded) and a new local file kept working, the
  old folder was removed.
- Self update, twice: 0.2.1.1 -> 0.2.2 and 0.2.3 -> 0.2.6: found, downloaded, identity checked,
  staged, sync stopped, registered; Windows restarted the app about a second later by itself,
  sync went on, the drive settings were saved.
- Refused, the running version kept going: a broken package (not a valid package), an
  unsigned one (refused by the app before anything happens; with Developer Mode on, Windows
  would stage it and then quietly not register it), a package of another app signed with the
  Fylvo key (other family), each blocked for a day afterwards.
- Note: Windows takes unchanged blocks from the installed version, so a byte changed in a
  file that did not change between versions is never read: the installed content is still
  exactly the signed one.
- Not tested for real: registration failing after Windows closed the app, killing the app in
  the middle of an update (the download part is covered by tests: nothing is left behind).
- Found on the way: `DriveManager::list_drives()` of the original always returns an empty
  list; the updater uses the new `mounts()`.


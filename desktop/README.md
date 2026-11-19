# Fylvo Desktop

Windows sync client for Fylvo. It is a fork of the official
[Cloudreve Desktop](https://github.com/cloudreve/desktop) client (MIT License, see `LICENSE`),
built with Tauri and React, and uses the Windows Cloud Files API (placeholders, files on demand).

Fylvo Desktop can be installed next to the Cloudreve app from the Microsoft Store. The two do not
share anything:

| | Cloudreve (Store) | Fylvo Desktop |
| --- | --- | --- |
| Package name | `2106abslant.Cloudreve` | `Fylvo.Desktop` |
| Executable | `cloudreve-desktop.exe` | `fylvo-desktop.exe` |
| Data folder (config, database, logs) | `%USERPROFILE%\.cloudreve` | `%USERPROFILE%\.fylvo` |
| Deep link | `cloudreve://` | `fylvo://` |
| Sync root provider ID | `cloudreve<hash>` | `fylvo<hash>` |
| COM class IDs (shell extensions, toasts) | original | new, see `package/AppxManifest.xml` |

The app does not contact any server except the Fylvo server you connect it to (no update check,
no telemetry, no crash reports).

## Sign-in and the web app

The app signs in with the server's built-in desktop OAuth client (`/callback/desktop`). The web
app then opens a deep link to hand the result to the desktop app. Fylvo Desktop marks its OAuth
`state` with the prefix `fylvo:` (`reauthorize:fylvo:` when signing in again), so the web app can
open `fylvo://mount?...` for it and keep `cloudreve://mount?...` for the Store app.

## Requirements

- Windows 10 1903 or later
- Rust (rustup), Node.js 20.19+ and Yarn 1
- Visual Studio Build Tools with the C++ workload, Windows 10/11 SDK (for `makeappx.exe`)
- Tauri CLI: `cargo install tauri-cli --version "^2" --locked`

## Build

```powershell
cd ui
yarn install --frozen-lockfile
cd ..
.\build-msix.ps1 -Arch x64
```

Result: `dist\Fylvo.x64.msix`. The build uses `--no-bundle` (the MSIX is made by the script).
The Tauri Rust crates and the `@tauri-apps` npm packages must stay on the same minor versions;
`Cargo.lock` and `ui/yarn.lock` are committed, so the build is reproducible.

The app needs package identity (shell extensions, sync root, toasts), so it must run as an MSIX
package, not as the bare `.exe`.

## Install (unsigned)

The package is not signed. Windows refuses unsigned packages that contain COM servers
("an unsigned package cannot include Executable activations"), even with `-AllowUnsigned`.
Without a certificate the package is registered in Developer Mode
(Settings > System > For developers):

```powershell
.\install-local.ps1
```

It unpacks `dist\Fylvo.x64.msix` to `%LOCALAPPDATA%\Programs\FylvoDesktop` and registers it from
there. Run it again after a new build to update. `dev-install.ps1` registers the same package
from `target/dev-package`; before switching between the two, remove the package (see below).

Remove it again with:

```powershell
Get-AppxPackage Fylvo.Desktop | Remove-AppxPackage
```

To install it as a normal signed package instead, sign `dist\Fylvo.x64.msix` with `signtool` and
change the `Publisher` in `package/AppxManifest.xml` (now `CN=Fylvo`) to exactly the subject of
the signing certificate. The certificate must be trusted on the PC.

## Development

```powershell
cargo tauri dev        # UI with hot reload (no package identity, shell features off)
.\dev-install.ps1      # build and register a copy of package/ (needs Developer Mode)
```

`dev-install.ps1` registers a copy of `package/` in `target/dev-package`; the files in `package/`
stay unchanged.

## Project structure

```
├── src-tauri/           # Tauri application shell
├── crates/
│   ├── cloudreve-sync/  # Core sync service
│   ├── cloudreve-api/   # REST client for the server
│   └── win32_notif/     # Windows notification utilities
├── ui/                  # React frontend (Vite + MUI)
├── package/             # MSIX manifest, images, resources.pri
├── dev-install.ps1      # Dev build + register script
├── install-local.ps1    # Install the built package on this PC (Developer Mode)
└── build-msix.ps1       # MSIX builder
```

`package/resources.pri` is generated with `makepri.exe` from the `package/` folder. Rebuild it
when images in `package/Images` change.

## License

[MIT](LICENSE), Copyright (c) 2025 Cloudreve.

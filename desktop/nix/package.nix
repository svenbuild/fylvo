# Fylvo Desktop for NixOS, built from this folder (`desktop/`). See LINUX.md, "NixOS".
{
  lib,
  rustPlatform,
  fetchYarnDeps,
  yarnConfigHook,
  cargo-tauri,
  nodejs,
  yarn,
  pkg-config,
  wrapGAppsHook3,
  gtk3,
  webkitgtk_4_1,
  libsoup_3,
  openssl,
  glib-networking,
  libayatana-appindicator,
  xdg-utils,
}:

rustPlatform.buildRustPackage {
  pname = "fylvo-desktop";
  version = (lib.importJSON ../src-tauri/tauri.conf.json).version;

  src = lib.cleanSourceWith {
    src = ./..;
    filter = path: _: !(lib.hasInfix "/node_modules" path || lib.hasInfix "/target" path || lib.hasInfix "/ui/dist" path);
  };

  cargoLock.lockFile = ../Cargo.lock;
  buildAndTestSubdir = "src-tauri";
  # Only the AppImage, rpm and deb update themselves; Nix updates this package.
  tauriBundleType = "deb";

  yarnOfflineCache = fetchYarnDeps {
    yarnLock = ../ui/yarn.lock;
    hash = "sha256-WvZWnpm9Z9x14jOpyHcXlcYjgd3V9mqZ9ngQA7hrU6M=";
  };
  dontYarnInstallDeps = true;
  postConfigure = ''
    pushd ui
    yarnConfigHook
    popd
  '';

  nativeBuildInputs = [
    cargo-tauri.hook
    nodejs
    yarn
    yarnConfigHook
    pkg-config
    wrapGAppsHook3
  ];

  buildInputs = [
    gtk3
    webkitgtk_4_1
    libsoup_3
    openssl
    glib-networking
    libayatana-appindicator
  ];

  # The tests that need a server or FUSE are ignored anyway; the rest ran in the repository.
  doCheck = false;

  preFixup = ''
    gappsWrapperArgs+=(
      # Loaded at run time for the tray icon.
      --prefix LD_LIBRARY_PATH : ${lib.makeLibraryPath [ libayatana-appindicator ]}
      # fusermount3 must be the setuid wrapper of NixOS (virtual drive).
      --prefix PATH : /run/wrappers/bin:${lib.makeBinPath [ xdg-utils ]}
      # Desktop files the app writes (autostart, file manager) start it by name, so they
      # survive updates; the package's desktop file handles fylvo:// links.
      --set FYLVO_LAUNCHER fylvo-desktop
    )
  '';

  meta = {
    description = "Keeps folders in sync with your own Fylvo server, or shows it as a drive";
    homepage = "https://github.com/svenbuild/fylvo";
    license = lib.licenses.mit;
    mainProgram = "fylvo-desktop";
    platforms = lib.platforms.linux;
  };
}

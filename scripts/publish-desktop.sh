#!/usr/bin/env bash
# Publishes a Fylvo Desktop package as the in-app update (Fylvo, desktop/UPDATER.md).
#
#   scripts/publish-desktop.sh path/to/Fylvo.x64.msix windows-x64
#
# Runs on the PC that builds the app (Git Bash is fine) with ssh access to the NAS. Before
# anything is copied it checks that the file is a Fylvo Desktop package of that platform (per
# platform, see check_<platform> below) and has a higher version than the published one.
#
# On the NAS it writes into the data folder of the instance (served by
# GET /api/v4/fylvo/desktop/<platform>/latest and .../package/<sha256>):
#   <platform>/<sha256>.pkg   the package, named after its checksum, never changed afterwards
#   <platform>/release.json   {"version","sha256","size","file_name"}, replaced last in one step
# Older packages of the platform are removed afterwards. A copy of every published package is
# kept on the PC afterwards (FYLVO_DESKTOP_KEEP), for a manual repair with an older good
# version.
#
# One-time setup on the server (the data folder belongs to root), with the ssh user as owner:
#   sudo install -d -o <ssh user> -m 755 <fylvo folder>/data/desktop
#
# Settings (environment):
#   FYLVO_NAS           ssh target, for example user@server
#   FYLVO_DESKTOP_DIR   folder on the server, for example /srv/fylvo/data/desktop
#   FYLVO_DESKTOP_KEEP  folder for the copies on the PC, default ~/Documents/Projects/Fylvo-Desktop
#   FYLVO_PUBLISH_LOCAL a local folder instead of the NAS (tests with a local server)
#   FYLVO_LINUX_KEY     private ed25519 key for Linux releases (PEM, no password), default
#                       ~/Documents/Projects/Fylvo-Signing/fylvo-desktop-linux-release.pem
set -euo pipefail

NAS="${FYLVO_NAS:-}"
BASE="${FYLVO_DESKTOP_DIR:-}"
KEEP="${FYLVO_DESKTOP_KEEP:-$HOME/Documents/Projects/Fylvo-Desktop}"
LOCAL="${FYLVO_PUBLISH_LOCAL:-}"
LINUX_KEY="${FYLVO_LINUX_KEY:-$HOME/Documents/Projects/Fylvo-Signing/fylvo-desktop-linux-release.pem}"
# The public key built into the app (fylvo_update::LINUX_RELEASE_KEY), as hex.
LINUX_PUBLIC="ef41a2fcf247e1961e57108e532b965fb512c4742313c8bfe718d5c4b95743b7"
SIGNATURE=""

fail() { echo "publish-desktop: $*" >&2; exit 1; }
[ -n "$LOCAL" ] || { [ -n "$NAS" ] && [ -n "$BASE" ]; } || fail "set FYLVO_NAS and FYLVO_DESKTOP_DIR, or FYLVO_PUBLISH_LOCAL"

FILE="${1:?usage: publish-desktop.sh path/to/package platform}"
PLATFORM="${2:?usage: publish-desktop.sh path/to/package platform}"
[ -f "$FILE" ] || fail "no file $FILE"
case "$PLATFORM" in
  windows-x64|windows-arm64) ;;
  linux-x64-appimage|linux-x64-rpm|linux-x64-deb|linux-arm64-appimage|linux-arm64-rpm|linux-arm64-deb) ;;
  *) fail "unknown platform $PLATFORM" ;;
esac
[ -n "$LOCAL" ] || case "$BASE" in /*) ;; *) fail "FYLVO_DESKTOP_DIR must be an absolute path" ;; esac
[ -n "$LOCAL" ] || case "$BASE" in *[!A-Za-z0-9/._-]*) fail "FYLVO_DESKTOP_DIR has unexpected characters" ;; esac
DIR="$BASE/$PLATFORM"

# Windows: the signed MSIX of Fylvo Desktop. Sets VERSION and EXT.
# SHA-1 thumbprint of the Fylvo Desktop signing certificate (public, desktop/package/fylvo-desktop.cer).
SIGNER="16A089077B5D78CCAA8996BD51EE523CF0264D79"
check_windows() {
  local arch="$1" out
  # Reads the manifest from the package (an MSIX is a zip) and the signature. The signature
  # must be "Valid": this PC trusts the Fylvo certificate (desktop/install.ps1).
  out="$(FYLVO_FILE="$(cygpath -w "$FILE" 2>/dev/null || echo "$FILE")" powershell.exe -NoProfile -NonInteractive -Command '
    $ErrorActionPreference = "Stop"
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [IO.Compression.ZipFile]::OpenRead($env:FYLVO_FILE)
    try {
      $entry = $zip.GetEntry("AppxManifest.xml")
      if (-not $entry) { throw "no AppxManifest.xml" }
      $reader = New-Object IO.StreamReader($entry.Open())
      try { $xml = [xml]$reader.ReadToEnd() } finally { $reader.Dispose() }
    } finally { $zip.Dispose() }
    $id = $xml.Package.Identity
    $sig = Get-AuthenticodeSignature -LiteralPath $env:FYLVO_FILE
    $thumb = if ($sig.SignerCertificate) { $sig.SignerCertificate.Thumbprint } else { "" }
    "{0}|{1}|{2}|{3}|{4}|{5}" -f $id.Name, $id.Publisher, $id.ProcessorArchitecture, $id.Version, $sig.Status, $thumb
  ' | tr -d '\r')" || fail "cannot read the package (not an MSIX?)"
  IFS='|' read -r NAME PUBLISHER PARCH VERSION STATUS THUMB <<< "$out"
  [ "$NAME" = "Fylvo.Desktop" ] || fail "package name is '$NAME', not Fylvo.Desktop"
  [ "$PUBLISHER" = "CN=Fylvo" ] || fail "publisher is '$PUBLISHER', not CN=Fylvo"
  [ "$PARCH" = "$arch" ] || fail "package is for '$PARCH', not $arch"
  [ "$STATUS" = "Valid" ] || fail "signature is '$STATUS', not Valid (unsigned, changed, or the Fylvo certificate is not trusted on this PC)"
  [ "$THUMB" = "$SIGNER" ] || fail "signed by $THUMB, not by the Fylvo Desktop certificate"
  EXT="msix"
}

# Linux: a package as `cargo tauri build` names it ("Fylvo Desktop_<version>_amd64.AppImage",
# "..._amd64.deb", "Fylvo Desktop-<version>-1.x86_64.rpm"), of the right format (checked by its
# first bytes) and architecture. Sets VERSION and EXT; the signature is made below, once the
# checksum is known. The real proof of origin is the signature: only the holder of the private
# key can publish a Linux update the app accepts.
check_linux() {
  local name arch format magic
  name="$(basename "$FILE")"
  format="${PLATFORM##*-}"
  arch="${PLATFORM#linux-}"; arch="${arch%%-*}"
  case "$format:$arch" in
    appimage:x64) [[ "$name" =~ ^Fylvo\ Desktop_([0-9.]+)_amd64\.AppImage$ ]] ;;
    appimage:arm64) [[ "$name" =~ ^Fylvo\ Desktop_([0-9.]+)_aarch64\.AppImage$ ]] ;;
    deb:x64) [[ "$name" =~ ^Fylvo\ Desktop_([0-9.]+)_amd64\.deb$ ]] ;;
    deb:arm64) [[ "$name" =~ ^Fylvo\ Desktop_([0-9.]+)_arm64\.deb$ ]] ;;
    rpm:x64) [[ "$name" =~ ^Fylvo\ Desktop-([0-9.]+)-1\.x86_64\.rpm$ ]] ;;
    rpm:arm64) [[ "$name" =~ ^Fylvo\ Desktop-([0-9.]+)-1\.aarch64\.rpm$ ]] ;;
    *) false ;;
  esac || fail "'$name' is not a Fylvo Desktop $format package for $arch (name as cargo tauri build makes it)"
  VERSION="${BASH_REMATCH[1]}"
  magic="$(head -c 16 "$FILE" | od -An -tx1 | tr -d ' \n')"
  case "$format" in
    # ELF, then "AI" and type 2 at offset 8.
    appimage) [[ "$magic" == 7f454c46*414902* ]] || fail "not an AppImage (type 2)"; EXT="AppImage" ;;
    # rpm lead.
    rpm) [[ "$magic" == edabeedb* ]] || fail "not an rpm"; EXT="rpm" ;;
    # ar archive whose first member is debian-binary.
    deb) [[ "$(head -c 21 "$FILE")" == $'!<arch>\ndebian-binary' ]] || fail "not a deb"; EXT="deb" ;;
  esac
  [ -f "$LINUX_KEY" ] || fail "no Linux release key at $LINUX_KEY (FYLVO_LINUX_KEY)"
  [ "$(openssl pkey -in "$LINUX_KEY" -pubout -outform DER | tail -c 32 | od -An -tx1 | tr -d ' \n')" = "$LINUX_PUBLIC" ] ||
    fail "the key at $LINUX_KEY is not the one the app trusts"
}

# Signs the release (Linux): ed25519 over the lines "fylvo-desktop-release", the platform, the
# version as a.b.c.d, the sha256 and the size, each ending in LF (release_message in
# desktop/crates/fylvo-update). Written base64 as "signature" into release.json.
sign_linux() {
  local four msg
  four="$(awk -v v="$VERSION" 'BEGIN { n = split(v, p, "."); for (i = n + 1; i <= 4; i++) p[i] = 0;
    printf "%d.%d.%d.%d", p[1], p[2], p[3], p[4] }')"
  msg="$(mktemp)"
  printf 'fylvo-desktop-release\n%s\n%s\n%s\n%s\n' "$PLATFORM" "$four" "$SHA" "$SIZE" > "$msg"
  SIGNATURE="$(openssl pkeyutl -sign -inkey "$LINUX_KEY" -rawin -in "$msg" | base64 | tr -d '\n')"
  rm -f "$msg"
  [ "${#SIGNATURE}" = 88 ] || fail "signing failed"
}

case "$PLATFORM" in
  windows-x64) check_windows x64 ;;
  windows-arm64) check_windows arm64 ;;
  linux-*) check_linux ;;
esac

# Versions: 1 to 4 numbers of 0..65535; compared part by part, missing parts are 0.
valid_version() { printf '%s' "$1" | grep -Eq '^[0-9]{1,5}(\.[0-9]{1,5}){0,3}$' &&
  ! printf '%s' "$1" | tr '.' '\n' | awk '$1 > 65535 { bad = 1 } END { exit !bad }'; }
# Prints 1 if $1 > $2.
newer() { awk -v a="$1" -v b="$2" 'BEGIN { n = split(a, x, "."); m = split(b, y, ".");
  for (i = 1; i <= 4; i++) { p = (i <= n) ? x[i] + 0 : 0; q = (i <= m) ? y[i] + 0 : 0;
    if (p > q) { print 1; exit } if (p < q) { print 0; exit } } print 0 }'; }
valid_version "$VERSION" || fail "unexpected version '$VERSION'"

SHA="$(sha256sum "$FILE" | cut -c1-64)"
SIZE="$(wc -c < "$FILE" | tr -d ' ')"
ARCH_OUT="${PLATFORM#*-}"
NAME_OUT="Fylvo-Desktop-$VERSION-${ARCH_OUT%%-*}.$EXT"
echo "Package: Fylvo Desktop $VERSION for $PLATFORM, $SIZE bytes, sha256 $SHA"
case "$PLATFORM" in linux-*) sign_linux ;; esac

# Run a command on the NAS, or locally for tests.
remote() {
  if [ -n "$LOCAL" ]; then bash -c "$1"; else ssh "$NAS" "$1"; fi
}
if [ -n "$LOCAL" ]; then DIR="$LOCAL/$PLATFORM"; mkdir -p "$DIR"; fi

# Only a higher version than the published one.
OLD="$(remote "cat '$DIR/release.json' 2>/dev/null || true" | sed -n 's/.*"version":"\([0-9.]*\)".*/\1/p')"
if [ -n "$OLD" ] && [ "$(newer "$VERSION" "$OLD")" != "1" ]; then
  fail "version $VERSION is not higher than the published $OLD"
fi
echo "Published so far: ${OLD:-nothing}"

# Copy, check, then switch over in one step.
remote "mkdir -p '$DIR' 2>/dev/null; [ -d '$DIR' ] && [ -w '$DIR' ]" ||
  fail "$DIR is missing or not writable, see the one-time setup at the top of this script"
# Each run uploads to its own file; checking it and switching over happen under a lock (two
# publishes at once could otherwise remove each other's package), and the published version
# is checked again under it. Without flock on the NAS nothing is published.
UP=".upload-$SHA-$$-$RANDOM.pkg"
LOCK_NEEDED=1
[ -n "$LOCAL" ] && ! command -v flock >/dev/null 2>&1 && LOCK_NEEDED=0
remote "umask 022; cat > '$DIR/$UP'" < "$FILE"
remote "set -e; cd '$DIR'
  if command -v flock >/dev/null 2>&1; then exec 9> .publish.lock; flock 9;
  elif [ '$LOCK_NEEDED' = 1 ]; then rm -f '$UP'; echo 'flock is missing' >&2; exit 1; fi
  NOW=\$(sed -n 's/.*\"version\":\"\([0-9.]*\)\".*/\\1/p' release.json 2>/dev/null || true)
  if [ -n \"\$NOW\" ] && ! awk -v a='$VERSION' -v b=\"\$NOW\" 'BEGIN { n = split(a, x, \".\"); m = split(b, y, \".\");
    for (i = 1; i <= 4; i++) { p = (i <= n) ? x[i] + 0 : 0; q = (i <= m) ? y[i] + 0 : 0;
      if (p > q) exit 0; if (p < q) exit 1 } exit 1 }'; then
    rm -f '$UP'; echo \"version $VERSION is not higher than the published \$NOW\" >&2; exit 1
  fi
  [ \"\$(sha256sum '$UP' | cut -c1-64)\" = '$SHA' ] || { rm -f '$UP'; echo 'checksum differs after the copy' >&2; exit 1; }
  chmod 644 '$UP'
  mv -f '$UP' $SHA.pkg
  if [ -n '$SIGNATURE' ]; then
    printf '{\"version\":\"%s\",\"sha256\":\"%s\",\"size\":%s,\"file_name\":\"%s\",\"signature\":\"%s\"}\n' '$VERSION' '$SHA' '$SIZE' '$NAME_OUT' '$SIGNATURE' > .release.json.tmp
  else
    printf '{\"version\":\"%s\",\"sha256\":\"%s\",\"size\":%s,\"file_name\":\"%s\"}\n' '$VERSION' '$SHA' '$SIZE' '$NAME_OUT' > .release.json.tmp
  fi
  chmod 644 .release.json.tmp
  mv -f .release.json.tmp release.json
  for f in *.pkg; do [ \"\$f\" = '$SHA.pkg' ] || rm -f -- \"\$f\"; done
  ls -l"
# Keep a copy on the PC, only of what was really published.
mkdir -p "$KEEP"
cp -f "$FILE" "$KEEP/$NAME_OUT"
echo "Published $VERSION for $PLATFORM (copy in $KEEP/$NAME_OUT)."

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
# kept on the PC (FYLVO_DESKTOP_KEEP), for a manual repair with an older good version.
#
# One-time setup on the NAS (the data folder belongs to root), with the ssh user as owner:
#   sudo install -d -o core -g admin -m 755 /srv/fylvo/data/desktop
#
# Settings (environment):
#   FYLVO_NAS           ssh target, default user@192.168.1.10
#   FYLVO_DESKTOP_DIR   folder on the NAS, default /srv/fylvo/data/desktop
#   FYLVO_DESKTOP_KEEP  folder for the copies on the PC, default ~/Documents/Projects/Fylvo-Desktop
#   FYLVO_PUBLISH_LOCAL a local folder instead of the NAS (tests with a local server)
set -euo pipefail

NAS="${FYLVO_NAS:-user@192.168.1.10}"
BASE="${FYLVO_DESKTOP_DIR:-/srv/fylvo/data/desktop}"
KEEP="${FYLVO_DESKTOP_KEEP:-$HOME/Documents/Projects/Fylvo-Desktop}"
LOCAL="${FYLVO_PUBLISH_LOCAL:-}"

fail() { echo "publish-desktop: $*" >&2; exit 1; }

FILE="${1:?usage: publish-desktop.sh path/to/package platform}"
PLATFORM="${2:?usage: publish-desktop.sh path/to/package platform}"
[ -f "$FILE" ] || fail "no file $FILE"
case "$PLATFORM" in
  windows-x64|windows-arm64) ;;
  linux-x64-appimage|linux-x64-rpm|linux-x64-deb|linux-arm64-appimage|linux-arm64-rpm|linux-arm64-deb) ;;
  *) fail "unknown platform $PLATFORM" ;;
esac
case "$BASE" in /*) ;; *) fail "FYLVO_DESKTOP_DIR must be an absolute path" ;; esac
case "$BASE" in *[!A-Za-z0-9/._-]*) fail "FYLVO_DESKTOP_DIR has unexpected characters" ;; esac
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

# Linux: the Linux app adds its package check here, and an ed25519 signature (base64) written
# as "signature" into release.json. It covers the lines "fylvo-desktop-release", the platform,
# the version as a.b.c.d, the sha256 and the size, each ending in LF (release_message in
# desktop/crates/fylvo-update).
check_linux() {
  fail "Linux packages are not supported yet (the Linux app adds its check here)"
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

# Keep a copy on the PC first.
mkdir -p "$KEEP"
cp -f "$FILE" "$KEEP/$NAME_OUT"

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
  printf '{\"version\":\"%s\",\"sha256\":\"%s\",\"size\":%s,\"file_name\":\"%s\"}\n' '$VERSION' '$SHA' '$SIZE' '$NAME_OUT' > .release.json.tmp
  chmod 644 .release.json.tmp
  mv -f .release.json.tmp release.json
  for f in *.pkg; do [ \"\$f\" = '$SHA.pkg' ] || rm -f -- \"\$f\"; done
  ls -l"
echo "Published $VERSION for $PLATFORM (copy in $KEEP/$NAME_OUT)."

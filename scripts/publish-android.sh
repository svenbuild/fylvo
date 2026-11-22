#!/usr/bin/env bash
# Publishes a signed Fylvo Android APK as the in-app update (Fylvo).
#
#   scripts/publish-android.sh path/to/app-release.apk
#
# Runs on the PC that builds the app (Git Bash is fine). It needs the Android SDK build tools
# (apksigner, aapt2) and ssh access to the server, or FYLVO_PUBLISH_LOCAL for a local folder.
# Before anything is copied it checks that the APK
# is signed with the Fylvo release key only, is the Fylvo package, and has a higher version code
# than the published one.
#
# On the server it writes into the data folder of the instance (served by
# GET /api/v4/fylvo/android/latest and /apk/<sha256>):
#   <sha256>.apk   the APK, named after its checksum, never changed afterwards
#   release.json   {"version_code","version_name","sha256","size"}, replaced last in one step
# Older APKs are removed afterwards.
#
# One-time setup on the server (the data folder belongs to root), with the ssh user as owner:
#   sudo install -d -o <ssh user> -m 755 <fylvo folder>/data/android
#
# Settings (environment):
#   FYLVO_NAS            ssh target, for example user@server
#   FYLVO_ANDROID_DIR    folder on the server, for example /srv/fylvo/data/android
#   FYLVO_PUBLISH_LOCAL  a local folder instead (the android folder of a data folder, or of the
#                        app update bundle of a release, see docs/releasing.md)
set -euo pipefail

PACKAGE="io.github.svenbuild.fylvo.android"
# SHA-256 of the Fylvo release certificate (public, see android/README.md).
RELEASE_CERT="504686eec2da091e6a7ab19d0cbf52e3976ce5ee3d35ad92cc327b65fdcddba4"
NAS="${FYLVO_NAS:-}"
DIR="${FYLVO_ANDROID_DIR:-}"
LOCAL="${FYLVO_PUBLISH_LOCAL:-}"

fail() { echo "publish-android: $*" >&2; exit 1; }

if [ -n "$LOCAL" ]; then
  mkdir -p "$LOCAL"
  DIR="$(cd "$LOCAL" && pwd)"
else
  [ -n "$NAS" ] && [ -n "$DIR" ] || fail "set FYLVO_NAS and FYLVO_ANDROID_DIR, or FYLVO_PUBLISH_LOCAL"
fi
# Runs a command on the server, or here for FYLVO_PUBLISH_LOCAL.
remote() {
  if [ -n "$LOCAL" ]; then bash -c "$1"; else ssh "$NAS" "$1"; fi
}

APK="${1:?usage: publish-android.sh path/to/app-release.apk}"
[ -f "$APK" ] || fail "no file $APK"
case "$DIR" in /*) ;; *) fail "FYLVO_ANDROID_DIR must be an absolute path" ;; esac
case "$DIR" in *[!A-Za-z0-9/._-]*) fail "FYLVO_ANDROID_DIR has unexpected characters" ;; esac

# Newest build tools of the SDK.
SDK="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}"
[ -n "$SDK" ] || fail "ANDROID_HOME is not set"
TOOLS="$(ls -d "$SDK"/build-tools/*/ 2>/dev/null | sort -V | tail -1)"
[ -n "$TOOLS" ] || fail "no build tools under $SDK/build-tools"
APKSIGNER="${TOOLS}apksigner"; [ -f "${APKSIGNER}.bat" ] && APKSIGNER="${APKSIGNER}.bat"
AAPT2="${TOOLS}aapt2"; [ -f "${AAPT2}.exe" ] && AAPT2="${AAPT2}.exe"

# 1. Signature: valid, exactly one signer, the Fylvo release key.
CERTS="$("$APKSIGNER" verify --print-certs "$APK" 2>/dev/null)" || fail "the APK signature is not valid (unsigned?)"
SIGNERS="$(printf '%s\n' "$CERTS" | grep -c 'certificate SHA-256 digest:' || true)"
[ "$SIGNERS" = "1" ] || fail "expected exactly one signer, found $SIGNERS"
CERT="$(printf '%s\n' "$CERTS" | sed -n 's/.*certificate SHA-256 digest: *//p' | tr -d '\r')"
[ "$CERT" = "$RELEASE_CERT" ] || fail "signed with $CERT, not with the Fylvo release key (debug build?)"

# 2. Package and version from the manifest.
BADGING="$("$AAPT2" dump badging "$APK" | sed -n 1p | tr -d '\r')"
NAME="$(printf '%s' "$BADGING" | sed -n "s/.* name='\([^']*\)'.*/\1/p")"
CODE="$(printf '%s' "$BADGING" | sed -n "s/.* versionCode='\([0-9]*\)'.*/\1/p")"
VERSION="$(printf '%s' "$BADGING" | sed -n "s/.* versionName='\([^']*\)'.*/\1/p")"
[ "$NAME" = "$PACKAGE" ] || fail "package is '$NAME', not $PACKAGE"
[ -n "$CODE" ] && [ "$CODE" -gt 0 ] || fail "no version code in the APK"
printf '%s' "$VERSION" | grep -Eq '^[0-9A-Za-z._-]{1,32}$' || fail "unexpected version name '$VERSION'"

SHA="$(sha256sum "$APK" | cut -c1-64)"
SIZE="$(wc -c < "$APK" | tr -d ' ')"
echo "APK: $PACKAGE $VERSION (code $CODE), $SIZE bytes, sha256 $SHA"

# 3. Only a higher version than the published one.
OLD="$(remote "cat '$DIR/release.json' 2>/dev/null || true" | sed -n 's/.*"version_code":\([0-9]*\).*/\1/p')"
if [ -n "$OLD" ] && [ "$CODE" -le "$OLD" ]; then
  fail "version code $CODE is not higher than the published $OLD"
fi
echo "Published so far: ${OLD:-nothing}"

# 4. Copy, check, then switch over in one step.
remote "[ -d '$DIR' ] && [ -w '$DIR' ]" ||
  fail "$DIR on ${NAS:-this PC} is missing or not writable, see the one-time setup at the top of this script"
remote "umask 022; cat > '$DIR/.upload-$SHA.apk'" < "$APK"
remote "set -e; cd '$DIR'
  [ \"\$(sha256sum .upload-$SHA.apk | cut -c1-64)\" = '$SHA' ] || { rm -f .upload-$SHA.apk; echo 'checksum differs after the copy' >&2; exit 1; }
  chmod 644 .upload-$SHA.apk
  mv -f .upload-$SHA.apk $SHA.apk
  printf '{\"version_code\":%s,\"version_name\":\"%s\",\"sha256\":\"%s\",\"size\":%s}\n' '$CODE' '$VERSION' '$SHA' '$SIZE' > .release.json.tmp
  chmod 644 .release.json.tmp
  mv -f .release.json.tmp release.json
  for f in *.apk; do [ \"\$f\" = '$SHA.apk' ] || rm -f -- \"\$f\"; done
  ls -l"
echo "Published $VERSION (code $CODE)."

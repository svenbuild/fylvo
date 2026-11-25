#!/bin/sh
# Downloads the Craft editors listed in craft.lock into a folder Fylvo serves them from
# (FYLVO_CRAFT_DIR, default ./craft). Every download is checked twice before it is used: the
# release's SHA256SUMS.txt must carry a valid signature of the pinned key (release-key.asc,
# fingerprint in craft.lock), and the zip must have the sum pinned in craft.lock.
# Big files are stored compressed (.br and .gz), the server sends what the browser takes.
# Needs: curl, gpg, unzip, sha256sum, gzip, brotli.
# Usage: scripts/craft/fetch.sh [target folder]
set -eu
HERE="$(cd "$(dirname "$0")" && pwd)"
TARGET="${1:-craft}"
LOCK="$HERE/craft.lock"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

export GNUPGHOME="$WORK/gnupg"
mkdir "$GNUPGHOME"
chmod 700 "$GNUPGHOME" 2>/dev/null || true
gpg --batch --quiet --import "$HERE/release-key.asc"
KEY="$(awk '$1 == "key" {print $2}' "$LOCK")"
gpg --batch --with-colons --fingerprint | grep -q "^fpr:::::::::$KEY:" || { echo "release-key.asc is not key $KEY" >&2; exit 1; }

mkdir -p "$TARGET"
grep -v '^#' "$LOCK" | awk '$1 != "key" && NF == 3' | while read -r APP VERSION SUM; do
  ZIP="$APP-web-$VERSION.zip"
  BASE="https://github.com/storytold/$APP/releases/download/v$VERSION"
  DIR="$WORK/$APP"
  mkdir -p "$DIR"
  echo "Fetching $APP $VERSION"
  curl -fsSL --retry 3 -o "$DIR/SHA256SUMS.txt" "$BASE/SHA256SUMS.txt"
  curl -fsSL --retry 3 -o "$DIR/SHA256SUMS.txt.asc" "$BASE/SHA256SUMS.txt.asc"
  curl -fsSL --retry 3 -o "$DIR/$ZIP" "$BASE/$ZIP"

  # The signature must be by the pinned key (VALIDSIG names the primary key last).
  gpg --batch --status-fd 1 --verify "$DIR/SHA256SUMS.txt.asc" "$DIR/SHA256SUMS.txt" 2>/dev/null \
    | grep -q "^\[GNUPG:\] VALIDSIG .* $KEY\$" || { echo "$APP: bad signature" >&2; exit 1; }
  ACTUAL="$(sha256sum "$DIR/$ZIP" | cut -d' ' -f1)"
  grep -q "^$ACTUAL  $ZIP\$" "$DIR/SHA256SUMS.txt" || { echo "$APP: zip not in the signed sums" >&2; exit 1; }
  if [ -n "${CRAFT_PRINT_SUMS:-}" ]; then
    # Updating craft.lock: the signed sum of the new version, nothing is installed.
    echo "$APP $VERSION $ACTUAL"
    continue
  fi
  [ "$ACTUAL" = "$SUM" ] || { echo "$APP: zip sum $ACTUAL, craft.lock has $SUM" >&2; exit 1; }

  unzip -q "$DIR/$ZIP" -d "$DIR/unpacked"
  SRC="$DIR/unpacked/$APP-web-$VERSION"
  [ -f "$SRC/index.html" ] || { echo "$APP: no index.html in $ZIP" >&2; exit 1; }
  # Only what the browser loads, and the licenses.
  find "$SRC" -type f \( -name '.htaccess' -o -name '_headers' -o -name 'HOSTING.md' -o -name 'README.md' \) -delete
  find "$SRC" -type f \( -name '*.wasm' -o -name '*.js' -o -name '*.onnx' \) | while read -r FILE; do
    gzip -9 -k -n "$FILE"
    brotli -q 11 -k "$FILE"
    rm "$FILE"
  done
  echo "$VERSION" > "$SRC/VERSION"
  rm -rf "${TARGET:?}/$APP"
  mv "$SRC" "$TARGET/$APP"
done
[ -n "${CRAFT_PRINT_SUMS:-}" ] && exit 0
# Every app of the lock must be there.
for APP in $(grep -v '^#' "$LOCK" | awk '$1 != "key" && NF == 3 {print $1}'); do
  [ -f "$TARGET/$APP/index.html" ] || { echo "$APP is missing in $TARGET" >&2; exit 1; }
done
echo "Craft editors are in $TARGET"

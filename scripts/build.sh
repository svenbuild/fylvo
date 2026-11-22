#!/usr/bin/env bash
# Builds the Fylvo server for Linux with the web app embedded.
# Result: release/fylvo-linux-<arch> for each architecture in FYLVO_ARCH (default: amd64),
# for example FYLVO_ARCH="amd64 arm64". FYLVO_VERSION is the Fylvo release (default: dev).
# Usage: scripts/build.sh
set -euo pipefail
cd "$(dirname "$0")/.."

# Server and web app must report the same base version, otherwise the server rejects the embedded
# web app at startup. It is the version of the Cloudreve base, which the database migration and the
# apps compare against; the Fylvo release is FYLVO_VERSION.
VERSION="4.19.1"
FYLVO_VERSION="${FYLVO_VERSION:-dev}"
ARCHES="${FYLVO_ARCH:-amd64}"
COMMIT="$(git rev-parse --short HEAD)"
MODULE="github.com/cloudreve/Cloudreve/v4"

export NODE_OPTIONS="--max-old-space-size=8192"
trap 'git checkout -- frontend/package.json' EXIT
(
  cd frontend
  rm -rf build
  corepack yarn install --frozen-lockfile --network-timeout 1000000
  corepack yarn version --new-version "$VERSION" --no-git-tag-version
  corepack yarn run build
)

# The server embeds the frontend as a zip whose entries start with assets/build/.
STAGE="$(mktemp -d)"
mkdir -p "$STAGE/assets"
cp -r frontend/build "$STAGE/assets/build"
if command -v zip >/dev/null 2>&1; then
  (cd "$STAGE" && zip -q -r assets.zip assets)
else
  (cd "$STAGE" && python -m zipfile -c assets.zip assets)
fi
mv "$STAGE/assets.zip" application/statics/assets.zip
rm -rf "$STAGE"

mkdir -p release
for ARCH in $ARCHES; do
  CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -trimpath     -ldflags "-s -w -X '$MODULE/application/constants.BackendVersion=$VERSION' -X '$MODULE/application/constants.FylvoVersion=$FYLVO_VERSION' -X '$MODULE/application/constants.LastCommit=$COMMIT'"     -o "release/fylvo-linux-$ARCH" .
  echo "Built release/fylvo-linux-$ARCH (Fylvo $FYLVO_VERSION, commit $COMMIT)"
done

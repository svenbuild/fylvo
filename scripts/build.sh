#!/usr/bin/env bash
# Builds the Fylvo server for the NAS (linux/amd64) with the web frontend embedded.
# Result: release/cloudreve
# Usage: scripts/build.sh [version]
set -euo pipefail
cd "$(dirname "$0")/.."

# Server and frontend must report the same version, otherwise the server
# rejects the embedded frontend at startup.
VERSION="${1:-4.19.1}"
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
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -X '$MODULE/application/constants.BackendVersion=$VERSION' -X '$MODULE/application/constants.LastCommit=$COMMIT'" \
  -o release/cloudreve .

echo "Built release/cloudreve (version $VERSION, commit $COMMIT)"

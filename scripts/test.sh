#!/usr/bin/env bash
# The Go tests the CI runs. Needs application/statics/assets.zip, so run scripts/build.sh first.
set -euo pipefail
cd "$(dirname "$0")/.."
# The simulated provider of development builds.
go test -tags fylvo_dev ./pkg/connected/ -run 'TestDev'
go test ./pkg/activity/ ./pkg/auth/ ./pkg/connected/... ./pkg/logging/ ./pkg/conf/ ./middleware/ ./pkg/attempts/ ./pkg/filemanager/... ./pkg/webdav/ ./pkg/setting/ ./pkg/util/ ./pkg/searcher/... ./pkg/request/... ./pkg/wopi/ ./inventory/... ./service/... ./routers/...

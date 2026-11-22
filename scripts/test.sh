#!/usr/bin/env bash
# The Go tests the CI runs. Needs application/statics/assets.zip, so run scripts/build.sh first.
set -euo pipefail
cd "$(dirname "$0")/.."
go test ./pkg/activity/ ./pkg/conf/ ./middleware/ ./pkg/attempts/ ./pkg/filemanager/... ./pkg/webdav/ ./pkg/setting/ ./pkg/balancer/ ./pkg/util/ ./pkg/searcher/... ./pkg/request/ssrftest/ ./pkg/wopi/ ./inventory/... ./service/... ./routers/...

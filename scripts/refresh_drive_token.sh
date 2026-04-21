#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR/src/go-master"

exec go run ./cmd/refresh_drive_token \
  -credentials ./credentials.json \
  -token ./token.json \
  -open-browser=false \
  "$@"

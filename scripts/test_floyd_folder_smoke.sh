#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_DIR="$ROOT_DIR/src/go-master"

export VIDEO_TITLE="${VIDEO_TITLE:-Floyd Mayweather training highlights and best moments}"
export CATEGORY="${CATEGORY:-Boxe}"
export CLIPS_ROOT_ID="${CLIPS_ROOT_ID:-}"
export AUTO_CATEGORY="${AUTO_CATEGORY:-0}"
export GOCACHE="${GOCACHE:-/tmp/go-build}"

cd "$GO_DIR"
go run ./cmd/tmp_channelmonitor_resolve_folder

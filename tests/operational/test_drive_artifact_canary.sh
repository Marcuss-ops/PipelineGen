#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT="$ROOT_DIR/scripts/drive-artifact-canary.sh"
[[ -x "$SCRIPT" ]] || { echo "Drive canary must be executable" >&2; exit 1; }
bash -n "$SCRIPT"
grep -q '/api/drive/canary-upload' "$SCRIPT"
grep -q '/api/drive/resolve-by-id' "$SCRIPT"
grep -q 'trap cleanup EXIT' "$SCRIPT"
grep -q 'VELOX_ADMIN_TOKEN' "$SCRIPT"

if VELOX_ADMIN_TOKEN=invalid DRIVE_CANARY_FOLDER_ID=folder "$SCRIPT" >/dev/null 2>&1; then
    echo "invalid credentials unexpectedly succeeded" >&2
    exit 1
fi

echo "Drive artifact canary tests passed"

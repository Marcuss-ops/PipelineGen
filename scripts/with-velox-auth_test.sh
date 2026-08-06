#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT

canonical=$(printf 'a%.0s' {1..64})
inherited=$(printf 'b%.0s' {1..64})
printf 'VELOX_ADMIN_TOKEN=%s\n' "$canonical" > "$WORK_DIR/pipelinegen.env"

observed=$(
    VELOX_ADMIN_TOKEN="$inherited" \
    TOKEN_FILE="$WORK_DIR/pipelinegen.env" \
    "$ROOT/scripts/with-velox-auth" bash -c 'printf %s "$VELOX_ADMIN_TOKEN"'
)

if [[ "$observed" != "$canonical" ]]; then
    echo "with-velox-auth did not prefer the canonical token file" >&2
    exit 1
fi

if TOKEN_FILE="$WORK_DIR/missing.env" VELOX_ADMIN_TOKEN="$inherited" \
    "$ROOT/scripts/with-velox-auth" true >/dev/null 2>&1; then
    echo "with-velox-auth unexpectedly accepted a missing canonical token file" >&2
    exit 1
fi

echo "with-velox-auth precedence: PASS"

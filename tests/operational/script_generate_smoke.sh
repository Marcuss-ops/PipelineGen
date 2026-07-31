#!/usr/bin/env bash
# Compatibility entrypoint. Canonical implementation: generate/run.sh.
set -euo pipefail
DIR=$(cd "$(dirname "$0")" && pwd)
if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '1,8p' "$0"
    exit 0
fi
exec bash "$DIR/generate/run.sh" basic.json "$@"

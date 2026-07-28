#!/usr/bin/env bash
set -euo pipefail

if [ -n "${BASH_SOURCE[0]:-}" ]; then
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
    echo "CI: cannot resolve check directory" >&2
    exit 1
fi
source "${SCRIPT_DIR}/lib/50_jobs_lib.sh"

echo "=== Check: forbid legacy Template/TimelineJSON writes outside canonical owners ==="
hits=$(rg -n --type go \
    -e 'Template:\s' -e 'TimelineJSON:\s' \
    --glob '!**/internal/application/scripts/adapters/processor_persistence.go' \
    --glob '!**/internal/application/scripts/adapters/repository.go' \
    --glob '!**/internal/application/voiceover/**' \
    --glob '!**/*_test.go' internal/ 2>/dev/null \
    | awk -F: '{
        rest = ""; for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next
        print
    }' || true)
if [ -n "$hits" ]; then
    echo "FAIL: legacy Template/TimelineJSON write outside canonical owners:" >&2
    echo "$hits" >&2
    exit 1
fi
echo "OK: legacy script columns have one canonical write/read surface"

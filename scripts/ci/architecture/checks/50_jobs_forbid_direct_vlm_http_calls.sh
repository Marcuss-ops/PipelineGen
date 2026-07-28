#!/usr/bin/env bash
set -euo pipefail

if [ -n "${BASH_SOURCE[0]:-}" ]; then
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
    echo "CI: cannot resolve check directory" >&2
    exit 1
fi
source "${SCRIPT_DIR}/lib/50_jobs_lib.sh"

echo "=== Check: forbid direct VLM HTTP calls outside the canonical client ==="
hits=$(rg -n --hidden '\bhttp(|Get|Post|NewRequest|NewRequestWithContext)\(.*"/vlm/' \
    internal/application internal/api 2>/dev/null || true)
filtered=""
if [ -n "$hits" ]; then
    while IFS= read -r hit; do
        [ -z "$hit" ] && continue
        file=${hit%%:*}; rest=${hit#*:}; line=${rest%%:*}
        if ! sed -n "$((line - 1))p" "$file" | grep -q 'ARCH-ALLOWLIST: vlm-direct-caller'; then
            filtered+="${hit}\n"
        fi
    done <<< "$hits"
fi
if [ -n "$filtered" ]; then
    echo "FAIL: direct VLM HTTP caller(s) outside the canonical client:" >&2
    printf '%b' "$filtered" >&2
    exit 1
fi
echo "OK: no direct VLM HTTP callers"

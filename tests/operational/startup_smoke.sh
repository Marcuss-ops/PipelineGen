#!/usr/bin/env bash
#
# startup_smoke.sh — PipelineGen black-box startup health probe
#
# Usage:
#   ./startup_smoke.sh            # real probes against a live server
#   ./startup_smoke.sh --dry      # print the would-be probes, exit 0
#
# Asserts HTTP 2xx for ALL THREE endpoints:
#   GET /health
#   GET /ready
#   GET /api/system/doctor
#
# Exit codes:
#   0  all three endpoints responded with HTTP 2xx
#   1  one or more endpoints returned non-2xx
#   2  setup error (missing token, bad flag)
#   124 overall timeout exceeded

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

# Help text (read first 14 lines after the shebang)
if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,15p' "$0"; exit 0
fi

if [[ "$DRY_RUN" == "1" ]]; then
    smoke_echo_safe "DRY RUN — would probe:"
    printf '  GET http://%s/health\n' "$SMOKE_API_BASE"
    printf '  GET http://%s/ready\n' "$SMOKE_API_BASE"
    printf '  GET http://%s/api/system/doctor\n' "$SMOKE_API_BASE"
    exit 0
fi

declare -a LABELS=("/health" "/ready" "/api/system/doctor")
declare -a FAILURES=()

for label in "${LABELS[@]}"; do
    smoke_wallclock_check
    smoke_log_section "GET ${label}"
    smoke_curl GET "$label" > /dev/null
    if ! smoke_assert_http_2xx "GET ${label}"; then
        FAILURES+=("$label")
        # Show a redacted snippet of the body for diagnostic context.
        if [[ -s "$SMOKE_LAST_BODY" ]]; then
            snippet=$(head -c 300 "$SMOKE_LAST_BODY" 2>/dev/null || true)
            smoke_echo_safe "  body: ${snippet}" >&2
        fi
    fi
done

if (( ${#FAILURES[@]} > 0 )); then
    printf '%sFAIL: %d/%d endpoint(s) non-2xx: %s%s\n' \
        "$RED" "${#FAILURES[@]}" "${#LABELS[@]}" \
        "$(IFS=, ; echo "${FAILURES[*]}")" "$RESET"
    exit 1
fi

printf '\n%sOK: all %d startup endpoints responded with HTTP 2xx%s\n' \
    "$GREEN" "${#LABELS[@]}" "$RESET"
exit 0

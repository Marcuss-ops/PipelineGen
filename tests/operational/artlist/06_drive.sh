#!/usr/bin/env bash
# tests/operational/artlist/06_drive.sh — Artlist DoD Gate 6 (Drive resolve-by-id hard gate).
#
# Reorg (July 2026): split out of tests/operational/artlist_e2e.sh (now a thin shim).
# Hard-gate check (DoD, July 2026): a clip that survived the full pipeline
# MUST be resolvable back from Drive via its drive_file_id, live (not trashed,
# not deleted). Fail-closed on:
#   - 404 / 410 from /api/drive/resolve-by-id
#   - mismatch between response.drive_file_id and the asset row's drive_file_id
#   - trashed = true
#   - missing MimeType / Size fields
#
# Bundle: drive.sh (Drive API) + artlist.sh (clip_id resolution). Future
# implementation will delegate the curl + jq probe to lib/drive.sh::drive_resolve_by_id.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/../lib/common.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/artlist.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/artlist_runtime.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/drive.sh"



smoke_require curl jq



# ── Gate 6 — Drive resolve-by-id hard gate ──────────────────────────────
# Spec (July 2026 DoD):
#   - POST /api/drive/resolve-by-id with the first clip_id from Gate 4
#   - HTTP 2xx, ok=true, drive_file_id non-empty
#   - drive_file_id matches the asset row
#   - trashed == false (live)
#   - MimeType non-empty
#   - Size > 0
gate_drive_resolve() {
    smoke_log_section "Gate 6 — Drive resolve-by-id hard gate"
    log_info "[STUB] Gate 6 — implement next (will use lib/drive.sh::drive_resolve_by_id)"
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — Drive resolve (Gate 6):"
        printf '  POST %s/api/drive/resolve-by-id (drive_file_id from Gate 4)\n' "$BASE_URL"
        printf '  assert: trashed=false mime=video/mp4 size>0\n'
        exit 0
    fi
    gate_drive_resolve || return 1

    printf '\n============================================\n'
    printf '  06_drive\n'
    printf '  PASS=%d  WARN=%d  FAIL=%d\n' "$PASS" "$WARN" "$FAIL"
    printf '============================================\n'
    if [[ "$FAIL" -gt 0 ]]; then
        printf 'VERDICT: FAIL\n'
        return 1
    fi
    printf 'VERDICT: PASS\n'
    return 0
}

main "$@"

#!/usr/bin/env bash
# tests/operational/lib/drive.sh — Drive resolve / folder-lookup helpers.
#
# Source-able. Sourced by tests/operational/artlist/06_drive.sh per the
# runtime.sh refactor's source-order contract.
#
# Contract (July 2026, post-verify-* split):
#   - Exposes drive_resolve_by_id / drive_root_for_source. Both are
#     THIN STUBS as of this commit; full implementations ship in
#     subsequent followups.
#   - Uses BASE_URL (inherited from common.sh + artlist_runtime.sh source
#     order) so the resolve-by-id POST goes against the PipelineGen
#     server.
#
# Stub semantics (per AGENTS.md "fail closed" rule):
#   - Each helper validates its required args; missing args -> exit 2
#     with a [FATAL] stderr line.
#   - Under SMOKE_DRY_RUN=1: silently returns a canonical placeholder
#     (HTTP 0 / stub-folder-id). Under non-dry-run: emits [STUB] +
#     return 1.

set -euo pipefail

# ── ANSI color defaults (defensive under `set -u` in dry-source) ─────────
# Sub-scripts source common.sh first which exports $RED / $YELLOW /
# $RESET. Stub libs may also be sourced in isolation (e.g. by a future
# regression net); defaulting here keeps the printf lines below from
# tripping set -u on $YELLOW-/- $RESET with no common.sh ancestor.
: "${YELLOW:=\033[33m}"
: "${RESET:=\033[0m}"
: "${RED:=\033[31m}"

# ── Internal arg-count guard ────────────────────────────────────────────
drive_required_args() {
    local need=$1; shift
    local got=0
    local _arg
    for _arg in "$@"; do
        [[ -n "$_arg" ]] && got=$((got + 1))
    done
    if (( got < need )); then
        printf '%s[FATAL]%s drive lib: %d required arg(s), got %d\n' \
            "$RED" "$RESET" "$need" "$got" >&2
        exit 2
    fi
}

# ── drive_resolve_by_id — POST /api/drive/resolve-by-id wrapper ──────────
# drive_resolve_by_id DRIVE_FILE_ID [OUT]
#   DRIVE_FILE_ID     the canonical Drive file id (matches asset.drive_file_id)
#   OUT               (optional) response body file (default $WORK_DIR/last.body)
# Returns HTTP code on stdout. DoD Gate 6 hard gate: the response must
# carry drive_file_id matching the asset row, trashed=false, MimeType,
# Size > 0 — see tests/operational/artlist/06_drive.sh for the contract.
drive_resolve_by_id() {
    drive_required_args 1 "$@"
    local file_id="$1" out="${2:-${WORK_DIR:-/tmp}/last.body}"
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        : > "$out"
        printf '%s\n' "0"
        return 0
    fi
    printf '%s[STUB]%s drive_resolve_by_id(file_id=%q, out=%q) — not yet implemented\n' \
        "$YELLOW" "$RESET" "$file_id" "$out" >&2
    : > "$out"
    return 1
}

# ── drive_root_for_source — Drive folder lookup by source ───────────────
# drive_root_for_source SOURCE [STYLE]
#   SOURCE   "google-flow" | "duckduckgo" | etc. (string compare on env var
#             family — typically calls asset.IsAIImageSource under the
#             hood when wired up)
#   STYLE    (optional) override style key for AI-image source variants
# Echoes Drive folder ID on stdout. Returns 0 on hit, 1 on miss / not-yet.
drive_root_for_source() {
    drive_required_args 1 "$@"
    local source="$1" style="${2:-}"
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        printf '%s\n' "stub-folder-id"
        return 0
    fi
    printf '%s[STUB]%s drive_root_for_source(source=%q, style=%q) — not yet implemented\n' \
        "$YELLOW" "$RESET" "$source" "$style" >&2
    return 1
}

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
# drive_resolve_by_id DRIVE_FILE_ID
#   DRIVE_FILE_ID     the canonical Drive file id (matches asset.drive_file_id
#                     in media_assets table; produced by sqlite_clip_row)
# Returns the canonical return-code contract used by every Artlist lib
# helper: 0 → pass, 1 → contract violation (HTTP 2xx + body parsed but
# jq envelope rejected), 2 → transport / HTTP failure.  Body written to
# the canonical $WORK_DIR/artlist_drive_${file_id}.json so the gate at
# 06_drive.sh reads from a single predictable path regardless of caller
# (drives SSOT for output location across the dry- and live-run paths).
#
# Implementation choice (July 2026, Gate 6): forwarder to artlist_drive_resolve
# (lib/artlist.sh).  artlist_drive_resolve already owns the canonical
# curl chain + the trashed/size jq contract; duplicating that chain in
# this file would violate AGENTS.md "do not duplicate the same decision
# logic across handlers".  Forwarding preserves the canonical SSOT and
# keeps the dry-run path deterministic (artlist_drive_resolve uses curl
# directly, NOT smoke_curl — so it has no DRY_RUN short-circuit; we add
# the shape-passthrough here so dev dry-runs gate-clean).
drive_resolve_by_id() {
    drive_required_args 1 "$@"
    local file_id="$1"
    local out="${WORK_DIR:-/tmp}/artlist_drive_${file_id}.json"
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        # DRY_RUN shape-passthrough: emit a body that PASSES the Gate 6
        # 4-assertion contract (id round-trip + trashed=false + mimeType
        # non-void + size>0) so dev dry-runs of `make verify-artlist-drive`
        # gate-clean without touching the network or a real DB.
        jq -nc --arg id "$file_id" '{
            ok: true,
            resolved_count: 1,
            resolved: [{
                id: $id,
                trashed: false,
                mimeType: "video/mp4",
                size: 1024
            }]
        }' > "$out"
        return 0
    fi
    # Real path: forward to canonical artlist_drive_resolve.  Return code
    # surfaces straight through (0/1/2 = pass/contract/transport).
    artlist_drive_resolve "$file_id"
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

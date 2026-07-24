#!/usr/bin/env bash
# tests/operational/lib/sqlite.sh — SQLite read-only helpers for Artlist
# batteries.
#
# Source-able. Sourced by tests/operational/artlist/01_startup.sh +
# 07_index.sh per the runtime.sh refactor's source-order contract.
#
# Contract (July 2026, post-verify-* split):
#   - Exposes sqlite_pending_jobs / sqlite_outbox_terminal / sqlite_clip_row.
#   - All three are THIN STUBS as of this commit; full implementations
#     ship in subsequent followups.
#   - Operates on the canonical $DB_PATH (typically ./data/media/media.db.sqlite).
#
# Stub semantics (per AGENTS.md "fail closed" rule):
#   - Each helper validates its required args; missing args -> exit 2
#     with a [FATAL] stderr line.
#   - Under SMOKE_DRY_RUN=1: silently returns a canonical placeholder
#     (0 pending / COMPLETED status / placeholder row) without
#     touching the DB. Under non-dry-run: emits [STUB] + return 1.

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
sqlite_required_args() {
    local need=$1; shift
    local got=0
    local _arg
    for _arg in "$@"; do
        [[ -n "$_arg" ]] && got=$((got + 1))
    done
    if (( got < need )); then
        printf '%s[FATAL]%s sqlite lib: %d required arg(s), got %d\n' \
            "$RED" "$RESET" "$need" "$got" >&2
        exit 2
    fi
}

# ── sqlite_pending_jobs — count artlist rows in non-terminal statuses ─────
# sqlite_pending_jobs [DB_PATH]
#   DB_PATH   (optional) absolute path to media.db.sqlite (default $DB_PATH)
# Echoes count on stdout. Returns 0 on success, 1 on fail / not yet
# implemented. Pattern mirrors the inline query in tests/operational/artlist/01_startup.sh:
#   SELECT COUNT(*) FROM jobs WHERE type LIKE 'media.artlist%'
#   AND status IN ('QUEUED','LEASED','RUNNING','FINALIZING','RETRY_WAIT')
sqlite_pending_jobs() {
    local db="${1:-${DB_PATH:-}}"
    sqlite_required_args 1 "$db"
    if ! [[ -f "$db" ]]; then
        printf '%s[STUB]%s sqlite_pending_jobs: db %s not found\n' \
            "$YELLOW" "$RESET" "$db" >&2
        printf '%s\n' "?"
        return 1
    fi
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        printf '%s\n' "0"
        return 0
    fi
    printf '%s[STUB]%s sqlite_pending_jobs(db=%q) — not yet implemented\n' \
        "$YELLOW" "$RESET" "$db" >&2
    return 1
}

# ── sqlite_outbox_terminal — classify outbox chain for clip_id ───────────
# sqlite_outbox_terminal CLIP_ID [DB_PATH]
#   CLIP_ID    asset id to inspect
#   DB_PATH    (optional, default $DB_PATH)
# Echoes chain_status on stdout (COMPLETED | DEAD_LETTER | PENDING |
# SUPERSEDED | MISSING). Returns 0 on success, 1 on fail / not yet.
sqlite_outbox_terminal() {
    sqlite_required_args 1 "$@"
    local clip_id="$1" db="${2:-${DB_PATH:-}}"
    if ! [[ -f "$db" ]]; then
        printf '%s[STUB]%s sqlite_outbox_terminal: db %s not found\n' \
            "$YELLOW" "$RESET" "$db" >&2
        printf '%s\n' "MISSING"
        return 1
    fi
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        printf '%s\n' "COMPLETED"
        return 0
    fi
    printf '%s[STUB]%s sqlite_outbox_terminal(clip_id=%q, db=%q) — not yet implemented\n' \
        "$YELLOW" "$RESET" "$clip_id" "$db" >&2
    return 1
}

# ── sqlite_clip_row — SELECT * FROM assets WHERE id = CLIP_ID ─────────────
# sqlite_clip_row CLIP_ID [DB_PATH]
#   CLIP_ID    asset id to fetch
#   DB_PATH    (optional, default $DB_PATH)
# Echoes the assets row as a stream on stdout (one tab-separated row).
# Returns 0 on hit, 1 on miss / not yet implemented.
sqlite_clip_row() {
    sqlite_required_args 1 "$@"
    local clip_id="$1" db="${2:-${DB_PATH:-}}"
    if ! [[ -f "$db" ]]; then
        printf '%s[STUB]%s sqlite_clip_row: db %s not found\n' \
            "$YELLOW" "$RESET" "$db" >&2
        return 1
    fi
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        printf '%s\n' "stub-clip-id|stub-name|stub-local-path="
        return 0
    fi
    printf '%s[STUB]%s sqlite_clip_row(clip_id=%q, db=%q) — not yet implemented\n' \
        "$YELLOW" "$RESET" "$clip_id" "$db" >&2
    return 1
}

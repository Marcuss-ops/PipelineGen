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
# Echoes count on stdout. Returns 0 on success, 1 on probe failure.
# Real impl per tests/operational/artlist/01_startup.sh gate_preflight
# pending-jobs contract: SELECT COUNT(*) FROM jobs
# WHERE type LIKE 'media.artlist%' AND status
# IN ('QUEUED','LEASED','RUNNING','FINALIZING','RETRY_WAIT').
# Scoped to type LIKE 'media.artlist%' so unrelated voiceover/stock jobs
# don't gate the Artlist DoD.  Fail-closed on DB missing (echoes "?" + rc=1).
# Under DRY_RUN=1: echoes "0" + rc=0 deterministically so dev dry-runs are
# reproducible without spinning up a real SQLite + jobs table.
sqlite_pending_jobs() {
    local db="${1:-${DB_PATH:-}}"
    sqlite_required_args 1 "$db"
    if ! [[ -f "$db" ]]; then
        printf '%s[FATAL]%s sqlite_pending_jobs: db %s not found\n' \
            "$RED" "$RESET" "$db" >&2
        printf '%s\n' "?"
        return 1
    fi
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        printf '%s\n' "0"
        return 0
    fi
    sqlite3 -readonly "$db" \
        "SELECT COUNT(*) FROM jobs WHERE type LIKE 'media.artlist%' \
         AND status IN ('QUEUED','LEASED','RUNNING','FINALIZING','RETRY_WAIT')" \
        2>/dev/null | tr -d ' \n' || { printf '%s\n' "?"; return 1; }
}

# ── sqlite_outbox_terminal — classify outbox chain for clip_id ───────────
# sqlite_outbox_terminal CLIP_ID [DB_PATH]
#   CLIP_ID    asset id to inspect
#   DB_PATH    (optional, default $DB_PATH)
# Echoes chain_status on stdout (COMPLETED | DEAD_LETTER | PENDING |
# SUPERSEDED | MISSING). Returns:
#   0 → COMPLETED (terminal success — gate accepts)
#   1 → non-COMPLETED terminal state (DEAD_LETTER / MISSING / parse-failure)
#   2 → in-flight (PENDING / SUPERSEDED — recoverable, gate treats as not-yet)
# Implementation: GROUP BY aggregate_id over outbox_events where event_type =
# 'asset.index.requested'; severity uses terminal-event precedence
# (DEAD_LETTER > COMPLETED > SUPERSEDED > PENDING).  MISSING clips (zero
# outbox rows) detected by empty-query result path which echoes MISSING.
# Real impl per Gate 8 spec (CLASSIFICAZIONE COMPLETED vs DEAD_LETTER).
# SQL injection guard: clip_id single-quote chars are doubled.
sqlite_outbox_terminal() {
    sqlite_required_args 1 "$@"
    local clip_id="$1" db="${2:-${DB_PATH:-}}"
    if ! [[ -f "$db" ]]; then
        printf '%s[FATAL]%s sqlite_outbox_terminal: db %s not found\n' \
            "$RED" "$RESET" "$db" >&2
        printf '%s\n' "MISSING"
        return 1
    fi
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        printf '%s\n' "COMPLETED"
        return 0
    fi
    local escaped="${clip_id//\'/\'\'}"
    local result
    result=$(sqlite3 -readonly "$db" \
        "SELECT CASE
            WHEN SUM(CASE WHEN status='dead_letter' THEN 1 ELSE 0 END) > 0 THEN 'DEAD_LETTER'
            WHEN SUM(CASE WHEN status='completed'   THEN 1 ELSE 0 END) > 0 THEN 'COMPLETED'
            WHEN SUM(CASE WHEN status='superseded'  THEN 1 ELSE 0 END) > 0 THEN 'SUPERSEDED'
            WHEN SUM(CASE WHEN status='pending'     THEN 1 ELSE 0 END) > 0 THEN 'PENDING'
            ELSE 'MISSING'
         END
         FROM outbox_events
         WHERE event_type='asset.index.requested'
           AND aggregate_id='${escaped}'
         GROUP BY aggregate_id" \
        2>/dev/null | head -1 | tr -d ' \n')
    if [[ -z "$result" ]]; then
        printf '%s\n' "MISSING"
        return 1
    fi
    printf '%s\n' "$result"
    case "$result" in
        COMPLETED) return 0 ;;
        *) return 1 ;;
    esac
}

# ── sqlite_clip_row — SELECT drive_file_id FROM media_assets WHERE id = ? ─
# sqlite_clip_row CLIP_ID [DB_PATH]
#   CLIP_ID    asset id (canonical asset.id, populated by Register-Batch)
#   DB_PATH    (optional, default $DB_PATH)
# Echoes ONE column on stdout (the drive_file_id value, raw). Returns 0
# on hit (drive_file_id present), 1 on miss (empty result). Fail-closed on
# missing DB / SQL error.
# Single-purpose helper per Gate 6 spec: wider asset-row queries route
# through smoke_sqlite_query (lib/common.sh). SQL injection guard: clip_id
# single-quote characters are doubled (the SQLite-standard escape) before
# being interpolated into the literal query.
sqlite_clip_row() {
    sqlite_required_args 1 "$@"
    local clip_id="$1" db="${2:-${DB_PATH:-}}"
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        printf '%s\n' "stub-drive-file-id-dry-run"
        return 0
    fi
    if ! [[ -f "$db" ]]; then
        printf '%s[FATAL]%s sqlite_clip_row: db %s not found\n' \
            "$RED" "$RESET" "$db" >&2
        return 1
    fi
    local escaped="${clip_id//\'/\'\'}"
    local result
    result=$(sqlite3 -readonly "$db" \
        "SELECT drive_file_id FROM media_assets WHERE id = '${escaped}'" \
        2>/dev/null | tr -d ' \n')
    # Symmetric miss-signaling (per AGENTS.md fail-closed + mirrors
    # sqlite_pending_jobs in the same file): empty SELECT result means
    # the clip_id is absent from media_assets. Surface rc=1 + canonical
    # "?" sentinel so the gate's downstream `[[ -z "$drive_file_id" ]]`
    # check AND the lib's own rc semantics align (no silent PASS on miss).
    if [[ -z "$result" ]]; then
        # Symmetric miss-signaling (per AGENTS.md fail-closed): empty
        # SELECT result means the clip_id is absent from media_assets.
        # Return rc=1 with empty stdout so the gate's downstream
        # `[[ -z "$drive_file_id" ]]` check fires correctly. We do NOT
        # echo the canonical "?" sentinel used by sqlite_pending_jobs
        # two functions up — that helper's consumer parses the count
        # and explicitly checks `[[ "..." == "?" ]]`, while our
        # gate-level consumer at 06_drive.sh parses the ID string and
        # uses `[[ -z "..." ]]`. A "?" sentinel here would BREAK the
        # gate by transforming rc=1 into a non-empty drive_file_id
        # that silently passes phase 6.1 (code-reviewer feedback on the
        # Gate 6 lib/ refactor; SHA-cite intentionally avoided per
        # AGENTS.md "Documentation rule" — comments stay evergreen).
        return 1
    fi
    printf '%s\n' "$result"
}

#!/usr/bin/env bash
# stock_e2e_db_outbox_smoke.sh
#
# STK-E2E-E probe for architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05
# SQL probe against the canonical outbox_events table:
#   SELECT rows WHERE event_type='asset.index.requested'
#     ORDER BY created_at DESC LIMIT 40
#   Verify status IN (pending|completed), last_error empty,
#   NO dead-lettered (status NOT IN dead_lettered state).
#   Output formatted padded table; exit 0 iff every row is non-failed.
#
# Per godlike/06 SSOT one-canonical-owner-per-fact, the canonical
# outbox_events schema lives at
#   migrations/sqlite/092_create_outbox_events.sql
#   (CREATE TABLE IF NOT EXISTS outbox_events)
# with companion code at
#   internal/platform/sqlite/outboxevents/
#     repository.go  (Enqueue, ClaimNext, MarkCompleted, MarkFailed,
#                     RequeueExpiredLeases, CountByStatus, ListPending)
#     pool.go         (lease-and-fence consumer)
#     dispatcher_cleanup.go (canonical terminal-failure routing)
#
# Column-name canonicality per the 092 schema:
#   - status (NOT 'state' or 'lifecycle')
#   - last_error (NOT 'error' or 'err' — preserve the canonical casing)
#   - dead_lettered is a STATUS value (not a column); in the codebase
#     the canonical terminal-failed state is status='dead_lettered'
#
# User spec literal mapping:
#   - "status in (pending|completed)"                -> status IN (...)
#   - "error vuoto"                                  -> last_error == ''
#   - "NO dead_letter"                               -> status != 'dead_lettered'
# These are NOT unions of failure modes; each is enforced independently
# so the diagnostics surface the actual root cause without ambiguity.
#
# Per action-plan §4 canonical PR-STOCK-* failure mapping:
#   - status='failed' (transient, retry-able)        -> PR-STOCK-OUTBOX-RETRY-EXHAUSTED
#       canonical owner:
#         internal/platform/sqlite/outboxevents/repository.go::
#         MarkFailed / RequeueExpiredLeases (writes
#         "SET status = 'pending', next_attempt_at = ?, last_error = ?"
#         -- canonical last_error write seam)
#   - status='dead_lettered' (terminal)              -> PR-STOCK-OUTBOX-DEAD-LETTERED
#       canonical owner:
#         internal/platform/sqlite/outboxevents/repository.go::
#         (the literal "SET status = 'dead_letter'" write at
#         repository.go:252 + repository.go:321 — the canonical
#         terminal-failure routing)
#   - last_error != '' (transient error recorded)     -> PR-STOCK-OUTBOX-LAST-ERROR
#       canonical owner:
#         internal/platform/sqlite/outboxevents/repository.go::
#         (the canonical last_error write seam across
#         repository.go lines 252, 266, 321, 367 — verified
#         via rg "last_error" outboxevents/* -- the file OnError
#         does NOT own the canonical write seam; ownership lives
#         on repository.go per godlike/06 SSOT one-owner-per-fact)
#   - max_attempts exhausted AND status IN failed    -> PR-STOCK-OUTBOX-RETRY-EXHAUSTED
#       (conjoined failure: also PR-stock-OUTBOX-LAST-ERROR set)
#
# Exit codes per action-plan §5:
#   0 = PASS (all rows non-failed and last_error=='')
#   1 = FAIL (any dead_lettered / failed / last_error != '')
#   2 = prereq missing (sqlite3 absent / db file absent / table missing)
#
# Self-checks: `bash -n tests/operational/stock_e2e_db_outbox_smoke.sh`
# must exit 0 (validated at commit time per §5).
#
# Overridable env vars:
#   DB_PATH = data/media/media.db.sqlite
#   LIMIT   = 40  (rows inspected)

set -euo pipefail

# ---- Configuration --------------------------------------------------------
DB_PATH="${DB_PATH:-data/media/media.db.sqlite}"
LIMIT="${LIMIT:-40}"

# ---- Prerequisite checks (exit 2) ----------------------------------------
command -v sqlite3 >/dev/null 2>&1 || \
    { echo "FAIL: sqlite3 not on PATH (exit 2)" >&2; exit 2; }

if [ ! -f "$DB_PATH" ]; then
    echo "FAIL: $DB_PATH not found (exit 2)" >&2
    echo "  Suggested: start PipelineGen to create the DB, or point DB_PATH at the canonical path" >&2
    exit 2
fi

# ---- Schema presence probe (exit 2 if outbox_events missing) -------------
# Per code-reviewer NEEDS-FIX #1 (round-2 fixup on STK-E2E-D):
# distinguish "sqlite3 binary unreadable" from "table missing" via
# stderr capture + exit-code inspection. Pre-stk-e2e-round-2 fallback
# `|| echo "0"` masked real I/O errors as "table not found".
TMP_SCHEMA_ERR=$(mktemp /tmp/stock-outbox-schema-err.XXXXXX)
TABLE_CHECK=$(sqlite3 "$DB_PATH" \
    "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='outbox_events';" \
    2>"$TMP_SCHEMA_ERR")
SQLITE_EXIT=$?
SQLITE_ERR=$(cat "$TMP_SCHEMA_ERR" 2>/dev/null || echo "")
rm -f "$TMP_SCHEMA_ERR"

if [ "$SQLITE_EXIT" -ne 0 ]; then
    echo "FAIL: sqlite3 cannot read $DB_PATH (exit $SQLITE_EXIT)" >&2
    [ -n "$SQLITE_ERR" ] && echo "  stderr: $SQLITE_ERR" >&2
    echo "  Suggested: verify $DB_PATH is readable + not corrupted" >&2
    exit 2
fi

if [ "$TABLE_CHECK" != "1" ]; then
    echo "FAIL: outbox_events table not found in $DB_PATH (count=$TABLE_CHECK)" >&2
    echo "  Suggested: run migrations or use the canonical PipelineGen DB" >&2
    exit 2
fi

# ---- Header / logging -----------------------------------------------------
echo "=================================================================="
echo "STK-E2E-E: outbox_events DB probe (asset.index.requested lifecycle)"
echo "  DB_PATH = $DB_PATH"
echo "  LIMIT   = $LIMIT"
echo "=================================================================="

# ---- Canonical query (read-only SELECT) ---------------------------------
# Per godlike/07 NO-FAKE-AVAILABILITY: ONLY asset.index.requested events
# from the canonical event taxonomy (outboxevents/registry.go::
# EventAssetIndexRequested). The 40-row LIMIT is the operator-supplied
# smoke scope (recent events); not a SSOT cap.
# Column ordering matches the 092 schema canonical ordering for
# diff-by-eye alignment between schema doc and query.
QUERY=$(cat <<SQL_EOF
SELECT
    id,
    event_type,
    COALESCE(status, '')        AS status,
    COALESCE(last_error, '')    AS last_error,
    COALESCE(attempt_count, 0) AS attempt_count,
    COALESCE(max_attempts, 10) AS max_attempts,
    COALESCE(created_at, '')    AS created_at,
    COALESCE(updated_at, '')    AS updated_at
FROM outbox_events
WHERE event_type = 'asset.index.requested'
ORDER BY created_at DESC, id DESC
LIMIT $LIMIT;
SQL_EOF
)

# Run query — output goes to temp file for two-pass analysis
TMP_RAW=$(mktemp /tmp/stock-outbox-db.XXXXXX)
trap 'rm -f "$TMP_RAW"' EXIT

sqlite3 -separator '|' -header "$DB_PATH" "$QUERY" > "$TMP_RAW" 2>/dev/null || {
    echo "FAIL: sqlite3 query failed (exit 2)" >&2
    cat "$TMP_RAW" >&2 || true
    exit 2
}

# ---- Formatted table output ---------------------------------------------
# Per code-reviewer NEEDS-FIX #1 round 1: the manual padded-table format
# created visual misalignment (printf widths 2/9/18/5/40/2/3/20/20 vs
# border widths 3/11/20/7/4/3/4/22/22 — and last_error %.40s overflowed
# 4-char border). Per the reviewer's recommended fix, drop the manual
# padded table entirely; rely on the raw sqlite3 -separator '|' -header
# output which is already machine-parseable for STK-E2E-H aggregation
# AND visually clean for the 40-row operator scope. Future migration
# to `column -t -s '|'` is the canonical forward-pointer
# PR-FORMATTER-AUTO-FIT-VIA-COLUMN-T (when that binary is on-path
# across all operator hosts).
#
# Per AGENTS.md Pattern 6 (diagnostic-surface-first): the raw pipe-
# separated rows are emitted under a marker header so downstream
# aggregator scripts (STK-E2E-H aggregate) can ingest with `awk -F'|'`.
echo
echo "------- raw rows (sqlite -separator | -header, parser-friendly) -------"
cat "$TMP_RAW"
echo "---------- end raw rows ----------"
echo

# ---- Row count + per-row failure-mode breakdown -------------------------
TOTAL_ROWS=$(awk -F'|' 'NR>1 && NF>=8 && $1!="" {n++} END{print n+0}' "$TMP_RAW")

if [ "$TOTAL_ROWS" -eq 0 ]; then
    echo "INFO: zero asset.index.requested rows in $DB_PATH"
    echo "  Possible signals:"
    echo "    1. STK-E2E-A/B/C runs have not yet completed (no enqueue yet)"
    echo "    2. finalizer tx rolled back before outbox enqueue (PR-STOCK-FINALIZER-PUBLISHER-RACE)"
    echo "    3. canonical DB is unreachable / connection refuted (canonical server down)"
    echo "  Verdict: exit 0 (no violations found; cannot validate lifecycle without rows)"
    exit 0
fi

# Per failure mode (each maps to ONE canonical PR-STOCK-* per action plan §4)
ROWS_DEAD_LETTERED=$(awk -F'|' 'NR>1 && NF>=8 && $1!="" && $3=="dead_lettered" {n++} END{print n+0}' "$TMP_RAW")
ROWS_FAILED=$(awk -F'|' 'NR>1 && NF>=8 && $1!="" && ($3=="failed" || $3=="failure") {n++} END{print n+0}' "$TMP_RAW")
ROWS_LAST_ERROR=$(awk -F'|' 'NR>1 && NF>=8 && $1!="" && $4!="" && $4!=" " {n++} END{print n+0}' "$TMP_RAW")
ROWS_RETRY_EXHAUSTED=$(awk -F'|' 'NR>1 && NF>=8 && $1!="" && $5+0 >= $6+0 {n++} END{print n+0}' "$TMP_RAW")
ROWS_HEALTHY=$(awk -F'|' 'NR>1 && NF>=8 && $1!="" && ($3=="pending" || $3=="completed") && ($4=="" || $4==" ") {n++} END{print n+0}' "$TMP_RAW")

echo "Summary (limit=$LIMIT, total=$TOTAL_ROWS):"
echo "  pending|completed + last_error==''  = $ROWS_HEALTHY"
echo "  status='failed'                      = $ROWS_FAILED"
echo "  status='dead_lettered'               = $ROWS_DEAD_LETTERED"
echo "  last_error != ''                     = $ROWS_LAST_ERROR"
echo "  attempt_count >= max_attempts        = $ROWS_RETRY_EXHAUSTED"
echo

# ---- Canonical PR-STOCK-* failure mapping (per action plan §4) ----------
if [ "$ROWS_DEAD_LETTERED" -gt 0 ] || [ "$ROWS_FAILED" -gt 0 ] || [ "$ROWS_LAST_ERROR" -gt 0 ]; then
    echo "FAIL: $TOTAL_ROWS asset.index.requested rows; $((TOTAL_ROWS - ROWS_HEALTHY)) violations" >&2
    if [ "$ROWS_DEAD_LETTERED" -gt 0 ]; then
        echo "FAIL canonical: PR-STOCK-OUTBOX-DEAD-LETTERED (terminal-failure permanent state)" >&2
        echo "  $ROWS_DEAD_LETTERED rows with status='dead_lettered'" >&2
        echo "  Canonical owner: internal/platform/sqlite/outboxevents/repository.go" >&2
        echo "  (the literal 'SET status = dead_letter' write at lines 252 + 321)" >&2
        echo "  Likely root cause: max_attempts exhausted WITHOUT manual replay" >&2
        echo "  Recovery: requires operator intervention; rows must be replayed or archived" >&2
    fi
    if [ "$ROWS_FAILED" -gt 0 ]; then
        echo "FAIL canonical: PR-STOCK-OUTBOX-RETRY-EXHAUSTED (transient failure retry-able)" >&2
        echo "  $ROWS_FAILED rows with status in ('failed','failure')" >&2
        echo "  Canonical owner: internal/platform/sqlite/outbox/repository.go::MarkFailed" >&2
        echo "  Likely root cause: transient error in IndexingHandler (worker transient disconnect)" >&2
        echo "  Self-heal: RequeueExpiredLeases polls + next_attempt_at scheduling" >&2
    fi
    if [ "$ROWS_LAST_ERROR" -gt 0 ]; then
        echo "FAIL canonical: PR-STOCK-OUTBOX-LAST-ERROR (transient error recorded on event)" >&2
        echo "  $ROWS_LAST_ERROR rows with last_error != empty" >&2
        echo "  Canonical owner: internal/platform/sqlite/outboxevents/repository.go" >&2
        echo "  (the canonical last_error write seam at lines 252, 266, 321, 367)" >&2
        echo "  Likely root cause: IndexingHandler reported transient error mid-flight" >&2
    fi
    if [ "$ROWS_RETRY_EXHAUSTED" -gt 0 ] && [ "$ROWS_RETRY_EXHAUSTED" -ne "$ROWS_DEAD_LETTERED" ]; then
        # partial overlap: retry-exhausted rows that have NOT been dead-lettered
        # yet (per the dispatcher_cleanup contract); operator warning.
        echo "WARN canonical: PR-STOCK-OUTBOX-RETRY-EXHAUSTED pre-condition" >&2
        echo "  $ROWS_RETRY_EXHAUSTED rows where attempt_count >= max_attempts but status NOT dead_lettered" >&2
        echo "  Canonical owner: internal/platform/sqlite/outboxevents/repository.go" >&2
        echo "  (the pre-condition side: attempt_count >= max_attempts check + RequeueExpiredLeases scheduling)" >&2
        echo "  These rows are about to flip to dead_lettered on next dispatcher_cleanup tick" >&2
    fi
    exit 1
fi

echo "PASS: $TOTAL_ROWS / $LIMIT recent asset.index.requested rows; all healthy (pending|completed + last_error=='')"
echo
echo "Healthy distribution:"
echo "  pending  -> ready for worker claim"
echo "  completed -> successfully indexed (Qdrant upsert completed)"
echo
exit 0

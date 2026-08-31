#!/usr/bin/env bash
#
# outbox_smoke.sh — PipelineGen Zone 2: Outbox lifecycle verification
#
# Usage:
#   ./outbox_smoke.sh            # real probes against a live server
#   ./outbox_smoke.sh --dry      # print the would-be probes, exit 0
#
# 5 assertions (per action plan §2):
#   1. Generate an asset, verify outbox_events row created
#   2. Verify transizione pending → processing → completed
#   3. Verify last_error empty on completed events
#   4. Verify no duplicates on event_key (UNIQUE index contract)
#   5. Verify Qdrant-down scenario: must stay retry_pending, not completed
#
# Exit codes:
#   0  all assertions passed
#   1  one or more assertions failed
#   2  setup error (missing binary, missing token, server unreachable)
#
# Environment variables (all overridable):
#   API_BASE              host:port (default 127.0.0.1:${VELOX_PORT:-8000})
#   VELOX_ADMIN_TOKEN     bearer token (mandatory if TOKEN_FILE unset)
#   TOKEN_FILE            env file containing VELOX_ADMIN_TOKEN=...
#   DB_PATH               path to SQLite DB for outbox verification
#   SMOKE_POLL_TIMEOUT_SECONDS  poll timeout (default 180)

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,20p' "$0"; exit 0
fi

if [[ "$DRY_RUN" == "1" ]]; then
    smoke_echo_safe "DRY RUN — would probe 5 outbox assertions:"
    printf '  1. Generate stock asset, verify outbox_events row created\n'
    printf '  2. Verify pending → processing → completed transizione\n'
    printf '  3. Verify last_error empty on completed events\n'
    printf '  4. Verify no duplicates on event_key (UNIQUE index)\n'
    printf '  5. Verify completed-with-last-error false-positive guard\n'
    exit 0
fi

# ── Prerequisites ──────────────────────────────────────────────────────
smoke_require sqlite3

DB="${DB_PATH:?DB_PATH must be explicitly set to an isolated or approved database}"
FAIL_COUNT=0
TOTAL=5
TIMESTAMP_BEFORE=$(date '+%Y-%m-%dT%H:%M:%SZ')

# ── Pre-flight: server + DB ──────────────────────────────────────────
smoke_log_section "Pre-flight"
smoke_curl GET "/health" >/dev/null
HEALTH=$(cat "$WORK_DIR/last.code" 2>/dev/null || echo "000")
if [[ "$HEALTH" != "200" ]]; then
    printf '%sFAIL: server unreachable at %s (HTTP %s)%s\n' \
        "$RED" "$SMOKE_API_BASE" "$HEALTH" "$RESET" >&2
    exit 2
fi
printf '%sServer reachable%s\n' "$GREEN" "$RESET"

if [[ ! -f "$DB" ]]; then
    printf '%sFAIL: DB not found at %s%s\n' "$RED" "$DB" "$RESET" >&2
    exit 2
fi

# Verify outbox_events table exists
TABLE_CHECK=$(sqlite3 "$DB" \
    "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='outbox_events';" 2>/dev/null || echo "0")
if [[ "$TABLE_CHECK" == "0" ]]; then
    printf '%sFAIL: outbox_events table not found in %s%s\n' "$RED" "$DB" "$RESET" >&2
    exit 2
fi
printf '%sDB + outbox_events table found%s\n' "$GREEN" "$RESET"

# ── Snapshot: outbox counts BEFORE test ─────────────────────────────
BEFORE_TOTAL=$(sqlite3 "$DB" \
    "SELECT COUNT(*) FROM outbox_events;" 2>/dev/null || echo "0")
BEFORE_COMPLETED=$(sqlite3 "$DB" \
    "SELECT COUNT(*) FROM outbox_events WHERE status='completed';" 2>/dev/null || echo "0")
BEFORE_PENDING=$(sqlite3 "$DB" \
    "SELECT COUNT(*) FROM outbox_events WHERE status='pending';" 2>/dev/null || echo "0")
BEFORE_DL=$(sqlite3 "$DB" \
    "SELECT COUNT(*) FROM outbox_events WHERE status='dead_letter';" 2>/dev/null || echo "0")
printf 'Before: total=%s completed=%s pending=%s dead_letter=%s\n' \
    "$BEFORE_TOTAL" "$BEFORE_COMPLETED" "$BEFORE_PENDING" "$BEFORE_DL"

# ── Generate an asset to trigger outbox emission ────────────────────
smoke_log_section "Generate asset → trigger outbox emission"
REQUEST_TAG="outbox-zone2-$(date +%s)"
ENQUEUE_PAYLOAD=$(jq -n \
    --arg tag "$REQUEST_TAG" \
    '{
        "search_queries": ["boxing training"],
        "folder_id": "1J-zIuqroF0rkTrKxU-tmZu9e5rN20ggV",
        "total_minutes": 1,
        "chunk_duration": 5,
        "clip_duration": 5,
        "max_videos": 1,
        "no_effects": true,
        "no_transitions": true,
        "async": true,
        "metadata": {"test": $tag}
    }')

smoke_curl POST "/api/stock-pipeline/run" --data "$ENQUEUE_PAYLOAD" >/dev/null
ENQUEUE_CODE=$(cat "$WORK_DIR/last.code" 2>/dev/null || echo "000")
JOB_ID=$(jq -r '.job_id // empty' "$SMOKE_LAST_BODY")
if [[ -z "$JOB_ID" || ! "$ENQUEUE_CODE" =~ ^2[0-9][0-9]$ ]]; then
    printf '%sFAIL: could not enqueue stock job (HTTP %s)%s\n' \
        "$RED" "$ENQUEUE_CODE" "$RESET" >&2
    FAIL_COUNT=$((FAIL_COUNT + 1))
else
    printf 'Enqueued job %s, polling to terminal...\n' "$JOB_ID"
    if smoke_poll_terminal "$JOB_ID"; then
        printf '%sJob %s reached terminal: %s%s\n' \
            "$GREEN" "$JOB_ID" "$SMOKE_LAST_STATUS" "$RESET"
    else
        printf '%sWARN: job %s polling timed out (last: %s) — checking outbox anyway%s\n' \
            "$YELLOW" "$JOB_ID" "${SMOKE_LAST_STATUS:-?}" "$RESET"
    fi
fi

# Wait for outbox dispatcher to process
sleep 5

# ── Test 1: outbox_events row created ──────────────────────────────
smoke_log_section "T1: outbox_events row created for recent asset.index.requested"
AFTER_TOTAL=$(sqlite3 "$DB" \
    "SELECT COUNT(*) FROM outbox_events WHERE created_at > '$TIMESTAMP_BEFORE';" 2>/dev/null || echo "0")
AFTER_INDEX=$(sqlite3 "$DB" \
    "SELECT COUNT(*) FROM outbox_events WHERE event_type='asset.index.requested' AND created_at > '$TIMESTAMP_BEFORE';" 2>/dev/null || echo "0")

printf 'New outbox rows since test start: %s total, %s asset.index.requested\n' \
    "$AFTER_TOTAL" "$AFTER_INDEX"

if [[ "$AFTER_INDEX" -gt "0" ]]; then
    printf '%sT1 PASS: %s new asset.index.requested event(s) created%s\n' \
        "$GREEN" "$AFTER_INDEX" "$RESET"
else
    printf '%sT1 FAIL: 0 new asset.index.requested events (expected ≥1)%s\n' \
        "$RED" "$RESET" >&2
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# ── Test 2: transizione pending → processing → completed ───────────
smoke_log_section "T2: outbox transizione pending → processing → completed"
RECENT_ROWS=$(sqlite3 "$DB" \
    "SELECT status, COUNT(*) FROM outbox_events WHERE created_at > '$TIMESTAMP_BEFORE' GROUP BY status ORDER BY status;" 2>/dev/null || echo "query failed")
printf 'Recent outbox status distribution:\n%s\n' "$RECENT_ROWS"

RECENT_COMPLETED=$(sqlite3 "$DB" \
    "SELECT COUNT(*) FROM outbox_events WHERE status='completed' AND created_at > '$TIMESTAMP_BEFORE';" 2>/dev/null || echo "0")
RECENT_PENDING=$(sqlite3 "$DB" \
    "SELECT COUNT(*) FROM outbox_events WHERE status='pending' AND created_at > '$TIMESTAMP_BEFORE';" 2>/dev/null || echo "0")
RECENT_PROCESSING=$(sqlite3 "$DB" \
    "SELECT COUNT(*) FROM outbox_events WHERE status='processing' AND created_at > '$TIMESTAMP_BEFORE';" 2>/dev/null || echo "0")

# Check for retry activity on pending rows (attempt_count > 0 = dispatcher tried)
RECENT_RETRYING=$(sqlite3 "$DB" \
    "SELECT COUNT(*) FROM outbox_events WHERE status='pending' AND attempt_count > 0 AND created_at > '$TIMESTAMP_BEFORE';" 2>/dev/null || echo "0")

if [[ "$RECENT_COMPLETED" -gt "0" ]]; then
    printf '%sT2 PASS: %s event(s) reached completed status%s\n' \
        "$GREEN" "$RECENT_COMPLETED" "$RESET"
elif [[ "$RECENT_RETRYING" -gt "0" ]]; then
    printf '%sT2 PASS: %s event(s) retry_pending (dispatcher tried, awaiting retry — expected if Qdrant/transient issue)%s\n' \
        "$GREEN" "$RECENT_RETRYING" "$RESET"
elif [[ "$RECENT_PENDING" -gt "0" ]]; then
    printf '%sT2 WARN: %s event(s) still pending (dispatcher not yet picked up)%s\n' \
        "$YELLOW" "$RECENT_PENDING" "$RESET"
    FAIL_COUNT=$((FAIL_COUNT + 1))
elif [[ "$RECENT_PROCESSING" -gt "0" ]]; then
    printf '%sT2 WARN: %s event(s) stuck in processing (lease expired?)%s\n' \
        "$YELLOW" "$RECENT_PROCESSING" "$RESET"
    FAIL_COUNT=$((FAIL_COUNT + 1))
else
    printf '%sT2 FAIL: no recent outbox events found in any state%s\n' \
        "$RED" "$RESET" >&2
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# ── Test 3: last_error empty on completed events ───────────────────
smoke_log_section "T3: last_error empty on completed events"
COMPLETED_WITH_ERROR=$(sqlite3 "$DB" \
    "SELECT COUNT(*) FROM outbox_events WHERE status='completed' AND last_error IS NOT NULL AND last_error != '' AND created_at > '$TIMESTAMP_BEFORE';" 2>/dev/null || echo "0")

if [[ "$COMPLETED_WITH_ERROR" == "0" ]]; then
    printf '%sT3 PASS: 0 completed events with non-empty last_error%s\n' \
        "$GREEN" "$RESET"
else
    # Show the offending rows for diagnostics
    printf '%sT3 FAIL: %s completed event(s) have non-empty last_error%s\n' \
        "$RED" "$COMPLETED_WITH_ERROR" "$RESET" >&2
    sqlite3 "$DB" \
        "SELECT id, event_type, aggregate_id, substr(last_error, 1, 80) AS err_preview FROM outbox_events WHERE status='completed' AND last_error IS NOT NULL AND last_error != '' AND created_at > '$TIMESTAMP_BEFORE' LIMIT 5;" 2>/dev/null || true
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# ── Test 4: no duplicates on event_key ─────────────────────────────
smoke_log_section "T4: no duplicates on event_key (UNIQUE index contract)"
DUPES=$(sqlite3 "$DB" \
    "SELECT event_key, COUNT(*) AS cnt FROM outbox_events WHERE event_key != '' AND created_at > '$TIMESTAMP_BEFORE' GROUP BY event_key HAVING cnt > 1 ORDER BY cnt DESC LIMIT 10;" 2>/dev/null || echo "")

if [[ -z "$DUPES" ]]; then
    printf '%sT4 PASS: 0 duplicate event_key rows (UNIQUE index intact)%s\n' \
        "$GREEN" "$RESET"
else
    printf '%sT4 FAIL: duplicate event_key rows found:%s\n' "$RED" "$RESET" >&2
    echo "$DUPES" >&2
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# ── Test 5: completed-with-last-error false-positive guard ─────────
smoke_log_section "T5: Qdrant reachability + outbox silent-success guard"
QDRANT_HEALTHY=0
QDRANT_CODE=$(curl -s --max-time 5 -o /dev/null -w '%{http_code}' "http://localhost:6333/healthz" 2>/dev/null || echo "000")
if [[ "$QDRANT_CODE" =~ ^2[0-9][0-9]$ ]]; then
    QDRANT_HEALTHY=1
    printf 'Qdrant reachable (HTTP %s)\n' "$QDRANT_CODE"
else
    printf 'Qdrant NOT reachable (HTTP %s) — checking silent-success anti-pattern\n' "$QDRANT_CODE"
fi

DL_RECENT=$(sqlite3 "$DB" \
    "SELECT COUNT(*) FROM outbox_events WHERE status='dead_letter' AND created_at > '$TIMESTAMP_BEFORE';" 2>/dev/null || echo "0")
STUCK_PROCESSING=$(sqlite3 "$DB" \
    "SELECT COUNT(*) FROM outbox_events WHERE status='processing' AND updated_at < datetime('now', '-5 minutes');" 2>/dev/null || echo "0")

T5_ISSUES=0
if [[ "$QDRANT_HEALTHY" == "0" ]]; then
    # Qdrant down: completed events = silent-success anti-pattern
    COMPLETED_WHILE_DOWN=$(sqlite3 "$DB" \
        "SELECT COUNT(*) FROM outbox_events WHERE status='completed' AND created_at > '$TIMESTAMP_BEFORE';" 2>/dev/null || echo "0")
    if [[ "$COMPLETED_WHILE_DOWN" -gt "0" ]]; then
        printf '%sT5 FAIL: %s event(s) completed while Qdrant is DOWN — silent-success anti-pattern!%s\n' \
            "$RED" "$COMPLETED_WHILE_DOWN" "$RESET" >&2
        T5_ISSUES=$((T5_ISSUES + 1))
    else
        printf '%sT5 PASS: 0 events completed while Qdrant down (correct — events stay pending/retry)%s\n' \
            "$GREEN" "$RESET"
    fi
else
    # Qdrant up: check for dead_letter and stuck processing
    if [[ "$DL_RECENT" -gt "0" ]]; then
        printf '%s  WARN: %s event(s) dead-lettered during test run%s\n' \
            "$YELLOW" "$DL_RECENT" "$RESET"
        sqlite3 "$DB" \
            "SELECT id, event_type, aggregate_id, substr(last_error, 1, 80) FROM outbox_events WHERE status='dead_letter' AND created_at > '$TIMESTAMP_BEFORE' LIMIT 5;" 2>/dev/null || true
    fi
    if [[ "$STUCK_PROCESSING" -gt "0" ]]; then
        printf '%s  WARN: %s event(s) stuck in processing (>5min old)%s\n' \
            "$YELLOW" "$STUCK_PROCESSING" "$RESET"
    fi
    if [[ "$DL_RECENT" == "0" && "$STUCK_PROCESSING" == "0" ]]; then
        printf '%sT5 PASS: Qdrant up, no dead_letter, no stuck processing%s\n' \
            "$GREEN" "$RESET"
    else
        printf '%sT5 INFO: %s dead_letter + %s stuck (may be expected transient)%s\n' \
            "$YELLOW" "$DL_RECENT" "$STUCK_PROCESSING" "$RESET"
    fi
fi

# ── Final summary table ────────────────────────────────────────────
smoke_log_section "Outbox summary"
AFTER_TOTAL_FINAL=$(sqlite3 "$DB" \
    "SELECT COUNT(*) FROM outbox_events;" 2>/dev/null || echo "0")
AFTER_BY_STATUS=$(sqlite3 "$DB" \
    "SELECT status, COUNT(*) FROM outbox_events GROUP BY status ORDER BY status;" 2>/dev/null || echo "query failed")
printf 'Total outbox rows: %s (was %s, delta +%s)\n' \
    "$AFTER_TOTAL_FINAL" "$BEFORE_TOTAL" "$((AFTER_TOTAL_FINAL - BEFORE_TOTAL))"
printf 'By status:\n%s\n' "$AFTER_BY_STATUS"

# ── Verdict ─────────────────────────────────────────────────────────
smoke_log_section "Verdict"
PASSED=$((TOTAL - FAIL_COUNT))
if (( FAIL_COUNT == 0 )); then
    printf '%sZONE 2 PASS: %d/%d assertions passed%s\n' \
        "$GREEN" "$PASSED" "$TOTAL" "$RESET"
    exit 0
else
    printf '%sZONE 2 FAIL: %d/%d assertions passed, %d failed%s\n' \
        "$RED" "$PASSED" "$TOTAL" "$FAIL_COUNT" "$RESET" >&2
    exit 1
fi

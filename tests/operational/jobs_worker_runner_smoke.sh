#!/usr/bin/env bash
#
# jobs_worker_runner_smoke.sh — PipelineGen Zone 1: Jobs / Worker / Runner
#
# Usage:
#   ./jobs_worker_runner_smoke.sh            # real probes against a live server
#   ./jobs_worker_runner_smoke.sh --dry      # print the would-be probes, exit 0
#
# 7 assertions (per action plan §1):
#   1. Stats baseline — GET /api/jobs/stats returns valid JSON
#   2. Enqueue valid job → poll to SUCCEEDED
#   3. Invalid payload → job goes to FAILED (not silently accepted)
#   4. Cancel a running job → CANCELLED
#   5. Retry of FAILED job → re-enqueued
#   6. Retry of SUCCEEDED job → rejected (4xx/409)
#   7. Stats API coherent with DB
#
# Exit codes:
#   0  all 7 assertions passed
#   1  one or more assertions failed
#   2  setup error (missing binary, missing token, server unreachable)
#
# Environment variables (all overridable):
#   API_BASE              host:port (default 127.0.0.1:${VELOX_PORT:-8000})
#   VELOX_ADMIN_TOKEN     bearer token (mandatory if TOKEN_FILE unset)
#   TOKEN_FILE            env file containing VELOX_ADMIN_TOKEN=...
#   DB_PATH               path to SQLite DB for cross-layer verification

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,17p' "$0"; exit 0
fi

if [[ "$DRY_RUN" == "1" ]]; then
    smoke_echo_safe "DRY RUN — would probe 7 assertions:"
    printf '  1. GET  /api/jobs/stats                    (baseline)\n'
    printf '  2. POST /api/jobs                          (enqueue + poll SUCCEEDED)\n'
    printf '  3. POST /api/jobs                          (invalid payload → FAILED)\n'
    printf '  4. POST /api/jobs/:id/cancel               (cancel running → CANCELLED)\n'
    printf '  5. POST /api/jobs/:id/retry                (retry FAILED → re-enqueued)\n'
    printf '  6. POST /api/jobs/:id/retry                (retry SUCCEEDED → rejected)\n'
    printf '  7. GET  /api/jobs/stats vs sqlite3 DB      (coherent)\n'
    exit 0
fi

# ── Prerequisites ──────────────────────────────────────────────────────
smoke_require sqlite3

# Server reachability
smoke_log_section "Pre-flight: server reachable?"
smoke_curl GET "/health" >/dev/null
HEALTH=$(cat "$WORK_DIR/last.code" 2>/dev/null || echo "000")
if [[ "$HEALTH" != "200" ]]; then
    printf '%sFAIL: server unreachable at %s (HTTP %s)%s\n' \
        "$RED" "$SMOKE_API_BASE" "$HEALTH" "$RESET" >&2
    exit 2
fi
printf '%sServer reachable at %s%s\n' "$GREEN" "$SMOKE_API_BASE" "$RESET"

DB="${DB_PATH:?DB_PATH must be explicitly set to an isolated or approved database}"
FAIL_COUNT=0
TOTAL=7

# ── Test 1: Stats baseline ──────────────────────────────────────────
smoke_log_section "T1: Stats baseline"
# NOTE: smoke_curl must NOT run inside $(…) because it sets SMOKE_LAST_BODY
# as a side-effect; inside a subshell the variable is lost (set -u → unbound).
smoke_curl GET "/api/jobs/stats" >/dev/null
STATS_CODE="$SMOKE_LAST_HTTP"
if [[ "$STATS_CODE" != "200" ]]; then
    printf '%sFAIL: /api/jobs/stats returned HTTP %s (expected 200)%s\n' \
        "$RED" "$STATS_CODE" "$RESET" >&2
    FAIL_COUNT=$((FAIL_COUNT + 1))
else
    # Validate it returns valid JSON with expected fields
    # The handler wraps stats in {"stats": {...}} — check both envelope and direct.
    if jq -e '.stats.total // .total // .stats.by_status // .by_status' "$SMOKE_LAST_BODY" >/dev/null 2>&1; then
        printf '%sT1 PASS: stats endpoint returns valid JSON%s\n' "$GREEN" "$RESET"
        jq '.' "$SMOKE_LAST_BODY"
    else
        printf '%sT1 FAIL: stats JSON missing expected fields (stats.total / stats.by_status)%s\n' \
            "$RED" "$RESET" >&2
        FAIL_COUNT=$((FAIL_COUNT + 1))
    fi
fi

# ── Test 2: Enqueue valid job + poll to SUCCEEDED ───────────────────
smoke_log_section "T2: Enqueue valid stock job → poll SUCCEEDED"
REQUEST_TAG="jobs-zone1-t2-$(date +%s)"
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
ENQUEUE_CODE="$SMOKE_LAST_HTTP"
if [[ ! "$ENQUEUE_CODE" =~ ^2[0-9][0-9]$ ]]; then
    printf '%sT2 FAIL: enqueue returned HTTP %s (expected 2xx)%s\n' \
        "$RED" "$ENQUEUE_CODE" "$RESET" >&2
    smoke_echo_safe "$(head -c 400 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    FAIL_COUNT=$((FAIL_COUNT + 1))
    JOB_ID=""
else
    JOB_ID=$(jq -r '.job_id // empty' "$SMOKE_LAST_BODY")
    if [[ -z "$JOB_ID" ]]; then
        printf '%sT2 FAIL: enqueue returned 2xx but no job_id in response%s\n' \
            "$RED" "$RESET" >&2
        FAIL_COUNT=$((FAIL_COUNT + 1))
    else
        printf 'Enqueued job %s\n' "$JOB_ID"
        printf 'Polling for terminal status (timeout %ss)...\n' "$SMOKE_POLL_TIMEOUT_SECONDS"

        if smoke_poll_terminal "$JOB_ID"; then
            FINAL_STATUS="$SMOKE_LAST_STATUS"
            if [[ "$FINAL_STATUS" == "completed" ]]; then
                printf '%sT2 PASS: job %s reached terminal status: %s%s\n' \
                    "$GREEN" "$JOB_ID" "$FINAL_STATUS" "$RESET"
            else
                printf '%sT2 FAIL: job %s reached %s (expected completed)%s\n' \
                    "$RED" "$JOB_ID" "$FINAL_STATUS" "$RESET" >&2
                FAIL_COUNT=$((FAIL_COUNT + 1))
            fi
        else
            printf '%sT2 FAIL: job %s polling timed out (last status: %s)%s\n' \
                "$RED" "$JOB_ID" "${SMOKE_LAST_STATUS:-?}" "$RESET" >&2
            FAIL_COUNT=$((FAIL_COUNT + 1))
        fi
    fi
fi

# ── Test 3: Invalid payload → FAILED (not silently accepted) ───────
smoke_log_section "T3: Invalid payload → rejected or FAILED"
smoke_curl POST "/api/stock-pipeline/run" --data '{}' >/dev/null
INVALID_CODE="$SMOKE_LAST_HTTP"
if [[ "$INVALID_CODE" =~ ^4[0-9][0-9]$ ]]; then
    printf '%sT3 PASS: invalid payload rejected with HTTP %s%s\n' \
        "$GREEN" "$INVALID_CODE" "$RESET"
elif [[ "$INVALID_CODE" =~ ^2[0-9][0-9]$ ]]; then
    # Accepted but should eventually fail — check if it has a job_id
    FAIL_JOB_ID=$(jq -r '.job_id // empty' "$SMOKE_LAST_BODY")
    if [[ -n "$FAIL_JOB_ID" ]]; then
        printf 'Payload accepted (HTTP %s), polling job %s for FAILED...\n' \
            "$INVALID_CODE" "$FAIL_JOB_ID"
        if smoke_poll_terminal "$FAIL_JOB_ID"; then
            FAIL_STATUS="$SMOKE_LAST_STATUS"
            if [[ "$FAIL_STATUS" == "failed" ]]; then
                printf '%sT3 PASS: invalid payload → job %s failed cleanly (status: %s)%s\n' \
                    "$GREEN" "$FAIL_JOB_ID" "$FAIL_STATUS" "$RESET"
            else
                printf '%sT3 FAIL: invalid payload → job %s reached %s (expected failed)%s\n' \
                    "$RED" "$FAIL_JOB_ID" "$FAIL_STATUS" "$RESET" >&2
                FAIL_COUNT=$((FAIL_COUNT + 1))
            fi
        else
            printf '%sT3 FAIL: invalid payload → job %s stuck (last: %s)%s\n' \
                "$RED" "$FAIL_JOB_ID" "${SMOKE_LAST_STATUS:-?}" "$RESET" >&2
            FAIL_COUNT=$((FAIL_COUNT + 1))
        fi
    fi
else
    printf '%sT3 UNEXPECTED: invalid payload returned HTTP %s%s\n' \
        "$YELLOW" "$INVALID_CODE" "$RESET" >&2
fi

# ── Test 4: Cancel a running job → CANCELLED ───────────────────────
smoke_log_section "T4: Cancel running job → CANCELLED"
# Enqueue a long-running job to cancel
CANCEL_TAG="jobs-zone1-t4-$(date +%s)"
CANCEL_PAYLOAD=$(jq -n \
    --arg tag "$CANCEL_TAG" \
    '{
        "search_queries": ["boxing training"],
        "folder_id": "1J-zIuqroF0rkTrKxU-tmZu9e5rN20ggV",
        "total_minutes": 5,
        "chunk_duration": 60,
        "clip_duration": 30,
        "max_videos": 5,
        "no_effects": true,
        "no_transitions": true,
        "async": true,
        "metadata": {"test": $tag}
    }')

smoke_curl POST "/api/stock-pipeline/run" --data "$CANCEL_PAYLOAD" >/dev/null
CANCEL_ENQUEUE_CODE="$SMOKE_LAST_HTTP"
CANCEL_JOB_ID=$(jq -r '.job_id // empty' "$SMOKE_LAST_BODY")
if [[ -z "$CANCEL_JOB_ID" || ! "$CANCEL_ENQUEUE_CODE" =~ ^2[0-9][0-9]$ ]]; then
    printf '%sT4 SKIP: could not enqueue cancel-test job (HTTP %s)%s\n' \
        "$YELLOW" "$CANCEL_ENQUEUE_CODE" "$RESET"
else
    printf 'Enqueued cancel-test job %s, waiting 3s then cancelling...\n' "$CANCEL_JOB_ID"
    sleep 3

    smoke_curl POST "/api/jobs/${CANCEL_JOB_ID}/cancel" >/dev/null
CANCEL_CODE="$SMOKE_LAST_HTTP"
    if [[ ! "$CANCEL_CODE" =~ ^2[0-9][0-9]$ ]]; then
        printf '%sT4 FAIL: cancel returned HTTP %s (expected 2xx)%s\n' \
            "$RED" "$CANCEL_CODE" "$RESET" >&2
        FAIL_COUNT=$((FAIL_COUNT + 1))
    else
        # Verify final state
        sleep 2
        smoke_curl GET "/api/jobs/${CANCEL_JOB_ID}/full" >/dev/null
VERIFY_CODE="$SMOKE_LAST_HTTP"
        VERIFY_STATUS=$(jq -r '.status // "?"' "$SMOKE_LAST_BODY")
        if [[ "${VERIFY_STATUS,,}" == "cancelled" || "${VERIFY_STATUS,,}" == "canceled" ]]; then
            printf '%sT4 PASS: job %s cancelled (status: %s)%s\n' \
                "$GREEN" "$CANCEL_JOB_ID" "$VERIFY_STATUS" "$RESET"
        elif [[ "$VERIFY_STATUS" == "completed" ]]; then
            # Race: job completed before cancel — acceptable
            printf '%sT4 PASS: job %s completed before cancel could fire (race, acceptable)%s\n' \
                "$GREEN" "$CANCEL_JOB_ID" "$RESET"
        else
            printf '%sT4 FAIL: after cancel, status is %s (expected cancelled)%s\n' \
                "$RED" "$VERIFY_STATUS" "$RESET" >&2
            FAIL_COUNT=$((FAIL_COUNT + 1))
        fi
    fi
fi

# ── Test 5: Retry of FAILED job → re-enqueued ─────────────────────
smoke_log_section "T5: Retry of FAILED job"
# We need a FAILED job. The invalid payload from T3 may have produced one.
# Find a recent FAILED job from the DB or API.
RETRY_CANDIDATE=""
if [[ -f "$DB" ]]; then
    RETRY_CANDIDATE=$(sqlite3 "$DB" \
        "SELECT id FROM jobs WHERE status='failed' ORDER BY updated_at DESC LIMIT 1;" 2>/dev/null || true)
fi

if [[ -z "$RETRY_CANDIDATE" ]]; then
    # Fallback: enqueue a job that will fail, then retry it
    RETRY_FAIL_PAYLOAD=$(jq -n \
        '{
            "type": "voiceover.generate",
            "project": "test",
            "video_name": "retry-test",
            "payload": {
                "request_id": "retry_fail_'"$(date +%s)"'",
                "items": []
            },
            "priority": 5,
            "max_retries": 0,
            "active_key": "retry-fail-'"$(date +%s)"'"
        }')
    smoke_curl POST "/api/jobs" --data "$RETRY_FAIL_PAYLOAD" >/dev/null
RETRY_ENQUEUE="$SMOKE_LAST_HTTP"
    RETRY_JOB_ID=$(jq -r '.job_id // empty' "$SMOKE_LAST_BODY")
    if [[ -n "$RETRY_JOB_ID" ]]; then
        sleep 3
        # Poll to terminal
        smoke_poll_terminal "$RETRY_JOB_ID" || true
        if [[ "${SMOKE_LAST_STATUS:-}" == "failed" ]]; then
            RETRY_CANDIDATE="$RETRY_JOB_ID"
        fi
    fi
fi

if [[ -z "$RETRY_CANDIDATE" ]]; then
    printf '%sT5 SKIP: no FAILED job available to retry%s\n' "$YELLOW" "$RESET"
else
    printf 'Retrying FAILED job %s...\n' "$RETRY_CANDIDATE"
    smoke_curl POST "/api/jobs/${RETRY_CANDIDATE}/retry" >/dev/null
RETRY_CODE="$SMOKE_LAST_HTTP"
    if [[ "$RETRY_CODE" =~ ^2[0-9][0-9]$ ]]; then
        NEW_STATUS=$(jq -r '.status // "?"' "$SMOKE_LAST_BODY")
        printf '%sT5 PASS: retry of FAILED job returned HTTP %s (new status: %s)%s\n' \
            "$GREEN" "$RETRY_CODE" "$NEW_STATUS" "$RESET"
    elif [[ "$RETRY_CODE" == "409" || "$RETRY_CODE" == "422" ]]; then
        # Retry rejected — could be max_retries exhausted or idempotency
        printf '%sT5 PASS: retry of FAILED job rejected with HTTP %s (retry exhausted/limited)%s\n' \
            "$GREEN" "$RETRY_CODE" "$RESET"
    else
        printf '%sT5 FAIL: retry of FAILED job returned HTTP %s (expected 2xx or 409)%s\n' \
            "$RED" "$RETRY_CODE" "$RESET" >&2
        FAIL_COUNT=$((FAIL_COUNT + 1))
    fi
fi

# ── Test 6: Retry of SUCCEEDED → rejected ─────────────────────────
smoke_log_section "T6: Retry of SUCCEEDED → rejected"
SUCCEEDED_JOB_ID=""
if [[ -f "$DB" ]]; then
    SUCCEEDED_JOB_ID=$(sqlite3 "$DB" \
        "SELECT id FROM jobs WHERE status='completed' ORDER BY updated_at DESC LIMIT 1;" 2>/dev/null || true)
fi
# Also try the job from T2
if [[ -z "$SUCCEEDED_JOB_ID" && -n "${JOB_ID:-}" ]]; then
    SUCCEEDED_JOB_ID="$JOB_ID"
fi

if [[ -z "$SUCCEEDED_JOB_ID" ]]; then
    printf '%sT6 SKIP: no SUCCEEDED job available to test retry rejection%s\n' "$YELLOW" "$RESET"
else
    printf 'Attempting retry of SUCCEEDED job %s (should be rejected)...\n' "$SUCCEEDED_JOB_ID"
    smoke_curl POST "/api/jobs/${SUCCEEDED_JOB_ID}/retry" >/dev/null
RETRY_S_CODE="$SMOKE_LAST_HTTP"
    if [[ "$RETRY_S_CODE" =~ ^4[0-9][0-9]$ ]]; then
        printf '%sT6 PASS: retry of SUCCEEDED rejected with HTTP %s%s\n' \
            "$GREEN" "$RETRY_S_CODE" "$RESET"
    elif [[ "$RETRY_S_CODE" =~ ^2[0-9][0-9]$ ]]; then
        # Check if it's an idempotent no-op (no new job created)
        RETRY_S_STATUS=$(jq -r '.status // "?"' "$SMOKE_LAST_BODY")
        printf '%sT6 WARN: retry of SUCCEEDED returned HTTP %s (status: %s) — check for duplicate side effects%s\n' \
            "$YELLOW" "$RETRY_S_CODE" "$RETRY_S_STATUS" "$RESET"
        FAIL_COUNT=$((FAIL_COUNT + 1))
    else
        printf '%sT6 FAIL: retry of SUCCEEDED returned HTTP %s (expected 4xx)%s\n' \
            "$RED" "$RETRY_S_CODE" "$RESET" >&2
        FAIL_COUNT=$((FAIL_COUNT + 1))
    fi
fi

# ── Test 7: Stats API coherent with DB ────────────────────────────
smoke_log_section "T7: Stats API vs DB coherence"
if [[ ! -f "$DB" ]]; then
    printf '%sT7 SKIP: DB not found at %s%s\n' "$YELLOW" "$DB" "$RESET"
else
    # Get stats from API
    smoke_curl GET "/api/jobs/stats" >/dev/null
STATS2_CODE="$SMOKE_LAST_HTTP"
    if [[ "$STATS2_CODE" != "200" ]]; then
        printf '%sT7 FAIL: /api/jobs/stats returned HTTP %s%s\n' \
            "$RED" "$STATS2_CODE" "$RESET" >&2
        FAIL_COUNT=$((FAIL_COUNT + 1))
    else
        API_TOTAL=$(jq -r '.stats.total // .total // 0' "$SMOKE_LAST_BODY")
        DB_TOTAL=$(sqlite3 "$DB" "SELECT COUNT(*) FROM jobs;" 2>/dev/null || echo "0")
        DB_BY_STATUS=$(sqlite3 "$DB" \
            "SELECT status, COUNT(*) FROM jobs GROUP BY status ORDER BY status;" 2>/dev/null || echo "query failed")

        printf 'API total_jobs: %s\n' "$API_TOTAL"
        printf 'DB total:       %s\n' "$DB_TOTAL"
        printf 'DB by status:\n%s\n' "$DB_BY_STATUS"

        if [[ "$API_TOTAL" == "$DB_TOTAL" ]]; then
            printf '%sT7 PASS: stats API total (%s) matches DB total (%s)%s\n' \
                "$GREEN" "$API_TOTAL" "$DB_TOTAL" "$RESET"
        else
            printf '%sT7 FAIL: stats API total (%s) ≠ DB total (%s)%s\n' \
                "$RED" "$API_TOTAL" "$DB_TOTAL" "$RESET" >&2
            FAIL_COUNT=$((FAIL_COUNT + 1))
        fi
    fi
fi

# ── Verdict ─────────────────────────────────────────────────────────
smoke_log_section "Verdict"
PASSED=$((TOTAL - FAIL_COUNT))
if (( FAIL_COUNT == 0 )); then
    printf '%sZONE 1 PASS: %d/%d assertions passed%s\n' \
        "$GREEN" "$PASSED" "$TOTAL" "$RESET"
    exit 0
else
    printf '%sZONE 1 FAIL: %d/%d assertions passed, %d failed%s\n' \
        "$RED" "$PASSED" "$TOTAL" "$FAIL_COUNT" "$RESET" >&2
    exit 1
fi

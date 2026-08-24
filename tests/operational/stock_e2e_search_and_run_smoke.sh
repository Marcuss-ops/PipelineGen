#!/usr/bin/env bash
# stock_e2e_search_and_run_smoke.sh
#
# STK-E2E-B probe for architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05
# Iterates over the 9 Drive folder fixtures (operator-supplied test scope-
# limit), invokes POST /api/stock-pipeline/search-and-run with the canonical
# `stock-e2e` payload (queries=boxing-training-gym + boxing-crowd-reaction,
# total_minutes=1, chunk_duration=10, clip_duration=10, max_videos=2) and
# polls /api/jobs/{job_id}/full every 3s for 60 iter (~180s budget per
# folder), aggregating pass/fail across all folders. Per google/static
# ssecrets-stock pipeline spec, the job reaches terminal success when
# status transitions to `SUCCEEDED|completed|INDEX_PENDING`; failure is
# detected on `FAILED|failed`.
#
# Per godlike/07 NO-FAKE-AVAILABILITY + AGENTS.md Pattern 6 (diagnostic-
# surface-first), this is the canonical operator-facing receipt that the
# stock pipeline's search-and-run path is wired end-to-end across the
# 9 canonical test folders. Per godlike/06 SSOT owner-per-fact, the
# /api/stock-pipeline/search-and-run canonical owner is
# `internal/api/assets/stock/handler.go::HandleSearchAndRun`.
#
# Tests 9 folders SEQUENTIALLY (parallel would race the shared curl/jq
# backend + the broker rate limit; worst-case runtime is 9 x 180s = ~27min
# for 9 PASS, ~3min for 9 FAIL since we break on first FAIL signal per
# folder).
#
# Per-folder receipt: writes
#   /tmp/stock-tests/{REQ_TAG}.json          (initial POST response body)
#   /tmp/stock-tests/{REQ_TAG}-final.json    (terminal-SUCCEEDED body)
#   /tmp/stock-tests/{REQ_TAG}-failed.json   (terminal-FAILED body + error)
# These feed the aggregator probe STK-E2E-H for the 14-point verdict.
#
# Exit codes (per action plan §5):
#   0 = PASS (all 9 folders reached SUCCEEDED|completed|INDEX_PENDING)
#   1 = FAIL (one or more folders failed; canonical PR-STOCK-* mapping
#             at action-plan §4)
#   2 = prerequisite missing (server down / token wrong / curl|jq missing)
#
# Self-checks: `bash -n tests/operational/stock_e2e_search_and_run_smoke.sh`
# must exit 0 (validated at commit time per §5).
#
# Overridable env vars:
#   BASE         = http://127.0.0.1:8000
#   AUTH         = "Authorization: Bearer ${VELOX_ADMIN_TOKEN:-}"
#   OUT_DIR      = /tmp/stock-tests
#   MAX_POLL_ITERATIONS  = 60
#   POLL_INTERVAL_SECONDS = 3

set -euo pipefail

# ---- 9 Drive folder fixtures (per user spec; CANONICAL TEST SCOPE-LIMIT) -
# NOTE: the authoritative folder registry lives at
# internal/platform/drive/folders/registry.go — these are operator-
# supplied test fixtures for the battery wave, NOT production SSOT.
FOLDERS=(
    "1lSp-s8mNJOUOxIZbuZ0NjvzbXVMB1Y3I"
    "120d5xpzKN4rE5obIC16AtG_66NXJrlF0"
    "1655kxyQMiJzN5Ugwh8uzNUdEgVJr3O9O"
    "1yhqumS6yG91ZDFBzxeJWXgsUP7mVPXfL"
    "1oOlaSOwq1P7_yLfanvBqxwMvEoV1n4Wo"
    "1bR5XyiB04bJxaUyQGpWNqN9BVgXAkc1C"
    "1BkxSjbV4Dysv_XffuHmqnfDxg5d0Xs9N"
    "1bNb14kz0m4Vxd_F3af8lcIL-bgvZFJ6P"
    "1FQ0RKrXVYKNvosp_IHIskh_2aJ2m7ok6"
)
FOLDER_COUNT=${#FOLDERS[@]}

# Per godlike/07 NO-FAKE-AVAILABILITY: empty FOLDERS = operator-misleading 'PASS'
# (zero folders tested). Fail-closed to prevent silent-success wave-flip.
if [ "${#FOLDERS[@]}" -eq 0 ]; then
    echo "FAIL: FOLDERS array is empty (zero test folders configured)" >&2
    echo "FAIL canonical: probe-config error (operator override gone wrong)" >&2
    exit 1
fi


# ─── Fail-closed auth gate (AGENTS.md "no-fake-availability") ───────────
# If VELOX_ADMIN_TOKEN is unset or empty, refuse to run. The canonical
# loader is `scripts/with-velox-auth`; the Makefile-level auth-check
# target runs the same loader against /api/artlist/job-consumer as a
# pre-flight gate. The historical placeholder `test-admin-token-12345`
# is forbidden by AGENTS.md and must never appear in this script or any
# other operational surface again — see AGENTS.md "Authentication SSOT".
: "${VELOX_ADMIN_TOKEN:?❌ VELOX_ADMIN_TOKEN unset — source scripts/with-velox-auth (or export manually before rerunning).}"

# ---- Configuration --------------------------------------------------------
BASE="${BASE:-http://127.0.0.1:8000}"
AUTH="${AUTH:-Authorization: Bearer ${VELOX_ADMIN_TOKEN:-}}"
OUT_DIR="${OUT_DIR:-/tmp/stock-tests}"
MAX_POLL_ITERATIONS="${MAX_POLL_ITERATIONS:-60}"
POLL_INTERVAL_SECONDS="${POLL_INTERVAL_SECONDS:-3}"

# ---- Aggregation state -----------------------------------------------------
declare -a PASS_FOLDERS=()
declare -a FAIL_FOLDERS=()
declare -a NOTERM_FOLDERS=()

# ---- Prerequisite checks (exit 2) ----------------------------------------
command -v curl >/dev/null 2>&1 || { echo "FAIL: curl not on PATH (exit 2)" >&2; exit 2; }
command -v jq >/dev/null 2>&1   || { echo "FAIL: jq not on PATH (exit 2)" >&2; exit 2; }

mkdir -p "$OUT_DIR"

# ---- Server reachability pre-flight (exit 2) ------------------------------
# Per code-reviewer round 1 (probe C): explicit endpoint logging + canonical
# route probe; bumped --max-time 5 -> 10 to avoid false down-flagging on
# slow warm-up. Canonical /api/stock-pipeline/search-and-run probe catches
# the 'server reachable but stock module not mounted' regression surface
# that a generic /healthz probe misses (middleware mismatch, route-not-
# registered).

PROBE_ENDPOINTS=(
    "$BASE/health"
    "$BASE/api/stock-pipeline/search-and-run"
)

PREFLIGHT_OK=0
for endpoint in "${PROBE_ENDPOINTS[@]}"; do
    HTTP=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 \
        -X GET "$endpoint" 2>/dev/null || echo "000")
    case "$HTTP" in
        2*|3*|400|405)
            echo "PRE-FLIGHT: $endpoint -> HTTP $HTTP (reachable + route mounted)"
            PREFLIGHT_OK=1
            break
            ;;
        000)
            echo "PRE-FLIGHT: $endpoint -> unreachable (curl failed)"
            ;;
        *)
            echo "PRE-FLIGHT: $endpoint -> HTTP $HTTP (unusual, continuing)"
            ;;
    esac
done

if [ "$PREFLIGHT_OK" -eq 0 ]; then
    echo
    echo "FAIL: PipelineGen server at $BASE unreachable on all pre-flight endpoints (exit 2)" >&2
    echo "FAIL canonical: server down or VELOX_PORT misconfigured (per action plan §4)" >&2
    exit 2
fi

# ---- Header / logging ------------------------------------------------------
echo "=================================================================="
echo "STK-E2E-B: stock search-and-run loop (${FOLDER_COUNT} folders)"
echo "  BASE           = $BASE"
echo "  OUT_DIR        = $OUT_DIR"
echo "  MAX_POLL       = $MAX_POLL_ITERATIONS x ${POLL_INTERVAL_SECONDS}s per folder"
echo "  Folders        = $FOLDER_COUNT (8-char prefix display only)"
echo "=================================================================="

# ---- Main loop ------------------------------------------------------------
for FOLDER_ID in "${FOLDERS[@]}"; do
    SHORT="${FOLDER_ID:0:8}"
    REQ_TAG="stock-e2e-${SHORT}-$(date +%s)"
    OUT_JSON="${OUT_DIR}/${REQ_TAG}.json"

    echo
    echo "--------------------------------------------------------------"
    echo "Folder $SHORT: $FOLDER_ID"
    echo "Tag: $REQ_TAG"
    echo "--------------------------------------------------------------"

    # Build JSON via jq @json to avoid heredoc shell-injection (no escapes).
    PAYLOAD=$(jq -n \
        --arg folder "$FOLDER_ID" \
        --arg folder_name "Stock E2E Test" \
        --arg subfolder "$REQ_TAG" \
        --arg tag "$REQ_TAG" \
        '{
            queries: [
                { q: "boxing training gym",     limit: 1 },
                { q: "boxing crowd reaction",  limit: 1 }
            ],
            total_minutes:  1,
            chunk_duration: 10,
            clip_duration:  10,
            max_videos:     2,
            folder_id:      $folder,
            folder_name:    $folder_name,
            subfolder:      $subfolder,
            no_audio:       false,
            no_effects:     true,
            no_transitions: true,
            async:          true,
            metadata: {
                test:        "stock-e2e",
                folder_id:   $folder,
                request_tag: $tag
            }
        }')

    # POST /api/stock-pipeline/search-and-run
    HTTP=$(curl -sS -X POST "$BASE/api/stock-pipeline/search-and-run" \
        -H "$AUTH" \
        -H "Content-Type: application/json" \
        --data "$PAYLOAD" \
        -o "$OUT_JSON" \
        -w '%{http_code}' \
        --max-time 30)

    echo "POST HTTP=$HTTP"

    # First-class failure signatures BEFORE parsing the response body.
    case "$HTTP" in
        404)
            echo "FAIL: $BASE/api/stock-pipeline/search-and-run returned HTTP 404 (folder=$SHORT)" >&2
            echo "FAIL canonical: PR-STOCK-ROUTE-REGISTRATION (gateway/middleware route not registered)" >&2
            FAIL_FOLDERS+=("$SHORT:HTTP_404")
            continue
            ;;
        503)
            echo "FAIL: $BASE/api/stock-pipeline/search-and-run returned HTTP 503 (folder=$SHORT)" >&2
            echo "FAIL canonical: PR-STOCK-COMPOSITION-WIRE (jobs.Service not wired in composition root)" >&2
            FAIL_FOLDERS+=("$SHORT:HTTP_503")
            continue
            ;;
    esac

    # Per godlike/07 NO-FAKE-AVAILABILITY: defensive jq guard for empty/invalid body.
    if [ ! -s "$OUT_JSON" ]; then
        echo "FAIL: HTTP $HTTP but empty response body (folder=$SHORT)" >&2
        echo "FAIL canonical: PR-STOCK-SEARCH-AND-RUN-FLOW (response body empty after POST)" >&2
        FAIL_FOLDERS+=("$SHORT:EMPTY_BODY")
        continue
    fi

    # Parse job_id from response body (with --null-input/-e error trap).
    JOB_ID=$(jq -r '.job_id // empty' "$OUT_JSON" 2>/dev/null) || {
        echo "FAIL: HTTP $HTTP but invalid JSON response (folder=$SHORT)" >&2
        echo "FAIL canonical: PR-STOCK-SEARCH-AND-RUN-FLOW (response body is not valid JSON)" >&2
        FAIL_FOLDERS+=("$SHORT:INVALID_JSON")
        continue
    }
    if [ -z "$JOB_ID" ] || [ "$JOB_ID" = "null" ]; then
        echo "FAIL: HTTP $HTTP but no job_id returned (folder=$SHORT)" >&2
        echo "FAIL canonical: PR-STOCK-SEARCH-AND-RUN-FLOW (search-and-run field ignored or mistyped)" >&2
        echo "Response body preserved: $OUT_JSON" >&2
        FAIL_FOLDERS+=("$SHORT:NULL_JOB_ID")
        continue
    fi

    echo "JOB_ID=$JOB_ID"

    # ---- Poll loop ----
    PASS=false
    for i in $(seq 1 "$MAX_POLL_ITERATIONS"); do
        POLL_JSON=$(curl -sS --max-time 10 "$BASE/api/jobs/$JOB_ID/full" -H "$AUTH")
        STATUS=$(echo "$POLL_JSON" | jq -r '.status // empty')
        ERROR=$(echo "$POLL_JSON" | jq -r '.error // empty')

        echo "  poll=$i status=$STATUS error=$ERROR"

        case "$STATUS" in
            SUCCEEDED|completed|INDEX_PENDING)
                echo "$POLL_JSON" > "${OUT_DIR}/${REQ_TAG}-final-${JOB_ID}.json"
                echo "  PASS: folder=$SHORT job=$JOB_ID reached terminal success after ~$((i * POLL_INTERVAL_SECONDS))s"
                PASS_FOLDERS+=("$SHORT")
                PASS=true
                break
                ;;
            FAILED|failed)
            echo "$POLL_JSON" > "${OUT_DIR}/${REQ_TAG}-failed-${JOB_ID}.json"
            echo "  FAIL: folder=$SHORT job=$JOB_ID FAILED after ~$((i * POLL_INTERVAL_SECONDS))s" >&2
            echo "    STATUS=$STATUS" >&2
            echo "    ERROR=$ERROR" >&2
            echo "  Mapping per action plan §4:" >&2
            echo "    stage_sources              -> PR-STOCK-STAGER-BOUND" >&2
            echo "    extract_clips              -> PR-STOCK-CUTTER" >&2
            echo "    compose_chunks             -> PR-STOCK-RENDERER" >&2
            echo "    finalize                   -> PR-STOCK-FINALIZER-PUBLISHER-RACE" >&2
            echo "    no terminal (timeout)      -> PR-STOCK-BROKER-TIMEOUT" >&2
            FAIL_FOLDERS+=("$SHORT:$STATUS")
            PASS=false
            break
            ;;
        esac

        sleep "$POLL_INTERVAL_SECONDS"
    done

    if [ "$PASS" != "true" ]; then
        NOTERM_FOLDERS+=("$SHORT:JOB_STUCK_AFTER_${MAX_POLL_ITERATIONS}_ITER")
        echo "  FAIL: folder=$SHORT job=$JOB_ID did not reach terminal status after $MAX_POLL_ITERATIONS polls" >&2
        echo "    Mapping per action plan §4: no terminal (timeout) -> PR-STOCK-BROKER-TIMEOUT" >&2
    fi
done

# ---- Aggregate verdict -----------------------------------------------------
PASS_COUNT=${#PASS_FOLDERS[@]}
FAIL_COUNT=${#FAIL_FOLDERS[@]}
NOTERM_COUNT=${#NOTERM_FOLDERS[@]}

echo
echo "=================================================================="
echo "STK-E2E-B AGGREGATE VERDICT"
echo "  Total folders:        $FOLDER_COUNT"
echo "  Passed:               $PASS_COUNT"
echo "  Failed (terminal):    $FAIL_COUNT"
echo "  Stuck (no terminal):  $NOTERM_COUNT"
echo "=================================================================="

if [ "$PASS_COUNT" -gt 0 ]; then
    echo
    echo "PASS folders:"
    for f in "${PASS_FOLDERS[@]}"; do echo "  - $f"; done
fi

if [ "$FAIL_COUNT" -gt 0 ] || [ "$NOTERM_COUNT" -gt 0 ]; then
    echo
    if [ "$FAIL_COUNT" -gt 0 ]; then
        echo "FAIL folders (canonical PR-STOCK-* mapping per action plan §4):" >&2
        for f in "${FAIL_FOLDERS[@]}"; do echo "  - $f" >&2; done
    fi
    if [ "$NOTERM_COUNT" -gt 0 ]; then
        echo
        echo "NOTERM folders (timeout / no terminal status; canonical mapping: PR-STOCK-BROKER-TIMEOUT):" >&2
        for f in "${NOTERM_FOLDERS[@]}"; do echo "  - $f" >&2; done
    fi
    echo
    echo "STK-E2E-B probe FAIL: at least one folder did not reach terminal success (exit 1)" >&2
    exit 1
fi

echo
echo "STK-E2E-B probe PASS: all $FOLDER_COUNT folders reached terminal success (exit 0)"
exit 0

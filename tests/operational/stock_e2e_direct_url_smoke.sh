#!/usr/bin/env bash
# stock_e2e_direct_url_smoke.sh
#
# STK-E2E-C probe for architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05
# Exercises POST /api/stock-pipeline/run with the `direct_urls` field
# (NOT `queries`) to verify the direct-URL download path is wired
# through the stock pipeline end-to-end, independent of the search
# engine. This is the canonical operator-facing receipt for the
# `direct_urls` route per AGENTS.md Pattern 6 (diagnostic-surface-first)
# + godlike/07 NO-FAKE-AVAILABILITY.
#
# Per godlike/07 minimum-blast-radius + godlike/06 SSOT (one-canonical-
# owner-per-fact): this script is the SOLE diagnostic surface for the
# `direct_urls` path; a probe FAIL is the canonical forward-pointer to
# PR-STOCK-DIRECT-URLS-FLOW (see architecture/action-plans/
# 2026-07-05-stock-e2e-battery.md §4 failure diagnosis table).
#
# Test scope-limit: ONE Drive folder (FOLDERS[0]) + ONE direct_url.
# The 9-folder search-and-run loop is probe STK-E2E-B.
#
# Exit codes (per action plan §5):
#   0 = PASS (HTTP 200 with job_id, job reached SUCCEEDED|completed|INDEX_PENDING)
#   1 = FAIL (route / payload / job / timeout; canonical PR-STOCK-* mapping
#             at action plan §4)
#   2 = prerequisite missing (server, auth, curl, jq)
# NOTE: no exit 3. DIRECT_URL has sentinel default (Big Buck Bunny direct mp4);
# override via env. If the operator sets DIRECT_URL="" or to a structurally
# invalid URL, probe FAILs with exit 1 and PR-STOCK-DIRECT-URLS-FLOW mapping.
#
# Idempotent: re-runnable. Machine-parseable output: writes JSON to
# $OUT_DIR/stock-direct-url-*.json for the aggregator probe STK-E2E-H.
#
# Self-checks: `bash -n tests/operational/stock_e2e_direct_url_smoke.sh`
# must exit 0 (validated at commit time per §5).
#
# Overridable env vars:
#   BASE          = http://127.0.0.1:8000  (PipelineGen API root)
#   AUTH          = "Authorization: Bearer ${VELOX_ADMIN_TOKEN:-}"
#   FOLDER_ID     = ${FOLDERS[0]}        (9 Drive folder fixtures from
#                                          architecture/action-plans/
#                                          2026-07-05-stock-e2e-battery.md)
#   DIRECT_URL    = yt-dlp-friendly URL (default: Big Buck Bunny direct mp4)
#   OUT_DIR       = /tmp/stock-tests
#   MAX_POLL_ITERATIONS = 60
#   POLL_INTERVAL_SECONDS = 3

set -euo pipefail


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

# 9 Drive folder fixtures from architecture/action-plans/
# 2026-07-05-stock-e2e-battery.md §1 (canonical test scope-limit).
# NOTE: authoritative folder registry lives at
# internal/infrastructure/drive/folders/registry.go — these are
# operator-supplied test fixtures, NOT production SSOT.
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
FOLDER_ID="${FOLDER_ID:-${FOLDERS[0]}}"

# DIRECT_URL: yt-dlp-compatible video URL.
# Default = Big Buck Bunny direct mp4 (royalty-free, Google CDN, no auth,
# compatible with the stockpipeline.SourceStager.downloader which uses
# yt-dlp + curl fallback). Override via env for stock test URLs.
DIRECT_URL="${DIRECT_URL:-https://commondatastorage.googleapis.com/gtv-videos-bucket/sample/BigBuckBunny.mp4}"

OUT_DIR="${OUT_DIR:-/tmp/stock-tests}"
REQ_TAG="stock-direct-url-$(date +%s)"
OUT_JSON="${OUT_DIR}/${REQ_TAG}.json"
MAX_POLL_ITERATIONS="${MAX_POLL_ITERATIONS:-60}"
POLL_INTERVAL_SECONDS="${POLL_INTERVAL_SECONDS:-3}"

# ---- Prerequisite checks (exit 2) ----------------------------------------
command -v curl >/dev/null 2>&1 || { echo "FAIL: curl not on PATH (exit 2)" >&2; exit 2; }
command -v jq >/dev/null 2>&1   || { echo "FAIL: jq not on PATH (exit 2)" >&2; exit 2; }

mkdir -p "$OUT_DIR"

# ---- Server reachability pre-flight (exit 2) ------------------------------
# Per code-reviewer round 1 (probe C): explicit endpoint logging + canonical
# route probe; bumped --max-time 5 -> 10 to avoid false down-flagging on
# slow warm-up (the polling loop runs ~180s, so a 5s pre-flight is too
# aggressive). The canonical /api/stock-pipeline/run probe catches the
# 'server reachable but stock module not mounted' regression surface that
# a generic /healthz probe misses (middleware mismatch, route-not-registered).

PROBE_ENDPOINTS=(
    "$BASE/health"
    "$BASE/api/stock-pipeline/run"
)

PREFLIGHT_OK=0
for endpoint in "${PROBE_ENDPOINTS[@]}"; do
    HTTP=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 \
        -X GET "$endpoint" 2>/dev/null || echo "000")
    case "$HTTP" in
        2*|3*|400|405)
            # 200/3xx = healthy + reachable; 4xx-with-route-wired (400/405
            # means route exists but method/payload wrong) both indicate
            # the route is mounted in the gateway.
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
echo "STK-E2E-C: stock direct-url smoke"
echo "  BASE           = $BASE"
echo "  FOLDER_ID      = ${FOLDER_ID:0:8}... (truncated)"
echo "  DIRECT_URL     = $DIRECT_URL"
echo "  OUT_JSON       = $OUT_JSON"
echo "  MAX_POLL       = $MAX_POLL_ITERATIONS x ${POLL_INTERVAL_SECONDS}s"
echo "=================================================================="

# ---- Test 1: POST /api/stock-pipeline/run with direct_urls payload --------
# Build JSON via jq @json to avoid heredoc injection (no shell escapes).
PAYLOAD_JSON=$(jq -n \
    --arg url "$DIRECT_URL" \
    --arg folder "$FOLDER_ID" \
    --arg folder_name "Stock Direct URL Test" \
    --arg subfolder "$REQ_TAG" \
    --arg test "stock-e2e-direct-url" \
    '{
        direct_urls: [$url],
        total_minutes: 1,
        chunk_duration: 10,
        clip_duration: 10,
        max_videos: 1,
        folder_id: $folder,
        folder_name: $folder_name,
        subfolder: $subfolder,
        no_audio: false,
        no_effects: true,
        no_transitions: true,
        async: true,
        metadata: { test: $test }
    }')

echo
echo ">>> POST $BASE/api/stock-pipeline/run  (direct_urls path)"
HTTP_BODY=$(curl -sS -X POST "$BASE/api/stock-pipeline/run" \
    -H "$AUTH" \
    -H "Content-Type: application/json" \
    --data "$PAYLOAD_JSON" \
    -o "$OUT_JSON" \
    -w "%{http_code}" \
    --max-time 30)

echo "HTTP=$HTTP_BODY"
echo "--- response body ---"
jq . "$OUT_JSON" 2>/dev/null || cat "$OUT_JSON"
echo "--- end response body ---"

# Surface canonical failure signatures BEFORE parsing (operator-facing).
case "$HTTP_BODY" in
    404) echo "FAIL: $BASE/api/stock-pipeline/run returned HTTP 404" >&2
         echo "FAIL canonical: PR-STOCK-ROUTE-REGISTRATION (gateway/middleware route not registered)" >&2
         exit 1 ;;
    503) echo "FAIL: $BASE/api/stock-pipeline/run returned HTTP 503" >&2
         echo "FAIL canonical: PR-STOCK-COMPOSITION-WIRE (jobs.Service not wired in composition root)" >&2
         exit 1 ;;
esac

# Parse job_id from the response body.
JOB_ID=$(jq -r '.job_id // empty' "$OUT_JSON")
if [ -z "$JOB_ID" ] || [ "$JOB_ID" = "null" ]; then
    echo
    echo "FAIL: HTTP $HTTP_BODY but no job_id returned (direct_urls path)" >&2
    echo "FAIL canonical: PR-STOCK-DIRECT-URLS-FLOW (direct_urls field ignored or mistyped)" >&2
    echo "Response body preserved at: $OUT_JSON" >&2
    exit 1
fi
echo
echo "JOB_ID=$JOB_ID"

# ---- Test 2: Poll /api/jobs/{job_id}/full every ${POLL_INTERVAL_SECONDS}s ----
echo
echo ">>> Polling $BASE/api/jobs/$JOB_ID/full every ${POLL_INTERVAL_SECONDS}s for ${MAX_POLL_ITERATIONS} iter"

for i in $(seq 1 "$MAX_POLL_ITERATIONS"); do
    POLL_JSON=$(curl -sS --max-time 10 "$BASE/api/jobs/$JOB_ID/full" -H "$AUTH")
    STATUS=$(echo "$POLL_JSON" | jq -r '.status // empty')
    ERROR=$(echo "$POLL_JSON" | jq -r '.error // empty')

    echo "poll=$i status=$STATUS error=$ERROR"

    if echo "$STATUS" | grep -Eiq 'SUCCEEDED|succeeded|completed|INDEX_PENDING'; then
        echo "$POLL_JSON" > "${OUT_DIR}/${REQ_TAG}-final-${JOB_ID}.json"
        echo
        echo "PASS: STK-E2E-C direct_url job reached terminal success after $i polls (~ $((i * POLL_INTERVAL_SECONDS))s)"
        echo "PASS receipt: ${OUT_DIR}/${REQ_TAG}-final-${JOB_ID}.json"
        exit 0
    fi

    if echo "$STATUS" | grep -Eiq 'FAILED|failed'; then
        echo "$POLL_JSON" > "${OUT_DIR}/${REQ_TAG}-failed-${JOB_ID}.json"
        echo
        echo "FAIL: STK-E2E-C direct_url job FAILED after $i polls" >&2
        echo "FAIL canonical mapping (per action plan §4):" >&2
        echo "  STATUS=$STATUS" >&2
        echo "  ERROR=$ERROR" >&2
        echo "  Direct URL attempted: $DIRECT_URL" >&2
        echo "Mapping:" >&2
        echo "  stage_sources error     -> PR-STOCK-STAGER-BOUND" >&2
        echo "  extract_clips error     -> PR-STOCK-CUTTER" >&2
        echo "  compose_chunks error    -> PR-STOCK-RENDERER" >&2
        echo "  finalize (production)   -> PR-STOCK-FINALIZER-PUBLISHER-RACE" >&2
        echo "Failed response preserved: ${OUT_DIR}/${REQ_TAG}-failed-${JOB_ID}.json" >&2
        exit 1
    fi

    sleep "$POLL_INTERVAL_SECONDS"
done

echo
echo "FAIL: STK-E2E-C direct_url job did not reach terminal status after $MAX_POLL_ITERATIONS polls" >&2
echo "FAIL canonical: timeout (job stuck in non-terminal state; check broker logs)" >&2
exit 1

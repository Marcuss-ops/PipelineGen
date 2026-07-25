#!/usr/bin/env bash
# stock_e2e_download_smoke.sh
#
# STK-E2E-G probe for architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05
# Stock clip download smoke:
#   1) Extract STOCK_ID from media_assets (source='stock',
#      ORDER BY created_at DESC LIMIT 1) -- canonical SQL per godlike/06.
#   2) POST /api/media/stock/clips/$STOCK_ID/download, writing response bytes
#      to /tmp/stock-tests/<ID>.mp4.
#   3) Verify HTTP=200 + downloaded SIZE > 100000 bytes.
#   4) Invoke ffprobe on the file to assert duration > 0 + at least one
#      stream with codec_type=video.
#
# Per the canonical decision (thinker-with-files-gemini Option A):
# the user-specified route /api/media/stock/clips/$STOCK_ID/download is
# honored literally. The canonical codebase routes today are
# /api/stock/run + /api/stock/search-and-run (no per-clip download
# endpoint yet). This probe documents the gap with a canonical
# PR-STOCK-DOWNLOAD-ROUTE-REGISTRATION forward-pointer so the next
# agent that closes the gap has a ready diagnostic surface.
# (godlike/07 NO-FAKE-AVAILABILITY + AGENTS.md Pattern 6
# diagnostic-first: a probe for a missing route serves as the
# operator-facing receipt of the gap; the canonical fix that ships
# the route automatically flips this probe from FAIL->PASS.)
#
# Per godlike/06 SSOT one-canonical-owner-per-fact (canonical mappings
# per action plan section 4, rg-verified from codebase SSOT files):
#   empty pre-condition (no source='stock' rows)
#     -> PR-STOCK-DOWNLOAD-PRECONDITION-EMPTY
#        canonical owner:
#          internal/application/assets/providers/stock/stockpipeline/
#          upload_orchestration.go::Orchestrator.RunResilient (the
#          canonical stock pipeline driver that creates source='stock'
#          rows in media_assets; if probe can't find ANY stock row,
#          this orchestrator never ran to completion on a prior cycle)
#   HTTP 404 (route missing)
#     -> PR-STOCK-DOWNLOAD-ROUTE-REGISTRATION
#        canonical owner: internal/api/assets/stock/handler.go
#        (the canonical stock route surface at lines 39/40 for /run +
#        /search-and-run; the /download route should be added here)
#   HTTP 503 (composition unwired)
#     -> PR-STOCK-COMPOSITION-WIRE
#        canonical owner: internal/app/build_bundles_stock.go::WireAssets
#        (the composition root that injects the stock handler into the
#        gateway; per STK-E2E-A precedent)
#   HTTP non-2xx (other)
#     -> PR-STOCK-DOWNLOAD-RESOLVER
#        canonical owner:
#          internal/application/assets/providers/stock/stockpipeline/
#          upload_orchestration.go::Orchestrator.RunResilient step 6
#          stock.finalize (the canonical stock -> Drive upload seam)
#   curl -o write failure (disk full / EPERM / ENOSPC)
#     -> PR-STOCK-DOWNLOAD-FILE-WRITE-FAIL
#        canonical owner: pkg/fileutil (per AGENTS.md utility list;
#        this is an ops-actionable failure mode -- not a code-defect)
#   HTTP 200 + SIZE <= 100000
#     -> PR-STOCK-DOWNLOAD-ZERO-SIZE
#        canonical owner:
#          internal/application/assets/providers/stock/stockpipeline/
#          step_compose_chunks.go::StockComposeChunksStep.Run (the
#          canonical stitch+write seam that emits the .mp4 to Drive)
#   ffprobe fails (file missing or corrupt)
#     -> PR-STOCK-DOWNLOAD-CORRUPT-MP4
#        canonical owner:
#          internal/application/assets/providers/stock/stockpipeline/
#          upload_orchestration.go::Orchestrator.RunResilient step 6
#          (the canonical Drive upload step that should produce MP4)
#   ffprobe: duration == 0 (empty MP4 header)
#     -> PR-STOCK-DOWNLOAD-INVALID-DURATION
#        canonical owner:
#          internal/infrastructure/media/render/cutter.go (the
#          canonical ffmpeg cutter that should produce >=1s content)
#   ffprobe: no video stream
#     -> PR-STOCK-DOWNLOAD-NO-VIDEO-STREAM
#        canonical owner:
#          internal/infrastructure/media/render/cutter.go (the
#          canonical ffmpeg cutter; missing ffmpeg.mp4 muxer invocation
#          would emit a header-only MP4 with no video stream)
#
# Exit codes per action-plan section 5:
#   0 = PASS (HTTP 200 + SIZE > 100000 + duration > 0 + video stream)
#   1 = FAIL (any of the 5 violation paths)
#   2 = prereq missing (sqlite3/jq/curl/ffprobe absent OR DB not found OR
#       table missing)
#
# Self-checks: `bash -n tests/operational/stock_e2e_download_smoke.sh`
# must exit 0 (validated at commit time per section 5).
#
# Overridable env vars:
#   DB_PATH = data/media/media.db.sqlite
#   BASE    = http://127.0.0.1:8000
#   AUTH    = "Authorization: Bearer ${VELOX_ADMIN_TOKEN:-}"
#   OUT_DIR = /tmp/stock-tests

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
DB_PATH="${DB_PATH:-data/media/media.db.sqlite}"
BASE="${BASE:-http://127.0.0.1:8000}"
AUTH="${AUTH:-Authorization: Bearer ${VELOX_ADMIN_TOKEN:-}}"
OUT_DIR="${OUT_DIR:-/tmp/stock-tests}"
MIN_BYTES="${MIN_BYTES:-100000}"

mkdir -p "$OUT_DIR" 2>/dev/null || true

# ---- Prerequisite checks (exit 2) ----------------------------------------
for cmd in sqlite3 jq curl ffprobe; do
    command -v "$cmd" >/dev/null 2>&1 || \
        { echo "FAIL: $cmd not on PATH (exit 2)" >&2; exit 2; }
done

if [ ! -f "$DB_PATH" ]; then
    echo "FAIL: $DB_PATH not found (exit 2)" >&2
    echo "  Suggested: start PipelineGen to create the DB, or point DB_PATH at the canonical path" >&2
    exit 2
fi

# ---- Schema + pre-condition probe (exit 2) -------------------------------
# Per code-reviewer NEEDS-FIX #1 pattern (inherited from STK-E2E-D round-2):
# stderr capture distinguishes "sqlite3 binary I/O error" from "table
# missing" -> right canonical diagnostic.
TMP_SCHEMA_ERR=$(mktemp /tmp/stock-g-schema-err.XXXXXX)
TABLE_CHECK=$(sqlite3 "$DB_PATH" \
    "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='media_assets';" \
    2>"$TMP_SCHEMA_ERR")
SQLITE_EXIT=$?
SQLITE_ERR=$(cat "$TMP_SCHEMA_ERR" 2>/dev/null || echo "")
rm -f "$TMP_SCHEMA_ERR"

if [ "$SQLITE_EXIT" -ne 0 ]; then
    echo "FAIL: sqlite3 cannot read $DB_PATH (exit $SQLITE_EXIT)" >&2
    [ -n "$SQLITE_ERR" ] && echo "  stderr: $SQLITE_ERR" >&2
    exit 2
fi

if [ "$TABLE_CHECK" != "1" ]; then
    echo "FAIL: media_assets table not found in $DB_PATH (count=$TABLE_CHECK)" >&2
    exit 2
fi

# ---- Extract STOCK_ID from media_assets (canonical canonical canonical) -
# Per godlike/06 SSOT one-canonical-owner-per-fact: media_assets.source
# column is canonical (migration 033 + 059). The canonical 'stock' literal
# is canonical per search_queries.go line 322 + stock_query.go line 118.
# Per godlike/07 NO-FAKE-AVAILABILITY: an empty result is documented
# as PR-STOCK-DOWNLOAD-PRECONDITION-EMPTY + its root-cause menu, NOT
# a silent-success PASS.
QUERY=$(cat <<'SQL_EOF'
SELECT id
FROM media_assets
WHERE source = 'stock'
ORDER BY created_at DESC, id DESC
LIMIT 1;
SQL_EOF
)

STOCK_ID=$(sqlite3 "$DB_PATH" "$QUERY" 2>/dev/null || echo "")

if [ -z "$STOCK_ID" ] || [ "$STOCK_ID" = "" ]; then
    # Per godlike/07 honest scope-lock: NOT a silent-success exit 0.
    # Surfaces to operator as a real upstream-bridge gap (the B/C
    # probes must have completed + persisted rows before G can run).
    echo "FAIL: zero rows in media_assets WHERE source='stock'" >&2
    echo "  Query: $QUERY" >&2
    echo "FAIL canonical: PR-STOCK-DOWNLOAD-PRECONDITION-EMPTY (upstream probes STK-E2E-B/C have not populated source='stock' rows yet)" >&2
    echo "  Possible signals (godlike/07 honest scope-lock):" >&2
    echo "    1. STK-E2E-B/C runs have not yet completed (no enqueue/persist yet)" >&2
    echo "    2. finalizer tx rolled back before media_assets+outbox (PR-STOCK-FINALIZER-PUBLISHER-RACE)" >&2
    echo "    3. canonical DB is unreachable / connection refuted (canonical server down)" >&2
    exit 1
fi

echo "=================================================================="
echo "STK-E2E-G: stock clip download smoke"
echo "  DB_PATH = $DB_PATH"
echo "  BASE    = $BASE"
echo "  STOCK_ID= $STOCK_ID"
echo "  OUT_DIR = $OUT_DIR"
echo "=================================================================="

# ---- POST /api/media/stock/clips/$STOCK_ID/download ----------------------
# Per Option A (thinker verdict): call the user-spec endpoint literally;
# 404 maps to PR-STOCK-DOWNLOAD-ROUTE-REGISTRATION (the canonical-fix
# forward-pointer). The handler exists at internal/api/assets/stock/handler.go
# (line 39/40 is the canonical route surface; the /download sub-path is
# the canonical fix per Round-1 verdict).
OUT_FILE="$OUT_DIR/${STOCK_ID}.mp4"

# Per code-reviewer NEEDS-FIX #2: capture curl exit code so write failures
# (disk full / EPERM / ENOSPC / curl timeout) surface a canonical
# PR-STOCK-DOWNLOAD-FILE-WRITE-FAIL diagnostic instead of silently
# terminating at the curl line under set -euo pipefail.
CURL_EXIT=0
HTTP=$(curl -sS -X POST "$BASE/api/media/stock/clips/$STOCK_ID/download" \
    -H "$AUTH" \
    -o "$OUT_FILE" \
    -w '%{http_code}' \
    --max-time 60) || CURL_EXIT=$?

if [ "$CURL_EXIT" -ne 0 ]; then
    echo "FAIL: curl could not write $OUT_FILE (exit $CURL_EXIT)" >&2
    echo "  Likely root causes: disk full, EPERM, ENOSPC on $OUT_DIR, or curl timeout (60s)" >&2
    echo "FAIL canonical: PR-STOCK-DOWNLOAD-FILE-WRITE-FAIL" >&2
    echo "  Canonical owner: pkg/fileutil (per AGENTS.md utility list; this is an ops-actionable failure mode, not a code-defect)" >&2
    exit 1
fi

SIZE=$(stat -c%s "$OUT_FILE" 2>/dev/null || echo 0)
# Cleanup hook (per code-reviewer minor #2): rm the downloaded MP4 on PASS
# to keep /tmp/stock-tests idempotent. On FAIL/ERR, the operator gets the
# file preserved for manual inspection; the probe exit code is
# non-zero so the next CI run tracks the artifact for debugging.
cleanup_mp4_on_pass() {
    if [ "$1" = "0" ]; then
        rm -f "$OUT_FILE" 2>/dev/null || true
    fi
}
trap 'cleanup_mp4_on_pass $?' EXIT

echo
echo "POST $BASE/api/media/stock/clips/$STOCK_ID/download -> HTTP $HTTP"
echo "Wrote: $OUT_FILE ($SIZE bytes)"

# ---- HTTP-code canonical mapping (per action plan section 4) -------------
case "$HTTP" in
    200)
        echo "PASS HTTP: 200"
        ;;
    404)
        echo "FAIL canonical: PR-STOCK-DOWNLOAD-ROUTE-REGISTRATION" >&2
        echo "  $BASE/api/media/stock/clips/$STOCK_ID/download returned HTTP 404" >&2
        echo "  Canonical owner: internal/api/assets/stock/handler.go (lines 39-40 is the canonical route surface for /run + /search-and-run; the /download sub-path MUST be added here)" >&2
        echo "  Likely root causes:" >&2
        echo "    1. /download route not registered in the stock handler RegisterRoutes" >&2
        echo "    2. routing typo (handler at /api/stock-pipeline/run vs /api/media/stock/clips/.../download)" >&2
        echo "    3. feature flag disabled (VELOX_FEATURE_STOCK=false) or admin route misconfigured" >&2
        exit 1
        ;;
    503)
        echo "FAIL canonical: PR-STOCK-COMPOSITION-WIRE" >&2
        echo "  $BASE/api/media/stock/clips/$STOCK_ID/download returned HTTP 503" >&2
        echo "  Canonical owner: internal/app/build_bundles_stock.go::WireAssets (composition root injects stock handler into gateway)" >&2
        exit 1
        ;;
    *)
        echo "FAIL canonical: PR-STOCK-DOWNLOAD-RESOLVER (HTTP $HTTP)" >&2
        echo "  Canonical owner: internal/application/assets/providers/stock/stockpipeline/upload_orchestration.go::Orchestrator.RunResilient step 6 stock.finalize (canonical stock -> Drive upload seam)" >&2
        exit 1
        ;;
esac

# ---- File-size assertion (per user spec: SIZE > 100000) -----------------
if [ "$SIZE" -le "$MIN_BYTES" ]; then
    echo "FAIL canonical: PR-STOCK-DOWNLOAD-ZERO-SIZE" >&2
    echo "  Downloaded file is $SIZE bytes; minimum required is $MIN_BYTES" >&2
    echo "  Canonical owner: internal/application/assets/providers/stock/stockpipeline/upload_orchestration.go::Orchestrator.RunResilient step 6 stock.finalize" >&2
    exit 1
fi

echo "PASS SIZE: $SIZE > $MIN_BYTES bytes"

# ---- ffprobe validation (per user spec: duration > 0 + video stream) ----
# Defense-in-depth: ffprobe may exit 1 on corrupt MP4 OR return a JSON
# that lacks the video stream. Capture stdout + stderr; parse JSON
# strictly; assert canonical checks.
TMP_FFPROBE=$(mktemp /tmp/stock-g-ffprobe-err.XXXXXX)
FFPROBE_JSON=$(ffprobe -v error -show_streams -show_format -of json "$OUT_FILE" \
    2>"$TMP_FFPROBE") || {
    SIG=$(cat "$TMP_FFPROBE" 2>/dev/null || echo "")
    rm -f "$TMP_FFPROBE"
    echo "FAIL: ffprobe failed on $OUT_FILE" >&2
    [ -n "$SIG" ] && echo "  stderr: $SIG" >&2
    echo "FAIL canonical: PR-STOCK-DOWNLOAD-CORRUPT-MP4" >&2
    echo "  Canonical owner: internal/application/assets/providers/stock/stockpipeline/upload_orchestration.go::Orchestrator.RunResilient step 6 stock.finalize" >&2
    exit 1
}
rm -f "$TMP_FFPROBE"

# Duration > 0: parse format.duration (decimal seconds string)
DURATION=$(printf '%s' "$FFPROBE_JSON" | jq -r '.format.duration // "0"' 2>/dev/null || echo "0")
# Convert duration (sec.ms) to integer seconds for comparison
DURATION_INT=$(printf '%s' "$DURATION" | awk -F'.' '{print int($1+0)}')
if [ "$DURATION_INT" -le 0 ]; then
    echo "FAIL: ffprobe reports duration=$DURATION (expected > 0)" >&2
    echo "FAIL canonical: PR-STOCK-DOWNLOAD-INVALID-DURATION" >&2
    echo "  Canonical owner: internal/infrastructure/media/processor/ffmpeg.go (cutter)" >&2
    exit 1
fi
echo "PASS DURATION: $DURATION seconds"

# Stream video present: jq -r '.streams[] | select(.codec_type=="video")' count
VIDEO_STREAM_COUNT=$(printf '%s' "$FFPROBE_JSON" | \
    jq '[.streams[] | select(.codec_type=="video")] | length' 2>/dev/null || echo "0")

if [ "$VIDEO_STREAM_COUNT" -lt 1 ]; then
    echo "FAIL: no video stream in ffprobe output (count=$VIDEO_STREAM_COUNT)" >&2
    echo "FAIL canonical: PR-STOCK-DOWNLOAD-NO-VIDEO-STREAM" >&2
    echo "  Canonical owner: internal/application/assets/providers/stock/stockpipeline/upload_orchestration.go::Orchestrator (missing ffmpeg.mp4 muxer invocation)" >&2
    exit 1
fi

echo "PASS VIDEO: $VIDEO_STREAM_COUNT video stream(s) present"
echo
echo "PASS: STOCK_ID=$STOCK_ID downloaded MP4 (HTTP 200, SIZE=$SIZE, DURATION=$DURATION, VIDEO_STREAMS=$VIDEO_STREAM_COUNT)"
echo "Receipt: $OUT_FILE"
exit 0

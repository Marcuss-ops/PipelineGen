#!/usr/bin/env bash
# youtube_dod_live_verify.sh
#
# PR-PIPELINEGEN-LIVE-VERIFY-RUNBOOK (P2, deadline 2026-08-15) — live
# verification runbook for the YouTube DoD pipeline (§11.1 of
# `architecture/action-plans/2026-07-08-youtube-clip-dod-action-plan.md`).
# Replicates the canonical 4 hard-pass verifications against a production
# PipelineGen server (port 8000, VELOX_ADMIN_TOKEN from .env).
#
# The 4 verifications (the "happy-path" end-to-end chain — if all 4 pass
# the pipeline actually succeeded; if any fails the job status is a lie):
#   V1: DoD 1  — Single mp4 present (media_assets row with canonical YouTube
#                 filename yt_<videoID>_<start>_<end>_v1*.mp4)
#   V2: DoD 6  — SQLite media_assets row for the test video (canonical SSOT)
#   V3: DoD 8  — outbox event asset.index.requested status=completed
#   V4: DoD 9  — Qdrant point present in media_assets_current collection
#
# Per godlike/07 NO-FAKE-AVAILABILITY: each row maps to ONE canonical
# PR-YT-DOD-* forward-pointer for diagnostic value (no stringly-typed
# branch). V2 + V3 use SQLite direct query (the canonical SSOT) with API
# fallback if the DB file is not accessible.
#
# Exit codes (per action-plan §5 convention):
#   0 = PASS (4/4 verifications green)
#   1 = FAIL (any verification failed; canonical PR-YT-DOD-* per row)
#   2 = prereq missing (server unreachable, curl/jq/sqlite3 absent, token missing)
#
# Self-checks: `bash -n tests/operational/youtube_dod_live_verify.sh`
# must exit 0 (validated at commit time per §5).
#
# Overridable env vars:
#   BASE             = http://127.0.0.1:8000   (PipelineGen API root)
#   ENV_FILE         = .env                    (dotenv file with VELOX_ADMIN_TOKEN)
#   DB_PATH          = data/media/media.db.sqlite  (canonical SQLite)
#   QDRANT_URL       = http://127.0.0.1:6333   (Qdrant REST root)
#   QDRANT_COLLECTION= media_assets_current    (canonical collection name)
#   TEST_VIDEO_ID    = vdC5GXxS-qU             (canonical Pacquiao/Broner clip)

set -euo pipefail

# ---- Configuration --------------------------------------------------------
BASE="${BASE:-http://127.0.0.1:8000}"
ENV_FILE="${ENV_FILE:-.env}"
DB_PATH="${DB_PATH:?DB_PATH must be explicitly set to an isolated or approved database}"
QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"
QDRANT_COLLECTION="${QDRANT_COLLECTION:-media_assets_current}"
TEST_VIDEO_ID="${TEST_VIDEO_ID:-vdC5GXxS-qU}"

# ---- Load VELOX_ADMIN_TOKEN from .env (canonical godlike/06 SSOT pattern) -
# Per .env-format: VELOX_ADMIN_TOKEN=<value> (no surrounding quotes typically;
# strip them defensively in case the value is wrapped).
if [ -z "${VELOX_ADMIN_TOKEN:-}" ] && [ -f "$ENV_FILE" ]; then
    VELOX_ADMIN_TOKEN=$(grep -E '^VELOX_ADMIN_TOKEN=' "$ENV_FILE" | head -1 | cut -d= -f2- | tr -d '"' | tr -d "'" | xargs)
fi
VELOX_ADMIN_TOKEN="${VELOX_ADMIN_TOKEN:-}"
if [ -z "$VELOX_ADMIN_TOKEN" ]; then
    echo "FAIL: VELOX_ADMIN_TOKEN not set and $ENV_FILE missing or empty (exit 2)" >&2
    exit 2
fi

# ---- Prerequisite checks (exit 2) ----------------------------------------
command -v curl >/dev/null 2>&1 || { echo "FAIL: curl not on PATH (exit 2)" >&2; exit 2; }
command -v jq >/dev/null 2>&1   || { echo "FAIL: jq not on PATH (exit 2)"   >&2; exit 2; }

# ---- Server reachability pre-flight (exit 2) ------------------------------
# Per STK-E2E-A round-1 lesson: explicit endpoint logging + canonical route
# probe. Bumped --max-time to 10 to avoid false down-flagging on slow warm-up.
PROBE_ENDPOINTS=(
    "$BASE/health"
    "$BASE/api/assets/clips?source=youtube&video_id=$TEST_VIDEO_ID&limit=1"
)
PREFLIGHT_OK=0
for endpoint in "${PROBE_ENDPOINTS[@]}"; do
    HTTP=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 \
        -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" \
        -X GET "$endpoint" 2>/dev/null || echo "000")
    case "$HTTP" in
        2*|3*|400)
            echo "PRE-FLIGHT: $endpoint -> HTTP $HTTP (reachable + auth ok)"
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
    echo "FAIL: PipelineGen at $BASE unreachable on all pre-flight endpoints (exit 2)" >&2
    exit 2
fi

# ---- Header / tally counters ---------------------------------------------
PASS=0
TOTAL=4
REQ_TAG="yt-dod-live-verify-$(date +%s)"
TMPDIR_RUNBOOK="/tmp/youtube-dod-tests"
mkdir -p "$TMPDIR_RUNBOOK"

echo "=================================================================="
echo "YT-DOD-LIVE-VERIFY: §11.1 4/4 verification suite (PR-PIPELINEGEN-LIVE-VERIFY-RUNBOOK)"
echo "  BASE             = $BASE"
echo "  TEST_VIDEO_ID    = $TEST_VIDEO_ID"
echo "  DB_PATH          = $DB_PATH"
echo "  QDRANT_URL       = $QDRANT_URL"
echo "  QDRANT_COLLECTION= $QDRANT_COLLECTION"
echo "  REQ_TAG          = $REQ_TAG"
echo "=================================================================="

# ---- V1: DoD 1 — Single mp4 present (API probe for media_assets row) -----
echo
echo "[V1/4] DoD 1: Single mp4 present for video $TEST_VIDEO_ID"
OUT_V1="$TMPDIR_RUNBOOK/${REQ_TAG}-v1-clip.json"
HTTP=$(curl -sS -o "$OUT_V1" -w '%{http_code}' --max-time 10 \
    -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" \
    "$BASE/api/assets/clips?source=youtube&video_id=$TEST_VIDEO_ID&limit=1" 2>/dev/null || echo "000")
if [ "$HTTP" = "200" ] && jq -e '.clips | length > 0' "$OUT_V1" >/dev/null 2>&1; then
    CLIP_NAME=$(jq -r '.clips[0].filename' "$OUT_V1")
    DRIVE_LINK=$(jq -r '.clips[0].drive_link // "(empty)"' "$OUT_V1")
    echo "  -> PASS: media_assets row present (filename=$CLIP_NAME, drive_link=$DRIVE_LINK)"
    PASS=$((PASS+1))
elif [ "$HTTP" = "200" ]; then
    echo "  -> FAIL: HTTP 200 but no clips in response (canonical: PR-YT-DOD-1)" >&2
    echo "  Receipt: $OUT_V1" >&2
else
    echo "  -> FAIL: HTTP $HTTP (canonical: PR-YT-DOD-1)" >&2
    echo "  Receipt: $OUT_V1" >&2
fi

# ---- V2: DoD 6 — SQLite media_assets row (canonical SSOT) -----------------
echo
echo "[V2/4] DoD 6: SQLite media_assets row for video $TEST_VIDEO_ID"
if [ -f "$DB_PATH" ] && command -v sqlite3 >/dev/null 2>&1; then
    COUNT=$(sqlite3 "$DB_PATH" \
        "SELECT COUNT(*) FROM media_assets WHERE source='youtube' AND filename LIKE 'yt_${TEST_VIDEO_ID}_%';" \
        2>/dev/null || echo "0")
    if [ "$COUNT" -gt 0 ]; then
        echo "  -> PASS: $COUNT row(s) in media_assets (SQLite SSOT)"
        PASS=$((PASS+1))
    else
        echo "  -> FAIL: 0 rows for video $TEST_VIDEO_ID (canonical: PR-YT-DOD-7)" >&2
    fi
else
    echo "  -> SKIP: DB at $DB_PATH not accessible or sqlite3 absent (fall back to API)"
    if [ -f "$OUT_V1" ] && jq -e '.clips | length > 0' "$OUT_V1" >/dev/null 2>&1; then
        API_COUNT=$(jq '.clips | length' "$OUT_V1")
        echo "  -> PASS (via API fallback): $API_COUNT clip(s) reported"
        PASS=$((PASS+1))
    else
        echo "  -> FAIL: no API fallback (canonical: PR-YT-DOD-7)" >&2
    fi
fi

# ---- V3: DoD 8 — Outbox event completed ----------------------------------
echo
echo "[V3/4] DoD 8: outbox event asset.index.requested completed"
if [ -f "$DB_PATH" ] && command -v sqlite3 >/dev/null 2>&1; then
    OUTBOX_COUNT=$(sqlite3 "$DB_PATH" \
        "SELECT COUNT(*) FROM outbox_events WHERE event_type='asset.index.requested' AND status='completed' AND idempotency_key LIKE 'yt_${TEST_VIDEO_ID}_%';" \
        2>/dev/null || echo "0")
    if [ "$OUTBOX_COUNT" -gt 0 ]; then
        echo "  -> PASS: $OUTBOX_COUNT outbox event(s) completed"
        PASS=$((PASS+1))
    else
        echo "  -> FAIL: 0 completed outbox events (canonical: PR-YT-DOD-8)" >&2
    fi
else
    echo "  -> SKIP: DB at $DB_PATH not accessible or sqlite3 absent"
    echo "  -> FAIL: cannot verify outbox SSOT (canonical: PR-YT-DOD-8)" >&2
fi

# ---- V4: DoD 9 — Qdrant point present ------------------------------------
echo
echo "[V4/4] DoD 9: Qdrant point in $QDRANT_COLLECTION for video $TEST_VIDEO_ID"
OUT_V4="$TMPDIR_RUNBOOK/${REQ_TAG}-v4-qdrant.json"
QDRANT_HTTP=$(curl -sS -o "$OUT_V4" -w '%{http_code}' --max-time 10 \
    -X POST "$QDRANT_URL/collections/$QDRANT_COLLECTION/points/scroll" \
    -H "Content-Type: application/json" \
    --data "{\"filter\":{\"must\":[{\"key\":\"video_id\",\"match\":{\"value\":\"$TEST_VIDEO_ID\"}}]},\"limit\":1}" \
    2>/dev/null || echo "000")
if [ "$QDRANT_HTTP" = "200" ] && jq -e '.result.points | length > 0' "$OUT_V4" >/dev/null 2>&1; then
    POINT_ID=$(jq -r '.result.points[0].id' "$OUT_V4")
    LIFECYCLE=$(jq -r '.result.points[0].payload.lifecycle_state // "(empty)"' "$OUT_V4")
    echo "  -> PASS: Qdrant point found (id=$POINT_ID, lifecycle_state=$LIFECYCLE)"
    PASS=$((PASS+1))
else
    echo "  -> FAIL: HTTP $QDRANT_HTTP or no Qdrant points (canonical: PR-YT-DOD-9)" >&2
    echo "  Receipt: $OUT_V4" >&2
fi

# ---- Final verdict --------------------------------------------------------
echo
echo "=================================================================="
echo "VERDICT: $PASS/$TOTAL verifications passed"
echo "=================================================================="
if [ "$PASS" -eq "$TOTAL" ]; then
    echo "PASS: §11.1 verification suite 4/4 GREEN"
    echo "  (job output above + receipts in $TMPDIR_RUNBOOK/)"
    exit 0
else
    echo "FAIL: $((TOTAL-PASS)) verification(s) failed (see canonical PR-YT-DOD-* per row)" >&2
    echo "  (receipts in $TMPDIR_RUNBOOK/)" >&2
    exit 1
fi

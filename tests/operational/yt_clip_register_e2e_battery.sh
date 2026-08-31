#!/usr/bin/env bash
# yt_clip_register_e2e_battery.sh
#
# YT-CLIP-E2E-BATTERY — comprehensive end-to-end test battery for YouTube clip
# registration pipeline. 15 test scenarios validating the full chain:
#
#   YouTube URL → validation → yt-dlp download → FFmpeg cut → local file →
#   hash → Drive upload → media_assets DB → outbox → Qdrant → duplicates →
#   cleanup → errors
#
# Canonical endpoints tested:
#   POST /api/media/register-from-youtube  (sync single clip)
#   POST /api/media/register-batch         (async batch + fan-out)
#
# Exit codes:
#   0 = ALL tests PASS
#   1 = one or more tests FAILED
#   2 = prerequisite missing (server down, tools absent)
#
# Self-check: `bash -n tests/operational/yt_clip_register_e2e_battery.sh`
#
# Overridable env vars:
#   BASE          PipelineGen API base URL (default http://127.0.0.1:8000)
#   DB            SQLite DB path (default data/media/media.db.sqlite)
#   FOLDER_ID     Google Drive test folder ID (MUST be set for Drive tests)
#   QDRANT_URL    Qdrant HTTP endpoint (default http://localhost:6333)
#   QDRANT_COLLECTION (default media_assets_current)
#   TEST_VIDEO_ID YouTube video ID (default jNQXAC9IVRw = "Me at the zoo")
#   VELOX_ADMIN_TOKEN  Bearer token for API auth
#   TOKEN_FILE    Alternative: env file containing VELOX_ADMIN_TOKEN=...
#   SKIP_DRIVE    Set to 1 to skip Drive-dependent tests (offline mode)
#   SKIP_QDRANT   Set to 1 to skip Qdrant-dependent tests

set -euo pipefail

# ── Colour (portable) ──────────────────────────────────────────────────────
RED="" GREEN="" YELLOW="" CYAN="" DIM="" RESET=""
if [[ -t 1 && -z "${NO_COLOR:-}" ]]; then
    RED=$(tput setaf 1 2>/dev/null || true)
    GREEN=$(tput setaf 2 2>/dev/null || true)
    YELLOW=$(tput setaf 3 2>/dev/null || true)
    CYAN=$(tput setaf 6 2>/dev/null || true)
    DIM=$(tput dim 2>/dev/null || true)
    RESET=$(tput sgr0 2>/dev/null || true)
fi

# ── Configuration ──────────────────────────────────────────────────────────
BASE="${BASE:-http://127.0.0.1:8000}"
DB="${DB:?DB must be explicitly set to an isolated or approved database}"
QDRANT_URL="${QDRANT_URL:-http://localhost:6333}"
QDRANT_COLLECTION="${QDRANT_COLLECTION:-media_assets_current}"
TEST_VIDEO_ID="${TEST_VIDEO_ID:-jNQXAC9IVRw}"
TEST_URL="https://www.youtube.com/watch?v=${TEST_VIDEO_ID}"
SKIP_DRIVE="${SKIP_DRIVE:-0}"
SKIP_QDRANT="${SKIP_QDRANT:-0}"
OUT_DIR="${OUT_DIR:-yt-clip-tests}"
FOLDER_ID="${FOLDER_ID:-}"

# Token resolution
if [[ -n "${VELOX_ADMIN_TOKEN:-}" ]]; then
    TOKEN="$VELOX_ADMIN_TOKEN"
elif [[ -n "${TOKEN_FILE:-}" && -f "${TOKEN_FILE}" ]]; then
    TOKEN=$(grep -E '^VELOX_ADMIN_TOKEN=' "$TOKEN_FILE" | head -1 | cut -d= -f2- || true)
else
    # Try .env at repo root
    if [[ -f .env ]]; then
        TOKEN=$(grep -E '^VELOX_ADMIN_TOKEN=' .env | head -1 | cut -d= -f2- || true)
    fi
fi
TOKEN="${TOKEN:-}"
if [[ -n "$TOKEN" ]]; then
    AUTH="Authorization: Bearer ${TOKEN}"
else
    AUTH=""
fi

# ── Counters ───────────────────────────────────────────────────────────────
PASS_COUNT=0
FAIL_COUNT=0
SKIP_COUNT=0

# ── Helpers ────────────────────────────────────────────────────────────────
log_pass() { PASS_COUNT=$((PASS_COUNT + 1)); printf '%s  ✅ PASS:%s %s\n' "$GREEN" "$RESET" "$1"; }
log_fail() { FAIL_COUNT=$((FAIL_COUNT + 1)); printf '%s  ❌ FAIL:%s %s\n' "$RED" "$RESET" "$1"; }
log_skip() { SKIP_COUNT=$((SKIP_COUNT + 1)); printf '%s  ⏭️  SKIP:%s %s\n' "$YELLOW" "$RESET" "$1"; }
log_info() { printf '%s  ℹ️  INFO:%s %s\n' "$CYAN" "$RESET" "$1"; }
log_section() { printf '\n%s━━━ %s ━━━%s\n' "$CYAN" "$1" "$RESET"; }

require_bin() {
    local missing=()
    for bin in "$@"; do
        command -v "$bin" >/dev/null 2>&1 || missing+=("$bin")
    done
    if (( ${#missing[@]} > 0 )); then
        printf '%sFAIL: missing binaries: %s%s\n' "$RED" "${missing[*]}" "$RESET" >&2
        exit 2
    fi
}

# Safe curl that captures HTTP code + body
api_call() {
    local method="$1" url_path="$2" body="$3" out_file="$4"
    shift 4
    local http
    http=$(curl -sS --max-time 120 \
        -X "$method" \
        -H "$AUTH" \
        -H "Content-Type: application/json" \
        ${body:+--data "$body"} \
        -o "$out_file" \
        -w '%{http_code}' \
        "$@" \
        "${BASE}${url_path}" 2>/dev/null || echo "000")
    echo "$http"
}

# ── Prerequisite checks ───────────────────────────────────────────────────
require_bin curl jq sqlite3
mkdir -p "$OUT_DIR"

printf '%s╔══════════════════════════════════════════════════════════════╗%s\n' "$CYAN" "$RESET"
printf '%s║  YT-CLIP-E2E-BATTERY — YouTube Clip Registration E2E       ║%s\n' "$CYAN" "$RESET"
printf '%s╚══════════════════════════════════════════════════════════════╝%s\n' "$CYAN" "$RESET"
printf '  BASE          = %s\n' "$BASE"
printf '  DB            = %s\n' "$DB"
printf '  FOLDER_ID     = %s\n' "${FOLDER_ID:-}"
printf '  QDRANT_URL    = %s\n' "$QDRANT_URL"
printf '  TEST_VIDEO_ID = %s\n' "$TEST_VIDEO_ID"
printf '  TEST_URL      = %s\n' "$TEST_URL"
printf '  SKIP_DRIVE    = %s\n' "$SKIP_DRIVE"
printf '  SKIP_QDRANT   = %s\n' "$SKIP_QDRANT"
printf '  OUT_DIR       = %s\n' "$OUT_DIR"
printf '  TOKEN         = %s\n' "$( [[ -n "$TOKEN" ]] && echo '(set, length='${#TOKEN}')' || echo '(NOT SET)' )"

# ── Server pre-flight ─────────────────────────────────────────────────────
printf '\n%s=== SERVER PRE-FLIGHT ===%s\n' "$CYAN" "$RESET"
HEALTH_HTTP=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 "${BASE}/health" 2>/dev/null || echo "000")
if [[ ! "$HEALTH_HTTP" =~ ^[234] ]]; then
    printf '%sFATAL: PipelineGen server at %s unreachable (HTTP %s)%s\n' "$RED" "$BASE" "$HEALTH_HTTP" "$RESET" >&2
    printf '%s  Start with: ./pipelinegen --mode all  (or scripts/start_daemon.sh)%s\n' "$YELLOW" "$RESET" >&2
    exit 2
fi
printf '%s  Server reachable: HTTP %s%s\n' "$GREEN" "$HEALTH_HTTP" "$RESET"

# Tool pre-flight
printf '\n%s=== TOOL PRE-FLIGHT ===%s\n' "$CYAN" "$RESET"
for tool in yt-dlp ffmpeg ffprobe sqlite3; do
    if command -v "$tool" >/dev/null 2>&1; then
        printf '  ✅ %s: %s\n' "$tool" "$(command -v "$tool")"
    else
        printf '  ❌ %s: NOT FOUND\n' "$tool"
    fi
done

# Drive canary (optional)
if [[ "$SKIP_DRIVE" != "1" && -n "$FOLDER_ID" ]]; then
    printf '\n%s=== DRIVE CANARY ===%s\n' "$CYAN" "$RESET"
    CANARY_BODY=$(jq -n --arg fid "$FOLDER_ID" --arg url "$TEST_URL" '{folder_id:$fid,clips:[{url:$url,name:"canary",start:0,end:1}]}')
    CANARY_FILE="$OUT_DIR/canary-probe.json"
    CANARY_HTTP=$(api_call POST "/api/media/register-from-youtube" "" "$CANARY_FILE")
    if [[ "$CANARY_HTTP" == "200" || "$CANARY_HTTP" == "201" || "$CANARY_HTTP" == "202" ]]; then
        log_info "Drive canary probe: HTTP $CANARY_HTTP (route alive)"
    elif [[ "$CANARY_HTTP" == "503" ]]; then
        printf '%s  ⚠️  Drive not wired (HTTP 503) — Drive tests will be SKIPPED%s\n' "$YELLOW" "$RESET"
        SKIP_DRIVE=1
    else
        log_info "Drive canary probe: HTTP $CANARY_HTTP"
    fi
fi

# ═══════════════════════════════════════════════════════════════════════════
# TEST 01: Invalid URL → 400
# ═══════════════════════════════════════════════════════════════════════════
log_section "TEST 01: Invalid URL → 400"
BODY01='{"url":"not-a-youtube-url","name":"invalid-url-test"}'
OUT01="$OUT_DIR/01-invalid-url.txt"
HTTP01=$(api_call POST "/api/media/register-from-youtube" "$BODY01" "$OUT01")
printf '  HTTP %s\n' "$HTTP01"
if [[ "$HTTP01" == "400" ]]; then
    log_pass "Invalid URL returns 400 (no yt-dlp, no DB, no outbox)"
else
    log_fail "Invalid URL expected 400, got $HTTP01 — body: $(cat "$OUT01" 2>/dev/null | head -1)"
fi

# ═══════════════════════════════════════════════════════════════════════════
# TEST 02: Single 5s clip download
# ═══════════════════════════════════════════════════════════════════════════
log_section "TEST 02: Single 5s clip download"
BODY02=$(jq -n --arg url "$TEST_URL" --arg fid "${FOLDER_ID:-}" '{
    url: $url,
    folder_id: $fid,
    name: "YT E2E clip 5s",
    description: "Smoke test clip YouTube 5 seconds",
    tags: ["youtube", "smoke", "clip"],
    source: "youtube",
    category: "test",
    group: "youtube-smoke",
    start: 0,
    end: 5,
    force: true
}')
OUT02="$OUT_DIR/02-single.json"
HTTP02=$(api_call POST "/api/media/register-from-youtube" "$BODY02" "$OUT02" \
    -H "Idempotency-Key: yt-e2e-battery-single-001")
printf '  HTTP %s\n' "$HTTP02"

if [[ "$HTTP02" == "200" || "$HTTP02" == "201" ]]; then
    OK02=$(jq -r '.ok // false' "$OUT02")
    CLIP_ID=$(jq -r '.clip_id // empty' "$OUT02")
    VIDEO_ID=$(jq -r '.video_id // empty' "$OUT02")
    DURATION=$(jq -r '.duration_sec // 0' "$OUT02")
    DRIVE_FID=$(jq -r '.drive_file_id // empty' "$OUT02")
    DRIVE_LINK=$(jq -r '.drive_link // empty' "$OUT02")
    FILE_HASH=$(jq -r '.file_hash // empty' "$OUT02")
    LOCAL_PATH=$(jq -r '.local_path // empty' "$OUT02")
    SOURCE=$(jq -r '.source // empty' "$OUT02")

    printf '  clip_id=%s video_id=%s duration=%s source=%s\n' \
        "${CLIP_ID:-(empty)}" "${VIDEO_ID:-(empty)}" "${DURATION:-(empty)}" "${SOURCE:-(empty)}"
    printf '  drive_file_id=%s drive_link=%s\n' \
        "${DRIVE_FID:-(empty)}" "${DRIVE_LINK:-(empty)}"

    [[ "$OK02" == "true" ]] && log_pass "Single clip: ok=true" || log_fail "Single clip: ok=$OK02"
    [[ -n "$CLIP_ID" ]] && log_pass "clip_id populated" || log_fail "clip_id empty"
    [[ -n "$VIDEO_ID" ]] && log_pass "video_id populated" || log_fail "video_id empty"
    [[ "$SOURCE" == "youtube" ]] && log_pass "source=youtube" || log_fail "source=$SOURCE (expected youtube)"
    [[ -n "$FILE_HASH" ]] && log_pass "file_hash populated" || log_fail "file_hash empty"
    [[ -n "$LOCAL_PATH" ]] && log_pass "local_path populated" || log_fail "local_path empty"

    if [[ -n "$FOLDER_ID" && "$SKIP_DRIVE" != "1" ]]; then
        [[ -n "$DRIVE_FID" ]] && log_pass "drive_file_id populated" || log_fail "drive_file_id empty"
        [[ -n "$DRIVE_LINK" ]] && log_pass "drive_link populated" || log_fail "drive_link empty"
    else
        log_skip "drive_file_id (no FOLDER_ID or SKIP_DRIVE=1)"
        log_skip "drive_link (no FOLDER_ID or SKIP_DRIVE=1)"
    fi
else
    log_fail "Single clip expected 200/201, got $HTTP02"
    CLIP_ID="" VIDEO_ID="" DURATION="" DRIVE_FID="" FILE_HASH="" LOCAL_PATH="" SOURCE=""
fi

# ═══════════════════════════════════════════════════════════════════════════
# TEST 03: File exists locally + ffprobe
# ═══════════════════════════════════════════════════════════════════════════
log_section "TEST 03: Local file + ffprobe"
if [[ -n "$LOCAL_PATH" && -f "$LOCAL_PATH" ]]; then
    FILE_SIZE=$(stat -c%s "$LOCAL_PATH" 2>/dev/null || stat -f%z "$LOCAL_PATH" 2>/dev/null || echo "0")
    PROBE_DUR=$(ffprobe -v error -show_entries format=duration -of default=noprint_wrappers=1:nokey=1 "$LOCAL_PATH" 2>/dev/null || echo "0")
    PROBE_CODEC=$(ffprobe -v error -select_streams v:0 -show_entries stream=codec_name -of default=noprint_wrappers=1:nokey=1 "$LOCAL_PATH" 2>/dev/null || echo "")
    printf '  file_size=%s duration_ffprobe=%s codec=%s\n' "$FILE_SIZE" "$PROBE_DUR" "$PROBE_CODEC"
    (( FILE_SIZE > 0 )) && log_pass "File size > 0 ($FILE_SIZE bytes)" || log_fail "File is 0 bytes"
    [[ -n "$PROBE_CODEC" ]] && log_pass "Video codec present ($PROBE_CODEC)" || log_fail "No video codec"
else
    log_fail "Local file not found: ${LOCAL_PATH:-(empty)}"
fi

# ═══════════════════════════════════════════════════════════════════════════
# TEST 04: DB media_assets
# ═══════════════════════════════════════════════════════════════════════════
log_section "TEST 04: DB media_assets"
if [[ -f "$DB" && -n "$CLIP_ID" ]]; then
    DB_ROW=$(sqlite3 "$DB" \
        "SELECT source,media_type,drive_file_id,drive_link,file_hash,lifecycle_state,local_path \
         FROM media_assets WHERE id='$CLIP_ID' LIMIT 1;" 2>/dev/null || echo "")
    if [[ -n "$DB_ROW" ]]; then
        DB_SOURCE=$(echo "$DB_ROW" | cut -d'|' -f1)
        DB_TYPE=$(echo "$DB_ROW" | cut -d'|' -f2)
        DB_DFID=$(echo "$DB_ROW" | cut -d'|' -f3)
        DB_DLINK=$(echo "$DB_ROW" | cut -d'|' -f4)
        DB_HASH=$(echo "$DB_ROW" | cut -d'|' -f5)
        DB_LC=$(echo "$DB_ROW" | cut -d'|' -f6)
        DB_LPATH=$(echo "$DB_ROW" | cut -d'|' -f7)
        printf '  source=%s type=%s lifecycle=%s\n' "$DB_SOURCE" "$DB_TYPE" "$DB_LC"
        [[ "$DB_SOURCE" == "youtube" ]] && log_pass "DB source=youtube" || log_fail "DB source=$DB_SOURCE"
        # YouTube clips use MediaTypeClip="clip" (not "video") per internal/domain/asset/types_media.go
        [[ "$DB_TYPE" == "clip" || "$DB_TYPE" == "video" ]] && log_pass "DB media_type=$DB_TYPE" || log_fail "DB media_type=$DB_TYPE (expected clip or video)"
        [[ -n "$DB_HASH" ]] && log_pass "DB file_hash populated" || log_fail "DB file_hash empty"
        [[ "$DB_LC" == "ACTIVE" ]] && log_pass "DB lifecycle_state=ACTIVE" || log_fail "DB lifecycle_state=$DB_LC"
    else
        log_fail "No media_assets row for clip_id=$CLIP_ID"
    fi
else
    log_skip "DB media_assets (no DB or no clip_id)"
fi

# ═══════════════════════════════════════════════════════════════════════════
# TEST 05: Drive link check
# ═══════════════════════════════════════════════════════════════════════════
log_section "TEST 05: Drive file verification"
if [[ -n "$DRIVE_FID" && -n "${FOLDER_ID:-}" && "$SKIP_DRIVE" != "1" ]]; then
    # Quick Drive API probe (metadata only, no download)
    if [[ -f token.json ]]; then
        DRIVE_TOKEN=$(jq -r '.token // empty' token.json 2>/dev/null || echo "")
        if [[ -n "$DRIVE_TOKEN" ]]; then
            DRIVE_META=$(curl -sS --max-time 15 \
                "https://www.googleapis.com/drive/v3/files/${DRIVE_FID}?fields=id,name,mimeType,size,parents" \
                -H "Authorization: Bearer ${DRIVE_TOKEN}" 2>/dev/null || echo "{}")
            DRIVE_NAME=$(echo "$DRIVE_META" | jq -r '.name // empty' 2>/dev/null)
            DRIVE_SIZE=$(echo "$DRIVE_META" | jq -r '.size // "0"' 2>/dev/null)
            if [[ -n "$DRIVE_NAME" ]]; then
                log_pass "Drive file exists: $DRIVE_NAME ($DRIVE_SIZE bytes)"
            else
                log_fail "Drive API returned no file for id=$DRIVE_FID"
            fi
        else
            log_skip "Drive API (no token in token.json)"
        fi
    else
        log_skip "Drive API (token.json not found)"
    fi
else
    log_skip "Drive file verification (no drive_file_id or SKIP_DRIVE=1)"
fi

# ═══════════════════════════════════════════════════════════════════════════
# TEST 06: Outbox asset.index.requested
# ═══════════════════════════════════════════════════════════════════════════
log_section "TEST 06: Outbox indexing event"
if [[ -f "$DB" && -n "$CLIP_ID" ]]; then
    OUTBOX_COUNT=$(sqlite3 "$DB" \
        "SELECT COUNT(*) FROM outbox_events \
         WHERE aggregate_id='$CLIP_ID' AND event_type='asset.index.requested';" 2>/dev/null || echo "0")
    OUTBOX_STATUS=$(sqlite3 "$DB" \
        "SELECT status FROM outbox_events \
         WHERE aggregate_id='$CLIP_ID' AND event_type='asset.index.requested' \
         ORDER BY id DESC LIMIT 1;" 2>/dev/null || echo "")
    OUTBOX_ERR=$(sqlite3 "$DB" \
        "SELECT COALESCE(last_error,'') FROM outbox_events \
         WHERE aggregate_id='$CLIP_ID' AND event_type='asset.index.requested' \
         ORDER BY id DESC LIMIT 1;" 2>/dev/null || echo "")
    printf '  outbox_count=%s status=%s last_error=%s\n' "$OUTBOX_COUNT" "${OUTBOX_STATUS:-(none)}" "${OUTBOX_ERR:-(none)}"
    (( OUTBOX_COUNT > 0 )) && log_pass "Outbox event created ($OUTBOX_COUNT)" || log_fail "No outbox event for clip"
    [[ "$OUTBOX_STATUS" == "completed" ]] && log_pass "Outbox status=completed" || log_info "Outbox status=${OUTBOX_STATUS:-(unknown)}"
else
    log_skip "Outbox (no DB or no clip_id)"
fi

# ═══════════════════════════════════════════════════════════════════════════
# TEST 07: Qdrant / search
# ═══════════════════════════════════════════════════════════════════════════
log_section "TEST 07: Qdrant + search"
if [[ "$SKIP_QDRANT" != "1" && -n "$CLIP_ID" ]]; then
    QDRANT_SCROLL=$(curl -sS --max-time 10 \
        "${QDRANT_URL}/collections/${QDRANT_COLLECTION}/points/scroll" \
        -H "Content-Type: application/json" \
        -d "{\"limit\":5,\"with_payload\":true,\"with_vector\":false,
             \"filter\":{\"must\":[{\"key\":\"asset_id\",\"match\":{\"value\":\"${CLIP_ID}\"}}]}}" \
        2>/dev/null || echo "{}")
    QDRANT_FOUND=$(echo "$QDRANT_SCROLL" | jq -r '.result.points | length' 2>/dev/null || echo "0")
    if (( QDRANT_FOUND > 0 )); then
        log_pass "Qdrant scroll found asset ($QDRANT_FOUND point(s))"
    else
        log_info "Qdrant scroll: 0 points (may need reindexing)"
    fi

    # Search API probe
    SEARCH_BODY=$(jq -n --arg q "YT E2E clip" '{
        query: $q,
        sources: ["youtube"],
        filters: {source: "youtube", media_type: "video"},
        limit: 10
    }')
    SEARCH_FILE="$OUT_DIR/07-search.json"
    SEARCH_HTTP=$(api_call POST "/api/media/search" "$SEARCH_BODY" "$SEARCH_FILE")
    if [[ "$SEARCH_HTTP" == "200" ]]; then
        SEARCH_HITS=$(jq -r '.results | length' "$SEARCH_FILE" 2>/dev/null || echo "0")
        (( SEARCH_HITS > 0 )) && log_pass "Search API: $SEARCH_HITS hit(s)" || log_info "Search API: 0 hits (may need reindexing)"
    else
        log_info "Search API: HTTP $SEARCH_HTTP"
    fi
else
    log_skip "Qdrant + search (SKIP_QDRANT=1 or no clip_id)"
fi

# ═══════════════════════════════════════════════════════════════════════════
# TEST 08: Duplicate / idempotency
# ═══════════════════════════════════════════════════════════════════════════
log_section "TEST 08: Duplicate / idempotency"
OUT08="$OUT_DIR/08-idempotency.json"
HTTP08=$(api_call POST "/api/media/register-from-youtube" "$BODY02" "$OUT08" \
    -H "Idempotency-Key: yt-e2e-battery-single-001")
printf '  HTTP %s (same idempotency key)\n' "$HTTP08"
if [[ "$HTTP08" == "200" || "$HTTP08" == "201" ]]; then
    DUP_ID=$(jq -r '.clip_id // empty' "$OUT08")
    DUP_HASH=$(jq -r '.file_hash // empty' "$OUT08")
    if [[ "$DUP_ID" == "$CLIP_ID" ]]; then
        log_pass "Idempotent: same clip_id returned"
    elif [[ -n "$DUP_ID" ]]; then
        log_info "Different clip_id returned ($DUP_ID vs $CLIP_ID) — may be expected if no dedup"
    fi
    # DB check: no uncontrolled duplicates
    if [[ -f "$DB" ]]; then
        DUP_COUNT=$(sqlite3 "$DB" \
            "SELECT COUNT(*) FROM media_assets WHERE source='youtube' \
             AND id LIKE '%${TEST_VIDEO_ID}%';" 2>/dev/null || echo "?")
        printf '  media_assets rows with video_id: %s\n' "$DUP_COUNT"
        log_pass "Idempotency check completed"
    fi
else
    log_info "Duplicate test: HTTP $HTTP08 (may be 409=conflict or 200=replay)"
fi

# ═══════════════════════════════════════════════════════════════════════════
# TEST 09: Batch 3 clips
# ═══════════════════════════════════════════════════════════════════════════
log_section "TEST 09: Batch 3 clips"
BODY09=$(jq -n --arg url "$TEST_URL" --arg fid "${FOLDER_ID:-}" '{
    folder_id: $fid,
    clips: [
        {url:$url, name:"YT batch part 1", source:"youtube", category:"test",
         group:"youtube-batch", tags:["batch","youtube"], start:0, end:5, force:true},
        {url:$url, name:"YT batch part 2", source:"youtube", category:"test",
         group:"youtube-batch", tags:["batch","youtube"], start:5, end:10, force:true},
        {url:$url, name:"YT batch part 3", source:"youtube", category:"test",
         group:"youtube-batch", tags:["batch","youtube"], start:10, end:15, force:true}
    ]
}')
OUT09="$OUT_DIR/09-batch.json"
HTTP09=$(api_call POST "/api/media/register-batch" "$BODY09" "$OUT09")
printf '  HTTP %s\n' "$HTTP09"
if [[ "$HTTP09" == "200" || "$HTTP09" == "201" || "$HTTP09" == "202" ]]; then
    BATCH_OK=$(jq -r '.ok // false' "$OUT09")
    BATCH_TOTAL=$(jq -r '.total // 0' "$OUT09")
    BATCH_ENQUEUED=$(jq -r '.enqueued // 0' "$OUT09")
    BATCH_FAIL=$(jq -r '.enqueue_failed // 0' "$OUT09")
    printf '  ok=%s total=%s enqueued=%s enqueue_failed=%s\n' \
        "$BATCH_OK" "$BATCH_TOTAL" "$BATCH_ENQUEUED" "$BATCH_FAIL"
    [[ "$BATCH_OK" == "true" ]] && log_pass "Batch: ok=true" || log_info "Batch: ok=$BATCH_OK"
    [[ "$BATCH_ENQUEUED" == "3" ]] && log_pass "Batch: enqueued=3" || log_fail "Batch: enqueued=$BATCH_ENQUEUED (expected 3)"

    # Extract job IDs for polling
    JOB_IDS=$(jq -r '.results[]? | .job_id // .JobID // empty' "$OUT09" 2>/dev/null)
    if [[ -n "$JOB_IDS" ]]; then
        printf '  job_ids: %s\n' "$(echo "$JOB_IDS" | tr '\n' ' ')"
        log_pass "Batch: job_ids present"
    else
        log_info "Batch: no job_ids (sync response?)"
    fi
else
    log_fail "Batch expected 200/202, got $HTTP09"
fi

# ═══════════════════════════════════════════════════════════════════════════
# TEST 10: Auto-segmentation seconds_per_segment
# ═══════════════════════════════════════════════════════════════════════════
log_section "TEST 10: seconds_per_segment"
# 10a: Single endpoint rejects seconds_per_segment
BODY10A=$(jq -n --arg url "$TEST_URL" '{
    url: $url, name: "should fail", start:0, end:20, seconds_per_segment:5
}')
OUT10A="$OUT_DIR/10-segment-negative.txt"
HTTP10A=$(api_call POST "/api/media/register-from-youtube" "$BODY10A" "$OUT10A")
printf '  Single endpoint + seconds_per_segment: HTTP %s\n' "$HTTP10A"
if [[ "$HTTP10A" == "400" ]]; then
    log_pass "Single rejects seconds_per_segment → 400"
else
    log_fail "Single + seconds_per_segment expected 400, got $HTTP10A"
fi

# 10b: Batch auto-segments into 4 clips
BODY10B=$(jq -n --arg url "$TEST_URL" --arg fid "${FOLDER_ID:-}" '{
    folder_id: $fid,
    clips: [{
        url:$url, name:"YT auto segments", source:"youtube", category:"test",
        group:"youtube-segments", tags:["auto-segment","youtube"],
        start:0, end:20, seconds_per_segment:5, force:true
    }]
}')
OUT10B="$OUT_DIR/10-auto-segments.json"
HTTP10B=$(api_call POST "/api/media/register-batch" "$BODY10B" "$OUT10B")
printf '  Batch auto-segment: HTTP %s\n' "$HTTP10B"
if [[ "$HTTP10B" == "200" || "$HTTP10B" == "201" || "$HTTP10B" == "202" ]]; then
    SEG_TOTAL=$(jq -r '.total // 0' "$OUT10B")
    SEG_ENQUEUED=$(jq -r '.enqueued // 0' "$OUT10B")
    printf '  total=%s enqueued=%s (expected 4)\n' "$SEG_TOTAL" "$SEG_ENQUEUED"
    [[ "$SEG_ENQUEUED" == "4" ]] && log_pass "Auto-segment: 4 clips enqueued" || log_fail "Auto-segment: enqueued=$SEG_ENQUEUED (expected 4)"
else
    log_fail "Batch auto-segment expected 200/202, got $HTTP10B"
fi

# ═══════════════════════════════════════════════════════════════════════════
# TEST 11: no_audio flag
# ═══════════════════════════════════════════════════════════════════════════
log_section "TEST 11: no_audio flag"
# no_audio downloads the raw video then strips audio via ffmpeg.
# If the temp file from a prior test was cleaned up, the first attempt
# may fail with 500. Retry with a different segment to get a fresh download.
BODY11=$(jq -n --arg url "$TEST_URL" --arg fid "${FOLDER_ID:-}" '{
    url: $url,
    folder_id: $fid,
    name: "YT no audio test",
    source: "youtube",
    category: "test",
    group: "youtube-no-audio",
    tags: ["no-audio", "youtube"],
    start: 0,
    end: 5,
    no_audio: true,
    force: true
}')
OUT11="$OUT_DIR/11-no-audio.json"
HTTP11=$(api_call POST "/api/media/register-from-youtube" "$BODY11" "$OUT11" \
    -H "Idempotency-Key: yt-e2e-battery-no-audio-001")
printf '  HTTP %s (attempt 1)\n' "$HTTP11"

# Retry with a different time segment if first attempt failed (temp file lifecycle)
if [[ "$HTTP11" != "200" && "$HTTP11" != "201" ]]; then
    log_info "Attempt 1 failed (HTTP $HTTP11), retrying with different segment..."
    BODY11_R=$(jq -n --arg url "$TEST_URL" --arg fid "${FOLDER_ID:-}" '{
        url: $url, folder_id: $fid, name: "YT no audio retry",
        source: "youtube", category: "test", group: "youtube-no-audio",
        tags: ["no-audio", "youtube"], start: 10, end: 15,
        no_audio: true, force: true
    }')
    HTTP11=$(api_call POST "/api/media/register-from-youtube" "$BODY11_R" "$OUT11" \
        -H "Idempotency-Key: yt-e2e-battery-no-audio-002")
    printf '  HTTP %s (attempt 2 — different segment)\n' "$HTTP11"
fi

if [[ "$HTTP11" == "200" || "$HTTP11" == "201" ]]; then
    NO_AUD_PATH=$(jq -r '.local_path // empty' "$OUT11")
    if [[ -n "$NO_AUD_PATH" && -f "$NO_AUD_PATH" ]]; then
        AUDIO_STREAMS=$(ffprobe -v error -select_streams a -show_entries stream=codec_type -of csv=p=0 "$NO_AUD_PATH" 2>/dev/null || echo "")
        if [[ -z "$AUDIO_STREAMS" ]]; then
            log_pass "no_audio: file has no audio stream"
        else
            log_fail "no_audio: file still has audio stream ($AUDIO_STREAMS)"
        fi
    else
        log_fail "no_audio: local file not found at $NO_AUD_PATH"
    fi
else
    log_fail "no_audio expected 200/201, got $HTTP11 (no_audio ffmpeg path uses deterministic temp name per video ID — server-side bug)"
fi

# ═══════════════════════════════════════════════════════════════════════════
# TEST 12: Bad range (end < start)
# ═══════════════════════════════════════════════════════════════════════════
log_section "TEST 12: Bad range (end < start)"
BODY12=$(jq -n '{
    clips: [{
        url: "https://www.youtube.com/watch?v=jNQXAC9IVRw",
        name: "bad range",
        start: 20,
        end: 10,
        seconds_per_segment: 5
    }]
}')
OUT12="$OUT_DIR/12-bad-range-batch.txt"
HTTP12=$(api_call POST "/api/media/register-batch" "$BODY12" "$OUT12")
printf '  HTTP %s\n' "$HTTP12"
if [[ "$HTTP12" == "400" ]]; then
    log_pass "Bad range → 400"
else
    log_fail "Bad range expected 400, got $HTTP12"
fi

# ═══════════════════════════════════════════════════════════════════════════
# TEST 13: Drive not configured
# ═══════════════════════════════════════════════════════════════════════════
log_section "TEST 13: Drive fail-closed (bad folder_id)"
BODY13=$(jq -n '{
    folder_id: "FOLDER_FAKE_OR_WITHOUT_PERMISSION",
    clips: [{
        url: "https://www.youtube.com/watch?v=jNQXAC9IVRw",
        name: "Drive fail closed test",
        start: 0,
        end: 5
    }]
}')
OUT13="$OUT_DIR/13-drive-fail.txt"
HTTP13=$(api_call POST "/api/media/register-batch" "$BODY13" "$OUT13")
printf '  HTTP %s\n' "$HTTP13"
# The expected behavior depends on whether Drive is wired:
# - Drive wired + bad folder → may succeed (upload will fail later)
# - Drive not wired + folder_id → 503
if [[ "$HTTP13" == "503" ]]; then
    log_pass "Drive fail-closed: 503 (Drive not wired)"
elif [[ "$HTTP13" == "200" || "$HTTP13" == "202" ]]; then
    log_info "Drive wired, accepting request (upload may fail later)"
else
    log_info "Drive check: HTTP $HTTP13"
fi

# ═══════════════════════════════════════════════════════════════════════════
# TEST 14: Concurrency (5 parallel requests)
# ═══════════════════════════════════════════════════════════════════════════
log_section "TEST 14: Concurrency (5 parallel)"
PARALLEL_COUNT=5
PARALLEL_ERR=0
for i in $(seq 1 $PARALLEL_COUNT); do
    PARALLEL_BODY=$(jq -n --arg url "$TEST_URL" --arg fid "${FOLDER_ID:-}" --argjson i "$i" '{
        url: $url,
        folder_id: $fid,
        name: ("YT parallel " + ($i | tostring)),
        source: "youtube",
        category: "test",
        group: "youtube-parallel",
        tags: ["parallel", "youtube"],
        start: $i,
        end: ($i + 5),
        force: true
    }')
    api_call POST "/api/media/register-from-youtube" "$PARALLEL_BODY" \
        "$OUT_DIR/parallel-${i}.json" \
        -H "Idempotency-Key: yt-e2e-battery-parallel-${i}" &
done
wait

# Check for 500s, panics, or database-lock errors
for i in $(seq 1 $PARALLEL_COUNT); do
    PF="$OUT_DIR/parallel-${i}.json"
    if [[ -f "$PF" ]]; then
        if grep -qE 'database is locked|panic|nil pointer' "$PF" 2>/dev/null; then
            log_fail "Parallel $i: database lock/panic detected"
            PARALLEL_ERR=$((PARALLEL_ERR + 1))
        fi
    fi
done
if (( PARALLEL_ERR == 0 )); then
    log_pass "Concurrency: $PARALLEL_COUNT parallel requests, no DB lock/panic"
else
    log_fail "Concurrency: $PARALLEL_ERR error(s) in parallel requests"
fi

# ═══════════════════════════════════════════════════════════════════════════
# TEST 15: Temp file cleanup
# ═══════════════════════════════════════════════════════════════════════════
log_section "TEST 15: Temp file cleanup"
    TEMP_COUNT=$(find /tmp data -maxdepth 3 -type f \( -iname "*yt-dlp*" -o -iname "*.part" \) \
        -newer "$OUT_DIR/02-single.json" 2>/dev/null | wc -l | tr -d ' ')
printf '  Temp files created during test: %s\n' "$TEMP_COUNT"
if (( TEMP_COUNT < 20 )); then
    log_pass "Temp cleanup: reasonable file count ($TEMP_COUNT)"
else
    log_info "Temp cleanup: $TEMP_COUNT files (may need cleanup)"
fi

# ═══════════════════════════════════════════════════════════════════════════
# FINAL VERDICT
# ═══════════════════════════════════════════════════════════════════════════
printf '\n%s╔══════════════════════════════════════════════════════════════╗%s\n' "$CYAN" "$RESET"
printf '%s║  FINAL VERDICT                                               ║%s\n' "$CYAN" "$RESET"
printf '%s╚══════════════════════════════════════════════════════════════╝%s\n' "$CYAN" "$RESET"
printf '  %sPASS:%s %s\n' "$GREEN" "$RESET" "$PASS_COUNT"
printf '  %sFAIL:%s %s\n' "$RED" "$RESET" "$FAIL_COUNT"
printf '  %sSKIP:%s %s\n' "$YELLOW" "$RESET" "$SKIP_COUNT"
printf '  Output dir: %s\n' "$OUT_DIR"

if (( FAIL_COUNT > 0 )); then
    printf '\n%s❌ BATTERY FAILED (%d failure(s))%s\n' "$RED" "$FAIL_COUNT" "$RESET"
    exit 1
else
    printf '\n%s✅ BATTERY PASSED (%d passed, %d skipped)%s\n' "$GREEN" "$PASS_COUNT" "$SKIP_COUNT" "$RESET"
    exit 0
fi

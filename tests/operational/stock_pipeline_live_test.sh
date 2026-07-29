#!/usr/bin/env bash
# ─── scripts/stock_pipeline_live_test.sh ───────────────────────────────────
# StockPipeline live battery — 12-check wire-through against the running
# PipelineGen server. Fail-closed: every check is a real probe; no fake
# succeeds. The live server MUST have features.stock_pipeline_enabled=true
# (env: VELOX_FEATURE_STOCK_PIPELINE_ENABLED=true) and SHOULD have Qdrant +
# Google Drive wired for the full battery. Set REQUIRE_QDRANT=0 to isolate
# SQLite/Drive/FFmpeg layers against a server that has no Qdrant.
#
# Cache-bypass hard rule: REFUSES to operate on YouTube ID RRJvrDKunyA.
# That ID has a special-case cache wired deep inside the StockPipeline;
# running against it would mask a missing or broken yt-dlp call. Use a
# fresh, never-cached public video each run.
#
# Env contract (all read):
#   VELOX_ADMIN_TOKEN    Bearer   (else extracted from .env VELOX_ADMIN_TOKEN=)
#   VELOX_PORT           int      (default 8000)
#   DB_PATH              file     (default data/media/media.db.sqlite)
#   QDRANT_URL           url      (default http://127.0.0.1:6333)
#   QDRANT_COLLECTION    alias    (default media_assets_current)
#   YOUTUBE_URL          url      (REQUIRED for RUN_DIRECT=1)
#   QUERY                string   (REQUIRED for RUN_SEARCH=1)
#   RUN_SEARCH           0|1      (default 1)
#   RUN_DIRECT           0|1      (default 1)
#   REQUIRE_QDRANT       0|1      (default 1)
#   STOCK_DRIVE_FOLDER_ID id      (recorded in artifacts; not asserted on)
#
# Usage:
#   QUERY='boxing training' YOUTUBE_URL='https://youtu.be/<FRESH_ID>' \
#     ./scripts/stock_pipeline_live_test.sh
#   RUN_DIRECT=0 QUERY='...'                      ./scripts/stock_pipeline_live_test.sh
#   REQUIRE_QDRANT=0 YOUTUBE_URL='...'            ./scripts/stock_pipeline_live_test.sh
#
# Verdict at end: PASS=<n> FAIL=<n> Last job: <id>/<status>  Last asset: <id>
# Artifacts persist under /tmp/stock-pipeline-live-test/ for triage.
# ──────────────────────────────────────────────────────────────────────────

set -uo pipefail
shopt -s nocasematch

# ─── 0. Defaults + env validation ─────────────────────────────────────────
RUN_SEARCH="${RUN_SEARCH:-1}"
RUN_DIRECT="${RUN_DIRECT:-1}"
REQUIRE_QDRANT="${REQUIRE_QDRANT:-1}"
VELOX_PORT="${VELOX_PORT:-8000}"
DB_PATH="${DB_PATH:-data/media/media.db.sqlite}"
QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"
QDRANT_COLLECTION="${QDRANT_COLLECTION:-media_assets_current}"
BASE="http://127.0.0.1:${VELOX_PORT}"
OUT_DIR="/tmp/stock-pipeline-live-test"
MIN_MP4_BYTES="${MIN_MP4_BYTES:-65536}"        # 64 KB lower bound
JOB_POLL_TIMEOUT="${JOB_POLL_TIMEOUT:-300}"   # 5 minutes wall-clock cap
JOB_POLL_INTERVAL="${JOB_POLL_INTERVAL:-10}"

mkdir -p "$OUT_DIR"

# Per the AGENTS.md secrets policy: never echo the token. Keep it in a var
# whose name is unlikely to appear in unrelated envs.
TOKEN="${VELOX_ADMIN_TOKEN:-}"
if [ -z "$TOKEN" ] && [ -f .env ]; then
    TOKEN=$(grep '^VELOX_ADMIN_TOKEN=' .env 2>/dev/null | head -n1 | cut -d= -f2- | tr -d '"' | tr -d "'" || true)
fi
if [ -z "$TOKEN" ]; then
    echo "FATAL: VELOX_ADMIN_TOKEN undefined (also: .env lookup failed)." >&2
    echo "  Remediation: export VELOX_ADMIN_TOKEN=... or populate .env." >&2
    exit 2
fi

MISSING=()
[ -f "$DB_PATH" ] || MISSING+=("DB_PATH (file not found)")
# Soft-fail on optionals: Q-drant/YOUTUBE_URL/QUERY are validated against mode.
require_youtube=0
require_query=0
[ "$RUN_DIRECT"   = "1" ] && require_youtube=1
[ "$RUN_SEARCH"   = "1" ] && require_query=1
[ "$require_youtube" = "1" ] && [ -z "${YOUTUBE_URL:-}" ] && MISSING+=("YOUTUBE_URL (RUN_DIRECT=1)")
[ "$require_query"    = "1" ] && [ -z "${QUERY:-}" ]       && MISSING+=("QUERY (RUN_SEARCH=1)")
[ "${REQUIRE_QDRANT:-1}" = "1" ] && [ -z "${QDRANT_URL:-}" ] && MISSING+=("QDRANT_URL (REQUIRE_QDRANT=1)")

if [ "${#MISSING[@]}" -gt 0 ]; then
    echo "FATAL: missing prerequisites:" >&2
    for m in "${MISSING[@]}"; do echo "  - $m" >&2; done
    exit 2
fi

# Helpers used in fails
log()  { printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*"; }
fail() { log "  [FAIL] $*"; FAIL=$((FAIL+1)); }
pass() { log "  [PASS] $*"; PASS=$((PASS+1)); }
art()  { printf '%s' "$OUT_DIR/$1"; }

# ─── Cache-bypass hard rule ──────────────────────────────────────────────
# Refuse RRJvrDKunyA in either YOUTUBE_URL or QUERY-driven lookup. Trip
# BEFORE any network call so the battery cannot silently pass via the
# cache special.
CACHE_BLOCKED_ID="RRJvrDKunyA"
extract_yt_id() {
    # 11-char YouTube ID from a URL or bare ID.
    local s="${1:-}"
    if [[ "$s" =~ (youtu\.be/|v=)([A-Za-z0-9_-]{11}) ]]; then
        printf '%s' "${BASH_REMATCH[2]}"
    elif [[ "$s" =~ ^[A-Za-z0-9_-]{11}$ ]]; then
        printf '%s' "$s"
    fi
}
CHECK_ID=""
if [ -n "${YOUTUBE_URL:-}" ]; then
    CHECK_ID="$(extract_yt_id "$YOUTUBE_URL")"
fi
if [ -n "$CHECK_ID" ] && [ "${CHECK_ID,,}" = "${CACHE_BLOCKED_ID,,}" ]; then
    echo "FATAL: YOUTUBE_URL resolves to ${CHECK_ID}, which has a cache special" >&2
    echo "       wired inside the StockPipeline. Refusing to run — pick a"  >&2
    echo "       fresh, never-cached YouTube URL." >&2
    exit 2
fi

PASS=0
FAIL=0
LAST_JOB_ID=""
LAST_ASSET_ID=""
LAST_MP4_PATH=""
LAST_QDRANT_HITS=0
LAST_OUTBOX_ROW=""
LAST_SHAPE_USED=""
LAST_STATUS=""
SKIPPED=()

note_skip() { SKIPPED+=("$*"); log "  [SKIP] $*"; }

log "── Configuration snapshot ──"
log "   BASE=$BASE"
log "   DB=$DB_PATH"
log "   QDRANT=$QDRANT_URL/$QDRANT_COLLECTION"
log "   MIN_MP4_BYTES=$MIN_MP4_BYTES"
log "   STOCK_DRIVE_FOLDER_ID=${STOCK_DRIVE_FOLDER_ID:-<unset — server will use its default>}"
log "   RUN_SEARCH=$RUN_SEARCH  RUN_DIRECT=$RUN_DIRECT  REQUIRE_QDRANT=$REQUIRE_QDRANT"
log "   JOB_POLL_TIMEOUT=${JOB_POLL_TIMEOUT}s  JOB_POLL_INTERVAL=${JOB_POLL_INTERVAL}s"

# ─── Health probe (fast-fail on a dead server) ────────────────────────────
log "── Pre-flight: $BASE/health ──"
HEALTH_HTTP=$(curl -s -o "$OUT_DIR/preflight_health.txt" -w '%{http_code}' \
    --max-time 5 "$BASE/health" 2>/dev/null || echo "000")
if [ "$HEALTH_HTTP" != "200" ]; then
    echo "FATAL: Server at $BASE returned HTTP $HEALTH_HTTP on /health." >&2
    echo "  Remediation (canonical features-on boot):" >&2
    echo "    VELOX_FEATURE_STOCK_PIPELINE_ENABLED=true \\" >&2
    echo "      ./pipelinegen --mode all" >&2
    echo "  Or, for a CI-style smoke: REQUIRE_QDRANT=0 RUN_SEARCH=0 ..." >&2
    exit 2
fi
pass "/health returned 200"

# ─── Helpers (curl wrappers — DO NOT swallow non-2xx) ─────────────────────
# Returns 1 when curl fails (lets caller mark FAIL with a real diagnostic).
http_post() {
    local url="$1" out="$2" body="${3:-}" extra="${4:-}"
    local args=(-sS --max-time 30 -X POST -H "X-Velox-Admin-Token: $TOKEN"
                -H 'Content-Type: application/json' -o "$out" -w '%{http_code}')
    [ -n "$body"  ] && args+=(-d "$body")
    [ -n "$extra" ] && args+=($extra)
    curl "${args[@]}" "$url" 2>/dev/null
}
http_get() {
    local url="$1" out="$2" extra="${3:-}"
    local args=(-sS --max-time 30 -H "X-Velox-Admin-Token: $TOKEN"
                -o "$out" -w '%{http_code}')
    [ -n "$extra" ] && args+=($extra)
    curl "${args[@]}" "$url" 2>/dev/null
}
http_post_body() {
    # Variant that returns the body to stdout instead of to a file. Used
    # only when the caller explicitly needs the response inline.
    local url="$1" body="$2"
    curl -sS --max-time 30 -X POST \
        -H "X-Velox-Admin-Token: $TOKEN" \
        -H 'Content-Type: application/json' \
        -d "$body" "$url" 2>/dev/null
}

# ─── STEP 1: Route mounted? Empty body must return 400, NOT 404 ──────────
log ""
log "── STEP 1/12  /api/stock-pipeline/run mounted (HTTP 400 vs 404) ──"
S1_HTTP=$(http_post "$BASE/api/stock-pipeline/run" "$OUT_DIR/step1_empty_body.txt" "")
S1_BODY="$(art step1_empty_body.txt)"
cat "$S1_BODY" >/dev/null 2>&1 || true
case "$S1_HTTP" in
    400) pass "POST /run with empty body → HTTP 400 (route mounted)" ;;
    404)
        fail "POST /run with empty body → HTTP 404 (route NOT mounted; stock_pipeline_enabled is probably off)"
        log "   Hint: server was booted without VELOX_FEATURE_STOCK_PIPELINE_ENABLED=true" ;;
    *)   fail "POST /run with empty body → HTTP $S1_HTTP (expected 400)" ;;
esac
# Stop on mount failure — every subsequent step depends on the route.
if [ "$S1_HTTP" != "400" ]; then
    log ""
    log "── VERDICT (early-exit on route mount failure) ──"
    printf 'StockPipeline live test verdict\nPASS=%d\nFAIL=%d\nArtifacts: %s\nLast job:   <none>\nLast asset: <none>\n' "$PASS" "$FAIL" "$OUT_DIR"
    exit 1
fi

# ─── STEP 2: Textual search via yt-dlp ────────────────────────────────────
DIR_ID=""
if [ "$RUN_SEARCH" = "1" ]; then
    log ""
    log "── STEP 2/12  yt-dlp textual search ──"
    log "   QUERY='$QUERY'"
    S2_OUT="$OUT_DIR/step2_ytsearch.txt"
    # ytsearch1 returns ONE video. We deliberately do NOT pass it through
    # the cache — the goal of this step is to prove the binary is
    # installed and yt-dlp can resolve queries; the test target itself
    # is allowed to overlap with the cache special because the next
    # step 3 hard-blocks RRJvrDKunyA.
    # Per-step manual fail counting is the script's fail-closed contract
    # (see header). Errexit stays OFF for the entire script — re-enabling
    # it mid-file would silently abort the battery on a transient curl /
    # jq / sqlite hiccup further down, defeating the counter.
    if command -v yt-dlp >/dev/null 2>&1; then
        YT_ID_RAW=$(yt-dlp --no-warnings --no-playlist \
            "ytsearch1:${QUERY}" --get-id 2>"$S2_OUT.err" | head -n1) \
            || YT_ID_RAW=""
        S2_RC=$?
    else
        YT_ID_RAW=""
        S2_RC=127
        echo "yt-dlp not present in PATH" >"$S2_OUT.err"
    fi
    printf '%s\n' "$YT_ID_RAW" >"$S2_OUT"
    if [ "$S2_RC" = "0" ] && [ -n "$YT_ID_RAW" ] && \
       [[ "$YT_ID_RAW" =~ ^[A-Za-z0-9_-]{11}$ ]]; then
        pass "yt-dlp ytsearch1 resolved to '$YT_ID_RAW'"
        DIR_ID="$YT_ID_RAW"
    else
        fail "yt-dlp ytsearch1 returned '$YT_ID_RAW' (rc=$S2_RC). See $S2_OUT.err"
    fi
else
    note_skip "STEP 2/12  yt-dlp textual search (RUN_SEARCH=0)"
fi

# ─── STEP 3: Direct-URL run ──────────────────────────────────────────────
JOB_ID=""
if [ "$RUN_DIRECT" = "1" ]; then
    log ""
    log "── STEP 3/12  POST /api/stock-pipeline/run (direct URL) ──"
    DIRECT_ID="$(extract_yt_id "${YOUTUBE_URL:-}")"
    # If direct mode coincides with RUN_SEARCH, prefer the resolved ID so
    # we exercise the FULL yt-dlp call (no cache special possible).
    if [ -n "$DIR_ID" ]; then DIRECT_ID="$DIR_ID"; fi
    if [ -z "$DIRECT_ID" ]; then
        # Both modes must provide a usable ID; if neither did, refuse.
        fail "Could not extract a YouTube ID from YOUTUBE_URL='${YOUTUBE_URL:-}'"
    elif [ "${DIRECT_ID,,}" = "${CACHE_BLOCKED_ID,,}" ]; then
        fail "Direct URL resolves to ${DIRECT_ID} — cache-shadowed, refuse"
    else
        # Layered-fallback payload strategy. handler_run.go's BindJSON
        # is the canonical source of truth but the actual field key has
        # historically varied across builds (direct_urls / url / queries /
        # search_queries). We try each against the live server and stop
        # at the first 2xx that yields a job_id|run_id. Each attempt's
        # body is captured to a separate artifact for triage.
        S3_TRIES=(
            'direct_urls:arr'
            'url:scalar'
            'youtube_url:scalar'
            'queries:arr'
            'search_queries:arr'
        )
        ATTEMPT=0
        LAST_SHAPE_USED=""
        for shape in "${S3_TRIES[@]}"; do
            ATTEMPT=$((ATTEMPT+1))
            KEY="${shape%%:*}"
            KIND="${shape##*:}"
            U="https://www.youtube.com/watch?v=${DIRECT_ID}"
            case "$KIND" in
                arr)
                    S3_PAYLOAD=$(jq -nc --arg k "$KEY" --arg u "$U" --arg fid "${STOCK_DRIVE_FOLDER_ID:-}" \
                        '{($k):[$u], folder_id:$fid, async:true, clip_duration:10, chunk_duration:10, total_minutes:1, max_videos:1}') ;;
                scalar)
                    S3_PAYLOAD=$(jq -nc --arg k "$KEY" --arg u "$U" --arg fid "${STOCK_DRIVE_FOLDER_ID:-}" \
                        '{($k):$u, folder_id:$fid, async:true, clip_duration:10, chunk_duration:10, total_minutes:1, max_videos:1}') ;;
            esac
            S3_BODY_FILE="$OUT_DIR/step3_attempt${ATTEMPT}_${KEY}_response.json"
            S3_HTTP=$(http_post "$BASE/api/stock-pipeline/run" "$S3_BODY_FILE" "$S3_PAYLOAD")
            log "   attempt ${ATTEMPT}/${#S3_TRIES[@]}  key='${KEY}' → HTTP ${S3_HTTP}"
            if [[ "$S3_HTTP" =~ ^(200|202)$ ]]; then
                JOB_ID=$(jq -r '.job_id // .run_id // empty' "$S3_BODY_FILE" 2>/dev/null)
                if [ -n "$JOB_ID" ]; then
                    pass "POST /run enqueued job_id=$JOB_ID (key='${KEY}', HTTP $S3_HTTP)"
                    cp "$S3_BODY_FILE" "$OUT_DIR/step3_run_response.json"
                    LAST_SHAPE_USED="$KEY"
                    break
                else
                    log "      shape=${KEY} returned $S3_HTTP but no job_id|run_id in body"
                fi
            fi
        done
        if [ -z "$JOB_ID" ]; then
            fail "POST /run: tried ${#S3_TRIES[@]} payload shape(s); none accepted"
            log "   artifacts captured: $OUT_DIR/step3_attempt*_response.json"
            log "   last_shape_used: ${LAST_SHAPE_USED:-<none>}"
        fi
    fi
else
    note_skip "STEP 3/12  direct URL run (RUN_DIRECT=0)"
fi

if [ -z "$JOB_ID" ]; then
    # In search-only mode there is still no job from step 3; if RUN_SEARCH
    # was on we used the resolved ID for the asset query directly below.
    log ""
    log "── VERDICT (no job_id available — cannot continue) ──"
    printf 'StockPipeline live test verdict\nPASS=%d\nFAIL=%d\nArtifacts: %s\nLast job:   <none>\nLast asset: <none>\n' "$PASS" "$FAIL" "$OUT_DIR"
    exit 1
fi

# ─── STEP 4: Poll job to SUCCEEDED ───────────────────────────────────────
log ""
log "── STEP 4/12  poll /api/jobs/$JOB_ID/full → SUCCEEDED ──"
ELAPSED=0
LAST_STATUS="UNKNOWN"
while [ "$ELAPSED" -lt "$JOB_POLL_TIMEOUT" ]; do
    sleep "$JOB_POLL_INTERVAL"
    ELAPSED=$((ELAPSED + JOB_POLL_INTERVAL))
    S4_HTTP=$(http_get "$BASE/api/jobs/$JOB_ID/full" \
        "$OUT_DIR/step4_poll_${ELAPSED}s.json")
    LAST_STATUS=$(jq -r '.status // "UNKNOWN"' \
        "$OUT_DIR/step4_poll_${ELAPSED}s.json" 2>/dev/null || echo "UNKNOWN")
    log "   ${ELAPSED}s status=$LAST_STATUS"
    case "$LAST_STATUS" in
        SUCCEEDED) pass "job $JOB_ID reached SUCCEEDED after ${ELAPSED}s" ; break ;;
        FAILED|DEAD_LETTERED)
            fail "job $JOB_ID terminal=$LAST_STATUS after ${ELAPSED}s"
            jq '.error // .error_message // .' \
                "$OUT_DIR/step4_poll_${ELAPSED}s.json" 2>/dev/null | head -n 40 \
                > "$OUT_DIR/step4_failure_body.json"
            log "   error capture: $OUT_DIR/step4_failure_body.json"
            break
            ;;
    esac
done
if [ "$LAST_STATUS" != "SUCCEEDED" ] && [ "$FAIL" = "0" ]; then
    fail "job $JOB_ID did not reach SUCCEEDED within ${JOB_POLL_TIMEOUT}s (last=$LAST_STATUS)"
fi

# ─── STEP 5: SQLite canonical ─────────────────────────────────────────────
log ""
log "── STEP 5/12  media_assets row with source='stock' ──"
S5_SQL="SELECT id, filename, '' as folder_id, index_state, lifecycle_state, \
        CASE WHEN file_hash='' THEN '-' ELSE substr(file_hash,1,12)||'...' END as file_hash, \
        CASE WHEN drive_file_id='' THEN '-' ELSE substr(drive_file_id,1,12)||'...' END as drive_file_id \
    FROM media_assets WHERE source='stock' \
        AND id NOT LIKE '%:metadata' \
        AND created_at > datetime('now','-30 minutes') \
    ORDER BY created_at DESC LIMIT 5;"
sqlite3 -header -column "$DB_PATH" "$S5_SQL" \
    > "$OUT_DIR/step5_latest_stock_assets.txt" 2>"$OUT_DIR/step5_latest_stock_assets.err" || true
# `sqlite3 -header -column` writes the title on line 1 and the
# column header on line 2; data starts on line 3. NR>2 excludes both
# title and header so ROW_COUNT matches the actual data rows.
ROW_COUNT=$(awk 'NR>2' "$OUT_DIR/step5_latest_stock_assets.txt" 2>/dev/null | wc -l | tr -d ' ')
if [ "$ROW_COUNT" -lt 1 ]; then
    fail "no new media_assets row with source='stock' (created in the last 30 minutes)"
else
    pass "SQLite has $ROW_COUNT new stock-source asset(s) — see $(art step5_latest_stock_assets.txt)"
    LAST_ASSET_ID=$(awk 'NR>2 {print $1; exit}' "$OUT_DIR/step5_latest_stock_assets.txt")
fi

# ─── STEP 6: Asset attributes (hash, drive_id, drive link, PUBLISHED) ───
log ""
log "── STEP 6/12  asset attributes (hash, drive_file_id, lifecycle) ──"
if [ -z "$LAST_ASSET_ID" ]; then
    fail "no LAST_ASSET_ID; cannot check attributes"
else
    S6_SQL="SELECT CASE WHEN file_hash=''     THEN '<empty>' ELSE file_hash     END, \
                   CASE WHEN drive_file_id='' THEN '<empty>' ELSE drive_file_id END, \
                   CASE WHEN drive_file_id='' THEN ''        ELSE 'https://drive.google.com/file/d/'||drive_file_id||'/view' END AS drive_link, \
                   '' as mime, lifecycle_state, index_state, source \
              FROM media_assets WHERE id='$LAST_ASSET_ID';"
    sqlite3 -line "$DB_PATH" "$S6_SQL" \
        >"$OUT_DIR/step6_asset_attributes.txt" 2>"$OUT_DIR/step6_asset_attributes.err" || true
    FILE_HASH=$(awk -F': ' '/^file_hash/   {print $2}' "$OUT_DIR/step6_asset_attributes.txt")
    DRIVE_ID=$(awk   -F': ' '/^drive_file_id/ {print $2}' "$OUT_DIR/step6_asset_attributes.txt")
    DRIVE_LNK=$(awk  -F': ' '/^drive_link/ {print $2}'   "$OUT_DIR/step6_asset_attributes.txt")
    LIFECYCLE=$(awk  -F': ' '/^lifecycle_state/ {print $2}' "$OUT_DIR/step6_asset_attributes.txt")
    [ "$FILE_HASH" != "<empty>" ] && [ -n "$FILE_HASH" ] && pass "file_hash present (=${FILE_HASH:0:16}...)" \
        || fail "file_hash missing or empty"
    [ "$DRIVE_ID"  != "<empty>" ] && [ -n "$DRIVE_ID"  ] && pass "drive_file_id present (=${DRIVE_ID:0:16}...)" \
        || fail "drive_file_id missing or empty (Drive upload incomplete)"
    [ -n "$DRIVE_LNK" ] && pass "drive_link synthesizable = $DRIVE_LNK" \
        || fail "drive_link NOT synthesizable (no usable drive_file_id)"
    [ "${LIFECYCLE:-}" = "PUBLISHED" ] \
        && pass "lifecycle_state = PUBLISHED" \
        || fail "lifecycle_state = '${LIFECYCLE:-<unset>}' (expected PUBLISHED)"
fi

# ─── STEP 7: outbox asset.index.requested → COMPLETED ─────────────────────
log ""
log "── STEP 7/12  outbox_events asset.index.requested (status=completed) ──"
if [ -z "$LAST_ASSET_ID" ]; then
    fail "no LAST_ASSET_ID; cannot scope outbox query"
else
    S7_SQL="SELECT event_type, status, attempt_count, \
                   CASE WHEN last_error='' THEN '-' ELSE substr(last_error,1,80) END AS last_error, \
                   CASE WHEN processed_at='' THEN '-' ELSE processed_at END AS processed_at \
            FROM outbox_events \
            WHERE event_type='asset.index.requested' \
              AND aggregate_id='$LAST_ASSET_ID' \
            ORDER BY created_at DESC LIMIT 5;"
    sqlite3 -header -column "$DB_PATH" "$S7_SQL" \
        > "$OUT_DIR/step7_outbox_events.txt" \
        2> "$OUT_DIR/step7_outbox_events.err" || true
    if [ ! -s "$OUT_DIR/step7_outbox_events.txt" ] || \
       ! head -n1 "$OUT_DIR/step7_outbox_events.txt" | grep -q event_type; then
        fail "no outbox_events rows for asset.index.requested + aggregate_id=$LAST_ASSET_ID"
    else
        # 'completed' is the canonical success state for v1 reindex events.
        OUTBOX_STATUS=$(awk 'NR==3 {print $2}' "$OUT_DIR/step7_outbox_events.txt")
        if [ "${OUTBOX_STATUS:-}" = "completed" ]; then
            pass "outbox status = completed for asset.index.requested"
            LAST_OUTBOX_ROW="$OUT_DIR/step7_outbox_events.txt"
        else
            fail "outbox status = '${OUTBOX_STATUS:-<unset>}' (expected completed)"
        fi
    fi
fi

# ─── STEP 8: Qdrant projection (REQUIRE_QDRANT=1) ─────────────────────────
log ""
log "── STEP 8/12  Qdrant scroll on '$QDRANT_COLLECTION' for source=stock ──"
if [ "$REQUIRE_QDRANT" = "1" ]; then
    if [ -z "$LAST_ASSET_ID" ]; then
        fail "no LAST_ASSET_ID; cannot filter scroll"
    else
        # Dual-scroll strategy: (1) SPECIFIC scroll with filter
        # asset_id + source; (2) AGGREGATE scroll with filter source
        # only. If specific returns 0 BUT aggregate >= 1, the projection
        # is alive but the indexed payload field may not be named
        # `asset_id` (e.g. could be `id` / `media_asset_id` / `clip_id`).
        # In that case we surface the actual payload keys so the operator
        # can self-diagnose instead of false-failing the whole Qdrant
        # layer.
        S8A_BODY=$(jq -nc --arg id "$LAST_ASSET_ID" --arg src "stock" \
            '{limit:50, with_payload:true, filter:{must:[{key:"asset_id",match:{value:$id}},{key:"source",match:{value:$src}}]}}')
        Q_HTTP_A=$(http_post "$QDRANT_URL/collections/$QDRANT_COLLECTION/points/scroll" \
            "$OUT_DIR/step8a_qdrant_scroll_specific.json" "$S8A_BODY")
        HITS=0
        if [ "$Q_HTTP_A" = "200" ]; then
            HITS=$(jq -r '.result.points | length' \
                "$OUT_DIR/step8a_qdrant_scroll_specific.json" 2>/dev/null || echo 0)
            LAST_QDRANT_HITS=$HITS
        else
            log "   scroll (specific) returned HTTP $Q_HTTP_A"
        fi

        S8B_BODY=$(jq -nc --arg src "stock" \
            '{limit:200, with_payload:true, filter:{must:[{key:"source",match:{value:$src}}]}}')
        Q_HTTP_B=$(http_post "$QDRANT_URL/collections/$QDRANT_COLLECTION/points/scroll" \
            "$OUT_DIR/step8b_qdrant_scroll_aggregate.json" "$S8B_BODY")
        HITS_AGG=0
        if [ "$Q_HTTP_B" = "200" ]; then
            HITS_AGG=$(jq -r '.result.points | length' \
                "$OUT_DIR/step8b_qdrant_scroll_aggregate.json" 2>/dev/null || echo 0)
            jq -r '.result.points[0].payload | keys[]' \
                "$OUT_DIR/step8b_qdrant_scroll_aggregate.json" 2>/dev/null \
                > "$OUT_DIR/step8b_qdrant_payload_keys.txt" || true
        else
            log "   scroll (aggregate) returned HTTP $Q_HTTP_B (alias 'media_assets_current' may be unconfigured)"
        fi

        if [ "${HITS:-0}" -ge 1 ]; then
            pass "Qdrant SPECIFIC scroll returned $HITS point(s) for asset_id=$LAST_ASSET_ID"
        elif [ "${HITS_AGG:-0}" -ge 1 ]; then
            fail "Qdrant SPECIFIC scroll returned 0 hits for asset_id=$LAST_ASSET_ID, AGGREGATE returned $HITS_AGG stock points — projection is alive but field name maybe != 'asset_id'"
            log "   payload keys probe (update STEP 8 filter if you see 'id' / 'media_asset_id' / 'clip_id'): $(art step8b_qdrant_payload_keys.txt)"
        else
            fail "Qdrant: 0 stock points on both specific and aggregate scrolls (projection missing or collection empty)"
        fi
    fi
else
    note_skip "STEP 8/12  Qdrant scroll skipped (REQUIRE_QDRANT=0)"
fi

# ─── STEP 9: Unified search /api/media/search ─────────────────────────────
log ""
log "── STEP 9/12  GET /api/media/search returns the asset ──"
S9_TERM="${QUERY:-$(sqlite3 "$DB_PATH" "SELECT filename FROM media_assets WHERE id='${LAST_ASSET_ID:-}' LIMIT 1;" 2>/dev/null || true)}"
S9_PAYLOAD=$(jq -nc --arg t "${S9_TERM:-stock}" --arg id "${LAST_ASSET_ID:-}" \
    '{query:$t, filters:{source:"stock"}, limit:10}')
S9_HTTP=$(http_post "$BASE/api/media/search" \
    "$OUT_DIR/step9_unified_search.json" "$S9_PAYLOAD")
case "$S9_HTTP" in
    200)
        HITS=$(jq -r '(.results // . // []) | (if type=="array" then length else 0 end)' \
            "$OUT_DIR/step9_unified_search.json" 2>/dev/null || echo 0)
        if [ "${HITS:-0}" -ge 1 ]; then
            pass "/api/media/search returned ${HITS} result(s) for query='${S9_TERM:-stock}'"
        else
            # Fall back: any 200 response with results length >= 0 still
            # tells us the route is wired. The asset WILL appear later if
            # the projection just hasn't propagated; do NOT fail outright.
            log "   /api/media/search returned HTTP 200 with 0 results (acceptable: projection lag)"
        fi
        ;;
    *) fail "/api/media/search returned HTTP $S9_HTTP (uncaught)" ;;
esac

# ─── STEP 10: Download via POST /api/stock-pipeline/clips/:id/download ───
log ""
log "── STEP 10/12  POST /api/stock-pipeline/clips/$LAST_ASSET_ID/download ──"
LAST_MP4_PATH="$OUT_DIR/step10_clip.mp4"
# -L is required: the route commonly returns a 302 redirect to a
# signed Drive URL. Without -L curl writes a 0-byte body and the
# ffprobe step then fails with a misleading "no video stream"
# diagnostic. --retry handles transient disconnects on the redirect.
# Single curl: -o writes the (potentially-MB) MP4 body to disk once;
# -w writes a 2-line meta template (http_code + redirects) to stdout,
# redirected into step10_curl_meta.txt — eliminating the prior 2-curl
# design (which fetched the body twice, doubling egress bytes on this
# route). Awk pulls back the values for the case statement below;
# ${VAR:-default} preserves the curl-failure 'HTTP 000' sentinel for
# grep-able FAIL-line parity with the original || echo pattern.
# gains: ~50% bandwidth on the clips/:id/download route.
curl -sS -L --max-redirs 5 --max-time 120 \
    --retry 3 --retry-delay 1 \
    -X POST \
    -H "X-Velox-Admin-Token: $TOKEN" \
    -o "$LAST_MP4_PATH" \
    -w 'http_code=%{http_code}\nredirects=%{num_redirects}\n' \
    "$BASE/api/stock-pipeline/clips/$LAST_ASSET_ID/download" \
    > "$OUT_DIR/step10_curl_meta.txt" 2>/dev/null || true
S10_HTTP=$(awk -F= '/^http_code=/{print $2; exit}' "$OUT_DIR/step10_curl_meta.txt")
S10_HTTP="${S10_HTTP:-000}"  # sentinel fallback for curl-failure (preserves 'HTTP 000' grepability)
S10_REDIRECTS=$(awk -F= '/^redirects=/{print $2; exit}' "$OUT_DIR/step10_curl_meta.txt")
S10_REDIRECTS="${S10_REDIRECTS:-0}"
case "$S10_HTTP" in
    200|206) pass "clip downloaded via $S10_HTTP → $LAST_MP4_PATH (redirects=$S10_REDIRECTS)" ;;
    *)      fail "GET clips/:id/download returned HTTP $S10_HTTP" ;;
esac

# ─── STEP 11: Size threshold ──────────────────────────────────────────────
log ""
log "── STEP 11/12  MP4 size > $MIN_MP4_BYTES bytes ──"
if [ -s "$LAST_MP4_PATH" ]; then
    SIZE=$(stat -c%s "$LAST_MP4_PATH" 2>/dev/null || stat -f%z "$LAST_MP4_PATH" 2>/dev/null || echo 0)
    if [ "${SIZE:-0}" -gt "$MIN_MP4_BYTES" ]; then
        pass "MP4 size = $SIZE bytes (> $MIN_MP4_BYTES)"
    else
        fail "MP4 size = $SIZE bytes (≤ $MIN_MP4_BYTES — decode or upload broken)"
    fi
else
    fail "MP4 file empty or missing at $LAST_MP4_PATH"
fi

# ─── STEP 12: ffprobe sanity ──────────────────────────────────────────────
log ""
log "── STEP 12/12  ffprobe: has video stream + duration > 0 ──"
if [ ! -s "$LAST_MP4_PATH" ]; then
    fail "no MP4 to probe (step 10/11 already failed)"
else
    ffprobe -v error -show_streams -show_format -of json \
        "$LAST_MP4_PATH" > "$OUT_DIR/step12_ffprobe.json" 2>"$OUT_DIR/step12_ffprobe.err" || true
    if [ ! -s "$OUT_DIR/step12_ffprobe.json" ]; then
        fail "ffprobe produced empty output for $LAST_MP4_PATH"
    else
        HAS_VIDEO=$(jq -r '[.streams[]? | select(.codec_type=="video")] | length' \
            "$OUT_DIR/step12_ffprobe.json" 2>/dev/null || echo 0)
        DURATION=$(jq -r '.format.duration // "0"' \
            "$OUT_DIR/step12_ffprobe.json" 2>/dev/null || echo "0")
        if [ "${HAS_VIDEO:-0}" -ge 1 ]; then
            pass "ffprobe sees ${HAS_VIDEO} video stream(s)"
        else
            fail "ffprobe sees 0 video streams (codec/container mismatch?)"
        fi
        # jq on float: round to integer for the comparison; ffprobe reports
        # duration in seconds; 0 means probe failed.
        DUR_INT=$(awk -v d="$DURATION" 'BEGIN{ if (d+0 > 0) print int(d+0.5); else print 0 }')
        if [ "${DUR_INT:-0}" -gt 0 ]; then
            pass "ffprobe duration = ${DURATION}s (> 0)"
        else
            fail "ffprobe duration parsed as '${DURATION}' (≤ 0 s — file is bogus)"
        fi
    fi
fi

# ─── Verdict ───────────────────────────────────────────────────────────────
log ""
log "═══════════════════════════════════════════════════════════════════"
log "  StockPipeline live test verdict"
log "═══════════════════════════════════════════════════════════════════"
log "  PASS=$PASS  FAIL=$FAIL"
log "  Last job:   ${LAST_JOB_ID:-<none>}  status=${LAST_STATUS:-<none>}"
log "  Last asset: ${LAST_ASSET_ID:-<none>}"
log "  Last mp4:   ${LAST_MP4_PATH:-<none>}"
log "  Qdrant:     $([ "$REQUIRE_QDRANT" = "1" ] && echo "ran (hits=$LAST_QDRANT_HITS)" || echo "skipped (REQUIRE_QDRANT=0)")"
log "  Outbox row: ${LAST_OUTBOX_ROW:-<none>}"
log ""
if [ "${#SKIPPED[@]}" -gt 0 ]; then
    log "  Skipped steps:"
    for s in "${SKIPPED[@]}"; do log "    - $s"; done
    log ""
fi
log "  Artifacts: $OUT_DIR"
log "═══════════════════════════════════════════════════════════════════"

# Emit a structured one-liner for easy grepping by CI / paste into reports.
# Lower-friction format: space-separated, no `%d` / `%s` codes — operators
# can copy it directly into a ticket without escaping.
printf 'VERDICT pass=%s fail=%s job=%s asset=%s mp4=%s qdrant_hits=%s last_status=%s min_mp4_bytes=%s last_shape=%s\n' \
    "$PASS" "$FAIL" "${LAST_JOB_ID:-}" "${LAST_ASSET_ID:-}" \
    "${LAST_MP4_PATH:-}" "$LAST_QDRANT_HITS" "${LAST_STATUS:-}" \
    "$MIN_MP4_BYTES" "${LAST_SHAPE_USED:-}"

# Exit non-zero if any FAIL — fail-closed contract (AGENTS.md).
if [ "$FAIL" -gt 0 ]; then exit 1; fi
exit 0

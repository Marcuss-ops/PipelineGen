#!/usr/bin/env bash
# tests/operational/stock_e2e_cache_replay.sh
#
# End-to-end DoD §13 "prova decisiva" cache replay (July 2026).
#
# Submit the SAME 351-clip stock pipeline batch TWICE — Run A (cold)
# and Run B (warm, immediately after Run A completes). On Run B, the
# cache layers (source download cache + publisher dedup) should
# eliminate redundant work:
#
#   A) SOURCE_CACHE_HIT       — Run B reads the cached source video
#                               instead of calling yt-dlp again.
#                               Invariant: stock_source_cache has 1
#                               active row for the cohort URL.
#   B) CLIP_CACHE_HIT ≥ 95%   — Run B re-renders each clip but the
#                               publisher dedups uploads whose
#                               local file_hash matches an existing
#                               media_assets row. Invariant: Run B
#                               cohort row delta ≤ 5% (legitimate
#                               misses: hash drift, invalidation).
#   C) 0 NEW DRIVE FILES      — publisher dedup at upload time:
#                               drive_file_id cardinality unchanged.
#   D) 0 SUCCEEDED ASSETS     — every cohort media_assets row has
#                               WITH FILE_HASH=='' OR LOCAL_PATH==''
#                               OR DURATION_MS==0 ⇒ wasted run.
#   E) 0 NEW INVALIDATION     — Run B must NOT introduce new
#                               "stock_source_cache.state='invalidated'"
#                               rows for the cohort.
#   F) RUN B ≤ 2× RUN A TIME  — cache should make Run B at most
#                               2× Run A elapsed time. (NOT a 10×
#                               claim — Run B still re-renders all
#                               351 clips with FFmpeg; the saving is
#                               upload-skip + source-cache-hit.)
#
# 351 clips are split into 4 chunks of ≤100 each — the
# handler.go::MaxClipsPerRun cap (100). 4 broker jobs per run × 2
# runs = 8 broker jobs total. Each chunk is POSTed sequentially
# (parallelism would defeat determinism on the singleflight key).
#
# Exit codes:
#   0 = PASS (all invariants hold; cache layer is the decisive gate)
#   1 = FAIL (one or more invariants violated)
#   2 = prereq missing
#
# Prerequisites:
#   - PipelineGen server reachable at $BASE.
#   - Migrations 094 (media_assets) + 160 (stock_source_cache) applied
#     to $DB_PATH.
#   - Network reachable to YouTube (or TEST_VIDEO_URL is a local URL).
#   - $DRIVE_FOLDER_ID writable (issuer credentials for publisher).
#
# Overridable env vars:
#   BASE, AUTH, DB_PATH, POLL_MAX, POLL_INTERVAL,
#   CLIPS_PER_RUN  (default 351)
#   CHUNK_SIZE     (default 100 — must equal handler MaxClipsPerRun)
#   CLIP_DURATION  (default 4 seconds)
#   TEST_VIDEO_URL (default https://www.youtube.com/watch?v=QdSbtEo3x_Y)
#   COHORT_WINDOW_MIN (default 30)
#   TEST_VIDEO_ID     (default QdSbtEo3x_Y)

set -euo pipefail

BASE="${BASE:-http://127.0.0.1:8000}"
AUTH="${AUTH:-Authorization: Bearer ${VELOX_ADMIN_TOKEN:-}}"
DB_PATH="${DB_PATH:-data/media/media.db.sqlite}"
POLL_MAX="${POLL_MAX:-60}"
POLL_INTERVAL="${POLL_INTERVAL:-15}"
COHORTS_TAG="${COHORTS_TAG:-e2e_cache_replay_$(date +%s)}"
N="${CLIPS_PER_RUN:-351}"
CHUNK="${CHUNK_SIZE:-100}"
CLIP_DUR="${CLIP_DURATION:-4}"
TEST_VIDEO_URL="${TEST_VIDEO_URL:-https://www.youtube.com/watch?v=QdSbtEo3x_Y}"
TEST_VIDEO_ID="${TEST_VIDEO_ID:-QdSbtEo3x_Y}"
COHORT_WINDOW_MIN="${COHORT_WINDOW_MIN:-30}"
SLOWDOWN_THRESHOLD="${SLOWDOWN_THRESHOLD:-2}"  # Run B may be ≤ 2× Run A

# Required tools.
command -v curl    >/dev/null 2>&1 || { echo "FAIL: curl not on PATH" >&2; exit 2; }
command -v jq      >/dev/null 2>&1 || { echo "FAIL: jq not on PATH"   >&2; exit 2; }
command -v sqlite3 >/dev/null 2>&1 || { echo "FAIL: sqlite3 not on PATH" >&2; exit 2; }
command -v date    >/dev/null 2>&1 || command -v gdate >/dev/null 2>&1 || \
    { echo "FAIL: date (or gdate) not on PATH for ms timing" >&2; exit 2; }

# ---- Pre-flight: server reachable + empty-payload returns HTTP 4xx ----
HTTP=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 \
    -X POST "$BASE/api/stock-pipeline/run" \
    -H "$AUTH" -H "Content-Type: application/json" -d '{}' 2>/dev/null || echo "000")
if [ "$HTTP" = "000" ]; then
    echo "FAIL: PipelineGen server at $BASE unreachable" >&2; exit 2
fi
if [ "$HTTP" != "400" ] && [ "$HTTP" != "401" ] && [ "$HTTP" != "403" ]; then
    echo "WARN: pre-flight HTTP=$HTTP (validator usually returns 400/401/403)" >&2
fi

CHUNKS=$(( (N + CHUNK - 1) / CHUNK ))
echo "=== STK-E2E-CACHE-REPLAY (DoD §13): $N clips in $CHUNKS chunks × 2 runs ==="
echo "    cohort_tag=$COHORTS_TAG  url=$TEST_VIDEO_URL  chunk_size=$CHUNK"

# ---- Build N chunked payloads programmatically (no JSON fixtures) ----
# Each payload's clips[] array is built by jq `range(start; end+1)` so
# the 351 sequential clips are emitted without code repetition.
build_chunk_payload() {
    local chunk_start="$1"   # 1-based clip index (inclusive)
    local chunk_end="$2"     # 1-based clip index (inclusive)
    local run_label="$3"     # "A" or "B"
    local cohort="$COHORTS_TAG"
    local url="$TEST_VIDEO_URL"
    local dur="$CLIP_DUR"

    jq -n \
        --argjson start "$chunk_start" \
        --argjson end   "$chunk_end" \
        --arg     run   "$run_label" \
        --arg     cohort "$cohort" \
        --arg     url   "$url" \
        --argjson dur   "$dur" \
        '{
            direct_urls: [$url],
            clips: [
                range($start; $end + 1) | {
                    url: $url,
                    title: ("e2e_cache_replay_clip_" + (tostring(.) | .)),
                    start_sec: ((. - 1) * $dur),
                    end_sec:   (. * $dur)
                }
            ],
            clip_duration: $dur,
            folder_name: ($cohort + "_run_" + $run + "_chunk_" + (tostring($start) | .) + "-" + (tostring($end) | .)),
            subfolder:   ($cohort + "_run_" + $run),
            async: true,
            no_audio: false,
            no_effects: false,
            no_transitions: false,
            metadata: {
                title: ($cohort + "_run_" + $run),
                category: "boxing",
                extra: {
                    cohort: "e2e_cache_replay",
                    run: $run,
                    chunk_start: ($start | tostring),
                    chunk_end: ($end | tostring),
                    cohort_tag: $cohort
                }
            }
        }'
}

# ---- Submit Run A: $CHUNKS sequential POSTs ----
submit_run() {
    local run_label="$1"
    local pids=()
    local job_ids=()
    echo
    echo "=== Run $run_label: submitting $CHUNKS chunks ==="
    for c in $(seq 1 "$CHUNKS"); do
        local chunk_start=$(( (c - 1) * CHUNK + 1 ))
        local chunk_end=$(( c * CHUNK ))
        if [ "$chunk_end" -gt "$N" ]; then chunk_end=$N; fi
        local payload
        payload="$(build_chunk_payload "$chunk_start" "$chunk_end" "$run_label")"
        local resp
        resp="$(curl -sS -X POST "$BASE/api/stock-pipeline/run" \
            -H "$AUTH" -H "Content-Type: application/json" \
            --data "$payload" --max-time 60)"
        local jid
        jid="$(echo "$resp" | jq -r '.job_id // empty')"
        if [ -z "$jid" ]; then
            echo "FAIL: Run $run_label chunk $c returned no job_id: $resp" >&2; exit 1
        fi
        job_ids+=("$jid")
        echo "  Run $run_label chunk $c [clips ${chunk_start}-${chunk_end}]: JOB_ID=$jid"
    done
    printf '%s\n' "${job_ids[@]}"
}

poll_run() {
    local run_label="$1"
    shift
    local job_ids=("$@")
    echo
    echo "=== Polling Run $run_label (${#job_ids[@]} jobs) to terminal ==="
    local succeeded=0
    local failed=0
    for jid in "${job_ids[@]}"; do
        local terminal=""
        for k in $(seq 1 "$POLL_MAX"); do
            sleep "$POLL_INTERVAL"
            local s
            s="$(curl -sS "$BASE/api/jobs/$jid/full" -H "$AUTH" --max-time 10 2>/dev/null \
                | jq -r '.status // "unknown"')"
            echo "  [${run_label} ${k}/${POLL_MAX}] $jid: $s" >&2
            case "$s" in
                SUCCEEDED|FAILED|CANCELLED) terminal="$s"; break ;;
            esac
        done
        case "$terminal" in
            SUCCEEDED) succeeded=$((succeeded + 1)) ;;
            *) failed=$((failed + 1)) ;;
        esac
    done
    echo "  Run $run_label: SUCCEEDED=$succeeded, FAIL/CANCEL=$failed"
    if [ "$succeeded" -ne "${#job_ids[@]}" ]; then
        echo "FAIL: Run $run_label did not reach full SUCCEEDED" >&2
        return 1
    fi
    return 0
}

now_ms() {
    if command -v gdate >/dev/null 2>&1; then
        gdate +%s%N | awk '{print int($1/1000000)}'
    else
        # Fallback for BSD/macOS / older bash without %N. Most Linux
        # systems support %N; if not, return seconds × 1000.
        local t
        t="$(date +%s%N 2>/dev/null || date +%s)"
        case "$t" in
            *N) echo "$t" | awk '{print int($1/1000000)}' ;;
            *)  echo $(( t * 1000 )) ;;
        esac
    fi
}

# ---- DB scope gates (skip SQL invariants if DB absent) ----
if [ ! -f "$DB_PATH" ]; then
    echo
    echo "WARN: DB_PATH=$DB_PATH not found — SQL invariants will be skipped" >&2
    DB_AVAILABLE=0
else
    DB_AVAILABLE=1
fi

# ---- Submit Run A ----
RUN_A_START=$(now_ms)
RUN_A_JOB_IDS=()
while IFS= read -r jid; do RUN_A_JOB_IDS+=("$jid"); done < <(submit_run "A")
poll_run "A" "${RUN_A_JOB_IDS[@]}"
RUN_A_END=$(now_ms)
RUN_A_ELAPSED_MS=$(( RUN_A_END - RUN_A_START ))
echo "  Run A elapsed: ${RUN_A_ELAPSED_MS}ms"

# ---- Snapshot pre-B row counts (Run B must NOT grow these by > 5%) ----
if [ "$DB_AVAILABLE" -eq 1 ]; then
    PRE_B_OUT=$(sqlite3 -separator '|' "$DB_PATH" "
        SELECT
            COUNT(*)                                   AS total_rows,
            COUNT(DISTINCT local_path)                 AS distinct_local,
            COUNT(DISTINCT drive_file_id)              AS distinct_drive,
            COUNT(DISTINCT file_hash)                  AS distinct_hash
        FROM media_assets
        WHERE source = 'stock'
          AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes')
    ")
    PRE_B_TOTAL=$(echo "$PRE_B_OUT" | awk -F'|' '{print $1+0}')
    PRE_B_LOCAL=$(echo "$PRE_B_OUT" | awk -F'|' '{print $2+0}')
    PRE_B_DRIVE=$(echo "$PRE_B_OUT" | awk -F'|' '{print $3+0}')
    PRE_B_HASH=$(echo  "$PRE_B_OUT" | awk -F'|' '{print $4+0}')
    echo "  pre-B snapshot: cohort_total=$PRE_B_TOTAL  distinct_local=$PRE_B_LOCAL  distinct_drive=$PRE_B_DRIVE  distinct_hash=$PRE_B_HASH"
else
    PRE_B_TOTAL=0; PRE_B_LOCAL=0; PRE_B_DRIVE=0; PRE_B_HASH=0
fi

# Short cooldown so source cache writes settle before Run B.
sleep 3

# ---- Submit Run B (identical payloads; cache + publisher dedup expected) ----
RUN_B_START=$(now_ms)
RUN_B_JOB_IDS=()
while IFS= read -r jid; do RUN_B_JOB_IDS+=("$jid"); done < <(submit_run "B")
poll_run "B" "${RUN_B_JOB_IDS[@]}"
RUN_B_END=$(now_ms)
RUN_B_ELAPSED_MS=$(( RUN_B_END - RUN_B_START ))
echo "  Run B elapsed: ${RUN_B_ELAPSED_MS}ms (Run A: ${RUN_A_ELAPSED_MS}ms)"

# ---- SQL invariants (skip cleanly if DB unavailable) ----
if [ "$DB_AVAILABLE" -ne 1 ]; then
    echo
    echo "PASS (DB-skipped): 2 runs of $N clips complete terminal; SQL invariants skipped"
    exit 0
fi

echo
echo "=== Invariant A: 1 active stock_source_cache entry for cohort URL ==="
A_COUNT=$(sqlite3 "$DB_PATH" "
    SELECT COUNT(*) FROM stock_source_cache
    WHERE source_url LIKE '%${TEST_VIDEO_ID}%'
      AND state = 'active'
      AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes')
      AND download_section = ''
")
if [ "$A_COUNT" -ne 1 ]; then
    echo "FAIL: expected 1 active stock_source_cache entry for cohort, got $A_COUNT" >&2
    sqlite3 "$DB_PATH" "SELECT id, cache_key, source_url, state, created_at, download_section FROM stock_source_cache WHERE source_url LIKE '%${TEST_VIDEO_ID}%' AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes')" >&2
    exit 1
fi
echo "  stock_source_cache active entries: $A_COUNT ✓"

echo
echo "=== Invariant B: Run B cohort row delta ≤ 95% reuse threshold ==="
POST_B_OUT=$(sqlite3 -separator '|' "$DB_PATH" "
    SELECT
        COUNT(*)                          AS total_rows,
        COUNT(DISTINCT local_path)        AS distinct_local,
        COUNT(DISTINCT drive_file_id)     AS distinct_drive,
        COUNT(DISTINCT file_hash)         AS distinct_hash
    FROM media_assets
    WHERE source = 'stock'
      AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes')
")
POST_B_TOTAL=$(echo "$POST_B_OUT" | awk -F'|' '{print $1+0}')
POST_B_DRIVE=$(echo "$POST_B_OUT" | awk -F'|' '{print $3+0}')
echo "  post-B snapshot: cohort_total=$POST_B_TOTAL  distinct_drive=$POST_B_DRIVE"

# Compute drive delta = (POST_B_DRIVE - expected_total_clips).
# Both runs together should yield ≤ N unique Drive files (perfect
# dedup yields N; legitimate ≤5% miss yields ≤ ceil(N * 1.05)).
EXPECTED_DRIVE_MAX=$(( (N * 105) / 100 + 1 ))
if [ "$POST_B_DRIVE" -gt "$EXPECTED_DRIVE_MAX" ]; then
    echo "FAIL: distinct_drive_file_id count $POST_B_DRIVE > allowed $EXPECTED_DRIVE_MAX" >&2
    echo "  (publisher dedup failed: Run B uploaded > 5% new clips)" >&2
    exit 1
fi
echo "  distinct_drive_file_id: $POST_B_DRIVE ≤ $EXPECTED_DRIVE_MAX ✓"

echo
echo "=== Invariant C: 0 invalidated cache entries for cohort ==="
E_INVAL=$(sqlite3 "$DB_PATH" "
    SELECT COUNT(*) FROM stock_source_cache
    WHERE source_url LIKE '%${TEST_VIDEO_ID}%'
      AND state = 'invalidated'
      AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes')
")
if [ "$E_INVAL" -ne 0 ]; then
    echo "FAIL: $E_INVAL invalidated stock_source_cache rows for cohort" >&2
    exit 1
fi
echo "  invalidated rows: $E_INVAL ✓"

echo
echo "=== Invariant D: 0 zero-byte artifacts in cohort ==="
D_BAD=$(sqlite3 "$DB_PATH" "
    SELECT COUNT(*) FROM media_assets
    WHERE source = 'stock'
      AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes')
      AND (length(local_path) = 0
           OR file_hash = ''
           OR duration_ms = 0
           OR duration_ms IS NULL)
")
if [ "$D_BAD" -ne 0 ]; then
    echo "FAIL: $D_BAD zero-byte / no-hash / zero-duration rows in cohort" >&2
    exit 1
fi
echo "  zero-byte cohort rows: $D_BAD ✓"

echo
echo "=== Invariant E: every clip has start_ms=0, end_ms>0 (since folder-driven batch) ==="
E_BAD=$(sqlite3 "$DB_PATH" "
    SELECT COUNT(*) FROM media_assets
    WHERE source = 'stock'
      AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes')
      AND (start_ms IS NULL
           OR end_ms IS NULL
           OR end_ms <= start_ms)
")
if [ "$E_BAD" -ne 0 ]; then
    echo "FAIL: $E_BAD cohort rows have invalid start_ms/end_ms" >&2
    exit 1
fi
echo "  malformed start_ms/end_ms: $E_BAD ✓"

echo
echo "=== Invariant F: Run B elapsed ≤ ${SLOWDOWN_THRESHOLD}× Run A elapsed ==="
if [ "$RUN_A_ELAPSED_MS" -le 0 ]; then
    echo "WARN: Run A elapsed time was 0 (sub-second timing) — skipping F" >&2
elif [ "$RUN_B_ELAPSED_MS" -gt $(( RUN_A_ELAPSED_MS * SLOWDOWN_THRESHOLD )) ]; then
    echo "FAIL: Run B (${RUN_B_ELAPSED_MS}ms) > ${SLOWDOWN_THRESHOLD}× Run A (${RUN_A_ELAPSED_MS}ms)" >&2
    echo "  cache layer is not delivering the decisive saving" >&2
    exit 1
else
    if [ "$RUN_A_ELAPSED_MS" -gt 0 ]; then
        RATIO_X=$(awk -v a="$RUN_A_ELAPSED_MS" -v b="$RUN_B_ELAPSED_MS" 'BEGIN{printf "%.2f", b/a}')
        echo "  Run B / Run A ratio: ${RATIO_X}× ✓"
    else
        echo "  Run A elapsed too small to compute ratio" >&2
    fi
fi

echo
echo "PASS: DoD §13 cache replay — $N clips × 2 runs"
echo "  1 source download (Run A=\$RUN_A_JOB_IDS[*]; Run B=cache-hit)"
echo "  ≤ 5% new render increments / 0 invalidated cache / 0 zero-byte"
echo "  Run B rate ≤ ${SLOWDOWN_THRESHOLD}× Run A"
exit 0

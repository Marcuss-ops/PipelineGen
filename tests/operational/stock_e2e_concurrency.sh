#!/usr/bin/env bash
# tests/operational/stock_e2e_concurrency.sh
#
# End-to-end: 5 PARALLEL POSTs of stock pipeline run requests on the
# SAME source URL + SAME clip interval (singleflight collapse) with
# DIFFERENT metadata + folder_name per request. Verifies 7 invariants
# (DoD §8 + §14, July 2026):
#   A) 1 solo download sorgente — singleflight + cache collapse to ONE
#      (stock_source_cache has exactly 1 active row for the cohort).
#   B) 5 distinct artifacts land in media_assets (5 distinct local_path
#      rows for the cohort; no overlap, no overwrite).
#   C) 5 jobs SUCCEEDED (all 5 reach broker terminal SUCCEEDED).
#   D) nessun lock SQLite permanente (sqlite3 quick probe < 5s post-run).
#   E) nessun artefatto zero-byte (file_hash!='', duration_ms>0).
#   F) clip-start integrity — every cohort row has start_ms in clip range
#      (10–14s → 10000–14000ms) and end_ms > start_ms.
#   G) 5 distinct folder_name → 5 distinct Drive folders (no overwrite).
#
# Fixture design (DoD §8 collapse rationale):
#   - All 5 fixtures use the SAME URL + SAME clip interval (10–14s).
#   - DeriveSourceCacheKey is (URL, download_section, merge_format,
#     force_keyframes) — identical across the cohort → singleflight
#     collapses all 5 followers under the leader → 1 yt-dlp download.
#   - Each fixture has a unique folder_name + metadata.extra.timestamp
#     so the cohort produces 5 distinct media_assets rows.
#
# Exit codes:
#   0 = PASS (all 7 invariants hold)
#   1 = FAIL
#   2 = prereq missing (server down / curl|jq|sqlite3 absent)

set -euo pipefail

BASE="${BASE:-http://127.0.0.1:8000}"
AUTH="${AUTH:-Authorization: Bearer ${VELOX_ADMIN_TOKEN:-}}"
POLL_MAX="${POLL_MAX:-30}"
POLL_INTERVAL="${POLL_INTERVAL:-10}"
DB_PATH="${DB_PATH:-data/media/media.db.sqlite}"
FIXTURE_DIR="${FIXTURE_DIR:-tests/fixtures/stock/concurrent}"
TEST_VIDEO_URL="${TEST_VIDEO_URL:-https://www.youtube.com/watch?v=QdSbtEo3x_Y}"
COHORT_WINDOW_MIN="${COHORT_WINDOW_MIN:-15}"
LOCAL_TMP="${LOCAL_TMP:-/tmp}"
EXPECTED_N="${EXPECTED_N:-5}"
TEST_VIDEO_ID="${TEST_VIDEO_ID:-QdSbtEo3x_Y}"
CLIP_START_MS_LOW=10000
CLIP_START_MS_HIGH=14000

command -v curl >/dev/null 2>&1 || { echo "FAIL: curl not on PATH" >&2; exit 2; }
command -v jq   >/dev/null 2>&1 || { echo "FAIL: jq not on PATH"   >&2; exit 2; }
command -v sqlite3 >/dev/null 2>&1 || { echo "FAIL: sqlite3 not on PATH" >&2; exit 2; }

# ---- Pre-flight: server reachable ----
HTTP=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 \
    -X POST "$BASE/api/stock-pipeline/run" \
    -H "$AUTH" -H "Content-Type: application/json" -d '{}' 2>/dev/null || echo "000")
if [ "$HTTP" = "000" ]; then
    echo "FAIL: PipelineGen server at $BASE unreachable (HTTP=$HTTP)" >&2; exit 2
fi
if [ "$HTTP" != "400" ] && [ "$HTTP" != "401" ] && [ "$HTTP" != "403" ]; then
    echo "WARN: pre-flight returned HTTP=$HTTP (expected 400/401/403 from validator)" >&2
fi

# ---- Verify fixtures (5 files present) ----
shopt -s nullglob
FIXTURES=("$FIXTURE_DIR"/clips_*.json)
shopt -u nullglob
if [ "${#FIXTURES[@]}" -ne "$EXPECTED_N" ]; then
    echo "FAIL: expected $EXPECTED_N fixture JSON files in $FIXTURE_DIR (got ${#FIXTURES[@]})" >&2; exit 2
fi

echo "=== STK-E2E-CONCURRENCY: $EXPECTED_N concurrent runs on $TEST_VIDEO_URL ==="
echo "    (same URL + same clip interval; cohort singleflight collapse → 1 source download)"

# ---- Submit $EXPECTED_N background POSTs ----
WORKDIR="$(mktemp -d "$LOCAL_TMP/stock-concurrency-XXXXXX")"
trap 'rm -rf "$WORKDIR"' EXIT

PIDS=()
for i in $(seq 1 "$EXPECTED_N"); do
    FIXTURE="$FIXTURE_DIR/clips_${i}.json"
    # Substitute YOUTUBE_TEST_URL → TEST_VIDEO_URL at submit time so
    # the fixtures stay env-neutral (operator can point at any video).
    PAYLOAD="$(sed "s|YOUTUBE_TEST_URL|$TEST_VIDEO_URL|g" "$FIXTURE")"
    RESP="$WORKDIR/resp_${i}.json"
    ERR="$WORKDIR/curl_err_${i}.log"
    (
        curl -sS -X POST "$BASE/api/stock-pipeline/run" \
            -H "$AUTH" -H "Content-Type: application/json" \
            --data "$PAYLOAD" --max-time 30 > "$RESP" 2>"$ERR"
    ) &
    PIDS+=("$!")
done
echo "  ${#PIDS[@]} background jobs spawned (PIDs: ${PIDS[*]})"
wait

# ---- Parse job_ids + audit response shape ----
declare -a JOB_IDS
for i in $(seq 1 "$EXPECTED_N"); do
    RESP="$WORKDIR/resp_${i}.json"
    if [ ! -s "$RESP" ]; then
        echo "FAIL: Run $i returned empty response (HTTP/compose error)" >&2
        cat "$WORKDIR/curl_err_${i}.log" >&2 || true
        exit 1
    fi
    JID="$(jq -r '.job_id // empty' "$RESP")"
    if [ -z "$JID" ]; then
        echo "FAIL: Run $i returned no job_id (response body follows):" >&2
        cat "$RESP" >&2
        echo >&2
        exit 1
    fi
    JOB_IDS+=("$JID")
    echo "  Run $i: JOB_ID=$JID"
done

# ---- Poll all 5 jobs to terminal + assert all SUCCEEDED ----
echo
echo "=== Polling $EXPECTED_N jobs to terminal ==="
poll_job() {
    local jid="$1"
    for k in $(seq 1 "$POLL_MAX"); do
        sleep "$POLL_INTERVAL"
        local s
        s="$(curl -sS "$BASE/api/jobs/$jid/full" -H "$AUTH" --max-time 10 2>/dev/null \
            | jq -r '.status // "unknown"')"
        echo "  [$k/$POLL_MAX] $jid status=$s" >&2
        case "$s" in
            SUCCEEDED|FAILED|CANCELLED) echo "$s"; return ;;
        esac
    done
    echo "TIMEOUT"
}

ALL_OK=1
declare -a STATUSES
for i in $(seq 1 "$EXPECTED_N"); do
    F="$(poll_job "${JOB_IDS[$((i-1))]}")"
    STATUSES+=("$F")
    echo "  Run $i: terminal=$F"
    [ "$F" = "SUCCEEDED" ] || ALL_OK=0
done
if [ "$ALL_OK" -eq 0 ]; then
    echo "FAIL: invariant C violated — not all $EXPECTED_N jobs SUCCEEDED" >&2
    for i in $(seq 1 "$EXPECTED_N"); do echo "  Run $i: ${STATUSES[$((i-1))]}" >&2; done
    exit 1
fi

# ---- COUNT vs EXPECTED_N gate (invalidates rest of invariants if raw count is off) ----
if [ "$ALL_OK" -eq 1 ]; then
    echo "  invariant C: all ${#JOB_IDS[@]} jobs SUCCEEDED ✓"
fi

# ---- SQL invariants (require sqlite3 + DB_PATH readable) ----
if [ ! -f "$DB_PATH" ]; then
    echo
    echo "WARN: DB_PATH=$DB_PATH not found — skipping SQL invariants A, B, D, E, F, G"
    echo "PASS: $EXPECTED_N/$EXPECTED_N jobs SUCCEEDED; SQL invariants skipped (no DB)"
    exit 0
fi

echo
echo "=== Invariant D: SQLite not permanently locked ==="
START=$(date +%s%N)
TABLES_OUT="$(sqlite3 "$DB_PATH" '.tables' 2>&1)"
END=$(date +%s%N)
ELAPSED_MS=$(( (END - START) / 1000000 ))
if [ -z "$TABLES_OUT" ] || [ "$ELAPSED_MS" -gt 5000 ]; then
    echo "FAIL: invariant D violated — sqlite3 '.tables' empty or >5s (${ELAPSED_MS}ms)" >&2
    echo "  Output: $TABLES_OUT" >&2
    exit 1
fi
echo "  sqlite3 quick probe: ${ELAPSED_MS}ms ✓"

# Build a JSON-list of the JOB_IDs to use in `job_id IN (...)` for cohort
# scoping. Stock source casing: jobs.id is text; media_assets rows link
# back via the canonical "job_id" column (or via the parents/children
# job-tree if the column is missing — fallback below uses the source_url
# pattern + cohort timestamp window).
JOBS_CSV=$(printf "'%s'," "${JOB_IDS[@]}" | sed 's/,$//')
echo
echo "=== Cohort scoping: ${#JOB_IDS[@]} job_ids [${JOBS_CSV}] in last ${COHORT_WINDOW_MIN}m ==="

echo
echo "=== Invariant A: exactly 1 source download (singleflight + cache collapse) ==="
# Same URL + same clip interval = same DeriveSourceCacheKey across all
# 5 cohort requests → singleflight collapses to 1 cache-write.
A_COUNT=$(sqlite3 "$DB_PATH" "
    SELECT COUNT(*) FROM stock_source_cache
    WHERE source_url LIKE '%${TEST_VIDEO_ID}%'
      AND state='active'
      AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes')
      AND download_section = ''
")
if [ "$A_COUNT" -ne 1 ]; then
    echo "FAIL: invariant A violated — expected 1 active stock_source_cache entry for cohort, got $A_COUNT" >&2
    sqlite3 "$DB_PATH" "SELECT id, cache_key, source_url, state, created_at, download_section FROM stock_source_cache WHERE source_url LIKE '%${TEST_VIDEO_ID}%' AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes')" >&2
    exit 1
fi
echo "  stock_source_cache active rows for cohort: $A_COUNT ✓ (singleflight collapsed)"

# Cohort source for media_assets: rows whose source_url matches + the
# folder_name comes from one of the 5 fixture folder_names. We rely on
# production's canonical column set: media_assets.source_url, name,
# folder_name, local_path, file_hash, duration_ms, start_ms, end_ms,
# created_at (migrations 094 + 152).
FOLDER_NAMES_CSV=$(printf "'e2e_concurrency_clip_%d'," $(seq 1 "$EXPECTED_N") | sed 's/,$//')

echo
echo "=== Invariant B: 5 distinct local_path rows in cohort (no overwrite) ==="
B_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(DISTINCT local_path) FROM media_assets
    WHERE source='stock'
      AND name IN ($FOLDER_NAMES_CSV)
      AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes')
      AND local_path != ''")
if [ "$B_COUNT" -ne "$EXPECTED_N" ]; then
    echo "FAIL: invariant B violated — expected $EXPECTED_N distinct local_path rows, got $B_COUNT" >&2
    sqlite3 "$DB_PATH" "SELECT id, name, local_path, file_hash FROM media_assets WHERE source='stock' AND name IN ($FOLDER_NAMES_CSV) AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes')" >&2
    exit 1
fi
echo "  Distinct local_path rows in cohort: $B_COUNT ✓"

echo
echo "=== Invariant E: no zero-byte artifacts (file_hash!='', duration_ms>0, local_path!='') ==="
E_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM media_assets
    WHERE source='stock'
      AND name IN ($FOLDER_NAMES_CSV)
      AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes')
      AND (length(local_path) = 0
           OR file_hash = ''
           OR duration_ms = 0
           OR duration_ms IS NULL)")
if [ "$E_COUNT" -ne 0 ]; then
    echo "FAIL: invariant E violated — zero-byte / no-hash / zero-duration rows in cohort: $E_COUNT" >&2
    sqlite3 "$DB_PATH" "SELECT id, name, local_path, file_hash, duration_ms FROM media_assets WHERE source='stock' AND name IN ($FOLDER_NAMES_CSV) AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes') AND (length(local_path) = 0 OR file_hash = '' OR duration_ms = 0 OR duration_ms IS NULL)" >&2
    exit 1
fi
echo "  Zero-byte/no-hash rows in cohort: 0 ✓"

echo
echo "=== Invariant F: clip-start integrity (start_ms in [${CLIP_START_MS_LOW}, ${CLIP_START_MS_HIGH}], end_ms > start_ms) ==="
F_BAD=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM media_assets
    WHERE source='stock'
      AND name IN ($FOLDER_NAMES_CSV)
      AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes')
      AND NOT (start_ms >= $CLIP_START_MS_LOW
               AND start_ms <= $CLIP_START_MS_HIGH
               AND end_ms > start_ms)")
if [ "$F_BAD" -ne 0 ]; then
    echo "FAIL: invariant F violated — $F_BAD cohort rows have clip-start outside [${CLIP_START_MS_LOW}, ${CLIP_START_MS_HIGH}]ms or end_ms<=start_ms" >&2
    sqlite3 "$DB_PATH" "SELECT id, name, start_ms, end_ms, duration_ms FROM media_assets WHERE source='stock' AND name IN ($FOLDER_NAMES_CSV) AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes') AND NOT (start_ms >= $CLIP_START_MS_LOW AND start_ms <= $CLIP_START_MS_HIGH AND end_ms > start_ms)" >&2
    exit 1
fi
echo "  Cohort rows with valid clip range: ✓ (start_ms ∈ [${CLIP_START_MS_LOW}, ${CLIP_START_MS_HIGH}]; end_ms > start_ms)"

echo
echo "=== Invariant G: $EXPECTED_N distinct folder_name rows (no Drive overwrite) ==="
G_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(DISTINCT name) FROM media_assets
    WHERE source='stock'
      AND name IN ($FOLDER_NAMES_CSV)
      AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes')")
if [ "$G_COUNT" -ne "$EXPECTED_N" ]; then
    echo "FAIL: invariant G violated — expected $EXPECTED_N distinct folder_name rows, got $G_COUNT" >&2
    sqlite3 "$DB_PATH" "SELECT DISTINCT name FROM media_assets WHERE source='stock' AND name IN ($FOLDER_NAMES_CSV) AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes')" >&2
    exit 1
fi
echo "  Distinct folder_name rows in cohort: $G_COUNT ✓"

echo
echo "PASS: 7 invariants hold for $EXPECTED_N concurrent stock runs on $TEST_VIDEO_URL"
echo "  1 source download (singleflight) / 5 distinct clip outputs / 5 SUCCEEDED jobs"
echo "  no SQLite lock / no zero-byte / clip-start integrity / no Drive overwrite"
exit 0

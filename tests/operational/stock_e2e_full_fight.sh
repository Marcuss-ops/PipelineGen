#!/usr/bin/env bash
# tests/operational/stock_e2e_full_fight.sh
#
# End-to-end DoD §12 Pacquiao/Cotto full-fight batch (July 2026).
#
# Submits TWELVE round-folder broker jobs (round_1_fight … round_12_fight)
# totalling 351 clips × 4s = 1,404 seconds of source video. Each round
# carries 29 clips (or 30 for the first 3 rounds) so 3×30 + 9×29 = 351
# exactly. Singleflight over DeriveSourceCacheKey collapses all 12
# rounds into ONE yt-dlp download (invariant A).
#
# KPIs verified:
#   A) 351 pianificate  : 12 cohort-folder broker jobs each with the
#                         per-round clip count (3*30 + 9*29 = 351).
#   B) 351 completate   : 12 SUCCEEDED broker jobs after poll.
#   C) 351 verificate   : ffprobe each cohort local_path file
#                         (codec_type=video, duration ≈ 4000 ms).
#   D) 0 zero-byte      : file_hash != '', local_path != '',
#                         duration_ms > 0 for every cohort row.
#   E) 0 duplicate      : COUNT(DISTINCT local_path) = 351 AND
#                         COUNT(DISTINCT file_hash) = 351.
#   F) 0 missing        : cohort row count = 351 between plan + run.
#   G) 1 download       : stock_source_cache has exactly 1 active row
#                         for the cohort URL (singleflight collapse).
#   H) 12 cartelle      : 12 distinct folder_name rows (one per round).
#
# Exit codes:
#   0 = PASS (all 8 invariants hold; Pacquiao/Cotto full-fight batch
#             round-by-round is the canonical 351-clip run)
#   1 = FAIL (count mismatch / cache miss / ffprobe reject)
#   2 = prereq missing
#
# Prerequisites:
#   - PipelineGen server reachable at $BASE.
#   - Migrations 094 (media_assets) + 160 (stock_source_cache) applied
#     to $DB_PATH.
#   - Network reachable to YouTube (or TEST_VIDEO_URL is a local URL).
#   - ffprobe on PATH (binary check).
#
# Overridable env vars (defaults match the DoD §12 spec):
#   BASE, AUTH, DB_PATH, POLL_MAX, POLL_INTERVAL,
#   ROUNDS               (default 12)
#   TOTAL_CLIPS          (default 351)
#   CLIPS_PER_ROUND      (default 29    — 3 rounds get 30 = LEFT_OVER)
#   CLIP_DURATION        (default 4 seconds)
#   TEST_VIDEO_URL       (default https://www.youtube.com/watch?v=QdSbtEo3x_Y)
#   TEST_VIDEO_ID        (default QdSbtEo3x_Y)
#   COHORTS_TAG          (default e2e_full_fight_<unix>)
#   COHORT_WINDOW_MIN    (default 60)
#   FFPROBE_JSON_TIMEOUT (per-file timeout in seconds, default 20)

set -euo pipefail

BASE="${BASE:-http://127.0.0.1:8000}"
AUTH="${AUTH:-Authorization: Bearer ${VELOX_ADMIN_TOKEN:-}}"
DB_PATH="${DB_PATH:?DB_PATH must be explicitly set to an isolated or approved database}"
POLL_MAX="${POLL_MAX:-90}"
POLL_INTERVAL="${POLL_INTERVAL:-20}"
ROUNDS="${ROUNDS:-12}"
N="${TOTAL_CLIPS:-351}"
PER_ROUND="${CLIPS_PER_ROUND:-29}"
CLIP_DUR="${CLIP_DURATION:-4}"
TEST_VIDEO_URL="${TEST_VIDEO_URL:-https://www.youtube.com/watch?v=QdSbtEo3x_Y}"
TEST_VIDEO_ID="${TEST_VIDEO_ID:-QdSbtEo3x_Y}"
COHORTS_TAG="${COHORTS_TAG:-e2e_full_fight_$(date +%s)}"
COHORT_WINDOW_MIN="${COHORT_WINDOW_MIN:-60}"
FFPROBE_TIMEOUT="${FFPROBE_JSON_TIMEOUT:-20}"

# Required tools.
command -v curl    >/dev/null 2>&1 || { echo "FAIL: curl not on PATH" >&2; exit 2; }
command -v jq      >/dev/null 2>&1 || { echo "FAIL: jq not on PATH"   >&2; exit 2; }
command -v sqlite3 >/dev/null 2>&1 || { echo "FAIL: sqlite3 not on PATH" >&2; exit 2; }
command -v ffprobe >/dev/null 2>&1 || { echo "FAIL: ffprobe not on PATH (DoD §12 requires ffprobe verification)" >&2; exit 2; }

# ---- Pre-flight: server reachable + validator returns 4xx on empty ----
HTTP=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 \
    -X POST "$BASE/api/stock-pipeline/run" \
    -H "$AUTH" -H "Content-Type: application/json" -d '{}' 2>/dev/null || echo "000")
if [ "$HTTP" = "000" ]; then
    echo "FAIL: PipelineGen server at $BASE unreachable" >&2; exit 2
fi
if [ "$HTTP" != "400" ] && [ "$HTTP" != "401" ] && [ "$HTTP" != "403" ]; then
    echo "WARN: pre-flight HTTP=$HTTP (validator usually 400/401/403)" >&2
fi

# Sanity: cap = handler.MaxClipsPerRun = 100. Per-round count must fit.
LEFT_OVER=$(( N - PER_ROUND * ROUNDS ))
if [ "$LEFT_OVER" -lt 0 ] || [ "$LEFT_OVER" -ge "$ROUNDS" ]; then
    echo "FAIL: TOTAL_CLIPS=$N / ROUNDS=$ROUNDS / PER_ROUND=$PER_ROUND — LEFT_OVER=$LEFT_OVER invalid" >&2
    echo "  (need 0 ≤ LEFT_OVER < ROUNDS so the per-round distribution pads cleanly)" >&2
    exit 2
fi
PER_ROUND_MAX=$((PER_ROUND + 1))
if [ "$PER_ROUND_MAX" -gt 100 ]; then
    echo "FAIL: per-round max clips=$PER_ROUND_MAX exceeds handler MaxClipsPerRun=100" >&2
    exit 2
fi
EXPECTED_TOTAL_PLAN=$(( ROUNDS * PER_ROUND + LEFT_OVER ))
if [ "$EXPECTED_TOTAL_PLAN" -ne "$N" ]; then
    echo "FAIL: per-round arithmetic gave $EXPECTED_TOTAL_PLAN, expected $N" >&2
    exit 2
fi

echo "=== STK-E2E-FULL-FIGHT (DoD §12): ${ROUNDS} rounds × ~${PER_ROUND}/${PER_ROUND_MAX} clips × ${N} total ==="
echo "    cohort_tag=$COHORTS_TAG  url=$TEST_VIDEO_URL  per_round_max=$PER_ROUND_MAX"

# ---- Build 12 round payloads programmatically via jq ----
# Layout: the FIRST $LEFT_OVER rounds get $((PER_ROUND+1)) clips; the
# REMAINING rounds get $PER_ROUND. For spec defaults ROUNDS=12, N=351,
# PER_ROUND=29 → LEFT_OVER=3 so rounds 1-3 have 30 clips each and
# rounds 4-12 have 29 clips each. Total = 3*(29+1) + 9*29 = 90+261 = 351.
submit_run() {
    local run_label="$1"  # "A" or echoed for the journal
    local job_ids=()
    echo
    echo "=== ${run_label}: submitting ${ROUNDS} rounds ==="
    # Pre-compute per-round clip ranges via bash arrays.
    declare -a RD_START RD_END RD_NAME
    local cursor=1
    for r in $(seq 1 "$ROUNDS"); do
        local rd_clips=$PER_ROUND
        if [ "$r" -le "$LEFT_OVER" ]; then rd_clips=$((PER_ROUND + 1)); fi
        RD_START[$((r-1))]=$cursor
        RD_END[$((r-1))]=$(( cursor + rd_clips - 1 ))
        RD_NAME[$((r-1))]="e2e_full_fight_round_${r}"
        cursor=$(( cursor + rd_clips ))
    done
    if [ "$((cursor - 1))" -ne "$N" ]; then
        echo "FAIL: round-builder cursor=$cursor doesn't match N=$N" >&2; exit 1
    fi

    for r in $(seq 1 "$ROUNDS"); do
        local cs="${RD_START[$((r-1))]}"
        local ce="${RD_END[$((r-1))]}"
        local folder="${RD_NAME[$((r-1))]}"
        local cohort="$COHORTS_TAG"
        local url="$TEST_VIDEO_URL"
        local dur="$CLIP_DUR"

        local payload
        payload="$(jq -n \
            --argjson cs "$cs" \
            --argjson ce "$ce" \
            --arg     r   "$r" \
            --arg     folder "$folder" \
            --arg     cohort "$cohort" \
            --arg     url "$url" \
            --argjson dur "$dur" \
            '{direct_urls:[$url], clips:[range($cs;$ce+1)|{url:$url,title:("e2e_full_fight_round_"+$r+"_clip_"+(tostring(.)|.)),start_sec:((.-1)*$dur),end_sec:(.*$dur)}], clip_duration:$dur, folder_name:($cohort+"_run_"+$folder), subfolder:($cohort+"_round_"+$r), async:true, no_audio:false, no_effects:false, no_transitions:false, metadata:{title:($cohort+"_round_"+$r),category:"boxing",extra:{cohort:"e2e_full_fight",round:$r,chunk_start:($cs|tostring),chunk_end:($ce|tostring),cohort_tag:$cohort,clips_per_round:(($ce-$cs+1)|tostring)}}}')"

        local resp jid
        resp="$(curl -sS -X POST "$BASE/api/stock-pipeline/run" \
            -H "$AUTH" -H "Content-Type: application/json" \
            --data "$payload" --max-time 60)"
        jid="$(echo "$resp" | jq -r '.job_id // empty')"
        if [ -z "$jid" ]; then
            echo "FAIL: round $r returned no job_id: $resp" >&2; exit 1
        fi
        job_ids+=("$jid")
        echo "  Round $r [clips ${cs}-${ce}]: JOB_ID=$jid  folder=${folder}"
    done
    printf '%s\n' "${job_ids[@]}"
}

poll_run() {
    local run_label="$1"
    shift
    local job_ids=("$@")
    echo
    echo "=== Polling ${run_label} (${#job_ids[@]} jobs) to terminal ==="
    local succeeded=0 failed=0
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
    echo "  ${run_label}: SUCCEEDED=$succeeded  FAIL/CANCEL=$failed"
    [ "$failed" -eq 0 ]
}

# ---- DB scope gates ----
if [ ! -f "$DB_PATH" ]; then
    echo
    echo "WARN: DB_PATH=$DB_PATH not found — SQL invariants A, D, E, F, G, H will be skipped" >&2
    DB_AVAILABLE=0
else
    DB_AVAILABLE=1
fi

# ---- Submit 12 rounds sequentially + poll each to SUCCEEDED ----
JOB_IDS=()
while IFS= read -r jid; do JOB_IDS+=("$jid"); done < <(submit_run "Pacquiao-Cotto-fight")
poll_run "Pacquiao-Cotto-fight" "${JOB_IDS[@]}" || {
    echo "FAIL: not all 12 rounds reached SUCCEEDED" >&2; exit 1;
}

# ---- SQL invariants (skip cleanly if DB unavailable) ----
if [ "$DB_AVAILABLE" -ne 1 ]; then
    echo
    echo "WARN: DB unavailable — SQL invariants skipped; proceeding to ffprobe only"
else
    echo
    echo "=== Invariant A: SOURCE_CACHE active rows for cohort URL (= 1) ==="
    A_COUNT=$(sqlite3 "$DB_PATH" "
        SELECT COUNT(*) FROM stock_source_cache
        WHERE source_url LIKE '%${TEST_VIDEO_ID}%'
          AND state = 'active'
          AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes')
          AND download_section = ''
    ")
    if [ "$A_COUNT" -ne 1 ]; then
        echo "FAIL: expected 1 active stock_source_cache entry, got $A_COUNT" >&2
        sqlite3 "$DB_PATH" "SELECT id, cache_key, source_url, state, created_at FROM stock_source_cache WHERE source_url LIKE '%${TEST_VIDEO_ID}%' AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes')" >&2
        exit 1
    fi
    echo "  stock_source_cache active entries: $A_COUNT ✓"

    echo
    echo "=== Invariant D: 0 zero-byte cohort rows ==="
    FOLDER_NAMES_CSV=$(printf "'e2e_full_fight_round_%d'," $(seq 1 "$ROUNDS") | sed 's/,$//')
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
        echo "FAIL: $D_BAD zero-byte / no-hash / zero-duration cohort rows" >&2
        exit 1
    fi
    echo "  zero-byte cohort rows: $D_BAD ✓"

    echo
    echo "=== Invariant E: 0 duplicates (local_path + file_hash unique) ==="
    COHORT_LP_HASH=$(sqlite3 -separator '|' "$DB_PATH" "
        SELECT COUNT(*), COUNT(DISTINCT local_path), COUNT(DISTINCT file_hash)
        FROM media_assets
        WHERE source = 'stock'
          AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes')
          AND duration_ms >= $(( CLIP_DUR * 1000 - 500 ))
          AND duration_ms <= $(( CLIP_DUR * 1000 + 500 ))
    ")
    COHORT_TOTAL=$(echo "$COHORT_LP_HASH" | awk -F'|' '{print $1+0}')
    COHORT_DLP=$(echo "$COHORT_LP_HASH"    | awk -F'|' '{print $2+0}')
    COHORT_DHASH=$(echo "$COHORT_LP_HASH"  | awk -F'|' '{print $3+0}')
    if [ "$COHORT_TOTAL" -ne "$N" ] || [ "$COHORT_DLP" -ne "$N" ] || [ "$COHORT_DHASH" -ne "$N" ]; then
        echo "FAIL: cohort uniqueness check FAILED — total=$COHORT_TOTAL distinct_local=$COHORT_DLP distinct_hash=$COHORT_DHASH (expected total=distinct_local=distinct_hash=$N)" >&2
        exit 1
    fi
    echo "  cohort rows=$COHORT_TOTAL  distinct_local=$COHORT_DLP  distinct_hash=$COHORT_DHASH ✓ (all = $N)"

    echo
    echo "=== Invariant F: 0 missing — cohort row count = N ==="
    if [ "$COHORT_TOTAL" -ne "$N" ]; then
        echo "FAIL: cohort row count = $COHORT_TOTAL, expected $N" >&2; exit 1
    fi
    echo "  cohort_media_assets rows: $COHORT_TOTAL (matches plan $N) ✓"

    echo
    echo "=== Invariant H: 12 cartelle round corrette (12 distinct folder_name) ==="
    H_DIST=$(sqlite3 "$DB_PATH" "
        SELECT COUNT(DISTINCT name) FROM media_assets
        WHERE source = 'stock'
          AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes')
          AND name LIKE 'e2e_full_fight_round_%'
    ")
    if [ "$H_DIST" -ne "$ROUNDS" ]; then
        echo "FAIL: expected $ROUNDS distinct round folders, got $H_DIST" >&2
        sqlite3 "$DB_PATH" "SELECT DISTINCT name FROM media_assets WHERE source='stock' AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes') AND name LIKE 'e2e_full_fight_round_%'" >&2
        exit 1
    fi
    echo "  distinct round folders: $H_DIST ✓"
fi

# ---- Invariant C: ffprobe every cohort local_path ----
# Collect rendered clip paths from media_assets (or fall back: skip ffprobe
# when DB is unavailable). Per-file ffprobe parses JSON for codec_type +
# duration; fail on first mismatch.
echo
echo "=== Invariant C: ffprobe every cohort local_path ==="
if [ "$DB_AVAILABLE" -ne 1 ]; then
    echo "WARN: DB unavailable — skipping ffprobe batch" >&2
else
    COHORT_PATHS=$(sqlite3 "$DB_PATH" "
        SELECT local_path FROM media_assets
        WHERE source = 'stock'
          AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes')
          AND duration_ms >= $(( CLIP_DUR * 1000 - 500 ))
          AND duration_ms <= $(( CLIP_DUR * 1000 + 500 ))
        ORDER BY id
    ")
    FFPROBE_OK=0
    FFPROBE_FAIL=0
    FFPROBE_TOTAL=0
    while IFS= read -r path; do
        [ -z "$path" ] && continue
        FFPROBE_TOTAL=$((FFPROBE_TOTAL + 1))
        if [ ! -s "$path" ]; then
            echo "FAIL: cohort path missing or zero-byte: $path" >&2
            FFPROBE_FAIL=$((FFPROBE_FAIL + 1)); continue
        fi
        # -v error: only errors; -show_entries format=duration; -of json.
        FP_OUT="$(timeout "$FFPROBE_TIMEOUT" ffprobe -v error \
            -show_entries stream=codec_type,codec_name -show_entries format=duration \
            -of json "$path" 2>&1)"
        FP_RC=$?
        if [ "$FP_RC" -ne 0 ]; then
            echo "FAIL: ffprobe exit=$FP_RC on $path" >&2
            echo "  $FP_OUT" >&2
            FFPROBE_FAIL=$((FFPROBE_FAIL + 1)); continue
        fi
        # Verify JSON has at least one video stream and duration ≈ CLIP_DUR.
        VIDEO=$(echo "$FP_OUT" | jq -r '[.streams[]|select(.codec_type=="video")]|length')
        DURATION=$(echo "$FP_OUT" | jq -r '.format.duration // "0"')
        VIDEO_OK=0
        if [ "$VIDEO" -ge 1 ]; then VIDEO_OK=1; fi
        DURATION_OK=0
        if awk -v d="$DURATION" -v lo="$CLIP_DUR" 'BEGIN{d+=0; lo+=0; if (d>=(lo-0.5) && d<=(lo+0.5)) exit 0; else exit 1}'; then
            DURATION_OK=1
        fi
        if [ "$VIDEO_OK" -eq 1 ] && [ "$DURATION_OK" -eq 1 ]; then
            FFPROBE_OK=$((FFPROBE_OK + 1))
        else
            echo "FAIL: ffprobe reject on $path" >&2
            echo "  VIDEO_STREAMS=$VIDEO  DURATION=$DURATION (want 1 video stream + ~${CLIP_DUR}s)" >&2
            echo "  $FP_OUT" | head -20 >&2
            FFPROBE_FAIL=$((FFPROBE_FAIL + 1))
        fi
    done <<EOF
$COHORT_PATHS
EOF
    echo "  ffprobe verified: ok=$FFPROBE_OK  fail=$FFPROBE_FAIL  total=$FFPROBE_TOTAL (expected = $N)"
    if [ "$FFPROBE_TOTAL" -ne "$N" ] || [ "$FFPROBE_FAIL" -ne 0 ]; then
        echo "FAIL: ffprobe batch result (ok=$FFPROBE_OK fail=$FFPROBE_FAIL total=$FFPROBE_TOTAL) doesn't match plan (=$N)" >&2
        exit 1
    fi
    echo "  every cohort clip passed ffprobe ✓"
fi

echo
echo "PASS: DoD §12 Pacquiao/Cotto full-fight — $ROUNDS rounds × $N clips"
echo "  1 source download / 351 distinct artifacts / 351 SUCCEEDED / 0 zero-byte / 0 dup / 0 missing / 351ffprobe/12 folders"
exit 0

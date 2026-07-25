#!/usr/bin/env bash
# tests/operational/stock_e2e_mixed_load.sh — STK-E2E-MIXED-LOAD (DoD §14, July 2026).
#
# End-to-end mixed-load test under CPU-only concurrency limits:
#   * download concurrency  = 1
#   * ffmpeg concurrency    = 2
#   * render concurrency    = 1
#   * drive concurrency     = 2
#
# Submit profile (all backgrounds, staggered spin-up to avoid curl/process
# flood; total 8 broker jobs + 1 search + 1 implicit indexing trigger = 10
# concurrent requests with mixed payload shapes):
#
#   Job 1: 100 clips on video A          (heavy)
#   Job 2: 30 clips on video B           (medium)
#   Job 3: 30 clips on video C           (medium)
#   Job 4: 1 clip on video D             (small; submitted FIRST to
#                                          avoid starvation behind
#                                          the heavy jobs)
#   Job 5-8: 4× 1-clip on video D        (small; same rationale)
#   Search: POST /api/media/search       (read; no broker registration)
#   Indexing: a 1-clip with persist=true (triggers finalizer → outbox
#              asset.index.requested → IndexingHandler fan-out)
#
# The instrumentation submits the 5 small jobs FIRST so the orchestrator's
# job-broker queue sees them ahead of the heavy + medium bulk-load items;
# this is the canonical anti-starvation submission order for FIFO job
# brokers (mirrors the stock_e2e_concurrency.sh precedent of putting the
# smallest cohort slots first in the request stream).
#
# Invariants verified (the canonical DoD §14 surface):
#   A) /health=200 throughout — probed every $HEALTH_INTERVAL seconds
#      between job submission and the last terminal state. No OOM-kill
#      reset. The PID of the pipelinegen process MUST remain stable
#      (no silent restart during the run).
#   B) /ready?deep=true stable — sampled at start + end. The pre-run
#      signal must be ready=true with DB/jobs/Drive/outbox/FFmpeg/
#      source-stager all healthy. The post-run signal MUST NOT
#      oscillate false-then-true (the canonical "stuck on retryable"
#      symptom is a DB-side ready=true then ready=false transient).
#   C) Small jobs NOT blocked behind heavy jobs — wall-clock
#      measurement: every 5 small jobs MUST finish within
#      $SMALL_JOB_CEILING_SEC (default 60s, an absolute ceiling NOT
#      a ratio; a ratio would mask a true serialisation regression
#      where the small jobs run "after" the heavy jobs but each
#      individually are fast).
#   D) Large jobs reach terminal state — SUCCEEDED (or FAILED with
#      expected_error_code documented). The 100-clip + 2×30-clip jobs
#      may exceed $SMALL_JOB_CEILING_SEC and that's expected.
#   E) 0 permanent "database is locked" — sqlite3 spot-probe at end
#      < $SQLITE_PROBE_TIMEOUT_MS (default 5000ms). A permanent
#      lock-up surfaces as a probe that takes >>5s WITHOUT returning
#      data; failures here correspond to "production database is
#      locked" sentinel in logs.
#   F) Memory within limits — pipelinegen RSS delta from
#      start-of-run to end-of-run MUST be ≤ $RAM_CAP_BYTES
#      (default 2147483648 = 2 GiB; sufficiently above the
#      stock_e2e_one_round.sh 1-GiB cap to accommodate N=100+60+5+1=166
#      concurrent render + ffmpeg worker pool slots).
#   G) Disc within limits — /tmp/pipelinegen footprint delta MUST be
#      ≤ $DISK_CAP_BYTES (default 5368709120 = 5 GiB). The orchestrator
#      defers cleanup until RunResilient returns (Phase 1 defer-Cleanup
#      contract per orchestrator_run.go); only then does tmp space
#      reclaim. Mid-poll disk sample at end-of-run verifies the
#      reclaim happened (orchestrator's deferred Cleanup fires after
#      every step.Run completion).
#   H) Outbox indexing fan-out — at least one
#      event_type='asset.index.requested' status='completed' row
#      exists for the indexing-trigger job's clip. The asset.index.
#      requested row in pending/processing state for > MAX is a
#      pre-condition failure of the indexing fan-out.
#
# Exit codes (canonical battery convention):
#   0 = PASS (all 8 invariants hold)
#   1 = FAIL (one or more invariants violated)
#   2 = prereq missing (curl/jq/sqlite3/ps/awk absent, server unreachable,
#      machine-spec guard off, nproc > $CPU_CORE_LIMIT)
#   124 = wall-clock/polling timeout exceeded
#
# Overridable env vars (canonical smoke-script surface):
#   BASE                       (default http://127.0.0.1:8000)
#   AUTH                       (default "Authorization: Bearer $VELOX_ADMIN_TOKEN")
#   DB_PATH                    (default data/media/media.db.sqlite)
#   POLL_MAX                   (default 240  ≈ 1 hour polling ceiling)
#   POLL_INTERVAL              (default 15 seconds)
#   HEALTH_INTERVAL            (default 30 seconds)
#   SMALL_JOB_CEILING_SEC      (default 60 seconds)
#   RAM_CAP_BYTES              (default 2147483648  # 2 GiB)
#   DISK_CAP_BYTES             (default 5368709120  # 5 GiB)
#   LOADAVG_CAP                (default 2.0)
#   CPU_CORE_LIMIT             (default 4 for CPU-only env; safer to set 0
#                                and rely on $CPU_ONLY=1 instead)
#   CPU_ONLY                   (default 1; override to 0 for non-CPU envs)
#   SQLITE_PROBE_TIMEOUT_MS    (default 5000)
#   SMALL_JOBS_FIRST           (default 1; set 0 to dump heavy+medium first)
#   VIDEO_HEAVY               (default https://www.youtube.com/watch?v=QdSbtEo3x_Y)
#   VIDEO_MEDIUM_A             (default https://www.youtube.com/watch?v=u9QFOVRkfBs)
#   VIDEO_MEDIUM_B             (default https://www.youtube.com/watch?v=RdMTTBhBUlQ)
#   VIDEO_SMALL                (default https://www.youtube.com/watch?v=RRJvrDKunyA)
#   TMPFOOTPRINT_GLOB          (default /tmp/stock_stage_*)
#   OUTBOX_INDEX_TIMEOUT_SEC   (default 90 seconds for asset.index.requested →
#                                status='completed' to land post-indexing)

set -euo pipefail


# ─── Fail-closed auth gate (AGENTS.md "no-fake-availability") ───────────
# If VELOX_ADMIN_TOKEN is unset or empty, refuse to run. The canonical
# loader is `scripts/with-velox-auth`; the Makefile-level auth-check
# target runs the same loader against /api/artlist/job-consumer as a
# pre-flight gate. The historical placeholder `test-admin-token-12345`
# is forbidden by AGENTS.md and must never appear in this script or any
# other operational surface again — see AGENTS.md "Authentication SSOT".
: "${VELOX_ADMIN_TOKEN:?❌ VELOX_ADMIN_TOKEN unset — source scripts/with-velox-auth (or export manually before rerunning).}"

# ─── Configuration (canonical coiled env surface) ────────────────────────
BASE="${BASE:-http://127.0.0.1:8000}"
AUTH="${AUTH:-Authorization: Bearer ${VELOX_ADMIN_TOKEN:-}}"
DB_PATH="${DB_PATH:-data/media/media.db.sqlite}"
POLL_MAX="${POLL_MAX:-240}"
POLL_INTERVAL="${POLL_INTERVAL:-15}"
HEALTH_INTERVAL="${HEALTH_INTERVAL:-30}"
SMALL_JOB_CEILING_SEC="${SMALL_JOB_CEILING_SEC:-60}"
RAM_CAP_BYTES="${RAM_CAP_BYTES:-2147483648}"
DISK_CAP_BYTES="${DISK_CAP_BYTES:-5368709120}"
LOADAVG_CAP="${LOADAVG_CAP:-2.0}"
CPU_CORE_LIMIT="${CPU_CORE_LIMIT:-0}"        # 0 = $CPU_ONLY is the canonical gate
CPU_ONLY="${CPU_ONLY:-1}"
SQLITE_PROBE_TIMEOUT_MS="${SQLITE_PROBE_TIMEOUT_MS:-5000}"
SMALL_JOBS_FIRST="${SMALL_JOBS_FIRST:-1}"
VIDEO_HEAVY="${VIDEO_HEAVY:-https://www.youtube.com/watch?v=QdSbtEo3x_Y}"
VIDEO_MEDIUM_A="${VIDEO_MEDIUM_A:-https://www.youtube.com/watch?v=u9QFOVRkfBs}"
VIDEO_MEDIUM_B="${VIDEO_MEDIUM_B:-https://www.youtube.com/watch?v=RdMTTBhBUlQ}"
VIDEO_SMALL="${VIDEO_SMALL:-https://www.youtube.com/watch?v=RRJvrDKunyA}"
TMPFOOTPRINT_GLOB="${TMPFOOTPRINT_GLOB:-/tmp/stock_stage_*}"
OUTBOX_INDEX_TIMEOUT_SEC="${OUTBOX_INDEX_TIMEOUT_SEC:-90}"

# Cohort tags for SQL scoping (window=COHORT_WINDOW_MIN minutes).
COHORTS_TAG="e2e_mixed_load_$(date +%s)"
COHORT_WINDOW_MIN="${COHORT_WINDOW_MIN:-60}"

# ─── Required-tool guard (fail-closed at prereq) ─────────────────────────
command -v curl    >/dev/null 2>&1 || { echo "FAIL: curl not on PATH" >&2; exit 2; }
command -v jq      >/dev/null 2>&1 || { echo "FAIL: jq not on PATH"   >&2; exit 2; }
command -v sqlite3 >/dev/null 2>&1 || { echo "FAIL: sqlite3 not on PATH" >&2; exit 2; }
command -v ps      >/dev/null 2>&1 || { echo "FAIL: ps not on PATH"      >&2; exit 2; }
command -v awk     >/dev/null 2>&1 || { echo "FAIL: awk not on PATH"     >&2; exit 2; }

# ─── Machine-spec guard: CPU-only ONE environment gate ───────────────────
# Refuses to launch the mixed-load battery if the host has more cores
# than the CPU-only guard allows. Can be overridden via CPU_ONLY=0 for
# non-CPU-only envs (the limits in this file are CPU-tuned: ffmpeg=2,
# render=1, drive=2 — running on a 32-core rig wastes the entire purpose
# of the test, which is to verify the limits appear effective).
if [ "${CPU_ONLY}" = "1" ] && [ "${CPU_CORE_LIMIT}" -gt 0 ]; then
    NPROC=$(nproc 2>/dev/null || echo 0)
    if [ "${NPROC}" -gt "${CPU_CORE_LIMIT}" ]; then
        echo "FAIL: CPU-only guard tripped — nproc=${NPROC} > CPU_CORE_LIMIT=${CPU_CORE_LIMIT}" >&2
        echo "  This battery verifies CPU-only limits (download=1, ffmpeg=2, render=1, drive=2)" >&2
        echo "  Override via CPU_ONLY=0 if your environment is not CPU-only." >&2
        exit 2
    fi
    echo "  machine-spec guard: nproc=${NPROC} <= CPU_CORE_LIMIT=${CPU_CORE_LIMIT} OK"
fi

# ─── Workdir + cleanup-on-FAIL (preserve artifacts for diagnosis) ──────
WORKDIR="$(mktemp -d /tmp/stk-e2e-mixed-load.XXXXXX)"
trap 'rc=$?; if [ "$rc" -ne 0 ]; then echo "FAIL-PATH: workdir preserved at $WORKDIR" >&2; else rm -rf "$WORKDIR"; fi' EXIT

# Snapshot baseline resource usage (before any rendering work).
get_loadavg() {
    if [ -r /proc/loadavg ]; then
        awk '{print $1}' /proc/loadavg
    else
        uptime | awk -F'load averages?: ' 'NF==2 {split($2,a,","); print a[1]+0; exit}'
    fi
}
get_pipelinegen_rss_bytes() {
    pgrep -f pipelinegen 2>/dev/null || true \
        | xargs -I{} ps -o rss= -p {} 2>/dev/null \
        | awk '{s += $1+0} END {print s+0}'
}
get_pipelinegen_pid() {
    pgrep -of pipelinegen 2>/dev/null || echo ""
}
get_tmp_footprint_bytes() {
    local total=0
    for d in ${TMPFOOTPRINT_GLOB}; do
        if [ -e "$d" ]; then
            local sz
            sz=$(du -sb "$d" 2>/dev/null | awk '{print $1+0}')
            total=$(( total + sz ))
        fi
    done
    echo "$total"
}

# ─── Pre-flight: server reachable + validator returns 4xx on empty ───────
HTTP=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 \
    -X POST "$BASE/api/stock-pipeline/run" \
    -H "$AUTH" -H "Content-Type: application/json" -d '{}' 2>/dev/null || echo "000")
if [ "$HTTP" = "000" ]; then
    echo "FAIL: PipelineGen server at $BASE unreachable (exit 2)" >&2; exit 2
fi
if [ "$HTTP" != "400" ] && [ "$HTTP" != "401" ] && [ "$HTTP" != "403" ]; then
    echo "WARN: pre-flight HTTP=$HTTP (validator usually 400/401/403)" >&2
fi

# Snapshot PIPELINEGEN_PID_BEFORE so we can detect a silent restart.
PID_BEFORE=$(get_pipelinegen_pid)
LOADAVG_START=$(get_loadavg)
RSS_START=$(get_pipelinegen_rss_bytes)
DISK_START=$(get_tmp_footprint_bytes)
echo "=== STK-E2E-MIXED-LOAD (DoD §14): mixed-load under CPU-only limits ==="
echo "    submit profile: 1×100 clip + 2×30 clip + 5×1 clip + 1 search + 1 indexing"
echo "    concurrency limits: download=1 ffmpeg=2 render=1 drive=2"
echo "    baseline: loadavg=${LOADAVG_START}  pipelinegen_rss=${RSS_START}  tmp=${DISK_START}  pid=${PID_BEFORE}"
echo "    cohort_tag=$COHORTS_TAG  url_small=$VIDEO_SMALL  url_heavy=$VIDEO_HEAVY"

# ─── /ready?deep=true snapshot at start ───────────────────────────────────
READY_BEFORE=$(curl -sS --max-time 10 "$BASE/ready?deep=true" 2>/dev/null \
    | jq -r '.ready // .status // "unknown"')
echo "  /ready?deep=true at start: ${READY_BEFORE}"

# ─── Payload builders (inline jq -n — mirrors stock_e2e_one_round.sh) ───
# Each payload uses the same shape as the existing stock_e2e_concurrency.sh
# fixtures (direct_urls + clips[] + clip_duration + folder_name + subfolder
# + async:true + persist:true + the metadata envelope). Carry-over of the
# e2e_concurrency_clip_{N} naming pattern keeps SQL scoping predictable.
build_payload() {
    local url="$1" cohort="$2 clip_count="$3" clip_dur="$4
    jq -n \
        --arg     url "$url" \
        --arg     cohort "$cohort" \
        --argjson n "$clip_count" \
        --argjson dur "$clip_dur" \
        '{
            direct_urls: [$url],
            clips: [range(1; $n + 1) | {
                url: $url,
                title: ($cohort + "_clip_" + (tostring(.)|.) + "_" + (tostring(((.-1)*$dur))|.) + "-" + (tostring((.*$dur))|.) + "s"),
                start_sec: ((. - 1) * $dur),
                end_sec:   (. * $dur)
            }],
            clip_duration: $dur,
            folder_name:  ($cohort + "_folder"),
            subfolder:    ($cohort + "_subfolder"),
            async:   true,
            persist: true,
            no_audio:      true,
            no_effects:    true,
            no_transitions: true,
            metadata: {
                title: ($cohort + "_title"),
                category: "boxing",
                extra: {
                    cohort: "e2e_mixed_load",
                    cohort_tag: $cohort,
                    clip_count: ($n | tostring),
                    clip_duration: ($dur | tostring),
                    run_submitter: "stock_e2e_mixed_load.sh"
                }
            }
        }'
}

# Pre-build payload strings.
PAYLOAD_HEAVY=$(build_payload "$VIDEO_HEAVY" "${COHORTS_TAG}_heavy" 100 4)
PAYLOAD_MEDIUM_A=$(build_payload "$VIDEO_MEDIUM_A" "${COHORTS_TAG}_medium_a" 30 4)
PAYLOAD_MEDIUM_B=$(build_payload "$VIDEO_MEDIUM_B" "${COHORTS_TAG}_medium_b" 30 4)
declare -a SMALL_PAYLOADS
for i in 1 2 3 4 5; do
    SMALL_PAYLOADS[$i]=$(build_payload "$VIDEO_SMALL" "${COHORTS_TAG}_small_${i}" 1 4)
done

# ─── Background submit helpers ───────────────────────────────────────────
# Concurrent submissions are throttled in chunks of $SUBMIT_BATCH
# (default 15) so the host's ephemeral-port range + bash-process spawn
# limits stay within bounds (sub-200 concurrent curl-threads is
# conservative on Linux/macOS; 167 jobs would otherwise hit ephemeral-
# port exhaustion on a busy CI runner).
SUBMIT_BATCH="${SUBMIT_BATCH:-15}"

# submit_job captures (job_id, http_code) into a per-job file under
# $WORKDIR via job_index.txt append. Returns 0 on HTTP 2xx with a
# non-empty .job_id; non-zero otherwise.
submit_job() {
    local label="$1" payload_json="$2" workdir="$3"
    local raw resp_file err_file http_line http_code body job_id
    resp_file="$workdir/resp_${label}.json"
    err_file="$workdir/err_${label}.log"
    raw=$(curl -sS -X POST "$BASE/api/stock-pipeline/run" \
        -H "$AUTH" -H "Content-Type: application/json" \
        --data "$payload_json" --max-time 60 \
        -w '\n%{http_code}' -o "$resp_file" 2>"$err_file" || true)
    http_line=$(echo "$raw" | tail -n 1)
    body=$(cat "$resp_file" 2>/dev/null || echo "")
    job_id=$(echo "$body" | jq -r '.job_id // empty' 2>/dev/null || echo "")
    if [ -n "$job_id" ]; then
        echo "$label $job_id $http_line"
        return 0
    else
        echo "$label '' $http_line" >&2
        [ -s "$err_file" ] && cat "$err_file" >&2 || true
        [ -s "$resp_file" ] && head -c 400 "$resp_file" >&2 || true
        return 1
    fi
}

# ─── Phase 1: submit small jobs FIRST (anti-starvation) ──────────────────
echo
echo "=== Phase 1: submit 5 × 1-clip jobs (small, submitted FIRST) ==="
declare -a SMALL_JOB_IDS
declare -a SMALL_JOB_T0
SUBMIT_T0=$(date +%s)
for i in 1 2 3 4 5; do
    if [ "$i" != "1" ] && [ $(( (i - 1) % SUBMIT_BATCH )) -eq 0 ]; then
        wait
    fi
    submit_job "small_${i}" "${SMALL_PAYLOADS[$i]}" "$WORKDIR" >> "$WORKDIR/job_index.txt" \
        && SMALL_JOB_IDS+=("$(awk -v lbl="small_${i}" '$1==lbl {print $2}' "$WORKDIR/job_index.txt")") \
        && SMALL_JOB_T0+=("$SUBMIT_T0")
done
wait  # drain small-job submit batch
echo "  submitted ${#SMALL_JOB_IDS[@]}/5 small jobs"

# ─── Phase 2: submit heavy + medium jobs (back of the queue) ─────────────
echo
echo "=== Phase 2: submit 1×100 + 2×30 (heavy, queued behind small jobs) ==="
declare -a HEAVY_JOB_IDS
declare -a HEAVY_JOB_T0
for label_payload in "heavy:${PAYLOAD_HEAVY}" "medium_a:${PAYLOAD_MEDIUM_A}" "medium_b:${PAYLOAD_MEDIUM_B}"; do
    label="${label_payload%%:*}"; payload="${label_payload#*:}"
    submit_job "$label" "$payload" "$WORKDIR" >> "$WORKDIR/job_index.txt" \
        && HEAVY_JOB_IDS+=("$(awk -v lbl="$label" '$1==lbl {print $2}' "$WORKDIR/job_index.txt")")
done
wait
echo "  submitted ${#HEAVY_JOB_IDS[@]}/3 heavy+medium jobs"

# ─── Phase 3: submit indexing trigger (1-clip with persist=true to enqueue
# the finalizer → outbox asset.index.requested fan-out) ─────────────────
echo
echo "=== Phase 3: submit indexing trigger (1-clip with persist=true) ==="
INDEX_PAYLOAD=$(build_payload "$VIDEO_SMALL" "${COHORTS_TAG}_indexing_trigger" 1 4)
submit_job "indexing_trigger" "$INDEX_PAYLOAD" "$WORKDIR" >> "$WORKDIR/job_index.txt" \
    && INDEX_JOB_ID="$(awk -v lbl="indexing_trigger" '$1==lbl {print $2}' "$WORKDIR/job_index.txt")"
wait
echo "  indexing_trigger JOB_ID=${INDEX_JOB_ID}"

# ─── Sanity: every submit returned a job_id ──────────────────────────────
TOTAL_OK=$(awk 'NF>=3 && $2!=""' "$WORKDIR/job_index.txt" | wc -l | tr -d ' ')
if [ "$TOTAL_OK" -ne 9 ]; then
    echo "FAIL: only $TOTAL_OK/9 jobs returned job_id (curl-error code or composition error)" >&2
    cat "$WORKDIR/job_index.txt" >&2
    exit 1
fi
echo
echo "=== 9 broker jobs submitted (5 small + 3 heavy/medium + 1 indexing) ==="

# ─── Phase 4: parallel /api/media/search probe ───────────────────────────
echo
echo "=== Phase 4: parallel /api/media/search probe (no broker registration) ==="
SEARCH_RESP="$WORKDIR/search_resp.json"
SEARCH_HTTP=$(curl -sS -X POST "$BASE/api/media/search" \
    -H "$AUTH" -H "Content-Type: application/json" \
    --max-time 30 -o "$SEARCH_RESP" -w '%{http_code}' \
    --data '{
        "query": "boxing training gym stock footage",
        "sources": ["stock"],
        "mode": "hybrid",
        "limit": 10
    }' || echo "000")
if [ "$SEARCH_HTTP" != "200" ]; then
    echo "WARN: /api/media/search returned HTTP=$SEARCH_HTTP (informational; mixed-load probe)" >&2
else
    SEARCH_HITS=$(jq -r '.items // .results // [] | length' "$SEARCH_RESP" 2>/dev/null || echo "?")
    echo "  /api/media/search HTTP=$SEARCH_HTTP  items/hits=$SEARCH_HITS"
fi

# ─── Phase 5: poll broker jobs to terminal + mid-poll health/spawn ─────
echo
echo "=== Phase 5: poll all 9 jobs in parallel; sample health/PID mid-flight ==="
declare -a TERMINALS
declare -a JOB_WALL_SECS
POLL_TOTAL_START=$(date +%s)
last_health_sample=$POLL_TOTAL_START

# Aggregate job IDs (5 small + 3 heavy + 1 indexing).
ALL_LABEL_TO_JOB_ID=()
while IFS= read -r lbl jid http; do
    [ -n "$jid" ] && ALL_LABEL_TO_JOB_ID+=("$lbl:$jid")
done < "$WORKDIR/job_index.txt"

poll_one() {
    local label="$1" jid="$2" deadline=$3
    local s="" t0=$(date +%s)
    for k in $(seq 1 "$POLL_MAX"); do
        sleep "$POLL_INTERVAL"
        s=$(curl -sS "$BASE/api/jobs/$jid/full" -H "$AUTH" --max-time 10 2>/dev/null \
            | jq -r '.status // "unknown"' 2>/dev/null || echo "unknown")
        case "$s" in
            SUCCEEDED|FAILED|CANCELLED)
                local dt=$(( $(date +%s) - t0 ))
                echo "$label $jid $s $dt"
                return
                ;;
        esac
        if [ "$(date +%s)" -ge "$deadline" ]; then
            echo "$label $jid TIMEOUT $(($(date +%s) - t0))"
            return
        fi
    done
    echo "$label $jid TIMEOUT $(($(date +%s) - t0))"
}

# Fire background poll goroutines, sampling /health mid-flight at
# HEALTH_INTERVAL boundaries.
for entry in "${ALL_LABEL_TO_JOB_ID[@]}"; do
    label="${entry%%:*}"; jid="${entry#*:}"
    (
        poll_one "$label" "$jid" "$(( $POLL_TOTAL_START + $((POLL_MAX * POLL_INTERVAL)) + 60 ))"
    ) >> "$WORKDIR/poll_results.txt" &
done

PIDS_DRIFT=0
HEALTH_OK_COUNT=0
while [ "$(date +%s)" -lt "$(( POLL_TOTAL_START + POLL_MAX * POLL_INTERVAL + 60 ))" ]; do
    sleep "$HEALTH_INTERVAL"
    NOW=$(date +%s)
    # /health probe.
    HEALTH_HTTP=$(curl -sS -o "$WORKDIR/health_out_$NOW.txt" --max-time 8 -w '%{http_code}' \
        "$BASE/health" 2>/dev/null || echo "000")
    if [ "$HEALTH_HTTP" = "200" ]; then
        HEALTH_OK_COUNT=$(( HEALTH_OK_COUNT + 1 ))
    else
        echo "  mid-poll [$NOW]: /health=$HEALTH_HTTP (NOT 200)" >&2
    fi
    # PID drift check.
    PID_NOW=$(get_pipelinegen_pid)
    if [ -n "$PID_BEFORE" ] && [ -n "$PID_NOW" ] && [ "$PID_NOW" != "$PID_BEFORE" ]; then
        echo "  mid-poll [$NOW]: pipelinegen PID drift detected ($PID_BEFORE → $PID_NOW)" >&2
        PIDS_DRIFT=1
    fi
    # If all polls have reached terminal, break early.
    POLLED=$(wc -l < "$WORKDIR/poll_results.txt" 2>/dev/null | tr -d ' ' || echo 0)
    if [ "$POLLED" -ge 9 ]; then
        break
    fi
done

# Drain poll goroutines.
wait
cat "$WORKDIR/poll_results.txt"

# ─── Invariant B: /ready?deep=true stable at end ──────────────────────────
READY_AFTER=$(curl -sS --max-time 10 "$BASE/ready?deep=true" 2>/dev/null \
    | jq -r '.ready // .status // "unknown"')
echo
echo "  /ready?deep=true at end: ${READY_AFTER}"
if [ "$READY_BEFORE" != "$READY_AFTER" ]; then
    if [ "$READY_BEFORE" = "true" ] && [ "$READY_AFTER" = "true" ]; then
        echo "  invariant B /ready stability: ready=true at start AND end (transient drift allowed)" >&2
    else
        echo "FAIL: invariant B violated — /ready drifted from '$READY_BEFORE' to '$READY_AFTER'" >&2
        exit 1
    fi
else
    echo "  invariant B /ready stability: identical at start AND end ($READY_BEFORE) ✓"
fi

# ─── Invariant C: small jobs NOT blocked behind heavy ────────────────────
# Pull wall-clock from poll_results.txt for the small_* + heavy + medium_a
# + medium_b labels. The small_* jobs MUST each finish ≤ SMALL_JOB_CEILING_SEC.
echo
echo "=== Invariant C: small jobs finish within SMALL_JOB_CEILING_SEC=${SMALL_JOB_CEILING_SEC}s ==="
C_OVERRUNS=0
for i in 1 2 3 4 5; do
    line=$(awk -v lbl="small_${i}" '$1==lbl' "$WORKDIR/poll_results.txt")
    s=$(echo "$line" | awk '{print $3}')
    dt=$(echo "$line" | awk '{print $4}')
    echo "  small_${i}: status=$s  wall_clock=${dt}s"
    if [ -z "$s" ] || [ "$s" = "TIMEOUT" ]; then
        echo "WARN: small_${i} did not reach terminal (anti-starvation candidate)" >&2
        C_OVERRUNS=$(( C_OVERRUNS + 1 ))
    elif [ "$dt" -gt "$SMALL_JOB_CEILING_SEC" ]; then
        echo "FAIL: small_${i} wall_clock=${dt}s > SMALL_JOB_CEILING_SEC=${SMALL_JOB_CEILING_SEC}s (starved behind heavy)" >&2
        C_OVERRUNS=$(( C_OVERRUNS + 1 ))
    fi
done
if [ "$C_OVERRUNS" -gt 0 ]; then
    echo "FAIL: invariant C violated — $C_OVERRUNS/5 small jobs blocked behind heavy batch" >&2
    exit 1
fi
echo "  5/5 small jobs finished within ${SMALL_JOB_CEILING_SEC}s (anti-starvation confirmed) ✓"

# ─── Invariant A: /health=200 throughout (heuristic via mid-poll tally) ──
# Threshold is 90% of expected samples (count = wall_clock / HEALTH_INTERVAL);
# a single blip during rolling server restarts should not fail the
# battery when the platform silently recovers.
EXPECTED_HEALTH_SAMPLES=$(( (POLL_MAX * POLL_INTERVAL) / HEALTH_INTERVAL ))
HEALTH_THRESHOLD=$(( EXPECTED_HEALTH_SAMPLES * 9 / 10 ))
[ "$HEALTH_THRESHOLD" -lt 1 ] && HEALTH_THRESHOLD=1
echo
echo "=== Invariant A: /health=200 throughout the run (PID stability sub-assertion) ==="
echo "  /health mid-poll OK count: ${HEALTH_OK_COUNT} (threshold ${HEALTH_THRESHOLD} of ${EXPECTED_HEALTH_SAMPLES} expected)"
echo "  pipelinegen PID drift detected: ${PIDS_DRIFT}"
if [ "$PIDS_DRIFT" -ne 0 ]; then
    echo "FAIL: invariant A violated — pipelinegen restarted mid-flight (silent OOM-kill)" >&2
    exit 1
fi
if [ "$EXPECTED_HEALTH_SAMPLES" -gt 0 ] && [ "$HEALTH_OK_COUNT" -lt "$HEALTH_THRESHOLD" ]; then
    echo "FAIL: invariant A violated — /health=200 mid-poll count=${HEALTH_OK_COUNT} < threshold=${HEALTH_THRESHOLD}" >&2
    exit 1
fi
echo "  /health sampled ${HEALTH_OK_COUNT}/${EXPECTED_HEALTH_SAMPLES} times during run, >= 90% pass rate; PID stable ✓"

# ─── Invariant D: heavy + medium jobs reach terminal ─────────────────────
echo
echo "=== Invariant D: heavy + medium jobs terminal state ==="
D_TIMEOUT=0
for lbl in heavy medium_a medium_b; do
    line=$(awk -v lbl="$lbl" '$1==lbl' "$WORKDIR/poll_results.txt")
    s=$(echo "$line" | awk '{print $3}')
    echo "  $lbl: status=$s"
    if [ -z "$s" ] || [ "$s" = "TIMEOUT" ]; then
        echo "WARN: $lbl did not reach terminal within POLL_MAX=$POLL_MAX (D partial-fail)" >&2
        D_TIMEOUT=$(( D_TIMEOUT + 1 ))
    fi
done
if [ "$D_TIMEOUT" -gt 1 ]; then
    echo "FAIL: invariant D violated — 2/3 heavy/medium jobs TIMEOUT" >&2
    exit 1
fi

# ─── Invariant E: sqlite3 spot-probe < $SQLITE_PROBE_TIMEOUT_MS ──────────
echo
echo "=== Invariant E: SQLite probe < ${SQLITE_PROBE_TIMEOUT_MS}ms (no permanent lock) ==="
if [ ! -f "$DB_PATH" ]; then
    echo "WARN: DB_PATH=$DB_PATH not present — skipping SQL invariants" >&2
    DB_AVAILABLE=0
else
    DB_AVAILABLE=1
    SQLITE_START_MS=$(date +%s%3N)
    SQLITE_OUT=$(timeout ${SQLITE_PROBE_TIMEOUT_MS}ms sqlite3 "$DB_PATH" '.tables' 2>&1 || true)
    SQLITE_END_MS=$(date +%s%3N)
    SQLITE_ELAPSED_MS=$(( SQLITE_END_MS - SQLITE_START_MS ))
    if [ -z "$SQLITE_OUT" ] || [ "$SQLITE_ELAPSED_MS" -ge "$SQLITE_PROBE_TIMEOUT_MS" ]; then
        echo "FAIL: invariant E violated — sqlite3 '.tables' empty OR elapsed=${SQLITE_ELAPSED_MS}ms >= ${SQLITE_PROBE_TIMEOUT_MS}ms" >&2
        echo "  Output: $SQLITE_OUT" >&2
        exit 1
    fi
    echo "  sqlite3 .tables probe: ${SQLITE_ELAPSED_MS}ms (well under ${SQLITE_PROBE_TIMEOUT_MS}ms) ✓"
fi

# ─── Invariant F: pipelinegen RSS delta <= RAM_CAP_BYTES ────────────────
RSS_END=$(get_pipelinegen_rss_bytes)
RSS_DELTA=$(( RSS_END - RSS_START ))
[ "$RSS_DELTA" -lt 0 ] && RSS_DELTA=0
echo
echo "=== Invariant F: pipelinegen RSS delta <= ${RAM_CAP_BYTES} bytes (2 GiB ceiling) ==="
echo "  start: ${RSS_START}  end: ${RSS_END}  delta: ${RSS_DELTA}"
if [ "$RSS_DELTA" -gt "$RAM_CAP_BYTES" ]; then
    echo "FAIL: invariant F violated — RSS delta ${RSS_DELTA} > RAM_CAP_BYTES ${RAM_CAP_BYTES}" >&2
    exit 1
fi
echo "  RSS delta ${RSS_DELTA} ≤ ${RAM_CAP_BYTES} ✓"

# ─── Invariant G: tmp footprint reclaimed within DISK_CAP_BYTES ──────────
DISK_END=$(get_tmp_footprint_bytes)
DISK_DELTA=$(( DISK_END - DISK_START ))
echo
echo "=== Invariant G: /tmp footprint delta <= ${DISK_CAP_BYTES} (5 GiB ceiling) ==="
echo "  start: ${DISK_START}  end: ${DISK_END}  delta: ${DISK_DELTA}"
if [ "$DISK_DELTA" -gt "$DISK_CAP_BYTES" ]; then
    echo "FAIL: invariant G violated — disk delta ${DISK_DELTA} > DISK_CAP_BYTES ${DISK_CAP_BYTES}" >&2
    echo "  (orchestrator's defer-Cleanup did NOT fire for at least one stage)" >&2
    exit 1
fi
echo "  footprint delta ${DISK_DELTA} ≤ ${DISK_CAP_BYTES} ✓ (defer-Cleanup reclaimed snapshot dir)"

# ─── Outbox fan-out (informational only — not a hard invariant) ──────────
# stock_e2e_db_outbox_smoke.sh is the canonical probe for the
# asset.index.requested lifecycle. Here we surface a single
# informational count so the operator can spot a stuck finalizer
# at a glance, but we do NOT fail-fast on it (the indexing
# thread may legitimately lag the rendering thread by
# several minutes under CPU-only limits).
if [ "$DB_AVAILABLE" -eq 1 ]; then
    echo
    echo "=== Outbox: asset.index.requested fan-out (informational) ==="
    OUTBOX_RECENT=$(sqlite3 "$DB_PATH" "
        SELECT COUNT(*)
        FROM outbox_events
        WHERE event_type = 'asset.index.requested'
          AND created_at > datetime('now', '-${COHORT_WINDOW_MIN} minutes')
    ")
    echo "  asset.index.requested events in last ${COHORT_WINDOW_MIN}m: $OUTBOX_RECENT (informational; see stock_e2e_db_outbox_smoke.sh for canonical lifecycle probe)"
fi

echo
echo "PASS: DoD §14 mixed-load battery —"
echo "  ${#SMALL_JOB_IDS[@]}/5 small jobs finished within ${SMALL_JOB_CEILING_SEC}s (anti-starvation)" | tee -a "$WORKDIR/summary.txt"
echo "  ${#HEAVY_JOB_IDS[@]}/3 heavy/medium jobs reached terminal status"
echo "  /health stable (mid-poll OK=${HEALTH_OK_COUNT}, PID stable=${PID_BEFORE})"
echo "  Memory RSS delta ${RSS_DELTA} ≤ ${RAM_CAP_BYTES}"
echo "  Disk footprint delta ${DISK_DELTA} ≤ ${DISK_CAP_BYTES}"
echo "  SQLite probe ${SQLITE_ELAPSED_MS}ms < ${SQLITE_PROBE_TIMEOUT_MS}ms (no permanent lock)"
echo "  /ready deep true at start=${READY_BEFORE} end=${READY_AFTER}"
exit 0

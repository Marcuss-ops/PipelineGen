#!/usr/bin/env bash
# scripts/bench/generate-video.sh — canonical PipelineGen video benchmark runner.
#
# Full pipeline benchmark: generate → render (watermark + subtitles + Chronon)
# → Drive upload. Timing is read from the job SSOT reports; this script only
# aggregates and derives batch-level values.
#
# Modes:
#   --topic TEXT        Generate new clips from topic via /api/script/generate
#   --clip-id ID        Render existing clip assets via /api/clips/render
#                       (can be specified multiple times)
#
# Prerequisites:
#   - PipelineGen server running and /ready == 200
#   - Preflight green (run `scripts/preflight-e2e.sh` or `make preflight-e2e`)
#   - VELOX_ADMIN_TOKEN set (for job submission + polling)
#
# Usage:
#   # Generate from topic (script → render → drive)
#   ./scripts/bench/generate-video.sh --topic "Matt Damon" --clips 5
#
#   # Render existing clip IDs directly
#   ./scripts/bench/generate-video.sh --clip-id asset_abc123 --clip-id asset_def456
#
#   # Mixed: generate + render specific assets
#   ./scripts/bench/generate-video.sh --topic "Dune" --clip-id asset_xyz789
#
# Exit codes: 0 benchmark complete; 1 preflight failed; 2 bad arguments.
set -Eeuo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"

# ── Load environment defaults ──────────────────────────────────────────────
# shellcheck source=scripts/lib/dotenv.sh
source "$ROOT_DIR/scripts/lib/dotenv.sh"
load_dotenv_missing "$ROOT_DIR/.env"

# ── Defaults ───────────────────────────────────────────────────────────────
BASE_URL="${BENCH_BASE_URL:-http://127.0.0.1:${VELOX_PORT:-8000}}"
ADMIN_TOKEN="${VELOX_ADMIN_TOKEN:-}"
OUTPUT_DIR="${BENCH_OUTPUT_DIR:-$ROOT_DIR/out}"
POLL_INTERVAL="${BENCH_POLL_INTERVAL:-2}"
POLL_MAX="${BENCH_POLL_MAX:-300}"  # max 5 minutes per job
TOPIC=""
CLIPS=5
OUTPUT_FILE=""
PREFLIGHT=1
VERBOSE=0
WATERMARK_ASSET_ID="${BENCH_WATERMARK_ASSET_ID:-}"
WATERMARK_TEXT="${BENCH_WATERMARK_TEXT:-}"
DRIVE_FOLDER_ID="${BENCH_DRIVE_FOLDER_ID:-}"
# Optional worker claim concurrency (slots) for the concurrency report;
# empty means unknown (report shows "-" and no utilization).
WORKER_SLOTS="${BENCH_WORKER_SLOTS:-}"

# Arrays for --clip-id accumulation
CLIP_IDS=()

# ── Argument parsing ───────────────────────────────────────────────────────
usage() {
    cat >&2 <<'USAGE'
Usage: generate-video.sh [OPTIONS]

Modes (at least one required):
  --topic TEXT        Generate new clips from topic via /api/script/generate
  --clip-id ID        Render existing clip asset via /api/clips/render
                      (repeatable: --clip-id A --clip-id B)

Options:
  --clips N           Number of clips to generate from topic (default: 5)
  --output FILE       Output JSON report file (default: out/benchmark-<ts>.json)
  --base-url URL      PipelineGen server URL (default: http://127.0.0.1:8000)
  --watermark ID      Watermark asset ID to apply during render
  --watermark-text T  Text watermark to apply during render (e.g. test)
  --drive-folder ID   Drive folder ID for upload destination
  --worker-slots N    Worker claim concurrency (slots) for the concurrency
                      report; when set, utilization = peak_running/slots
  --no-preflight      Skip preflight check (for debugging only)
  --verbose           Print poll status on stderr
  --help              Show this help

Pipeline stages timed:
  1. submit    — HTTP round-trip to enqueue the job
  2. generate  — script generation (text → script + scenes)
  3. render    — clip.render (watermark + subtitles + Chronon/Vulkan)
  4. drive     — Drive artifact upload + verification
  5. total     — wall-clock from first submit to last terminal

Report: per-phase wall_ms / work_ms / critical_path_ms (LLM, render, Drive)
plus the real worker facts already exposed in the job result (transcript,
timings, render.backend, gpu_copy_bytes). No synthetic 60/40 splits: phases
come from /api/jobs/{id}/full (result + RunReport timing).

Critical path semantics:
  - script.generate jobs: the RunReport critical path (ordered chain of
    top-level sequential stages) is the per-job serial chain.
  - clip.render jobs: the worker serial chain prepare → render → drive →
    finalize (each measured phase's wall is its critical-path contribution).
  - The batch critical path is the union of per-job phase windows on the
    batch clock; the batch bottleneck is the phase with the largest
    critical-path share — never the phase with the most accumulated work.
USAGE
    exit 2
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --topic)        TOPIC="$2"; shift 2 ;;
        --clip-id)      CLIP_IDS+=("$2"); shift 2 ;;
        --clips)        CLIPS="$2"; shift 2 ;;
        --output)       OUTPUT_FILE="$2"; shift 2 ;;
        --base-url)     BASE_URL="$2"; shift 2 ;;
        --watermark)    WATERMARK_ASSET_ID="$2"; shift 2 ;;
        --watermark-text) WATERMARK_TEXT="$2"; shift 2 ;;
        --drive-folder) DRIVE_FOLDER_ID="$2"; shift 2 ;;
        --worker-slots) WORKER_SLOTS="$2"; shift 2 ;;
        --no-preflight) PREFLIGHT=0; shift ;;
        --verbose)      VERBOSE=1; shift ;;
        --help)         usage ;;
        *)              echo "Unknown option: $1" >&2; usage ;;
    esac
done

# Validate: at least one mode
if [[ -z "$TOPIC" ]] && (( ${#CLIP_IDS[@]} == 0 )); then
    echo "ERROR: provide --topic or --clip-id (or both)" >&2
    usage
fi

[[ -n "$OUTPUT_FILE" ]] || OUTPUT_FILE="$OUTPUT_DIR/benchmark-$(date -u +%Y%m%dT%H%M%S).json"
mkdir -p "$(dirname "$OUTPUT_FILE")"

# Per-job /full responses (result + RunReport timing) are staged here so the
# report emitter (Stage 3/4) parses real worker data — never fake splits.
JOBS_DIR="$OUTPUT_DIR/.bench-jobs"
rm -rf "$JOBS_DIR"
mkdir -p "$JOBS_DIR"

# ── Preflight gate ─────────────────────────────────────────────────────────
if [[ "$PREFLIGHT" == "1" ]]; then
    echo "[bench] Running preflight..."
    PREFLIGHT_BASE_URL="$BASE_URL" PREFLIGHT_REQUIRE_MAIN=0 \
        bash "$ROOT_DIR/scripts/preflight-e2e.sh" || {
        echo "[bench] ❌ Preflight failed. Aborting." >&2
        exit 1
    }
fi

# ── Auth guard ─────────────────────────────────────────────────────────────
if [[ -z "$ADMIN_TOKEN" ]]; then
    echo "[bench] ERROR: VELOX_ADMIN_TOKEN not set" >&2
    exit 1
fi

# ── Environment fingerprint (full capture) ────────────────────────────────
FINGERPRINT_DIR="$OUTPUT_DIR/.bench-fingerprints"
mkdir -p "$FINGERPRINT_DIR"
FINGERPRINT_FILE="$FINGERPRINT_DIR/fingerprint-$(date -u +%Y%m%dT%H%M%S).json"

FINGERPRINT_BASE_URL="$BASE_URL" \
FINGERPRINT_DB_PATH="${PREFLIGHT_DB_PATH:-$ROOT_DIR/data/pipelinegen.db}" \
    bash "$ROOT_DIR/scripts/bench/capture-fingerprint.sh" > "$FINGERPRINT_FILE" 2>/dev/null || true

# Extract key fields for the banner
GIT_SHA=$(python3 -c "import json; d=json.load(open('$FINGERPRINT_FILE')); print(d['git']['sha'])" 2>/dev/null || echo "unknown")
GIT_BRANCH=$(python3 -c "import json; d=json.load(open('$FINGERPRINT_FILE')); print(d['git']['branch'])" 2>/dev/null || echo "detached")
CONFIG_SHA=$(python3 -c "import json; d=json.load(open('$FINGERPRINT_FILE')); print(d['config_sha'])" 2>/dev/null || echo "absent")
DB_SHA=$(python3 -c "import json; d=json.load(open('$FINGERPRINT_FILE')); print(d['database']['primary_sha'])" 2>/dev/null || echo "absent")
WORKER_IDS=$(python3 -c "import json; d=json.load(open('$FINGERPRINT_FILE')); print(','.join(d['workers']['ids']))" 2>/dev/null || echo "")
WORKER_VERSIONS=$(python3 -c "import json; d=json.load(open('$FINGERPRINT_FILE')); print(','.join(d['workers']['versions']))" 2>/dev/null || echo "")
CHRONON_SHA=$(python3 -c "import json; d=json.load(open('$FINGERPRINT_FILE')); print(d['chronon']['sha256'][:12])" 2>/dev/null || echo "absent")
QDRANT_COLLECTION=$(python3 -c "import json; d=json.load(open('$FINGERPRINT_FILE')); print(d['qdrant']['active_collection'])" 2>/dev/null || echo "absent")
QDRANT_POINTS=$(python3 -c "import json; d=json.load(open('$FINGERPRINT_FILE')); print(d['qdrant']['point_count'])" 2>/dev/null || echo "0")

# Legacy fields for the JSON report (backward compat)
GIT_SHA_FULL="$GIT_SHA"
DB_PATH="${PREFLIGHT_DB_PATH:-$ROOT_DIR/data/pipelinegen.db}"

echo "════════════════════════════════════════════════════════════════"
echo "  PIPELINEGEN BENCHMARK — SETUP"
echo "════════════════════════════════════════════════════════════════"
printf "  %-30s %s\n" "Git SHA:" "${GIT_SHA:0:12}"
printf "  %-30s %s\n" "Git branch:" "$GIT_BRANCH"
printf "  %-30s %s\n" "Config SHA:" "${CONFIG_SHA:0:12}"
printf "  %-30s %s\n" "DB SHA:" "${DB_SHA:0:12}"
printf "  %-30s %s\n" "Worker IDs:" "${WORKER_IDS:-<none>}"
printf "  %-30s %s\n" "Worker versions:" "${WORKER_VERSIONS:-<none>}"
printf "  %-30s %s\n" "Chronon SHA:" "${CHRONON_SHA:-absent}"
printf "  %-30s %s (%s pts)\n" "Qdrant collection:" "$QDRANT_COLLECTION" "$QDRANT_POINTS"
printf "  %-30s %s\n" "Base URL:" "$BASE_URL"
printf "  %-30s %s\n" "Fingerprint:" "$FINGERPRINT_FILE"
printf "  %-30s %s\n" "Output:" "$OUTPUT_FILE"
echo "════════════════════════════════════════════════════════════════"
echo ""

# ── Helper functions ───────────────────────────────────────────────────────
api() {
    local method="$1" path="$2"; shift 2
    curl -fsS --max-time 60 \
        -X "$method" \
        -H "Authorization: Bearer $ADMIN_TOKEN" \
        -H "Content-Type: application/json" \
        "$@" \
        "$BASE_URL$path"
}

api_raw() {
    # Returns HTTP body + status code; does not fail on non-2xx.
    local method="$1" path="$2"; shift 2
    curl -sS --max-time 60 \
        -X "$method" \
        -H "Authorization: Bearer $ADMIN_TOKEN" \
        -H "Content-Type: application/json" \
        -w '\n__HTTP_CODE__%{http_code}' \
        "$@" \
        "$BASE_URL$path"
}

ms_now() {
    # Used only for polling/submission bookkeeping and output filenames. It is
    # never used as an authoritative execution or render timer.
    python3 -c 'import time; print(int(time.time()*1000))'
}

log_v() {
    [[ "$VERBOSE" == "1" ]] && echo "[bench] $*" >&2 || true
}

json_field() {
    # Extract a field from JSON stdin. $1 = field name, $2 = default.
    python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    print(d.get('$1', '$2'))
except Exception:
    print('$2')
"
}

json_field_int() {
    python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    v = d.get('$1', '$2')
    print(int(v) if v else '$2')
except Exception:
    print('$2')
"
}

poll_job() {
    # Poll a job until terminal. Returns status + timing JSON on stdout.
    # $1 = job_id, $2 = start_ms, $3 = label
    local jid="$1" start_ms="$2" label="$3"
    local polled=0 status="unknown" elapsed=0

    while (( polled < POLL_MAX )); do
        RESP=$(api GET "/api/jobs/$jid" 2>/dev/null || echo '{}')
        STATUS=$(echo "$RESP" | json_field "status" "unknown" | tr '[:upper:]' '[:lower:]')
        ELAPSED=$(( $(ms_now) - start_ms ))

        log_v "  [$jid] status=$STATUS elapsed=${ELAPSED}ms"

        case "$STATUS" in
            completed|succeeded|failed|cancelled)
                echo "$STATUS"
                return 0
                ;;
        esac

        polled=$(( polled + POLL_INTERVAL ))
        sleep "$POLL_INTERVAL"
    done

    echo "timeout"
    return 0
}

# ── Build work list ────────────────────────────────────────────────────────
# Each entry: "mode:topic_or_id"  mode = "generate" | "render"
WORK_LIST=()

for cid in "${CLIP_IDS[@]}"; do
    WORK_LIST+=("render:${cid}")
done

if [[ -n "$TOPIC" ]]; then
    for ((i=1; i<=CLIPS; i++)); do
        WORK_LIST+=("generate:${TOPIC} #$i")
    done
fi

echo "[bench] Work items: ${#WORK_LIST[@]}"
echo ""

# ── Stage 1: Submit all jobs ──────────────────────────────────────────────
echo "[bench] ═══ STAGE 1: SUBMIT ═══"

# Local clocks are used only for operational polling, never for benchmark metrics.

# Per-job identity/status only. Timing is never measured locally: all report
# metrics come from the fetched job RunReport/result SSOT.
declare -a J_JOB_IDS=()
declare -a J_LABELS=()
declare -a J_MODES=()
declare -a J_STATUS=()

SUCCESS_COUNT=0

for entry in "${WORK_LIST[@]}"; do
    MODE="${entry%%:*}"
    PAYLOAD_ARG="${entry#*:}"
    SUBMIT_T0=""

    if [[ "$MODE" == "generate" ]]; then
        # ── Generate: POST /api/script/generate ──────────────────────────
        PAYLOAD=$(cat <<EOJSON
{
  "version": 2,
  "preset": "custom",
  "items": [{
    "id": "bench-$(ms_now)",
    "title": "Benchmark: ${PAYLOAD_ARG}",
    "language": "en",
    "source": {"type": "text", "topic": "${PAYLOAD_ARG}"},
    "script_params": {"target_words": 500},
    "output": {"generate_scene_images": false}
  }]
}
EOJSON
)
        RESP=$(api POST "/api/script/generate" -d "$PAYLOAD" 2>/dev/null || echo '{"error":"submit_failed"}')
        JOB_ID=$(echo "$RESP" | json_field "job_id" "")

        if [[ -z "$JOB_ID" ]]; then
            echo "[bench] ❌ Submit failed for '$PAYLOAD_ARG'"
            J_JOB_IDS+=("")
            J_LABELS+=("$PAYLOAD_ARG")
            J_MODES+=("generate")
            : # submission timing is transport-only and never enters the report
            J_STATUS+=("submit_failed")
            continue
        fi

        echo "[bench] ✅ [generate] $JOB_ID ← '$PAYLOAD_ARG'"
        J_JOB_IDS+=("$JOB_ID")
        J_LABELS+=("$PAYLOAD_ARG")
        J_MODES+=("generate")
        : # submission timing is transport-only and never enters the report
        J_STATUS+=("submitted")

    elif [[ "$MODE" == "render" ]]; then
        # ── Render: POST /api/clips/render ───────────────────────────────
        ASSET_ID="$PAYLOAD_ARG"
        WM_BLOCK="{}"
        if [[ -n "$WATERMARK_ASSET_ID" ]]; then
            WM_BLOCK="{\"enabled\":true,\"asset_id\":\"${WATERMARK_ASSET_ID}\",\"position\":\"top_right\",\"opacity\":0.25}"
        elif [[ -n "$WATERMARK_TEXT" ]]; then
            WM_BLOCK="{\"enabled\":true,\"text\":\"${WATERMARK_TEXT}\",\"position\":\"top_right\",\"opacity\":0.25}"
        fi
        DEST_BLOCK="{}"
        if [[ -n "$DRIVE_FOLDER_ID" ]]; then
            DEST_BLOCK="{\"drive_folder_id\":\"${DRIVE_FOLDER_ID}\"}"
        fi

        PAYLOAD=$(cat <<EOJSON
{
  "source_asset_id": "${ASSET_ID}",
  "background": {"mode": "none"},
  "watermark": ${WM_BLOCK},
  "transcript": {"mode": "reuse_or_generate", "language": "en"},
  "subtitles": {"enabled": true, "mode": "burn"},
  "output": {"contract": "VELOX_ASSEMBLY_READY_V1", "width": 1920, "height": 1080, "fps_num": 24, "fps_den": 1},
  "audio": {"mode": "copy_if_compatible"},
  "destination": ${DEST_BLOCK},
  "execution": {"require_gpu": false}
}
EOJSON
)
        RESP=$(api POST "/api/clips/render" -d "$PAYLOAD" 2>/dev/null || echo '{"error":"submit_failed"}')
        JOB_ID=$(echo "$RESP" | json_field "job_id" "")

        if [[ -z "$JOB_ID" ]]; then
            echo "[bench] ❌ Submit failed for clip $ASSET_ID"
            J_JOB_IDS+=("")
            J_LABELS+=("$ASSET_ID")
            J_MODES+=("render")
            : # submission timing is transport-only and never enters the report
            J_STATUS+=("submit_failed")
            continue
        fi

        echo "[bench] ✅ Render $JOB_ID ← clip $ASSET_ID"
        J_JOB_IDS+=("$JOB_ID")
        J_LABELS+=("$ASSET_ID")
        J_MODES+=("render")
        : # submission timing is transport-only and never enters the report
        J_STATUS+=("submitted")
    fi
done

echo ""
echo "[bench] All jobs submitted"

# ── Stage 2: Poll for completion ──────────────────────────────────────────
echo ""
echo "[bench] ═══ STAGE 2: POLL ═══"

POLLED=0
while true; do
    ALL_DONE=1
    for i in "${!J_JOB_IDS[@]}"; do
        JID="${J_JOB_IDS[$i]}"
        [[ -n "$JID" ]] || continue
        CUR="${J_STATUS[$i]}"
        [[ "$CUR" == "completed" || "$CUR" == "succeeded" || "$CUR" == "failed" || "$CUR" == "cancelled" || "$CUR" == "submit_failed" || "$CUR" == "timeout" ]] && continue

        RESP=$(api GET "/api/jobs/$JID" 2>/dev/null || echo '{}')
        STATUS=$(echo "$RESP" | json_field "status" "unknown" | tr '[:upper:]' '[:lower:]')
        ELAPSED=""

        log_v "  [$JID] status=$STATUS elapsed=${ELAPSED}ms"

        case "$STATUS" in
            completed|succeeded|failed|cancelled)
                J_STATUS[$i]="$STATUS"
                echo "[bench] Job $JID → $STATUS"
                ;;
            *)
                ALL_DONE=0
                ;;
        esac
    done

    [[ "$ALL_DONE" == "1" ]] && break

    POLLED=$(( POLLED + POLL_INTERVAL ))
    if (( POLLED >= POLL_MAX )); then
        echo "[bench] ⚠️  Poll timeout (${POLL_MAX}s). Marking remaining as timeout." >&2
        for i in "${!J_JOB_IDS[@]}"; do
            JID="${J_JOB_IDS[$i]}"
            [[ -n "$JID" ]] || continue
            CUR="${J_STATUS[$i]}"
            [[ "$CUR" == "completed" || "$CUR" == "succeeded" || "$CUR" == "failed" || "$CUR" == "cancelled" || "$CUR" == "submit_failed" || "$CUR" == "timeout" ]] && continue
            J_STATUS[$i]="timeout"
        done
        break
    fi

    sleep "$POLL_INTERVAL"
done

# ── Stage 3: Fetch /api/jobs/{id}/full (result + RunReport timing) ────────
# Real facts only: the /full response carries the sealed job result
# (transcript, timings, render.backend, gpu_copy_bytes, metrics_v2) plus the
# RunReport timing summary (stages, operations, critical_path, fanout). The
# report emitter parses these files — no synthetic 60/40 splits.
echo ""
echo "[bench] ═══ STAGE 3: FETCH /full (result + timing) ═══"


for i in "${!J_JOB_IDS[@]}"; do
    JID="${J_JOB_IDS[$i]}"
    if [[ -n "$JID" ]] && [[ "${J_STATUS[$i]}" != "submit_failed" ]]; then
        api GET "/api/jobs/$JID/full" > "$JOBS_DIR/job-$i.json" 2>/dev/null || echo '{}' > "$JOBS_DIR/job-$i.json"
        log_v "  [$JID] /full detail → job-$i.json ($(wc -c < "$JOBS_DIR/job-$i.json") bytes)"
    else
        echo '{}' > "$JOBS_DIR/job-$i.json"
    fi
    if [[ "${J_STATUS[$i]}" == "completed" || "${J_STATUS[$i]}" == "succeeded" ]]; then
        SUCCESS_COUNT=$(( SUCCESS_COUNT + 1 ))
    fi
done

TOTAL_ELAPSED=0 # legacy CLI argument; excluded from all metrics

# ── Stage 4: Emit wall / work / critical-path report ──────────────────────
echo ""
echo "[bench] ═══ STAGE 4: REPORT (wall / work / critical path) ═══"

python3 - "$OUTPUT_FILE" "$FINGERPRINT_FILE" \
    "$GIT_SHA" "$GIT_BRANCH" "$CONFIG_SHA" "$DB_SHA" "$WORKER_IDS" "$BASE_URL" \
    "0" "0" "0" \
    "$SUCCESS_COUNT" "${#J_JOB_IDS[@]}" "$JOBS_DIR" \
    "${J_JOB_IDS[@]}" "${J_LABELS[@]}" "${J_MODES[@]}" "${J_STATUS[@]}" \
    "$WORKER_SLOTS" <<'PYEOF'
import json
import os
import sys
from collections import Counter
from datetime import datetime, timezone

args = sys.argv[1:]
idx = 0


def grab(n=1):
    global idx
    v = args[idx:idx + n]
    idx += n
    return v[0] if n == 1 else v


out_file = grab()
fingerprint_file = grab()
git_sha, git_branch, config_sha, db_sha, worker_ids, base_url = grab(6)
_legacy_start, _legacy_end, _legacy_elapsed = [int(x) for x in grab(3)]
success_count, n_jobs = int(grab()), int(grab())
jobs_dir = grab()

j_ids = grab(n_jobs)
j_labels = grab(n_jobs)
j_modes = grab(n_jobs)
j_statuses = grab(n_jobs)
# Optional worker slots (claim concurrency) from BENCH_WORKER_SLOTS; empty
# means unknown (the report then shows "-" and no utilization).
worker_slots_raw = grab()
try:
    worker_slots = int(worker_slots_raw)
except (TypeError, ValueError):
    worker_slots = 0


def load_full(i):
    p = os.path.join(jobs_dir, "job-%d.json" % i)
    if not os.path.isfile(p):
        return {}
    try:
        with open(p) as f:
            return json.load(f)
    except Exception:
        return {}


def ms(v):
    if isinstance(v, bool):
        return 0
    try:
        return int(v)
    except (TypeError, ValueError):
        return 0


def sentinel_ok(v):
    # metrics_v2 NOT_INSTRUMENTED sentinel serializes as the string
    # "NOT_INSTRUMENTED" (and -1 in-process). Only a real measured value
    # (>= 0) counts.
    if isinstance(v, str):
        return False
    try:
        return int(v) >= 0
    except (TypeError, ValueError):
        return False


def render_work(metrics):
    # Coarse backend (ffmpeg_fallback / cuda_native): composite_ms covers the
    # decode→encode span, so render WORK = startup + composite. A per-phase
    # backend instead sums every instrumented phase.
    if sentinel_ok(metrics.get("composite_ms")):
        return ms(metrics.get("renderer_startup_ms")) + ms(metrics.get("composite_ms"))
    total = 0
    for k in ("decode_ms", "composite_ms", "subtitle_raster_ms", "watermark_raster_ms",
              "frame_conversion_ms", "encode_ms", "audio_mux_ms"):
        if sentinel_ok(metrics.get(k)):
            total += ms(metrics.get(k))
    return total


def materialization(timings, result):
    # Per-asset materialization facts straight from the preparer's PhaseTiming
    # list (now exposed on the job result as timings.phases): each
    # materialize_<asset> phase carries wall_ms / work_ms plus notes with
    # from_cache and size_bytes. This is the real "bring assets to disk"
    # cost per asset — cache hits (from_cache=true) are visible as cheap
    # phases, and the bytes tell whether Drive was actually read.
    mat = {}
    for ph in timings.get("phases") or []:
        name = ph.get("phase", "")
        if not name.startswith("materialize_"):
            continue
        asset = name[len("materialize_"):]
        notes = ph.get("notes") or {}
        size_bytes = ms(notes.get("size_bytes"))
        mat[asset] = {
            "wall_ms": ms(ph.get("wall_ms")),
            "work_ms": ms(ph.get("work_ms")),
            "from_cache": bool(notes.get("from_cache")),
            "cache_hit": bool(notes.get("cache_hit", notes.get("from_cache"))),
            "size_bytes": size_bytes,
            "download_bytes": ms(notes.get("download_bytes", 0 if notes.get("from_cache") else size_bytes)),
            "asset_id": notes.get("asset_id", ""),
        }
    # Fallback when the server predates timings.phases: derive source facts
    # from the result's source block (from_cache + size_bytes) with the
    # materialize wall already folded into asset_materialize_ms.
    if not mat:
        src = result.get("source") or {}
        if src:
            mat["source"] = {
                "wall_ms": 0, "work_ms": 0,
                "from_cache": bool(src.get("from_cache")),
                "cache_hit": bool(src.get("cache_hit", src.get("from_cache"))),
                "size_bytes": ms(src.get("size_bytes")),
                "download_bytes": ms(src.get("download_bytes", 0 if src.get("from_cache") else src.get("size_bytes"))),
                "asset_id": src.get("asset_id", ""),
            }
    return mat


def phase(j, name):
    for p in j["phases"]:
        if p["name"] == name:
            return p
    return None


def fmt_sec(v):
    if v <= 0:
        return "-"
    return f"{v/1000:.1f}s"


def fmt_work(p):
    if not p:
        return "-"
    return "%s/%s" % (fmt_sec(ms(p.get("wall_ms"))), fmt_sec(ms(p.get("work_ms"))))


def build_job(full, i):
    result = full.get("result") or {}
    timing = full.get("timing") or {}
    phases = []

    # ── RunReport facts (LLM / tts / audio / drive / finalize) ──────────
    stages = {}
    for s in timing.get("stages") or []:
        stages[s.get("name", "")] = ms(s.get("duration_ms"))
    cp = {}
    cp_order = []
    for c in timing.get("critical_path") or []:
        cp[c.get("name", "")] = ms(c.get("duration_ms"))
        cp_order.append(c.get("name", ""))
    llm_work = llm_calls = 0
    tts_work = tts_calls = 0
    audio_work = 0
    drive_work = 0
    drive_download_ms = drive_download_work = drive_upload_ms = drive_upload_work = 0
    doc_publish_ms = doc_publish_work = 0
    # publish_wall is assigned by the clip.render envelope branch; default it
    # so a job with no render facts (missing result file, e.g.) never crashes
    # the report at the drive dict.
    publish_wall = 0
    for op in timing.get("operations") or []:
        comp = op.get("component", "")
        opname = op.get("operation", "")
        w = ms(op.get("work_ms"))
        c = ms(op.get("calls"))
        if comp == "ollama":
            llm_work += w
            llm_calls += c
        elif comp == "tts":
            tts_work += w
            tts_calls += c
        elif comp == "rust" and opname == "audio_render":
            audio_work += w
        elif comp == "audio" and opname in ("audio_plan_compile", "mix", "aac_encode", "probe", "hash"):
            audio_work += w
        elif comp in ("drive", "google_docs") or opname in ("upload", "publish"):
            drive_work += w
            if opname in ("download", "fetch", "materialize"):
                drive_download_ms += ms(op.get("duration_ms"))
                drive_download_work += w
            elif comp == "google_docs" or opname in ("document.publish", "doc_publish"):
                doc_publish_ms += ms(op.get("duration_ms"))
                doc_publish_work += w
            else:
                drive_upload_ms += ms(op.get("duration_ms"))
                drive_upload_work += w
    # Ollama split facts come from the operation metadata merged by
    # TimingOperation (numeric values summed across calls, cold_start counted,
    # model kept when uniform). queue_wait_ms is the accumulated queue wait.
    llm = {}
    for op in timing.get("operations") or []:
        if op.get("component") == "ollama" and op.get("operation") == "generate":
            meta = op.get("metadata") or {}
            model = meta.get("model")
            if not isinstance(model, str):
                model = ""
            llm = {
                "calls": ms(op.get("calls")),
                "queue_wait_ms": ms(op.get("queue_wait_ms")),
                "work_ms": ms(op.get("work_ms")),
                "model": model,
                "input_tokens": ms(meta.get("input_tokens")),
                "output_tokens": ms(meta.get("output_tokens")),
                "model_load_ms": ms(meta.get("model_load_ms")),
                "prompt_eval_ms": ms(meta.get("prompt_eval_ms")),
                "inference_wall_ms": ms(meta.get("inference_wall_ms")),
                "inference_work_ms": ms(meta.get("inference_work_ms")),
                "tokens_per_second": ms(meta.get("tokens_per_second")),
                "cold_start": ms(meta.get("cold_start")),
            }
            break
    if "generate" in stages or llm_work > 0:
        phases.append({"name": "llm", "wall_ms": stages.get("generate", 0), "work_ms": llm_work,
                       "critical_ms": cp.get("generate", 0), "calls": llm_calls, "parallel": False})
    if "tts" in stages or tts_work > 0:
        phases.append({"name": "tts", "wall_ms": stages.get("tts", 0), "work_ms": tts_work,
                       "critical_ms": cp.get("tts", 0), "calls": tts_calls, "parallel": False})
    if "audio_compile" in stages:
        phases.append({"name": "audio", "wall_ms": stages.get("audio_compile", 0), "work_ms": audio_work,
                       "critical_ms": cp.get("audio_compile", 0), "calls": 0, "parallel": False})
    if drive_work > 0 or stages.get("document.publish", 0) > 0:
        phases.append({"name": "drive", "wall_ms": stages.get("document.publish", 0) or stages.get("document", 0),
                       "work_ms": drive_work, "critical_ms": cp.get("document.publish", 0) or cp.get("document", 0),
                       "calls": 0, "parallel": False})
    if "post_writer_finalize" in stages:
        # post_writer_finalize is a top-level serial stage (recorded by the
        # worker runner after the handler returns), so its wall IS its
        # critical-path contribution; the RunReport critical path is only
        # consulted when present.
        fw = stages.get("post_writer_finalize", 0)
        phases.append({"name": "finalize", "wall_ms": fw, "work_ms": 0,
                       "critical_ms": cp.get("post_writer_finalize", fw), "calls": 0, "parallel": False})

    # ── clip.render result facts (prepare / render / drive) ─────────────
    timings = result.get("timings") or {}
    render = result.get("render") or {}
    metrics = render.get("metrics_v2") or {}
    # The RunReport clip.* stages (recorded by the worker since the
    # observability instrumentation) are the SSOT for the clip.render serial
    # chain: clip.prepare → clip.subtitles → clip.render → clip.probe →
    # clip.overlay → clip.publish. Each stage's wall is its critical-path
    # contribution. Fall back to the result envelope only for servers that
    # predate the stage instrumentation.
    clip_stages = {s.get("name", ""): ms(s.get("duration_ms")) for s in timing.get("stages") or []
                   if str(s.get("name", "")).startswith("clip.")}
    if clip_stages:
        prep_wall = clip_stages.get("clip.prepare", 0)
        prep_work = ms(timings.get("total_work_ms"))
        if prep_wall > 0 or prep_work:
            phases.append({"name": "prepare", "wall_ms": prep_wall, "work_ms": prep_work,
                           "critical_ms": prep_wall, "calls": 0, "parallel": bool(timings.get("parallel"))})
        if clip_stages.get("clip.subtitles", 0) > 0:
            phases.append({"name": "subs", "wall_ms": clip_stages["clip.subtitles"], "work_ms": 0,
                           "critical_ms": clip_stages["clip.subtitles"], "calls": 0, "parallel": False})
        # Render-side serial chain: render + probe + overlay certification are
        # one contiguous wall span inside the worker.
        render_wall = (clip_stages.get("clip.render", 0) + clip_stages.get("clip.probe", 0)
                       + clip_stages.get("clip.overlay", 0))
        if render_wall > 0 or render:
            phases.append({"name": "render", "wall_ms": render_wall, "work_ms": render_work(metrics),
                           "critical_ms": render_wall, "calls": 0, "parallel": False})
        if clip_stages.get("clip.publish", 0) > 0:
            pw = clip_stages["clip.publish"]
            phases.append({"name": "drive", "wall_ms": pw, "work_ms": pw,
                           "critical_ms": pw, "calls": 0, "parallel": False})
    elif timings or render:
        prep_wall = ms(timings.get("total_wall_ms"))
        prep_work = ms(timings.get("total_work_ms"))
        publish_wall = ms(metrics.get("publication_total_ms")) if sentinel_ok(metrics.get("publication_total_ms")) else 0
        if publish_wall == 0:
            # Legacy fallback: publish_ms is the deprecated Rust-side local
            # finalize rename, never the publication wall.
            publish_wall = ms(metrics.get("renderer_finalize_ms")) if sentinel_ok(metrics.get("renderer_finalize_ms")) else ms(metrics.get("publish_ms")) if sentinel_ok(metrics.get("publish_ms")) else 0
        # render_wall_ms is exposed both on the render block (worker_result.go)
        # and inside metrics_v2 — the same worker-owned value; prefer the V2
        # report and keep the render block as the legacy fallback.
        rw = ms(metrics.get("render_wall_ms")) if sentinel_ok(metrics.get("render_wall_ms")) else None
        if rw is None:
            rw = ms(render.get("render_wall_ms")) if sentinel_ok(render.get("render_wall_ms")) else None
        if prep_wall or prep_work:
            # prepare / render / drive are strictly sequential inside the
            # clip.render worker (preparer → renderer → publisher), so each
            # phase's measured wall IS its critical-path contribution within
            # the job — never 0 and never the accumulated work.
            phases.append({"name": "prepare", "wall_ms": prep_wall, "work_ms": prep_work,
                           "critical_ms": prep_wall, "calls": 0, "parallel": bool(timings.get("parallel"))})
        if render or metrics:
            if rw is None:
                # Fallback for servers without render_wall_ms: derive from the
                # job wall minus the render phase + publish walls (includes
                # backend selection + unaccounted).
                job_wall = ms(timing.get("execution_wall_ms", timing.get("wall_ms")))
                rw = max(0, job_wall - prep_wall - publish_wall)
            phases.append({"name": "render", "wall_ms": rw, "work_ms": render_work(metrics),
                           "critical_ms": rw, "calls": 0, "parallel": False})
        if publish_wall > 0:
            phases.append({"name": "drive", "wall_ms": publish_wall, "work_ms": publish_wall,
                           "critical_ms": publish_wall, "calls": 0, "parallel": False})

    # Order phases by the job's REAL critical path.
    # - clip.render jobs: the worker serial chain is prepare → render →
    #   drive → post_writer_finalize (enforced explicitly — the RunReport
    #   cp_order for such jobs only contains post_writer_finalize and would
    #   otherwise sort it first).
    # - RunReport-backed jobs (script.generate): use the ordered chain of
    #   top-level sequential stages from the RunReport critical path.
    #   (prepare/render exist only on clip.render jobs, so they are the
    #   discriminator — a generate job's "drive" phase must NOT trigger the
    #   serial reorder.)
    if any(p["name"] in ("prepare", "render") for p in phases):
        serial = ["prepare", "subs", "render", "drive", "finalize"]
        phases.sort(key=lambda p: serial.index(p["name"]) if p["name"] in serial else 10 ** 9)
    elif cp_order:
        rank = {name: k for k, name in enumerate(cp_order)}
        phases.sort(key=lambda p: rank.get(p["name"], 10 ** 9))

    mat = materialization(timings, result)
    subtitles = result.get("subtitles") or {}
    cache_facts = {}
    if "content_cache_hit" in subtitles:
        cache_facts["content_cache_hit"] = bool(subtitles["content_cache_hit"])
    if "artifact_cache_hit" in subtitles:
        cache_facts["artifact_cache_hit"] = bool(subtitles["artifact_cache_hit"])
    cache_facts["measured"] = bool(cache_facts)
    # Prefer the worker's explicit materialization report. The phase fallback
    # is retained only for older job results.
    explicit_materialization = result.get("materialization") or {}
    if explicit_materialization:
        mat = explicit_materialization

    # The benchmark must not invent a wall timer from local polling clocks.
    # Prefer the server-owned RunReport wall; the job lifecycle timestamps are
    # retained only as a compatibility fallback for older responses.
    wall = ms(timing.get("wall_ms"))
    if wall <= 0:
        wall = ms(timing.get("execution_wall_ms", timing.get("wall_ms")))
    work = sum(p["work_ms"] for p in phases)
    # critical_order is the per-job serial execution chain used to place
    # phases on the batch critical path: the RunReport critical path for
    # generate jobs, the measured serial chain for clip.render jobs.
    critical_order = [p["name"] for p in phases]
    queue_wait = ms(timing.get("queue_wait_ms"))
    critical_path = sum((p.get("critical_ms") or 0) for p in phases)
    publish = phase({"phases": phases}, "drive")
    runtime_start = ms(timing.get("started_at_ms")) if timing.get("started_at_ms") is not None else None
    runtime_finish = ms(timing.get("finished_at_ms")) if timing.get("finished_at_ms") is not None else None
    return {"phases": phases, "critical_order": critical_order, "wall_ms": wall,
            "runtime_started_ms": runtime_start,
            "runtime_finished_ms": runtime_finish,
            "work_ms": work, "queue_wait_ms": queue_wait,
            "critical_path_ms": critical_path,
            "publish_wall_ms": (publish or {}).get("wall_ms", 0),
            "publish_work_ms": (publish or {}).get("work_ms", 0),
            "materialization": mat, "subtitle_cache": cache_facts,
            "drive": {
                "download_wall_ms": drive_download_ms,
                "download_work_ms": drive_download_work,
                "upload_wall_ms": drive_upload_ms,
                "upload_work_ms": drive_upload_work,
                "document_publish_wall_ms": doc_publish_ms,
                "renderer_finalize_ms": publish_wall,
                "document_publish_work_ms": doc_publish_work,
            },
            "llm": llm, "result": result, "timing": timing}


jobs = [build_job(load_full(i), i) for i in range(n_jobs)]


def job_assets(j):
    r = j["result"]
    if r.get("asset") and r["asset"].get("asset_id"):
        return 1
    manifest = (r.get("data") or {}).get("__artifact_manifest") or {}
    artifacts = manifest.get("artifacts") or []
    return len(artifacts) if artifacts else (1 if r.get("data") else 0)


def batch_intervals(jobs):
    # Per-phase wall-clock intervals on the batch clock, placed along each
    # job's REAL critical path: phases run serially inside the job in
    # critical_order, each occupying its critical_ms (phases with 0 critical
    # contribution — e.g. TTS work overlapped by generate — are skipped, so
    # the union reflects true wall-time occupancy, never accumulated work).
    # The window is the server-owned execution span [started_at, finished_at].
    out = {}
    for i, j in enumerate(jobs):
        order = j.get("critical_order") or []
        if not order:
            continue
        start = j.get("runtime_started_ms")
        end = j.get("runtime_finished_ms")
        if start is None or end is None:
            continue
        offset = 0
        for name in order:
            p = next((p for p in j["phases"] if p["name"] == name), None)
            crit = (p or {}).get("critical_ms") or 0
            if crit > 0:
                s = start + offset
                e = min(end, s + crit)
                if e > s:
                    out.setdefault(name, []).append((s, e))
            offset += crit
    return out


def union_ms(intervals):
    if not intervals:
        return 0
    iv = sorted(intervals)
    total = 0
    cs, ce = iv[0]
    for s, e in iv[1:]:
        if s > ce:
            total += ce - cs
            cs, ce = s, e
        else:
            ce = max(ce, e)
    return total + ce - cs


def batch_concurrency(jobs):
    # Real concurrency from the per-job execution windows recorded by the runtime.
    # peak = max simultaneous running jobs; average = time-weighted mean;
    # both measured — never guessed from the submit pattern.
    events = []
    for i, j in enumerate(jobs):
        start = j.get("runtime_started_ms")
        end = j.get("runtime_finished_ms")
        if start is None or end is None:
            continue
        if end > start:
            events.append((start, 1))
            events.append((end, -1))
    if not events:
        return 0, 0.0
    events.sort()
    active, weighted, peak, last = 0, 0, 0, events[0][0]
    span = events[-1][0] - events[0][0]
    for at, delta in events:
        weighted += active * (at - last)
        active += delta
        if active > peak:
            peak = active
        last = at
    avg = weighted / span if span > 0 else float(peak)
    return peak, avg


peak_concurrency, average_concurrency = batch_concurrency(jobs)
concurrency_utilization = round(peak_concurrency / worker_slots * 100, 1) if worker_slots > 0 else None

# Batch aggregates are derived only from job-runtime SSOT events and the
# per-job RunReport values. Local submit/poll clocks are not metric inputs.
# batch_wall is the PARALLEL WALL-CLOCK of the batch: the elapsed span from
# the earliest runtime execution start to the latest runtime execution finish.
# It is deliberately NOT the max per-clip E2E wall: with concurrency > 1 the
# two differ whenever clips are staggered (queue wait pushes starts apart).
# batch_work is the sum of each clip's E2E execution wall — the
# sequential-equivalent total, never a wall clock. The cumulative FFmpeg work
# is a third quantity (batch_render_work_ms, Σ render-phase work below).
# These are different magnitudes: none may be subtracted from another to get
# "the overhead" when concurrency > 1.
runtime_windows = [
    (j["runtime_started_ms"], j["runtime_finished_ms"]) for j in jobs
    if (j.get("runtime_started_ms") or 0) > 0
    and (j.get("runtime_finished_ms") or 0) > (j.get("runtime_started_ms") or 0)
]
report_walls = [ms(j["timing"].get("wall_ms")) for j in jobs if ms(j["timing"].get("wall_ms")) > 0]
if runtime_windows:
    batch_wall = max(f for _, f in runtime_windows) - min(s for s, _ in runtime_windows)
    batch_wall_source = "runtime_execution_span"
elif report_walls:
    # Fallback for servers predating started_at_ms/finished_at_ms: the max
    # per-clip E2E wall. Understated whenever clip starts are staggered.
    batch_wall = max(report_walls)
    batch_wall_source = "max_per_clip_e2e_wall_fallback"
else:
    batch_wall = 0
    batch_wall_source = "unavailable"
batch_work = sum(ms(j.get("timing", {}).get("execution_wall_ms", j["wall_ms"])) for j in jobs)
intervals = batch_intervals(jobs)
all_intervals = [iv for ivs in intervals.values() for iv in ivs]
# Batch critical path is the union of per-job phase windows on the batch
# clock. For a batch of independent jobs it is the longest serial chain
# through the run — the honest denominator for bottleneck shares (never the
# summed work, which double-counts parallel phases).
batch_critical = union_ms(all_intervals) if all_intervals else batch_wall
phases_summary = {}
for name, ivs in intervals.items():
    work = sum(p["work_ms"] for j in jobs for p in j["phases"] if p["name"] == name)
    wall = sum(p["wall_ms"] for j in jobs for p in j["phases"] if p["name"] == name)
    crit = union_ms(ivs)
    phases_summary[name] = {
        "work_ms": work,
        "wall_ms": wall,
        "critical_path_ms": crit,
        "critical_share": round(crit / batch_critical * 100, 1) if batch_critical else 0.0,
        "jobs": sum(1 for j in jobs for p in j["phases"] if p["name"] == name and p["wall_ms"] > 0),
    }
# Cumulative FFmpeg work across the batch (Σ render-phase work). This is the
# "41.08 s" figure in batch analyses — a work total, never comparable with
# the parallel wall clock by subtraction.
render_work_total = phases_summary.get("render", {}).get("work_ms", 0)
# The batch bottleneck is the phase with the LARGEST critical-path
# contribution (wall occupancy on the serial chain) — never the phase with
# the most accumulated work.
bottleneck = max(phases_summary, key=lambda n: phases_summary[n]["critical_path_ms"]) if phases_summary else None


def job_bottleneck(j):
    # Per-job bottleneck from the job's own serial chain (max critical_ms);
    # falls back to the RunReport bottleneck_stage for generate jobs.
    best, best_ms = None, 0
    for p in j["phases"]:
        c = p.get("critical_ms") or 0
        if c > best_ms:
            best_ms = c
            best = p["name"]
    if best is None:
        best = (j["timing"] or {}).get("bottleneck_stage") or "-"
    return best

totals = [j["wall_ms"] for j in jobs if j["wall_ms"] > 0]
min_t = min(totals) if totals else 0
max_t = max(totals) if totals else 0
avg_t = int(sum(totals) / len(totals)) if totals else 0
total_assets = sum(job_assets(j) for j in jobs)
submit_total = 0


# ── Batch worker facts (aggregates over the sealed job results) ───────────
# Materialization is reported per asset and retained alongside the existing
# aggregate asset_materialize_ms. These are facts from the worker/preparer;
# this script only aggregates them.
def materialization_summary(jobs):
    summary = {}
    for asset_name in ("source", "watermark", "background"):
        rows = [j["materialization"].get(asset_name) for j in jobs
                if isinstance(j.get("materialization"), dict)
                and isinstance(j["materialization"].get(asset_name), dict)]
        summary[asset_name] = {
            "wall_ms": sum(ms(row.get("wall_ms")) for row in rows),
            "work_ms": sum(ms(row.get("work_ms")) for row in rows),
            "size_bytes": sum(ms(row.get("size_bytes")) for row in rows),
            "download_bytes": sum(ms(row.get("download_bytes")) for row in rows),
            "cache_hits": sum(1 for row in rows if row.get("cache_hit", row.get("from_cache", False))),
            "cache_misses": sum(1 for row in rows if not row.get("cache_hit", row.get("from_cache", False))),
            "jobs": len(rows),
        }
    return summary

materialization_totals = materialization_summary(jobs)

# These are the facts the worker already publishes on the result envelope
# (worker_result.go): source.from_cache, transcript.reused,
# timings.total_wall_ms/work_ms/parallel, render.backend, gpu_copy_bytes,
# asset.drive_file_id. Aggregated here so the report shows them explicitly.
src_cache_hits = sum(1 for j in jobs if (j["result"].get("source") or {}).get("from_cache"))
transcript_reused = sum(1 for j in jobs if (j["result"].get("transcript") or {}).get("reused"))
prep_parallel = sum(1 for j in jobs if (phase(j, "prepare") or {}).get("parallel"))
backends = Counter(str((j["result"].get("render") or {}).get("backend") or "-") for j in jobs)
drive_file_ids = [str(a) for j in jobs for a in [(j["result"].get("asset") or {}).get("drive_file_id")] if a]
# gpu_copy_bytes: metrics_v2 is the authoritative source; the legacy
# render.gpu_copy_bytes key is only a pre-V2 fallback.
metrics_by_job = [
    ((j["result"].get("render") or {}).get("metrics_v2") or {})
    for j in jobs
]
# Resource metrics are sourced from each render's canonical metrics_v2.
# CPU is aggregated as user+system time; RSS is a batch high-water mark.
total_cpu_user_ms = sum(ms(m.get("cpu_user_ms")) for m in metrics_by_job if sentinel_ok(m.get("cpu_user_ms")))
total_cpu_system_ms = sum(ms(m.get("cpu_system_ms")) for m in metrics_by_job if sentinel_ok(m.get("cpu_system_ms")))
total_network_rx_bytes = sum(ms(m.get("network_rx_bytes")) for m in metrics_by_job if sentinel_ok(m.get("network_rx_bytes")))
total_network_tx_bytes = sum(ms(m.get("network_tx_bytes")) for m in metrics_by_job if sentinel_ok(m.get("network_tx_bytes")))
peak_cpu_percent = max((float(m.get("peak_cpu_percent", 0)) for m in metrics_by_job if isinstance(m.get("peak_cpu_percent"), (int, float))), default=0.0)

total_gpu_copy_bytes = sum(
    ms(metrics.get("gpu_copy_bytes"))
    for metrics in metrics_by_job
    if isinstance(metrics.get("gpu_copy_bytes"), (int, float)) and metrics.get("gpu_copy_bytes") >= 0
)
total_peak_rss_bytes = max(
    (ms(metrics.get("peak_rss_bytes")) for metrics in metrics_by_job
     if isinstance(metrics.get("peak_rss_bytes"), (int, float)) and metrics.get("peak_rss_bytes") >= 0),
    default=0,
)
total_disk_read_bytes = sum(
    ms(metrics.get("disk_read_bytes")) for metrics in metrics_by_job
    if isinstance(metrics.get("disk_read_bytes"), (int, float)) and metrics.get("disk_read_bytes") >= 0
)
total_disk_write_bytes = sum(
    ms(metrics.get("disk_write_bytes")) for metrics in metrics_by_job
    if isinstance(metrics.get("disk_write_bytes"), (int, float)) and metrics.get("disk_write_bytes") >= 0
)
total_network_rx_bytes = sum(ms(m.get("network_rx_bytes")) for m in metrics_by_job if sentinel_ok(m.get("network_rx_bytes")))
total_network_tx_bytes = sum(ms(m.get("network_tx_bytes")) for m in metrics_by_job if sentinel_ok(m.get("network_tx_bytes")))


# ── Console report ─────────────────────────────────────────────────────────
print("")
print("════════════════════════════════════════════════════════════════")
print("  PIPELINEGEN BENCHMARK REPORT — WALL / WORK / CRITICAL PATH")
print("════════════════════════════════════════════════════════════════")
print("")
print("  %-40s %s" % ("Git SHA:", git_sha[:12]))
print("  %-40s %s" % ("Git branch:", git_branch))
print("  %-40s %s" % ("Config SHA:", config_sha[:12]))
print("  %-40s %s" % ("DB SHA:", db_sha[:12]))
print("  %-40s %s" % ("Worker IDs:", worker_ids or "<none>"))
print("  %-40s %s" % ("Base URL:", base_url))
print("  %-40s %s" % ("Timestamp:", datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")))
print("")
print("  %-40s %d / %d" % ("Jobs succeeded:", success_count, n_jobs))
print("  %-40s %d" % ("Total assets:", total_assets))
print("")
print("  ── Batch (wall / work / critical path / concurrency) ──")
print("  %-40s %s" % ("batch_parallel_wall_ms:", f"{batch_wall:,}"))
print("  %-40s %s" % ("batch_wall_source:", batch_wall_source))
print("  %-40s %s" % ("batch_total_work_ms:", f"{batch_work:,}"))
print("  %-40s %s" % ("batch_render_work_ms:", f"{render_work_total:,}"))
if batch_wall > 0:
    print("  %-40s %s" % ("batch_wall_minus_render_work_ms:", f"{batch_wall - render_work_total:,}"))
print("  %-40s %s" % ("overhead_rule:", "wall − Σ render work is NOT overhead when concurrency > 1; per-clip overhead = E2E − RENDER (per-clip table)"))
print("  %-40s %s" % ("batch_critical_path_ms:", f"{batch_critical:,}"))
if bottleneck:
    print("  %-40s %s @ %.1f%%" % (
        "batch bottleneck (critical path):", bottleneck, phases_summary[bottleneck]["critical_share"]))
if batch_wall > 0:
    print("  %-40s %.2fx" % ("parallelism_factor (Σ clip E2E / wall):", batch_work / batch_wall))
print("  %-40s %d" % ("peak_running_clips:", peak_concurrency))
print("  %-40s %.1f" % ("average_running_clips:", average_concurrency))
print("  %-40s %s" % ("queue_wait_total_ms:", f"{sum(j.get('queue_wait_ms', 0) for j in jobs):,}"))
print("  %-40s %s" % ("queue_wait_max_ms:", f"{max((j.get('queue_wait_ms', 0) for j in jobs), default=0):,}"))
if worker_slots > 0:
    print("  %-40s %d" % ("worker_slots:", worker_slots))
    print("  %-40s %.1f%%" % ("concurrency_utilization (peak/slots):", concurrency_utilization))
else:
    print("  %-40s %s" % ("worker_slots:", "- (set BENCH_WORKER_SLOTS to compare)"))
print("")
print("  ── Phase breakdown ──")
print("  wall = Σ per-job wall (sequential-equivalent); work = Σ measured work;")
print("  wall = elapsed phase time; work = accumulated operation work (parallel work may exceed wall).")
print("  critical_path = wall occupancy on the batch critical path; shares use this denominator.")
print("  The bottleneck is the largest critical-path contribution, never the largest work total.")
print("  %-12s %10s %10s %12s %8s %5s" % ("phase", "work_ms", "wall_ms", "critical_ms", "share", "jobs"))
print("  %-12s %10s %10s %12s %8s %5s" % ("─────", "───────", "───────", "────────────", "─────", "────"))
for name in sorted(phases_summary):
    p = phases_summary[name]
    print("  %-12s %10s %10s %12s %7.1f%% %5d" % (
        name, f"{p['work_ms']:,}", f"{p['wall_ms']:,}", f"{p['critical_path_ms']:,}",
        p["critical_share"], p["jobs"]))
print("")
print("  ── Throughput / resources ──")
source_seconds = sum(float((j["result"].get("render") or {}).get("duration_sec") or 0) for j in jobs)
print("  %-40s %.2f" % ("clips_per_minute:", (n_jobs / (batch_wall / 60000.0)) if batch_wall > 0 else 0.0))
print("  %-40s %.3fx" % ("batch_speed_factor (video/wall, >1 faster):", (source_seconds / (batch_wall / 1000.0)) if batch_wall > 0 else 0.0))
print("  %-40s %.3fx" % ("batch_xrt (wall/video, <1 faster):", ((batch_wall / 1000.0) / source_seconds) if source_seconds > 0 else 0.0))
print("  %-40s %s" % ("cpu_user_ms:", f"{total_cpu_user_ms:,}" if total_cpu_user_ms else "-"))
print("  %-40s %s" % ("cpu_system_ms:", f"{total_cpu_system_ms:,}" if total_cpu_system_ms else "-"))
print("  %-40s %s" % ("peak_cpu_percent:", f"{peak_cpu_percent:.1f}" if peak_cpu_percent else "-"))
print("  %-40s %s" % ("peak_rss_bytes:", f"{total_peak_rss_bytes:,}" if total_peak_rss_bytes else "-"))
print("  %-40s %s" % ("disk_read_bytes:", f"{total_disk_read_bytes:,}" if total_disk_read_bytes else "-"))
print("  %-40s %s" % ("disk_write_bytes:", f"{total_disk_write_bytes:,}" if total_disk_write_bytes else "-"))
print("")
print("  ── Per-job (real worker facts) ──")
print("  %-16s %-9s %-11s %-16s %13s %15s %8s %10s %8s %10s" % (
    "JOB_ID", "MODE", "STATUS", "BACKEND", "PREP(w/work)", "RENDER(w/work)", "DRIVE", "TOTAL", "TRANSCR", "GPU_COPY"))
print("  %-16s %-9s %-11s %-16s %13s %13s %8s %10s %8s %10s" % (
    "──────", "────", "───────────", "──────", "───────────", "───────────", "─────", "─────", "───────", "────────"))
for i in range(n_jobs):
    j = jobs[i]
    r = j["result"]
    render = r.get("render") or {}
    trans = r.get("transcript") or {}
    prep = phase(j, "prepare")
    ren = phase(j, "render")
    drv = phase(j, "drive")
    prep_s = (fmt_work(prep) if prep else "-")
    ren_s = (fmt_work(ren) if ren else "-")
    drv_s = fmt_sec(drv["wall_ms"]) if drv and drv["wall_ms"] else "-"
    trans_s = "-"
    if trans:
        tag = "reuse" if trans.get("reused") else "gen"
        trans_s = "%s/%s/%s" % (tag, trans.get("language", "?"), trans.get("cues", "?"))
    gpu = (render.get("metrics_v2") or {}).get("gpu_copy_bytes")
    if gpu is None:
        gpu = render.get("gpu_copy_bytes")  # legacy fallback (pre-V2 servers)
    gpu_s = f"{ms(gpu):,}" if isinstance(gpu, (int, float)) else "-"
    wall_s = fmt_sec(j["wall_ms"])
    backend = render.get("backend") or "-"
    print("  %-16s %-9s %-11s %-16.16s %-13s %-13s %8s %10s %8s %10s" % (
        (j_ids[i] or "N/A")[:14], j_modes[i], j_statuses[i], str(backend),
        prep_s, ren_s, drv_s, wall_s, trans_s, gpu_s))


print("")
print("  ── Per-clip table (Queue / Prepare / Subs / Render / Upload / Total) ──")
print("  Queue = queue_wait_ms (RunReport); Prepare = clip.prepare wall;")
print("  Subs = clip.subtitles wall; Render = clip.render+probe+overlay wall;")
print("  Publish = clip.publish wall/work; Queue is excluded from execution total and shown separately.")
print("  OVERHEAD = TOTAL wall − RENDER wall: this clip's time outside the render;")
print("  it is per-clip (clips overlap), never additive into a batch total.")
print("  BOTTLENECK = largest critical-path phase on this clip.")
print("  %-14s %8s %15s %14s %15s %15s %15s %15s %15s %-10s" % ("JOB_ID", "QUEUE", "PREP wall/work", "SUBS wall/work", "RENDER wall/work", "PUBLISH wall/work", "TOTAL wall/work", "OVERHEAD", "CRITICAL", "BOTTLENECK"))
print("  %-14s %8s %15s %14s %15s %15s %15s %15s %15s %-10s" % ("──────", "─────", "───────────────", "──────────────", "───────────────", "───────────────", "───────────────", "────────", "──────", "──────────"))
for i in range(n_jobs):
    j = jobs[i]
    prep = phase(j, "prepare")
    subs = phase(j, "subs")
    ren = phase(j, "render")
    drv = phase(j, "drive")
    # Only clip.render jobs carry the serial clip phases; generate jobs have
    # no per-clip queue/prepare/render breakdown (their phases are llm/tts/
    # audio/finalize and stay in the phase breakdown table).
    if not prep and not subs and not ren and not drv:
        continue
    queue_s = fmt_sec(j.get("queue_wait_ms") or 0)
    prep_s = fmt_work(prep) if prep else "-"
    subs_s = fmt_work(subs) if subs else "-"
    ren_s = fmt_work(ren) if ren else "-"
    drv_s = fmt_work(drv) if drv else "-"
    total_s = "%s/%s" % (fmt_sec(j["wall_ms"]), fmt_sec(j["work_ms"]))
    overhead_s = fmt_sec(j["wall_ms"] - ren["wall_ms"]) if ren and ren["wall_ms"] > 0 else "-"
    critical_s = fmt_sec(j.get("critical_path_ms", 0))
    print("  %-14s %8s %15s %14s %15s %15s %15s %15s %15s %-10s" % (
        (j_ids[i] or "N/A")[:12], queue_s, prep_s, subs_s, ren_s, drv_s, total_s,
        overhead_s, critical_s, job_bottleneck(j)[:10]))


# ── Render phase split (metrics_v2, measured by the renderer) ──────────────
# The Rust boundary now splits the coarse ffmpeg wall into probe / decode /
# composite / encode (bench_all per-frame sums + the filter-graph residual).
# Only rows with at least one measured phase are shown; the rest stay
# NOT_INSTRUMENTED on the wire and "-" here.
render_rows = []
for i in range(n_jobs):
    j = jobs[i]
    m = (j["result"].get("render") or {}).get("metrics_v2") or {}
    if not any(sentinel_ok(m.get(k)) for k in ("probe_ms", "decode_ms", "composite_ms", "encode_ms")):
        continue
    cell = lambda k: fmt_sec(ms(m.get(k))) if sentinel_ok(m.get(k)) else "-"
    render_rows.append((
        (j_ids[i] or "N/A")[:12],
        cell("probe_ms"), cell("decode_ms"), cell("composite_ms"),
        cell("encode_ms"), cell("render_wall_ms")))
if render_rows:
    print("")
    print("  ── Render phase split (metrics_v2, measured by the renderer) ──")
    print("  probe = ffprobe wall; decode/encode = per-frame bench sums;")
    print("  composite = filter-graph residual (subtitles+watermark+compositing+mux);")
    print("  render_wall = worker-measured render wall (selection + execution).")
    print("  %-14s %8s %8s %10s %8s %12s" % ("JOB_ID", "PROBE", "DECODE", "COMPOSITE", "ENCODE", "RENDER_WALL"))
    print("  %-14s %8s %8s %10s %8s %12s" % ("──────", "─────", "──────", "─────────", "──────", "───────────"))
    for row in render_rows:
        print("  %-14s %8s %8s %10s %8s %12s" % row)


print("")
print("  ── Worker facts (source / timings / asset / bottleneck, from job result) ──")
print("  SRC_CACHE = source.from_cache; PREP_PAR = timings.parallel;")
print("  DRIVE_FILE = asset.drive_file_id (published MP4 on Drive);")
print("  BOTTLENECK = phase with the largest critical-path contribution in this job.")
print("  %-14s %-10s %-9s %-20s %-10s" % ("JOB_ID", "SRC_CACHE", "PREP_PAR", "DRIVE_FILE_ID", "BOTTLENECK"))
print("  %-14s %-10s %-9s %-20s %-10s" % ("──────", "─────────", "────────", "─────────────", "──────────"))
for i in range(n_jobs):
    j = jobs[i]
    r = j["result"]
    src = r.get("source") or {}
    prep = phase(j, "prepare")
    asset = r.get("asset") or {}
    src_s = "cache" if src.get("from_cache") else ("miss" if src else "-")
    par_s = "yes" if (prep and prep.get("parallel")) else ("no" if prep else "-")
    dfid = asset.get("drive_file_id") or "-"
    print("  %-14s %-10s %-9s %-20.20s %-10s" % (
        (j_ids[i] or "N/A")[:12], src_s, par_s, str(dfid), job_bottleneck(j)[:10]))


print("")
print("  ── Batch worker facts (aggregates) ──")
print("  %-40s %s" % ("source cache hits:", f"{src_cache_hits}/{n_jobs}"))
print("  %-40s %s" % ("transcript reused:", f"{transcript_reused}/{n_jobs}"))
print("  %-40s %s" % ("prep parallel:", f"{prep_parallel}/{n_jobs}"))
print("  %-40s %s" % ("backends:", ", ".join(f"{k} x{v}" for k, v in backends.most_common()) or "-"))
print("  %-40s %d" % ("drive files published:", len(drive_file_ids)))
print("  %-40s %s" % ("gpu_copy_bytes total:", f"{total_gpu_copy_bytes:,}" if total_gpu_copy_bytes else "-"))
print("  %-40s %s" % ("peak_rss_bytes max:", f"{total_peak_rss_bytes:,}" if total_peak_rss_bytes else "-"))
print("  %-40s %s" % ("disk_read_bytes total:", f"{total_disk_read_bytes:,}" if total_disk_read_bytes else "-"))
print("  %-40s %s" % ("disk_write_bytes total:", f"{total_disk_write_bytes:,}" if total_disk_write_bytes else "-"))
print("")
print("  ── Drive / Google Doc ──")
print("  Download = Drive reads/materialization; Upload = clip artifacts; Document = Google Doc publication.")
for label, key in (("drive download wall/work", "download"), ("drive upload wall/work", "upload"), ("google doc publish wall/work", "document_publish")):
    wall = sum(j["drive"].get(f"{key}_wall_ms", 0) for j in jobs)
    work = sum(j["drive"].get(f"{key}_work_ms", 0) for j in jobs)
    print("  %-40s %s" % (f"{label}:", f"{wall:,}/{work:,} ms"))
print("")
print("  ── Materialization aggregates (SSOT) ──")
print("  asset_materialize_ms remains available in metrics_v2; these are per-asset totals.")
for asset_name in ("source", "watermark", "background"):
    m = materialization_totals[asset_name]
    print("  %-40s %s" % (f"{asset_name} wall/work:", f"{m['wall_ms']:,}/{m['work_ms']:,} ms" if m["jobs"] else "-"))
    print("  %-40s %s" % (f"{asset_name} size bytes:", f"{m['size_bytes']:,}" if m["jobs"] else "-"))
    print("  %-40s %s" % (f"{asset_name} download bytes:", f"{m['download_bytes']:,}" if m["jobs"] else "-"))
    print("  %-40s %s" % (f"{asset_name} cache hits/misses:", f"{m['cache_hits']}/{m['cache_misses']}" if m["jobs"] else "-"))


print("")
print("  ── LLM (Ollama split, from operation metadata) ──")
print("  wall/work/queue from the generate operation; load/eval/tokens from Ollama.")
print("  cold_start is a count (0/1 per call); model is 'mixed' when calls differ.")
print("  %-16s %5s %-12s %8s %9s %10s %12s %10s %8s %6s %6s" % (
    "JOB_ID", "CALLS", "MODEL", "QUEUE", "LOAD_MS", "PROMPT_MS", "INF_WALL/WORK", "TOK_IN/OUT", "TOK/S", "COLD", "WARM"))
print("  %-16s %5s %-12s %8s %9s %10s %12s %10s %8s %6s %6s" % (
    "──────", "─────", "────", "─────", "───────", "─────────", "────────────", "─────────", "─────", "────", "────"))
for i in range(n_jobs):
    llm = jobs[i]["llm"]
    if not llm or not llm.get("calls"):
        print("  %-16s %5s %-12s %8s %9s %10s %12s %10s %8s %6s %6s" % (
            (j_ids[i] or "N/A")[:14], "-", "-", "-", "-", "-", "-", "-", "-", "-", "-"))
        continue
    calls = llm["calls"]
    cold = llm["cold_start"]
    warm = max(0, calls - cold)
    inf = "%s/%s" % (fmt_sec(llm["inference_wall_ms"]), fmt_sec(llm["inference_work_ms"]))
    toks = "%s/%s" % (f"{llm['input_tokens']:,}" if llm["input_tokens"] else "-",
                      f"{llm['output_tokens']:,}" if llm["output_tokens"] else "-")
    print("  %-16s %5d %-12.12s %8s %9s %10s %12s %10s %8s %6d %6d" % (
        (j_ids[i] or "N/A")[:14], calls, llm["model"],
        fmt_sec(llm["queue_wait_ms"]), fmt_sec(llm["model_load_ms"]),
        fmt_sec(llm["prompt_eval_ms"]), inf, toks,
        f"{llm['tokens_per_second']:.0f}" if llm["tokens_per_second"] else "-", cold, warm))
print("")
print("  ── Materialization (PhaseTiming per asset) ──")
print("  wall/work, download bytes and cache flags come from the materializer SSOT.")
print("  %-16s %-11s %-13s %-6s %12s %12s %12s" % ("JOB_ID", "ASSET", "WALL/WORK", "CACHE", "SIZE", "DOWNLOAD", "ASSET_ID"))
print("  %-16s %-11s %-13s %-6s %12s %12s %12s" % ("──────", "─────", "─────────", "─────", "────", "────────", "────────"))
for i in range(n_jobs):
    mat = jobs[i]["materialization"]
    if not mat:
        print("  %-16s %-11s %-13s %-6s %12s %12s %12s" % (
            (j_ids[i] or "N/A")[:14], "-", "-", "-", "-", "-", "-"))
        continue
    for asset in ("source", "watermark", "background"):
        m = mat.get(asset)
        if not m:
            continue
        cache_s = "hit" if m["from_cache"] else "miss"
        bytes_s = f"{m['size_bytes']:,}" if m["size_bytes"] > 0 else "-"
        download_s = f"{m['download_bytes']:,}" if m["download_bytes"] > 0 else "0"
        print("  %-16s %-11s %-13s %-6s %12s %12s %12s" % (
            (j_ids[i] or "N/A")[:14], asset,
            fmt_work(m), cache_s, bytes_s, download_s, (m.get("asset_id") or "-")[:12]))
print("")
print("  ── Min / Max / Avg per-job wall ──")
print("  %-40s %s" % ("Min:", fmt_sec(min_t)))
print("  %-40s %s" % ("Max:", fmt_sec(max_t)))
print("  %-40s %s" % ("Avg:", fmt_sec(avg_t)))
print("")
print("════════════════════════════════════════════════════════════════")

# ── Emit JSON report ───────────────────────────────────────────────────────
fingerprint = {}
if fingerprint_file and os.path.isfile(fingerprint_file):
    try:
        with open(fingerprint_file) as f:
            fingerprint = json.load(f)
    except Exception:
        fingerprint = {"error": "failed to load fingerprint"}

jobs_out = []
for i in range(n_jobs):
    j = jobs[i]
    r = j["result"]
    render = r.get("render") or {}
    timing = j["timing"] or {}
    clip_render_wall = (phase(j, "render") or {}).get("wall_ms") or 0
    clip_overhead_ms = (j["wall_ms"] - clip_render_wall) if clip_render_wall > 0 else 0
    jobs_out.append({
        "job_id": j_ids[i],
        "label": j_labels[i],
        "mode": j_modes[i],
        "status": j_statuses[i],
        "assets": job_assets(j),
        "wall_ms": j["wall_ms"],
        "work_ms": j["work_ms"],            "timing_source": "job_runtime_run_report",
        "phases": j["phases"],
        "facts": {
            "transcript": r.get("transcript"),
            "materialization_summary": {
                asset_name: {
                    "wall_ms": ms((j["materialization"].get(asset_name) or {}).get("wall_ms")),
                    "work_ms": ms((j["materialization"].get(asset_name) or {}).get("work_ms")),
                    "size_bytes": ms((j["materialization"].get(asset_name) or {}).get("size_bytes")),
                    "download_bytes": ms((j["materialization"].get(asset_name) or {}).get("download_bytes")),
                    "cache_hit": bool((j["materialization"].get(asset_name) or {}).get("cache_hit", (j["materialization"].get(asset_name) or {}).get("from_cache", False))),
                }
                for asset_name in ("source", "watermark", "background")
                if j["materialization"].get(asset_name)
            },
            "timings": r.get("timings"),
            "materialization": j["materialization"],
            "subtitle_cache": j["subtitle_cache"],
            "llm": j["llm"],
            "source": r.get("source"),
            "render": {
                "backend": render.get("backend"),
                # metrics_v2 is the authoritative render metrics object;
                # legacy scalar fields are not copied as independent values.
                "metrics_v2": render.get("metrics_v2"),
                "render_wall_ms": (render.get("metrics_v2") or {}).get("render_wall_ms"),
                "duration_sec": render.get("duration_sec"),
                "size_bytes": render.get("size_bytes"),
                "audio_copy_eligible": render.get("audio_copy_eligible"),
                "audio_encode_passes": render.get("audio_encode_passes"),
                "subtitle_raster_cpu": (render.get("metrics_v2") or {}).get("subtitle_raster_cpu", render.get("subtitle_raster_cpu")),
                "gpu_copy_bytes": (render.get("metrics_v2") or {}).get("gpu_copy_bytes"),
            },
            "asset": r.get("asset"),
            "subtitles": r.get("subtitles"),
            "subtitle_cache": j["subtitle_cache"],
        },
        "per_clip": {
            "queue_wait_ms": j.get("queue_wait_ms") or 0,
            "prepare_wall_ms": (phase(j, "prepare") or {}).get("wall_ms", 0),
            "prepare_work_ms": (phase(j, "prepare") or {}).get("work_ms", 0),
            "subs_wall_ms": (phase(j, "subs") or {}).get("wall_ms", 0),
            "subs_work_ms": (phase(j, "subs") or {}).get("work_ms", 0),
            "render_wall_ms": (phase(j, "render") or {}).get("wall_ms", 0),
            "render_work_ms": (phase(j, "render") or {}).get("work_ms", 0),
            # overhead_ms = clip E2E wall − clip render wall: this clip's
            # out-of-render time (prepare, publish, scheduling — queue is
            # reported separately). The honest per-clip overhead figure;
            # not additive across clips when concurrency > 1.
            "overhead_ms": clip_overhead_ms,
            "upload_wall_ms": j.get("publish_wall_ms", 0),
            "upload_work_ms": j.get("publish_work_ms", 0),
            "total_wall_ms": j["wall_ms"],
            "total_work_ms": j["work_ms"],
            "critical_path_ms": j.get("critical_path_ms", 0),
            "publish_wall_ms": j.get("publish_wall_ms", 0),
            "publish_work_ms": j.get("publish_work_ms", 0),
            "bottleneck": job_bottleneck(j),
        },
        "run_report": {
            "wall_ms": timing.get("wall_ms"),
            "execution_wall_ms": timing.get("execution_wall_ms", timing.get("wall_ms")),
            "started_at": timing.get("started_at"),
            "finished_at": timing.get("finished_at"),
            "queue_wait_ms": timing.get("queue_wait_ms"),
            "attributed_ms": timing.get("attributed_ms"),
            "unattributed_ms": timing.get("unattributed_ms"),
            "unattributed_percent": timing.get("unattributed_percent"),
            "overlapped_ms": timing.get("overlapped_ms"),
            "bottleneck_stage": timing.get("bottleneck_stage"),
            "bottleneck_operation": timing.get("bottleneck_operation"),
            "bottleneck_percent": timing.get("bottleneck_percent"),
            "critical_path": timing.get("critical_path"),
            "stages": timing.get("stages"),
            "operations": timing.get("operations"),
            "fanout": timing.get("fanout"),
        },
        "bottleneck": {
            "phase": job_bottleneck(j),
            "critical_path_ms": max((p.get("critical_ms") or 0 for p in j["phases"]), default=0),
        },
        "critical_order": j["critical_order"],
    })

report = {
    "schema_version": "pipelinegen-benchmark-v3",
    "fingerprint": fingerprint,
    "git_sha": git_sha,
    "git_branch": git_branch,
    "config_sha": config_sha,
    "db_sha": db_sha,
    "worker_ids": worker_ids.split(",") if worker_ids else [],
    "base_url": base_url,
    "timestamp": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "summary": {
        "total_jobs": n_jobs,
        "queue": {
            "total_wait_ms": sum(j.get("queue_wait_ms", 0) for j in jobs),
            "max_wait_ms": max((j.get("queue_wait_ms", 0) for j in jobs), default=0),
            "average_wait_ms": round(sum(j.get("queue_wait_ms", 0) for j in jobs) / n_jobs, 2) if n_jobs else 0.0,
            "measured_jobs": sum(1 for j in jobs if j.get("queue_wait_ms", 0) > 0),
        },
        "succeeded": success_count,
        "failed": n_jobs - success_count,
        "total_assets": total_assets,
        "batch": {
            "batch_total_wall_ms": batch_wall,
            "batch_wall_source": batch_wall_source,
            "batch_total_work_ms": batch_work,
            "batch_render_work_ms": render_work_total,
            "batch_wall_minus_render_work_ms": (batch_wall - render_work_total) if batch_wall > 0 else 0,
            "derived_only": True,
            "derivation": {
                "batch_total_wall_ms": "parallel wall-clock: earliest runtime execution start → latest runtime execution finish; fallback = max per-clip E2E wall",
                "batch_wall_source": batch_wall_source,
                "batch_total_work_ms": "sum of per-clip E2E execution_wall_ms — sequential-equivalent, NOT wall",
                "batch_render_work_ms": "sum of render-phase work_ms — the cumulative FFmpeg work",
                "batch_wall_minus_render_work_ms": "wall − Σ render work; NOT the batch overhead when concurrency > 1 (clips overlap)",
                "batch_overhead_rule": "per-clip overhead = clip E2E wall − clip render wall (per_clip.overhead_ms)",
                "peak_concurrency": "max overlap of runtime execution spans",
                "average_concurrency": "time-weighted overlap over batch wall",
                "parallelism_factor": "Σ per-clip E2E execution wall / parallel wall",
            },
            "batch_critical_path_ms": batch_critical,
            "parallelism_factor": round(batch_work / batch_wall, 3) if batch_wall else 0.0,
            "parallelism_efficiency": round(batch_work / batch_wall, 3) if batch_wall else 0.0,
            "peak_running_clips": peak_concurrency,
            "average_running_clips": round(average_concurrency, 2),
            "bottleneck": {
                "phase": bottleneck,
                "critical_path_ms": phases_summary[bottleneck]["critical_path_ms"] if bottleneck else 0,
                "critical_share": phases_summary[bottleneck]["critical_share"] if bottleneck else 0.0,
            },
            "concurrency": {
                "peak_running_clips": peak_concurrency,
                "average_running_clips": round(average_concurrency, 2),
                "worker_slots": worker_slots if worker_slots > 0 else None,
                "concurrency_utilization_percent": concurrency_utilization,
            },
            "source": "job_runtime_run_reports_only",
            "derived_only": True,
        },
        "phases": phases_summary,
        "stage_timing": {
            # Submission round-trip is transport bookkeeping, not a render
            # phase and is intentionally excluded from SSOT-derived timing.
            "submit_ms": 0,
            "generate_ms": phases_summary.get("llm", {}).get("wall_ms", 0),
            "render_ms": phases_summary.get("render", {}).get("wall_ms", 0),
            "drive_ms": phases_summary.get("drive", {}).get("wall_ms", 0),
            "wall_clock_ms": batch_wall,
        },
        "per_job": {"min_ms": min_t, "max_ms": max_t, "avg_ms": avg_t},
        "worker_facts": {
            "source_cache_hits": src_cache_hits,
            "transcript_reused": transcript_reused,
            "prep_parallel": prep_parallel,
            "backends": dict(backends),
            "drive_file_ids": drive_file_ids,
            "gpu_copy_bytes_total": total_gpu_copy_bytes,
            "throughput": {
                "clips_per_minute": (n_jobs / (batch_wall / 60000.0)) if batch_wall > 0 else 0.0,
                # batch_speed_factor = video seconds produced / batch wall
                # (>1 = faster than realtime). pipeline_rtf is a legacy alias
                # of the same number — it is NOT an xRT-style factor; the
                # true inverse (wall / video, <1 = faster) is batch_xrt.
                "batch_speed_factor": (source_seconds / (batch_wall / 1000.0)) if batch_wall > 0 else 0.0,
                "batch_xrt": ((batch_wall / 1000.0) / source_seconds) if source_seconds > 0 else 0.0,
                "pipeline_rtf": (source_seconds / (batch_wall / 1000.0)) if batch_wall > 0 else 0.0,
            },
            "resources": {
                "cpu_user_ms": total_cpu_user_ms,
                "cpu_system_ms": total_cpu_system_ms,
                "peak_cpu_percent": peak_cpu_percent,
                "peak_rss_bytes": total_peak_rss_bytes,
                "disk_read_bytes": total_disk_read_bytes,
                "disk_write_bytes": total_disk_write_bytes,
                "network_rx_bytes": total_network_rx_bytes,
                "network_tx_bytes": total_network_tx_bytes,
            },
            "peak_rss_bytes_max": total_peak_rss_bytes,
            "disk_read_bytes_total": total_disk_read_bytes,
            "disk_write_bytes_total": total_disk_write_bytes,
            "materialization": materialization_totals,
            "drive": {
                "download_wall_ms": sum(j["drive"].get("download_wall_ms", 0) for j in jobs),
                "download_work_ms": sum(j["drive"].get("download_work_ms", 0) for j in jobs),
                "upload_wall_ms": sum(j["drive"].get("upload_wall_ms", 0) for j in jobs),
                "upload_work_ms": sum(j["drive"].get("upload_work_ms", 0) for j in jobs),
                "google_doc_publish_wall_ms": sum(j["drive"].get("document_publish_wall_ms", 0) for j in jobs),
                "google_doc_publish_work_ms": sum(j["drive"].get("document_publish_work_ms", 0) for j in jobs),
            },
            "source_materialize_ms": materialization_totals["source"]["wall_ms"],
            "watermark_materialize_ms": materialization_totals["watermark"]["wall_ms"],
            "background_materialize_ms": materialization_totals["background"]["wall_ms"],
            "download_bytes": {
                "source": materialization_totals["source"]["download_bytes"],
                "watermark": materialization_totals["watermark"]["download_bytes"],
                "background": materialization_totals["background"]["download_bytes"],
                "total": sum(m["download_bytes"] for m in materialization_totals.values()),
            },
            "cache_hits": {
                "source": materialization_totals["source"]["cache_hits"],
                "watermark": materialization_totals["watermark"]["cache_hits"],
                "background": materialization_totals["background"]["cache_hits"],
                "total": sum(m["cache_hits"] for m in materialization_totals.values()),
            },
        },
    },
    "jobs": jobs_out,
}

with open(out_file, "w") as f:
    json.dump(report, f, indent=2)
    f.write("\n")

print(f"[bench] Report written to {out_file}")
PYEOF

echo ""
echo "[bench] ✅ Benchmark complete."

#!/usr/bin/env bash
# scripts/bench/generate-video.sh — canonical PipelineGen video benchmark runner.
#
# Full pipeline benchmark: generate → TTS/audio → render (watermark + subtitles + Chronon) → Docs/Drive
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
# shellcheck source=scripts/lib/canonical_db_path.sh
source "$ROOT_DIR/scripts/lib/canonical_db_path.sh"
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
VOICEOVER="${BENCH_VOICEOVER:-0}"
DOCS_FOLDER_ID="${BENCH_DOCS_FOLDER_ID:-}"

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
  --voiceover         Enable real TTS with COMBINED_TIMELINE for generated jobs
  --docs-folder ID    Google Drive folder ID for generated documents
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

Report: per-phase wall_ms / work_ms / critical_path_ms (LLM, TTS, audio, Docs, render, Drive)
plus the real worker facts already exposed in the job result (transcript,
timings, render.backend). No synthetic 60/40 splits: phases come from
/api/jobs/{id}/full (result + RunReport timing).

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
        --voiceover)    VOICEOVER=1; shift ;;
        --docs-folder)  DOCS_FOLDER_ID="$2"; shift 2 ;;
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
FINGERPRINT_DB_PATH="$(canonical_primary_db_path "$ROOT_DIR")" \
    bash "$ROOT_DIR/scripts/bench/capture-fingerprint.sh" > "$FINGERPRINT_FILE" 2>/dev/null || exit 2

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
DB_PATH="$(canonical_primary_db_path "$ROOT_DIR")"

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
    local ikey
    ikey=$(python3 -c 'import uuid; print(uuid.uuid4())')
    curl -fsS --max-time 60 \
        -X "$method" \
        -H "Authorization: Bearer $ADMIN_TOKEN" \
        -H "Idempotency-Key: $ikey" \
        -H "Content-Type: application/json" \
        "$@" \
        "$BASE_URL$path"
}

api_raw() {
    # Returns HTTP body + status code; does not fail on non-2xx.
    local method="$1" path="$2"; shift 2
    local ikey
    ikey=$(python3 -c 'import uuid; print(uuid.uuid4())')
    curl -sS --max-time 60 \
        -X "$method" \
        -H "Authorization: Bearer $ADMIN_TOKEN" \
        -H "Idempotency-Key: $ikey" \
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

# ── Stages 1-4 (work list → submit → poll → report) ────────────────────────
# The remaining pipeline runs from the sourced stage module in this shell so
# every shared variable stays in scope (same semantics as the former single
# top-to-bottom script).
source "$ROOT_DIR/scripts/bench/lib/generate_video_stages.sh"


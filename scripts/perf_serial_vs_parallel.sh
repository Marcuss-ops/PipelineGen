#!/usr/bin/env bash
# scripts/perf_serial_vs_parallel.sh — controlled before/after benchmark of the
# script.generate SceneTextReady DAG.
#
# Runs ONE scratch PipelineGen instance (port 8023, isolated data dir) in the
# requested mode, dispatches two controlled jobs (NASA 1-scene + 10-scene),
# polls them to terminal, and prints each job's canonical TimingSummary
# (.timing from /api/jobs/:id/full): wall_ms, critical_path, bottleneck_percent,
# stages, operations, and fan-out (calls / work_ms / max_ms vs wall_ms).
#
# Production is NEVER touched: the scratch instance reuses the real external
# backends (Ollama, Edge TTS, Rust muscles, Google Drive) but its SQLite and
# media dir are isolated under /tmp, and background jobs are disabled.
#
# Usage:
#   scripts/perf_serial_vs_parallel.sh serial     # "before": entities → TTS, pools=1
#   scripts/perf_serial_vs_parallel.sh parallel   # "after" : SceneTextReady DAG, pools=4
#
# Modes are toggled purely via environment (no file edits):
#   serial   : PIPELINEGEN_SCRIPT_SERIAL_MODE=true,  VELOX_VOICEOVER_MAX_CONCURRENT_TTS=1
#   parallel : PIPELINEGEN_SCRIPT_SERIAL_MODE=false, VELOX_VOICEOVER_MAX_CONCURRENT_TTS=4
#
# timing.mode=best_effort mirrors scripts/perf_compare_batches.sh: the live
# Edge TTS backend may emit no word boundaries, and required+word would fail
# the job before any timing is recorded.
set -euo pipefail
umask 077

MODE="${1:?usage: $0 serial|parallel}"
case "$MODE" in serial|parallel) ;; *) echo "usage: $0 serial|parallel" >&2; exit 2;; esac

REPO="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO"

PORT=8023
DATA="/tmp/pg-bench-$MODE"
RESULTS="/tmp/pg-bench-$MODE-results"
LOG="/tmp/pg-bench-$MODE.log"
mkdir -p "$RESULTS"

# ── Load canonical env WITHOUT echoing any secret ────────────────────────────
set -a
while IFS= read -r line; do
  case "$line" in ''|'#'*) continue;; esac
  [[ "$line" == *=* ]] || continue
  export "$line"
done < /etc/pipelinegen/pipelinegen.env
set +a
# Repo-local .env fills only missing values (mirrors start_server.sh).
# shellcheck source=scripts/lib/dotenv.sh
source scripts/lib/dotenv.sh
load_dotenv_missing .env

# ── Benchmark overrides (env wins over config.yaml) ──────────────────────────
export VELOX_PORT=$PORT
export VELOX_DATA_DIR="$DATA"
export VELOX_MASTER_URL="http://127.0.0.1:$PORT"
export VELOX_BASE_URL="http://127.0.0.1:$PORT"
export VELOX_LEXICON_ROOT="$REPO/config/lexicons"
export VELOX_FEATURE_IMAGES_ENABLED=true
if [[ "$MODE" == serial ]]; then
  export PIPELINEGEN_SCRIPT_SERIAL_MODE=true
  export VELOX_VOICEOVER_MAX_CONCURRENT_TTS=1
else
  export PIPELINEGEN_SCRIPT_SERIAL_MODE=false
  export VELOX_VOICEOVER_MAX_CONCURRENT_TTS=4
fi

TOKEN="${VELOX_ADMIN_TOKEN:-}"
API="http://127.0.0.1:$PORT"

# ── Boot the scratch instance ────────────────────────────────────────────────
rm -rf "$DATA"; mkdir -p "$DATA"
./bin/pipelinegen --mode all >"$LOG" 2>&1 &
PID=$!
echo "[$MODE] server pid=$PID port=$PORT data=$DATA"

cleanup() {
  kill "$PID" 2>/dev/null || true
  wait "$PID" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# Wait for /ready (script_generate fully wired). Migrations on a fresh DB take
# tens of seconds; allow 240s.
ready=0
for _ in $(seq 1 120); do
  if ! kill -0 "$PID" 2>/dev/null; then
    echo "[$MODE] server died during boot — tail of log:" >&2
    tail -40 "$LOG" | sed -E 's/(token|secret|hmac|authorization)=[^[:space:]]+/\1=<REDACTED>/gI' >&2
    exit 1
  fi
  code=$(curl -s -o "$RESULTS/ready.json" -w '%{http_code}' --max-time 5 "$API/ready" || echo 000)
  if [[ "$code" == "200" ]]; then
    ready=1
    break
  fi
  sleep 2
done
if [[ "$ready" != "1" ]]; then
  echo "[$MODE] /ready did not turn 200 in 240s — tail of log:" >&2
  tail -60 "$LOG" | sed -E 's/(token|secret|hmac|authorization)=[^[:space:]]+/\1=<REDACTED>/gI' >&2
  exit 1
fi
echo "[$MODE] ready: $(jq -c '{status,ok}' "$RESULTS/ready.json" 2>/dev/null || echo '?')"

# ── Controlled payloads ──────────────────────────────────────────────────────
NASA_TEXT="NASA supports the Genesis Mission. President Donald Trump announced that the United States will participate. The White House Office of Science and Technology Policy coordinates the initiative for Earth research. NASA and the Genesis Mission will continue the work."

TEN_SCENE_TEXT="In 1958 the United States created NASA to lead civilian space exploration after the Soviet Union launched Sputnik. The agency absorbed older aviation laboratories and set an ambitious goal of putting humans into orbit before the end of the decade. Early engineers worked with modest computers and rockets that often failed on the pad. The Mercury program selected seven military test pilots as the first American astronauts. Alan Shepard rode a small capsule on a short suborbital hop in 1961 while the world watched on television. John Glenn later circled the Earth three times and became a national hero overnight. Mission control grew from a single room into a network of tracking stations spread across the globe. The Gemini program followed with two-man capsules designed to practice rendezvous and docking maneuvers in orbit. Astronauts learned to walk in space and to dock with unmanned targets while travelling at seventeen thousand miles per hour. These techniques were essential preparation for the lunar voyages that would soon follow. The Apollo program aimed to land a crew on the Moon and return them safely to Earth before 1970. Apollo eight carried three men around the far side of the Moon on Christmas Eve in 1968 and beamed photographs back to an amazed public. Engineers built a giant Saturn rocket with millions of moving parts and a tiny computer that guided the spacecraft. Neil Armstrong and Buzz Aldrin stepped onto the lunar surface in July 1969 while Michael Collins waited in orbit above. The astronauts collected rocks and planted instruments before lifting off to rejoin the command module. Six successful landings followed as scientists studied the Moon's origin and geology in remarkable detail. Skylab demonstrated that crews could live and work in orbit for months at a time. The space shuttle introduced a reusable winged vehicle that carried astronauts, laboratories and satellites into low Earth orbit. Its long career included launching the Hubble Space Telescope and assembling the early pieces of the International Space Station. Hubble returned sharp images of distant galaxies and helped astronomers measure the expansion of the universe. The space station grew into a permanent laboratory where crews from many nations conduct research in weightlessness. Robotic rovers have explored the surface of Mars for years, searching for evidence that the planet once held liquid water. The Voyager probes crossed into interstellar space and still transmit faint signals from the edge of the solar system. Today the Artemis program plans a new generation of lunar landings with commercial partners building landers and capsules. Private companies now carry cargo and crews to orbit while NASA focuses on deep space exploration and science. The next human footsteps on the Moon will test the technologies needed for an eventual voyage to Mars."

STYLE="Write a short English narration. Use only the supplied source text. Preserve the named people, places and main concepts. Do not invent additional people, places, organizations, quotes or events."
TONE="clear, factual and conversational"
FOLDER="${PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID:-${VELOX_DRIVE_SCRIPTS_GENERATE:-}}"
if [[ -z "$FOLDER" ]]; then
  echo "[$MODE] setup error: PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID / VELOX_DRIVE_SCRIPTS_GENERATE missing" >&2
  exit 2
fi

build_payload() {
  local id="$1" title="$2" text="$3" words="$4"
  jq -nc --arg id "$id" --arg title "$title" --arg topic "$title" \
    --arg text "$text" --arg tone "$TONE" --arg style "$STYLE" \
    --arg folder "$FOLDER" --argjson words "$words" '{
      version: 2, preset: "custom", force_refresh: true,
      items: [{
        id: $id, title: $title, project: "perf-serial-vs-parallel",
        language: "en", tone: $tone, style: $style,
        source: { type: "text", topic: $topic, source_text: $text },
        script_params: { target_words: $words, min_words: (($words * 5 / 6) | floor), segment_words: 60, use_memory: false },
        output: {
          save_to_db: true, extract_entities: true, generate_metadata: false,
          generate_scene_images: false, generate_timeline: true, voiceover_enabled: true
        },
        audio: { mode: "COMBINED_TIMELINE", timing: { mode: "best_effort", boundary: "word", formats: ["json", "srt", "vtt"] } },
        docs: { enabled: true, languages: ["en"], folder_id: $folder }
      }]
    }'
}

dispatch_and_collect() {
  local label="$1" payload="$2"
  echo "[$MODE] dispatch $label ..."
  local body job_id code
  body=$(curl -s --max-time 15 -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    -H "Idempotency-Key: bench-$MODE-$label-$(date +%s)" -d "$payload" "$API/api/script/generate")
  job_id=$(jq -r '.job_id // .id // empty' <<<"$body")
  if [[ -z "$job_id" || "$job_id" == "null" ]]; then
    echo "[$MODE] FAIL dispatch $label: $(jq -c '.error // .message // .' <<<"$body" 2>/dev/null | head -c 500)" >&2
    return 1
  fi
  echo "[$MODE] $label job_id=$job_id"

  # Poll to terminal.
  local status=""
  local deadline=$(( $(date +%s) + 900 ))
  while (( $(date +%s) < deadline )); do
    body=$(curl -s --max-time 10 -H "Authorization: Bearer $TOKEN" "$API/api/jobs/$job_id")
    status=$(jq -r '.status // .job.status // "?"' <<<"$body")
    case "$status" in
      SUCCEEDED|completed|failed|FAILED|cancelled|dead_letter) break ;;
    esac
    sleep 3
  done
  echo "[$MODE] $label -> $status"
  if [[ "$status" != "SUCCEEDED" && "$status" != "completed" ]]; then
    echo "[$MODE] FAIL $label ended $status" >&2
    return 1
  fi

  local full="$RESULTS/$label-full.json"
  curl -s --max-time 15 -H "Authorization: Bearer $TOKEN" "$API/api/jobs/$job_id/full" > "$full"
  echo "$job_id" >> "$RESULTS/job-ids.txt"
  echo "[$MODE] $label timing:"
  jq -c '{wall_ms:.timing.wall_ms, bottleneck:.timing.bottleneck_stage, bottleneck_percent:.timing.bottleneck_percent, critical_path:.timing.critical_path, unattributed_percent:.timing.unattributed_percent}' "$full"
  jq -c '.timing.fanout' "$full" | head -c 2000; echo
  return 0
}

# ── Run the two jobs ─────────────────────────────────────────────────────────
NASA_PAYLOAD=$(build_payload "nasa-1-scene" "NASA Genesis Mission" "$NASA_TEXT" 80)
dispatch_and_collect "nasa-1-scene" "$NASA_PAYLOAD" || true

if [[ "${PERF_INCLUDE_TEN_SCENE:-}" == "1" ]]; then
  TEN_PAYLOAD=$(build_payload "ten-scene" "A History of Spaceflight" "$TEN_SCENE_TEXT" 600)
  dispatch_and_collect "ten-scene" "$TEN_PAYLOAD" || true
else
  echo "[$MODE] skip ten-scene (PERF_INCLUDE_TEN_SCENE != 1)"
fi

echo "[$MODE] done. results: $RESULTS"
echo "[$MODE] job ids: $(tr '\n' ' ' < "$RESULTS/job-ids.txt" 2>/dev/null || true)"

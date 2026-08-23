#!/usr/bin/env bash
# Jackie Chan: PipelineGen unified clips generate -> Velox remote-render smoke.

set -euo pipefail
umask 077

MODE="fixture"
DRY_RUN=0
for arg in "$@"; do
  case "$arg" in
    --generate) MODE="generate" ;;
    --fixture) MODE="fixture" ;;
    --dry) DRY_RUN=1 ;;
    -h|--help)
      echo "usage: $0 [--generate|--fixture] [--dry]"
      exit 0
      ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

for bin in jq curl; do
  command -v "$bin" >/dev/null 2>&1 || { echo "missing required binary: $bin" >&2; exit 2; }
done

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
REQUEST_FIXTURE="$ROOT_DIR/tests/fixtures/script-generation/jackie_chan_generate_clips.json"
SPECSCENE_FIXTURE="$ROOT_DIR/tests/fixtures/script-generation/jackie_chan_generate_clips_specscene.json"
[[ -f "$REQUEST_FIXTURE" ]] || { echo "missing request fixture: $REQUEST_FIXTURE" >&2; exit 2; }
[[ -f "$SPECSCENE_FIXTURE" ]] || { echo "missing SpecScene fixture: $SPECSCENE_FIXTURE" >&2; exit 2; }

PIPELINEGEN_BASE=${PIPELINEGEN_BASE:-http://127.0.0.1:8080}
VELOX_RENDER_BASE=${VELOX_RENDER_BASE:-http://127.0.0.1:8000}
POLL_SECONDS=${E2E_POLL_SECONDS:-3}
TIMEOUT_SECONDS=${E2E_TIMEOUT_SECONDS:-3600}

WORK_DIR=$(mktemp -d /tmp/jackie-chan-e2e.XXXXXX)
trap 'rm -rf "$WORK_DIR"' EXIT INT TERM

json_http() {
  local method=$1 url=$2 token=$3 body_file=${4:-} out_file=$5 code
  local args=(-sS --max-time 60 -X "$method" -o "$out_file" -w '%{http_code}'
    -H "Authorization: Bearer $token" -H 'Content-Type: application/json')
  [[ -n "$body_file" ]] && args+=(--data-binary "@$body_file")
  code=$(curl "${args[@]}" "$url")
  printf '%s' "$code"
}

poll_job() {
  local base=$1 token=$2 template=$3 job_id=$4 out_file=$5
  local deadline=$(( $(date +%s) + TIMEOUT_SECONDS )) status code path
  while (( $(date +%s) < deadline )); do
    path=${template//\{job_id\}/$job_id}
    code=$(json_http GET "$base$path" "$token" "" "$out_file")
    if [[ "$code" == "200" ]]; then
      status=$(jq -r '.status // .job.status // ""' "$out_file" | tr '[:upper:]' '[:lower:]')
      printf 'job=%s status=%s\n' "$job_id" "${status:-unknown}" >&2
      case "$status" in
        completed|succeeded|failed|cancelled|dead_letter|quarantined)
          printf '%s' "$status"
          return 0
          ;;
      esac
    fi
    sleep "$POLL_SECONDS"
  done
  return 124
}

validate_specscene() {
  jq -e '
    .version == 1 and
    (.scenes | length) == 3 and
    ([.scenes[].index] == [0,1,2]) and
    (all(.scenes[]; (.id | length) > 0 and (.text | length) > 0)) and
    (all(.scenes[]; (.bindings.clip.drive_link | length) > 0)) and
    (all(.scenes[]; .bindings.voiceover.status == "completed")) and
    (all(.scenes[]; (.bindings.voiceover.link | length) > 0))
  ' "$1" >/dev/null
}

SPEC_FILE="$WORK_DIR/specscene.json"
TITLE="Jackie Chan Doc Voiceover"
CORRELATION_ID="jackie-chan-specscene-fixture-v1"
AUDIO_LANGUAGE="en"

if [[ "$MODE" == "generate" ]]; then
  if [[ "$DRY_RUN" != "1" && -z "${PIPELINEGEN_ADMIN_TOKEN:-}" ]]; then
    echo 'PIPELINEGEN_ADMIN_TOKEN is required with --generate' >&2
    exit 2
  fi

  AUDIO_LANGUAGE=$(jq -r '.items[0].output.translate_to // .items[0].language // "en"' "$REQUEST_FIXTURE")
  if [[ "$DRY_RUN" == "1" ]]; then
    echo "DRY RUN: would POST $REQUEST_FIXTURE to $PIPELINEGEN_BASE/api/script/generate"
    cp "$SPECSCENE_FIXTURE" "$SPEC_FILE"
  else
    PG_SUBMIT="$WORK_DIR/pipelinegen-submit.json"
    code=$(json_http POST "$PIPELINEGEN_BASE/api/script/generate" "$PIPELINEGEN_ADMIN_TOKEN" "$REQUEST_FIXTURE" "$PG_SUBMIT")
    [[ "$code" == "200" || "$code" == "202" ]] || {
      echo "PipelineGen submit failed: HTTP $code" >&2
      jq . "$PG_SUBMIT" >&2 || cat "$PG_SUBMIT" >&2
      exit 1
    }
    PG_JOB_ID=$(jq -r '.job_id // ""' "$PG_SUBMIT")
    [[ -n "$PG_JOB_ID" ]] || { echo 'PipelineGen response missing job_id' >&2; exit 1; }

    PG_FULL="$WORK_DIR/pipelinegen-full.json"
    PG_STATUS=$(poll_job "$PIPELINEGEN_BASE" "$PIPELINEGEN_ADMIN_TOKEN" "/api/jobs/{job_id}/full" "$PG_JOB_ID" "$PG_FULL") || {
      echo 'PipelineGen polling timed out' >&2
      exit 124
    }
    [[ "$PG_STATUS" == "completed" || "$PG_STATUS" == "succeeded" ]] || {
      echo "PipelineGen job ended as $PG_STATUS" >&2
      jq . "$PG_FULL" >&2 || true
      exit 1
    }

    # script.generate returns a direct GenerationResult for one item. Keep the
    # batch fallback only so the smoke remains diagnostic for multi-item runs.
    jq -e '(.result.output.specscene // .result.items[0].result.output.specscene)' "$PG_FULL" > "$SPEC_FILE"
    TITLE=$(jq -r '.result.title // .result.items[0].result.title // "Jackie Chan Doc Voiceover"' "$PG_FULL")
    CORRELATION_ID="pipelinegen-$PG_JOB_ID"
  fi
else
  cp "$SPECSCENE_FIXTURE" "$SPEC_FILE"
fi

validate_specscene "$SPEC_FILE" || {
  echo 'SpecScene validation failed: expected 3 ordered scenes with clip and completed voiceover bindings' >&2
  jq . "$SPEC_FILE" >&2 || true
  exit 1
}

VELOX_PAYLOAD="$WORK_DIR/velox-render-request.json"
jq -n \
  --arg title "$TITLE" \
  --arg correlation_id "$CORRELATION_ID" \
  --arg audio_language "$AUDIO_LANGUAGE" \
  --slurpfile spec "$SPEC_FILE" \
  '{
    source: {type: "clips"},
    video_name: $title,
    script_text: ($spec[0].scenes | map(.text) | join("\n\n")),
    scenes: $spec[0].scenes,
    scenes_json: ($spec[0].scenes | tojson),
    voiceover_paths: [$spec[0].scenes[].bindings.voiceover.link],
    correlation_id: $correlation_id,
    audio_language: $audio_language,
    video_mode: "clip",
    skip_creator: true
  }' > "$VELOX_PAYLOAD"

jq -e '
  .source.type == "clips" and
  (.video_name | length) > 0 and
  (.script_text | length) > 0 and
  (.scenes | length) == 3 and
  (.voiceover_paths | length) == 3 and
  .skip_creator == true
' "$VELOX_PAYLOAD" >/dev/null

if [[ "$DRY_RUN" == "1" ]]; then
  echo "DRY RUN: would POST the generated payload to $VELOX_RENDER_BASE/api/v1/script/generate"
  jq '{source,video_name,correlation_id,audio_language,scene_count:(.scenes|length),voiceover_count:(.voiceover_paths|length),skip_creator}' "$VELOX_PAYLOAD"
  exit 0
fi

[[ -n "${VELOX_RENDER_ADMIN_TOKEN:-}" ]] || { echo 'VELOX_RENDER_ADMIN_TOKEN is required' >&2; exit 2; }

WORKERS_FILE="$WORK_DIR/velox-workers.json"
workers_code=$(json_http GET "$VELOX_RENDER_BASE/api/v1/workers" "$VELOX_RENDER_ADMIN_TOKEN" "" "$WORKERS_FILE")
[[ "$workers_code" == "200" ]] || { echo "Velox worker preflight failed: HTTP $workers_code" >&2; exit 1; }
CAPABLE_WORKERS=$(jq -r '[.workers[] | select((.status|ascii_upcase)=="CONNECTED") | select(any(.executors[]?; (.id|startswith("scene.composite.v1")))) | .worker_id] | join(",")' "$WORKERS_FILE")
[[ -n "$CAPABLE_WORKERS" ]] || { echo 'No CONNECTED Velox worker advertises scene.composite.v1' >&2; exit 1; }

VELOX_SUBMIT="$WORK_DIR/velox-submit.json"
code=$(json_http POST "$VELOX_RENDER_BASE/api/v1/script/generate" "$VELOX_RENDER_ADMIN_TOKEN" "$VELOX_PAYLOAD" "$VELOX_SUBMIT")
[[ "$code" == "200" || "$code" == "202" ]] || { echo "Velox submit failed: HTTP $code" >&2; exit 1; }
VELOX_JOB_ID=$(jq -r '.job_id // .enqueue.job_id // ""' "$VELOX_SUBMIT")
[[ -n "$VELOX_JOB_ID" ]] || { echo 'Velox response missing job_id' >&2; exit 1; }

VELOX_FULL="$WORK_DIR/velox-full.json"
VELOX_STATUS=$(poll_job "$VELOX_RENDER_BASE" "$VELOX_RENDER_ADMIN_TOKEN" "/api/v1/script/jobs/{job_id}/full" "$VELOX_JOB_ID" "$VELOX_FULL") || exit 124
[[ "$VELOX_STATUS" == "completed" || "$VELOX_STATUS" == "succeeded" ]] || {
  echo "Velox job ended as $VELOX_STATUS" >&2
  jq . "$VELOX_FULL" >&2 || true
  exit 1
}

printf '\nPASS: PipelineGen SpecScene -> Velox unified generate job -> remote worker completed\n'
printf 'mode=%s\n' "$MODE"
[[ -n "${PG_JOB_ID:-}" ]] && printf 'pipelinegen_job_id=%s\n' "$PG_JOB_ID"
printf 'velox_job_id=%s\n' "$VELOX_JOB_ID"
printf 'final_status=%s\n' "$VELOX_STATUS"

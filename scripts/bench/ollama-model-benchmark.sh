#!/usr/bin/env bash
# Compare Ollama models across cold, prewarmed, and steady-state runs.
# All timing values come from Ollama's response fields; shell timing is only
# used for orchestration and is not reported as inference timing.
set -Eeuo pipefail

OLLAMA_URL="${OLLAMA_URL:-http://127.0.0.1:11434}"
MODEL_LIST="${BENCH_MODELS:-gemma4:e4b gemma4:e2b}"
read -r -a MODELS <<< "$MODEL_LIST"
RUNS="${BENCH_STEADY_RUNS:-5}"
OUTPUT="${BENCH_OUTPUT:-out/ollama-model-benchmark-$(date -u +%Y%m%dT%H%M%S).json}"
PROMPT="${BENCH_PROMPT:-Write a concise 120-word narration about Matt Damon using only verifiable, general facts. Return plain text.}"

command -v curl >/dev/null || { echo "curl is required" >&2; exit 2; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }
[[ "$RUNS" =~ ^[1-9][0-9]*$ ]] || { echo "BENCH_STEADY_RUNS must be a positive integer" >&2; exit 2; }
mkdir -p "$(dirname "$OUTPUT")"

curl -fsS --max-time 10 "$OLLAMA_URL/api/tags" >/dev/null || {
  echo "Ollama is unavailable at $OLLAMA_URL" >&2
  exit 1
}

for model in "${MODELS[@]}"; do
  curl -fsS --max-time 30 -X POST "$OLLAMA_URL/api/generate" \
    -H 'Content-Type: application/json' \
    -d "$(jq -nc --arg model "$model" '{model:$model,prompt:"ping",stream:false,keep_alive:0}')" >/dev/null || {
      echo "Model is unavailable: $model" >&2
      exit 1
    }
done

results='[]'
run_case() {
  local model="$1" phase="$2" index="$3" keep_alive="$4"
  local body response
  body=$(jq -nc --arg model "$model" --arg prompt "$PROMPT" --argjson keep "$keep_alive" \
    '{model:$model,prompt:$prompt,stream:false,keep_alive:$keep}')
  response=$(curl -fsS --max-time "${BENCH_TIMEOUT_SECONDS:-300}" \
    -X POST "$OLLAMA_URL/api/generate" -H 'Content-Type: application/json' -d "$body") || return 1
  jq -e --arg model "$model" --arg phase "$phase" --argjson index "$index" \
    '. + {model:$model, phase:$phase, run:index}' <<<"$response"
}

for model in "${MODELS[@]}"; do
  # Cold: force eviction immediately before the measured generation.
  cold=$(run_case "$model" cold 1 0) || { echo "Cold run failed for $model" >&2; exit 1; }
  results=$(jq --argjson item "$cold" '. + [$item]' <<<"$results")

  # Prewarmed: load and retain the model, then measure a separate generation.
  curl -fsS --max-time "${BENCH_TIMEOUT_SECONDS:-300}" -X POST "$OLLAMA_URL/api/generate" \
    -H 'Content-Type: application/json' \
    -d "$(jq -nc --arg model "$model" --arg prompt "$PROMPT" '{model:$model,prompt:$prompt,stream:false,keep_alive:"30m"}')" >/dev/null
  prewarmed=$(run_case "$model" prewarmed 1 '"30m"') || { echo "Prewarmed run failed for $model" >&2; exit 1; }
  results=$(jq --argjson item "$prewarmed" '. + [$item]' <<<"$results")

  # Steady state: retain the same model for exactly N consecutive runs.
  for ((i=1; i<=RUNS; i++)); do
    steady=$(run_case "$model" steady_state "$i" '"30m"') || { echo "Steady-state run failed for $model #$i" >&2; exit 1; }
    results=$(jq --argjson item "$steady" '. + [$item]' <<<"$results")
  done

  # Evict before switching models so each model's cold case is meaningful.
  curl -fsS --max-time 30 -X POST "$OLLAMA_URL/api/generate" \
    -H 'Content-Type: application/json' \
    -d "$(jq -nc --arg model "$model" '{model:$model,prompt:"",stream:false,keep_alive:0}')" >/dev/null
 done

jq --arg url "$OLLAMA_URL" --arg prompt "$PROMPT" --argjson steady_runs "$RUNS" \
  --argjson results "$results" \
  '{schema_version:"ollama-model-benchmark.v1",branch:"main",ollama_url:$url,prompt:$prompt,steady_state_runs:$steady_runs,results:$results}' \
  > "$OUTPUT"

jq -r '.results[] | [.model,.phase,(.run // 0),(.load_duration // 0),(.prompt_eval_count // 0),(.prompt_eval_duration // 0),(.eval_count // 0),(.eval_duration // 0)] | @tsv' "$OUTPUT" \
  | awk 'BEGIN { print "model\tphase\trun\tload_ns\tprompt_tokens\tprompt_ns\toutput_tokens\toutput_ns" } { print }'
echo "report=$OUTPUT"

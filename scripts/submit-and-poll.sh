#!/usr/bin/env bash
set -Eeuo pipefail

# Submit one JSON payload with the canonical auth/idempotency flow and wait for
# its terminal job state. Secrets are loaded by with-velox-auth and never
# printed. This is the operator path used by live certification jobs.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASE_URL="${VELOX_BASE_URL:-http://127.0.0.1:8000}"
PAYLOAD="${1:?usage: submit-and-poll.sh PAYLOAD.json PROJECT [IDEMPOTENCY_KEY] [TIMEOUT_SECONDS]}"
PROJECT="${2:?usage: submit-and-poll.sh PAYLOAD.json PROJECT [IDEMPOTENCY_KEY] [TIMEOUT_SECONDS]}"
IDEMPOTENCY_KEY="${3:-$(basename "$PAYLOAD" .json)-$(date -u +%Y%m%dT%H%M%SZ)}"
TIMEOUT_SECONDS="${4:-3600}"
INTERVAL_SECONDS="${VELOX_POLL_INTERVAL_SECONDS:-3}"
# The same floor the artifact staging layer enforces (2 GiB): submitting a run
# that cannot stage its own output only burns a worker and ends in
# `ErrDiskSpaceLow` after the render. Keep the two numbers aligned.
MIN_FREE_BYTES="${VELOX_MIN_FREE_BYTES:-2147483648}"
DF_BIN="${VELOX_DF_BIN:-df}"
CURL_BIN="${VELOX_CURL_BIN:-curl}"

[[ -r "$PAYLOAD" ]] || { echo "payload non leggibile: $PAYLOAD" >&2; exit 2; }
[[ "$TIMEOUT_SECONDS" =~ ^[0-9]+$ ]] || { echo "timeout non valido: $TIMEOUT_SECONDS" >&2; exit 2; }

# Fail before the Master creates an attempt, not after the render. Both checks
# are the failures that actually blocked the 2026-11-03 runs: a full disk
# (`free disk space below minimum`) and a service that was not answering.
preflight() {
  local free_bytes
  free_bytes="$("$DF_BIN" -Pk "$ROOT" | awk 'NR==2 {print $4 * 1024}')"
  if [[ ! "$free_bytes" =~ ^[0-9]+$ ]]; then
    echo "preflight: impossibile leggere lo spazio libero di $ROOT" >&2
    return 2
  fi
  if (( free_bytes < MIN_FREE_BYTES )); then
    printf 'preflight: solo %s byte liberi su %s (minimo %s); libera spazio prima del submit\n' \
      "$free_bytes" "$ROOT" "$MIN_FREE_BYTES" >&2
    return 3
  fi
  if ! "$CURL_BIN" -fsS --max-time 3 "$BASE_URL/health" >/dev/null; then
    printf 'preflight: %s/health non risponde; avvia il servizio prima del submit\n' "$BASE_URL" >&2
    return 3
  fi
  printf 'preflight: free=%s byte, health=OK, payload=%s\n' "$free_bytes" "$PAYLOAD" >&2
}

if [[ "${VELOX_SKIP_PREFLIGHT:-0}" != "1" ]]; then
  preflight || exit $?
fi
if [[ "${VELOX_PREFLIGHT_ONLY:-0}" == "1" ]]; then
  exit 0
fi

"$ROOT/scripts/with-velox-auth" bash -c '
  set -Eeuo pipefail
  payload="$1"
  project="$2"
  idem="$3"
  base="$4"
  response=$(curl -fsS --retry 3 --retry-delay 2 --max-time 30 -X POST "$base/api/script/generate" \
    -H "X-Velox-Admin-Token: $VELOX_ADMIN_TOKEN" \
    -H "Content-Type: application/json" \
    -H "Idempotency-Key: $idem" \
    --data-binary "@$payload")
  job_id=$(printf "%s" "$response" | jq -r ".job_id // empty")
  [[ -n "$job_id" ]] || { printf "%s\n" "$response"; exit 1; }
  printf "%s\n" "$response" | jq --arg project "$project" --arg key "$idem" \
    ". + {project: \$project, idempotency_key: \$key}"
  deadline=$(( $(date +%s) + $5 ))
  misses=0
  while (( $(date +%s) < deadline )); do
    # A single transient poll failure must not abort a long certification run:
    # retry the status read, and fail only after several consecutive misses.
    if ! body=$(curl -fsS --max-time 30 \
      -H "X-Velox-Admin-Token: $VELOX_ADMIN_TOKEN" \
      "$base/api/jobs/$job_id/full"); then
      misses=$((misses + 1))
      if (( misses >= 5 )); then
        echo "poll fallito 5 volte di fila per il job $job_id" >&2
        exit 2
      fi
      sleep "$6"
      continue
    fi
    misses=0
    status=$(printf "%s" "$body" | jq -r ".status // .job.status // \"UNKNOWN\"")
    case "$status" in
      SUCCEEDED|COMPLETED|SUCCESS|DONE)
        printf "%s\n" "$body" | jq --arg job_id "$job_id" \
          "{job_id: \$job_id, status, current_stage,
            expected: (.result.result.render_metrics.expected // null),
            successful: (.result.result.render_metrics.successful // null),
            failed: (.result.result.render_metrics.failed // null),
            localized_renders: (.result.result.localized_renders | length // 0)}"
        exit 0
        ;;
      FAILED|ERROR|CANCELLED|REJECTED|DEAD_LETTER)
        printf "%s\n" "$body" | jq --arg job_id "$job_id" \
          "{job_id: \$job_id, status, current_stage, error, events: (.events[-10:] // [])}"
        exit 1
        ;;
    esac
    sleep "$6"
  done
  echo "timeout in attesa del job $job_id" >&2
  exit 2
' _ "$PAYLOAD" "$PROJECT" "$IDEMPOTENCY_KEY" "$BASE_URL" "$TIMEOUT_SECONDS" "$INTERVAL_SECONDS"

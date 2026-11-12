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
# The fixed floor is only one of three conditions. A percentage of the actual
# filesystem protects large volumes where 2 GiB is noise, and an explicit
# staging estimate protects a retry that must hold input + output + cache at
# once. The required amount is the maximum of the three.
MIN_FREE_PERCENT="${VELOX_MIN_FREE_PERCENT:-5}"
REQUIRED_STAGING_BYTES="${VELOX_REQUIRED_STAGING_BYTES:-0}"
# Probe the filesystem the worker will write to, not necessarily the checkout
# that submits the request.
WORKER_ROOT="${VELOX_WORKER_ROOT:-$ROOT}"
DF_BIN="${VELOX_DF_BIN:-df}"
CURL_BIN="${VELOX_CURL_BIN:-curl}"
# The polling loop runs inside the auth wrapper's child shell; export the
# resolved binary so the override (and the test harness) survives that seam.
export CURL_BIN
AUTH_BIN="${VELOX_AUTH_BIN:-$ROOT/scripts/with-velox-auth}"

[[ -r "$PAYLOAD" ]] || { echo "payload non leggibile: $PAYLOAD" >&2; exit 2; }
[[ "$TIMEOUT_SECONDS" =~ ^[0-9]+$ ]] || { echo "timeout non valido: $TIMEOUT_SECONDS" >&2; exit 2; }
[[ "$MIN_FREE_BYTES" =~ ^[0-9]+$ ]] || { echo "VELOX_MIN_FREE_BYTES non valido: $MIN_FREE_BYTES" >&2; exit 2; }
[[ "$MIN_FREE_PERCENT" =~ ^[0-9]+$ ]] && (( MIN_FREE_PERCENT <= 100 )) || { echo "VELOX_MIN_FREE_PERCENT non valido: $MIN_FREE_PERCENT" >&2; exit 2; }
[[ "$REQUIRED_STAGING_BYTES" =~ ^[0-9]+$ ]] || { echo "VELOX_REQUIRED_STAGING_BYTES non valido: $REQUIRED_STAGING_BYTES" >&2; exit 2; }

# Fail before the Master creates an attempt, not after the render. Both checks
# are the failures that actually blocked the 2026-11-03 runs: a full disk
# (`free disk space below minimum`) and a service that was not answering.
preflight() {
  local total_kb free_kb total_bytes free_bytes required_bytes percent_bytes
  read -r total_kb free_kb < <("$DF_BIN" -Pk "$WORKER_ROOT" | awk 'NR==2 {print $2, $4}')
  if [[ ! "$total_kb" =~ ^[0-9]+$ || ! "$free_kb" =~ ^[0-9]+$ ]]; then
    echo "preflight: impossibile leggere lo spazio libero di $WORKER_ROOT" >&2
    return 2
  fi
  total_bytes=$(( total_kb * 1024 ))
  free_bytes=$(( free_kb * 1024 ))
  required_bytes=$MIN_FREE_BYTES
  if (( MIN_FREE_PERCENT > 0 )); then
    percent_bytes=$(( total_bytes * MIN_FREE_PERCENT / 100 ))
    if (( percent_bytes > required_bytes )); then required_bytes=$percent_bytes; fi
  fi
  if (( REQUIRED_STAGING_BYTES > required_bytes )); then required_bytes=$REQUIRED_STAGING_BYTES; fi
  if (( free_bytes < required_bytes )); then
    printf 'preflight: solo %s byte liberi su %s (richiesti %s = max(floor=%s, percent=%s%%, staging=%s)); libera spazio prima del submit\n' \
      "$free_bytes" "$WORKER_ROOT" "$required_bytes" "$MIN_FREE_BYTES" "$MIN_FREE_PERCENT" "$REQUIRED_STAGING_BYTES" >&2
    return 3
  fi
  if ! "$CURL_BIN" -fsS --max-time 3 "$BASE_URL/health" >/dev/null; then
    printf 'preflight: %s/health non risponde; avvia il servizio prima del submit\n' "$BASE_URL" >&2
    return 3
  fi
  printf 'preflight: free=%s total=%s required=%s root=%s health=OK payload=%s\n' \
    "$free_bytes" "$total_bytes" "$required_bytes" "$WORKER_ROOT" "$PAYLOAD" >&2
}

if [[ "${VELOX_SKIP_PREFLIGHT:-0}" != "1" ]]; then
  preflight || exit $?
fi
if [[ "${VELOX_PREFLIGHT_ONLY:-0}" == "1" ]]; then
  exit 0
fi

"$AUTH_BIN" bash -c '
  set -Eeuo pipefail
  payload="$1"
  project="$2"
  idem="$3"
  base="$4"
  response=$("$CURL_BIN" -fsS --retry 3 --retry-delay 2 --max-time 30 -X POST "$base/api/script/generate" \
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
  polling_unavailable=0
  while (( $(date +%s) < deadline )); do
    # POLLING_UNAVAILABLE is an observation of the transport, not a verdict on
    # the remote job. A long render can outlive a transient master/network
    # outage, so after five consecutive misses the loop reports the distinct
    # observation and keeps watching the SAME job id. It never resubmits.
    if ! body=$("$CURL_BIN" -fsS --max-time 30 \
      -H "X-Velox-Admin-Token: $VELOX_ADMIN_TOKEN" \
      "$base/api/jobs/$job_id/full"); then
      misses=$((misses + 1))
      if (( misses >= 5 && polling_unavailable == 0 )); then
        echo "POLLING_UNAVAILABLE job_id=$job_id consecutive_misses=$misses (JOB_FAILED not inferred; still polling)" >&2
        polling_unavailable=1
      fi
      sleep "$6"
      continue
    fi
    if (( polling_unavailable == 1 )); then
      echo "POLLING_RECOVERED job_id=$job_id" >&2
      polling_unavailable=0
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
  if (( polling_unavailable == 1 )); then
    echo "POLLING_UNAVAILABLE job_id=$job_id reason=deadline (JOB_FAILED not inferred; do not resubmit)" >&2
    exit 4
  fi
  echo "timeout in attesa del job $job_id" >&2
  exit 2
' _ "$PAYLOAD" "$PROJECT" "$IDEMPOTENCY_KEY" "$BASE_URL" "$TIMEOUT_SECONDS" "$INTERVAL_SECONDS"

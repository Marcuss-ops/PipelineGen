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

[[ -r "$PAYLOAD" ]] || { echo "payload non leggibile: $PAYLOAD" >&2; exit 2; }
[[ "$TIMEOUT_SECONDS" =~ ^[0-9]+$ ]] || { echo "timeout non valido: $TIMEOUT_SECONDS" >&2; exit 2; }

"$ROOT/scripts/with-velox-auth" bash -c '
  set -Eeuo pipefail
  payload="$1"
  project="$2"
  idem="$3"
  base="$4"
  response=$(curl -fsS --max-time 30 -X POST "$base/api/script/generate" \
    -H "X-Velox-Admin-Token: $VELOX_ADMIN_TOKEN" \
    -H "Content-Type: application/json" \
    -H "Idempotency-Key: $idem" \
    --data-binary "@$payload")
  job_id=$(printf "%s" "$response" | jq -r ".job_id // empty")
  [[ -n "$job_id" ]] || { printf "%s\n" "$response"; exit 1; }
  printf "%s\n" "$response" | jq --arg project "$project" --arg key "$idem" \
    ". + {project: \$project, idempotency_key: \$key}"
  deadline=$(( $(date +%s) + $5 ))
  while (( $(date +%s) < deadline )); do
    body=$(curl -fsS --max-time 30 \
      -H "X-Velox-Admin-Token: $VELOX_ADMIN_TOKEN" \
      "$base/api/jobs/$job_id/full")
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

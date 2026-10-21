#!/usr/bin/env bash
# Submit one Garlasco animation-audit payload and persist the full job body.
#
#   run_audit.sh overlay|final
#
# Evidence lands next to the payload:
#   evidence/<profile>.job_id
#   evidence/<profile>.full.json
#   evidence/<profile>.poll.log
#
# The admin token is loaded by refactored/scripts/with-velox-auth and is never
# printed. The job may run for a long time: run this script in the background.
set -uo pipefail

PROFILE=${1:?usage: run_audit.sh overlay|final}
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO=$(cd "$HERE/../../.." && pwd)  # refactored/ pipeline root
BASE_URL=${VELOX_BASE_URL:-http://127.0.0.1:8000}
PAYLOAD="$HERE/payloads/$PROFILE.json"
EVIDENCE="$HERE/evidence"
LOG="$EVIDENCE/$PROFILE.poll.log"
TIMEOUT=${AUDIT_TIMEOUT_SECONDS:-5400}
JOB_ID_FILE="$EVIDENCE/$PROFILE.job_id"
PAYLOAD_SHA_FILE="$EVIDENCE/$PROFILE.payload.sha256"

[[ -r "$PAYLOAD" ]] || { echo "missing payload $PAYLOAD (run build_payloads.py)"; exit 2; }
mkdir -p "$EVIDENCE"
: > "$LOG"

# A stable request fingerprint makes POST replay safe even if this process
# exits after the server accepted the request but before job_id was saved.
payload_sha=$(sha256sum "$PAYLOAD" | awk '{print $1}')
idempotency_key="garlasco-animation-audit-$PROFILE-$payload_sha"

# Resume polling for the same payload before submitting anything. The worker
# itself resumes from its durable phase checkpoint on retry; this wrapper also
# avoids creating a second job when the operator reruns it after a disconnect.
job_id=""
if [[ -s "$JOB_ID_FILE" && -s "$PAYLOAD_SHA_FILE" ]] \
    && [[ "$(<"$PAYLOAD_SHA_FILE")" == "$payload_sha" ]]; then
  job_id=$(<"$JOB_ID_FILE")
fi

cd "$REPO" || exit 1

submit() {
  ./scripts/with-velox-auth bash -c '
    set -euo pipefail
    curl -fsS --retry 2 --max-time 60 -X POST "$0/api/script/generate" \
      -H "X-Velox-Admin-Token: $VELOX_ADMIN_TOKEN" \
      -H "Content-Type: application/json" \
      -H "Idempotency-Key: $1" \
      --data-binary @"$2"
  ' "$BASE_URL" "$idempotency_key" "$PAYLOAD"
}

if [[ -z "$job_id" ]]; then
  resp=$(submit) || { echo "submit failed: $resp" | tee -a "$LOG"; exit 1; }
  job_id=$(printf '%s' "$resp" | jq -r '.job_id // empty')
  if [[ -z "$job_id" ]]; then
    echo "no job_id in submit response: $resp" | tee -a "$LOG"
    exit 1
  fi
  printf '%s\n' "$payload_sha" > "$PAYLOAD_SHA_FILE"
  printf '%s\n' "$job_id" > "$JOB_ID_FILE"
  echo "submitted $PROFILE job_id=$job_id at $(date -u +%H:%M:%S)" | tee -a "$LOG"
else
  echo "resuming $PROFILE job_id=$job_id (payload sha256=$payload_sha) at $(date -u +%H:%M:%S)" | tee -a "$LOG"
fi

deadline=$(( $(date +%s) + TIMEOUT ))
misses=0
while (( $(date +%s) < deadline )); do
  body=$(./scripts/with-velox-auth bash -c '
    curl -fsS -m 30 -H "X-Velox-Admin-Token: $VELOX_ADMIN_TOKEN" "$0/api/jobs/$1/full"
  ' "$BASE_URL" "$job_id" 2>/dev/null) || {
    misses=$((misses + 1))
    echo "poll miss $misses at $(date -u +%H:%M:%S)" >> "$LOG"
    (( misses >= 5 )) && { echo "5 consecutive poll failures" | tee -a "$LOG"; exit 2; }
    sleep 10
    continue
  }
  misses=0
  status=$(printf '%s' "$body" | jq -r '.status // .job.status // "UNKNOWN"')
  echo "$(date -u +%H:%M:%S) status=$status" >> "$LOG"
  case "$status" in
    SUCCEEDED|COMPLETED|SUCCESS|DONE)
      printf '%s' "$body" > "$EVIDENCE/$PROFILE.full.json"
      echo "terminal status=$status evidence=$EVIDENCE/$PROFILE.full.json" | tee -a "$LOG"
      exit 0
      ;;
    FAILED|ERROR|CANCELED|CANCELLED)
      printf '%s' "$body" > "$EVIDENCE/$PROFILE.full.json"
      echo "terminal status=$status evidence=$EVIDENCE/$PROFILE.full.json" | tee -a "$LOG"
      exit 4
      ;;
  esac
  sleep 15
done
echo "timeout after ${TIMEOUT}s waiting for $job_id" | tee -a "$LOG"
exit 3

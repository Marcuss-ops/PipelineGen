#!/usr/bin/env bash
# Driver per il gate live overlay-only senza mappe (90 overlay target).
# Uso: run_golden_nomaps.sh <label>
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$ROOT"

LABEL="${1:?uso: run_golden_nomaps.sh <label>}"
PAYLOAD="ops/jobs/overlay_throughput_golden_90_20261028.v1-nomaps.generate.json"
RES="ops/jobs/overlay_throughput_golden_100_20261022/results/$LABEL"
mkdir -p "$RES"
cp "$PAYLOAD" "$RES/payload.json"
sha256sum "$RES/payload.json" > "$RES/payload.sha256"

KEY="overlay-throughput-golden-nomaps-${LABEL}-$(date -u +%Y%m%dT%H%M%SZ)"
printf '%s\n' "$KEY" > "$RES/idempotency_key.txt"
echo "[run_golden_nomaps] label=$LABEL key=$KEY start=$(date -u +%FT%TZ)"

set +e
scripts/submit-and-poll.sh "$PAYLOAD" overlay-throughput-golden-90 "$KEY" 3600 > "$RES/submit.log" 2>&1
rc=$?
set -e
echo "[run_golden_nomaps] submit_rc=$rc end=$(date -u +%FT%TZ)"

job_id="$(grep -o 'job_[0-9][0-9a-zA-Z_]*' "$RES/submit.log" | head -1 || true)"
echo "[run_golden_nomaps] job_id=$job_id"
[ -n "$job_id" ] || { echo "[run_golden_nomaps] nessun job_id catturato"; exit 2; }

scripts/with-velox-auth bash -c \
  'curl -fsS --max-time 120 -H "X-Velox-Admin-Token: $VELOX_ADMIN_TOKEN" \
   "http://127.0.0.1:8000/api/jobs/'"$job_id"'/full"' > "$RES/job.full.json"

go run ./cmd/goldensummary --job-full "$RES/job.full.json" \
  --db data/media/media.db.sqlite --out "$RES/summary.json"
echo "[run_golden_nomaps] summary scritto per $LABEL"

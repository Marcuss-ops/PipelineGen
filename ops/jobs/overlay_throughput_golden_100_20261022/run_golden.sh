#!/usr/bin/env bash
# Driver per una run golden overlay-only (payload immutabile v2).
# Uso: run_golden.sh <label>   (es. overlay-only-v20)
# Produce in results/<label>/: submit.log, job.full.json, summary.json
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$ROOT"

LABEL="${1:?uso: run_golden.sh <label>}"
PAYLOAD="ops/jobs/overlay_throughput_golden_100_20261022.v2.generate.json"
RES="ops/jobs/overlay_throughput_golden_100_20261022/results/$LABEL"
mkdir -p "$RES"

KEY="overlay-throughput-golden-${LABEL}-$(date -u +%Y%m%dT%H%M%SZ)"
printf '%s\n' "$KEY" > "$RES/idempotency_key.txt"
echo "[run_golden] label=$LABEL key=$KEY start=$(date -u +%FT%TZ)"

set +e
scripts/submit-and-poll.sh "$PAYLOAD" overlay-throughput-golden-100 "$KEY" 3600 > "$RES/submit.log" 2>&1
rc=$?
set -e
echo "[run_golden] submit_rc=$rc end=$(date -u +%FT%TZ)"

job_id="$(grep -o 'job_[0-9][0-9a-zA-Z_]*' "$RES/submit.log" | head -1 || true)"
echo "[run_golden] job_id=$job_id"
[ -n "$job_id" ] || { echo "[run_golden] nessun job_id catturato"; exit 2; }

scripts/with-velox-auth bash -c \
  'curl -fsS --max-time 120 -H "X-Velox-Admin-Token: $VELOX_ADMIN_TOKEN" \
   "http://127.0.0.1:8000/api/jobs/'"$job_id"'/full"' > "$RES/job.full.json"

go run ./cmd/goldensummary --job-full "$RES/job.full.json" \
  --db data/media/media.db.sqlite --out "$RES/summary.json"
echo "[run_golden] summary scritto per $LABEL"

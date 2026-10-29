#!/usr/bin/env bash
# Driver per una run golden overlay-only (payload immutabile v2).
# Uso: run_golden.sh <label>   (es. overlay-only-v20)
# Produce in results/<label>/: submit.log, job.full.json, summary.json
#
# ATTENZIONE (29/10/2026): il payload v2 storico contiene
# `map_animation=region_zoom`, id ritirato e fail-closed: ogni run v2 muore in
# `overlay plan compilation` (rilevato sulla run v2-r8 del 29/10; anche le
# v6…v28 col vecchio binario non riproducibili). Il driver punta ora alla
# v2.1-nomaps (SHA ad5e3468a5eee166df5f4f0899439e6de239b7e10490f042f9237f728db9f159):
# corpo identico alla v2 con territory_glow_reveal (id certificato) e
# identifieri `…-v2.1-nomaps`. Le mappe restano DISABLED come in v2: l'id non
# raggiunge mai un renderer, serve solo alla validazione a compile.
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$ROOT"

LABEL="${1:?uso: run_golden.sh <label>}"
# Payload runnabile v2.1-nomaps di default; override esplicito per varianti
# (es. v2.2-streaming) conservando la provenienza del driver.
PAYLOAD="${PAYLOAD_OVERRIDE:-ops/jobs/overlay_throughput_golden_100_20261022.v2.1-nomaps.generate.json}"
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

#!/usr/bin/env bash
# Driver run golden overlay-only sul payload v3.1-maps
# (ops/jobs/overlay_throughput_golden_100_20261022.v3.1-maps.generate.json,
#  SHA-256 ba3befed44c7676f810945dcf04ef68daf8898e28aac6d49c2ca5ad72fc8d846).
# v3.1 = v3-maps con l'unico campo presentazionale ritirato dal renderer:
# map_animation region_zoom → territory_glow_reveal (famiglia region_highlight,
# id certificato). v3 non può più compilare (fail-closed), v3.1 è il contratto.
# Uso: run_golden_v3.sh <label>   (es. v3maps-r8)
# Produce in results/<label>/: submit.log, submission.json, job.full.json, summary.json
# Topologia richiesta (cartello parità): 3 daemon Chronon3d attivi (socket
# primaria + B + C), worker con gpu_lanes=3, nessun altro carico pesante.
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$ROOT"

LABEL="${1:?uso: run_golden_v3.sh <label>}"
PAYLOAD="ops/jobs/overlay_throughput_golden_100_20261022.v3.1-maps.generate.json"
RES="ops/jobs/overlay_throughput_golden_100_20261022/results/$LABEL"
mkdir -p "$RES"

KEY="overlay-throughput-golden-${LABEL}-$(date -u +%Y%m%dT%H%M%SZ)"
printf '%s\n' "$KEY" > "$RES/idempotency_key.txt"
echo "[run_golden_v3] label=$LABEL key=$KEY payload_sha=$(sha256sum "$PAYLOAD" | awk '{print $1}') start=$(date -u +%FT%TZ)"

set +e
scripts/submit-and-poll.sh "$PAYLOAD" overlay-throughput-golden-100 "$KEY" 3600 > "$RES/submit.log" 2>&1
rc=$?
set -e

job_id="$(grep -o 'job_[0-9][0-9a-zA-Z_]*' "$RES/submit.log" | head -1 || true)"
echo "[run_golden_v3] submit_rc=$rc job_id=$job_id end=$(date -u +%FT%TZ)"

if [ -s "$RES/submit.log" ] && grep -q '"job_id"' "$RES/submit.log"; then
  # submit-and-poll prints a submission line with job_id; persist both shapes.
  cp "$RES/submit.log" "$RES/submission.json.tmp" && mv "$RES/submission.json.tmp" "$RES/submission.json"
fi
[ -n "$job_id" ] || { echo "[run_golden_v3] nessun job_id catturato"; exit 2; }

scripts/with-velox-auth bash -c \
  'curl -fsS --max-time 120 -H "X-Velox-Admin-Token: $VELOX_ADMIN_TOKEN" \
   "http://127.0.0.1:8000/api/jobs/'"$job_id"'/full"' > "$RES/job.full.json"

if [ "$rc" -eq 0 ]; then
  go run ./cmd/goldensummary --job-full "$RES/job.full.json" \
    --db data/media/media.db.sqlite --out "$RES/summary.json"
  echo "[run_golden_v3] summary scritto per $LABEL"
else
  echo "[run_golden_v3] run $LABEL NON riuscita (rc=$rc); snapshot job.full salvato" >&2
  exit "$rc"
fi

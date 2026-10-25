#!/usr/bin/env bash
# scripts/deploy/deploy_golden_chain.sh — the golden-run leg of the deploy
# chain, as ONE command (TICKET-OVERLAY-THROUGHPUT-100-MIN measurement
# contract: "stesso payload immutabile, mediana 3 run, item validati only"):
#
#   build -> deploy (install -> restart -> wait-for-ready -> verify identity)
#         -> golden run x3 (same immutable payload)
#         -> goldensummary aggregate (median-of-3 summary set)
#
# Uso: deploy_golden_chain.sh [label-prefix]
#   Ogni run produce ops/jobs/.../results/<prefix>-rN/ (submit.log,
#   job.full.json, summary.json); la mediana finisce in
#   results/<prefix>/summary-set.json (schema
#   pipelinegen.golden-overlay-summary-set.v1).
#
# Fail-closed: il primo passo che fallisce chiude la catena (set -e); una run
# golden senza job_id esce con codice 2 da run_golden.sh e non viene
# aggregata.
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

PREFIX="${1:-golden-chain-$(date -u +%Y%m%dT%H%M%SZ)}"
GOLDEN_DIR="ops/jobs/overlay_throughput_golden_100_20261022"
SET_DIR="$GOLDEN_DIR/results/$PREFIX"

echo "[deploy-golden] start=$(date -u +%FT%TZ) prefix=$PREFIX"

# 1. build -> install -> restart -> wait-for-ready -> verify identity
bash scripts/deploy/pipelinegen_deploy.sh

# 2. golden run x3 — same immutable payload (run_golden.sh owns the
#    submit-and-poll + job.full.json snapshot + per-run goldensummary).
summaries=()
for i in 1 2 3; do
	bash "$GOLDEN_DIR/run_golden.sh" "$PREFIX-r$i"
	summaries+=("$GOLDEN_DIR/results/$PREFIX-r$i/summary.json")
done

# 3. goldensummary aggregate — the median-of-3 artifact the measurement
#    contract reports (median + range, per-run rows preserved).
mkdir -p "$SET_DIR"
go run ./cmd/goldensummary --aggregate --out "$SET_DIR/summary-set.json" "${summaries[@]}"
echo "[deploy-golden] summary-set=$SET_DIR/summary-set.json"
echo "[deploy-golden] end=$(date -u +%FT%TZ)"

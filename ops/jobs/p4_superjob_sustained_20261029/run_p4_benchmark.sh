#!/usr/bin/env bash
# Benchmark live P4 packed superjob — capacità sustained reale.
#
# Misura in due rung distinte (nessuna delle due è una stima):
#   R1 (engine) — sessione segmentata CHRONON vs N sessioni singole sul binario
#      live (TestDaemonSegmentedServiceCostIsBelowPerItemSessions), più parità
#      pixel per segmento (TestSegmentedSessionMatchesIndependentSingleItemRenders)
#      e retry indotto live (TestFinalizeSegmentsLiveInducedRetryRendersOnlyTheFailedSegment).
#   R2 (e2e) — N ripetizioni della catena golden nomaps (run_golden_nomaps.sh)
#      con monitor di carico; una run conta solo se il workerlog contiene la
#      riga "P4 segmented output" (altrimenti ha misurato il percorso fused
#      manifest e NON vale come rung P4).
#
# Sequenza live: apre daemon (R1) e sottomette job reali (R2). Va lanciata
# solo nella finestra autorizzata, con 3 daemon attivi e nessun altro carico
# pesante (contratto di misura del golden README).
#
# Uso: run_p4_benchmark.sh [N_REPETIZIONI] [PREFIX_LABEL]
# Default: 3 ripetizioni, prefix p4-sustained.
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$ROOT"

N_RUNS="${1:-3}"
PREFIX="${2:-p4-sustained}"
GOLDEN_DIR="ops/jobs/overlay_throughput_golden_100_20261022"
RES="ops/jobs/p4_superjob_sustained_20261029/results"
mkdir -p "$RES"

fail() { echo "[p4-benchmark] FATAL: $*" >&2; exit 2; }
warn() { echo "[p4-benchmark] ATTENZIONE: $*" >&2; }

# ── Precheck (bloccante: una run in condizioni sbagliate non è evidenza) ────
for unit in chronon3d chronon3d-b chronon3d-c; do
  systemctl is-active --quiet "$unit" 2>/dev/null || fail "unit $unit non attiva: la topologia P4 richiede 3 daemon"
done
pgrep -f 'cc1plus|cc1 |ninja -j|cargo|cmake --build' >/dev/null 2>&1 \
  && fail "build concorrente in corso: nessun altro carico pesante durante la misura"
command -v ffmpeg >/dev/null || fail "ffmpeg non disponibile (richiesto dalle prove live)"

CHRONON_BINARY="${CHRONON_BINARY:-}"
CHRONON_ROOT="$(cd "$ROOT/.." && pwd)/Chronon3d"
if [ -z "$CHRONON_BINARY" ]; then
  for candidate in \
    "$CHRONON_ROOT/build/chronon/linux-video-release/apps/chronon3d_cli/chronon3d_cli" \
    "$CHRONON_ROOT/build/chronon/linux-video-fast-dev/apps/chronon3d_cli/chronon3d_cli" \
    "$CHRONON_ROOT/build/chronon/linux-video-test/apps/chronon3d_cli/chronon3d_cli" \
    "$CHRONON_ROOT/build/chronon/linux-fast-dev/apps/chronon3d_cli/chronon3d_cli"; do
    if [ -x "$candidate" ]; then CHRONON_BINARY="$candidate"; break; fi
  done
fi
[ -x "$CHRONON_BINARY" ] || fail "binario Chronon3d non trovato (impostare CHRONON_BINARY)"
echo "[p4-benchmark] CHRONON_BINARY=$CHRONON_BINARY"
[ "${CHRONON_HOME:-}" ] || warn "CHRONON_HOME non impostato: le prove live usano il default del binario (provenienza nella README)"

DB="data/media/media.db.sqlite"
[ -f "$DB" ] || fail "manca $DB (serve a goldensummary e alle query di coda)"

echo "[p4-benchmark] R1/2: prove engine (costo sessione segmentata vs singole, parità, retry)"
set +e
(
  cd "../RenderingGen/renderinggen" &&
  CHRONON_BINARY="$CHRONON_BINARY" GOMAXPROCS=2 go test ./internal/chronon -count=1 -v \
    -run 'TestDaemonSegmentedRequestRefusesThePipeLane|TestDaemonSegmentedServiceCostIsBelowPerItemSessions'
) > "$RES/probe-engine-service-cost.log" 2>&1
R1_SERVICE_RC=$?
(
  cd "../RenderingGen/renderinggen" &&
  CHRONON_BINARY="$CHRONON_BINARY" GOMAXPROCS=2 go test ./internal/processor -count=1 -v \
    -run 'TestSegmentedSessionMatchesIndependentSingleItemRenders|TestFinalizeSegmentsLiveInducedRetryRendersOnlyTheFailedSegment'
) > "$RES/probe-parity-retry.log" 2>&1
RC_PARITY=$?
set -e
echo "[p4-benchmark] R1 rc: service_cost=$R1_SERVICE_RC parity_retry=$RC_PARITY"
grep -h 'P4 service cost:' "$RES"/probe-*.log > "$RES/probe-service-cost.txt" || true
if [ "$R1_SERVICE_RC" -ne 0 ] || [ "$RC_PARITY" -ne 0 ]; then
  warn "una prova engine è fallita (log in $RES): NON usare R2 per chiudere il rung"
  if [ "${P4_STOP_ON_PROBE_FAIL:-1}" = "1" ]; then exit "$R1_SERVICE_RC"; fi
fi

# ── R2: catena e2e golden nomaps con monitor di carico ──────────────────────
SUCCEEDED_LABELS=""
for i in $(seq 1 "$N_RUNS"); do
  LABEL="$PREFIX-r$i"
  echo "[p4-benchmark] R2/$N_RUNS: run $LABEL..."
  bash "$GOLDEN_DIR/load_monitor.sh" "$RES/load-monitor-$LABEL.tsv" 15 &
  MON_PID=$!
  trap 'kill "$MON_PID" 2>/dev/null || true' EXIT
  set +e
  bash "$GOLDEN_DIR/run_golden_nomaps.sh" "$LABEL"
  RC=$?
  set -e
  kill "$MON_PID" 2>/dev/null || true
  wait "$MON_PID" 2>/dev/null || true
  trap - EXIT
  if [ "$RC" -ne 0 ]; then
    warn "run $LABEL fallita (rc=$RC), snapshot in $GOLDEN_DIR/results/$LABEL"
    continue
  fi
  SUCCEEDED_LABELS="$SUCCEEDED_LABELS $LABEL"
  JOB_ID="$(grep -o 'job_[0-9][0-9a-zA-Z_]*' "$GOLDEN_DIR/results/$LABEL/submit.log" | head -1 || true)"
  if [ -z "$JOB_ID" ]; then
    warn "$LABEL: nessun job_id catturato: skip verifica attivazione P4"
    continue
  fi
  # Provenienza: la run conta come P4 SOLO se il worker ha dichiarato la
  # sessione segmentata ("P4 segmented output: N semantic item windows ...").
  # La riga è job-scoped: contiene i job id dei batch renderizzati dal
  # worker (i batch child portano il parent id del job sottomesso).
  journalctl --no-pager -o cat -u pipelinegen-worker -u renderinggen \
    --since "-2 hours" 2>/dev/null |
    grep 'P4 segmented output' | grep -F "$JOB_ID" \
    > "$RES/p4-activation-$LABEL.log" || true
  if [ -s "$RES/p4-activation-$LABEL.log" ]; then
    echo "[p4-benchmark] $LABEL: percorso P4 ATTIVO ($(wc -l < "$RES/p4-activation-$LABEL.log") dichiarazioni)"
  else
    warn "$LABEL: nessuna riga 'P4 segmented output' riferibile a $JOB_ID: la run ha percorso fused manifest, NON vale come rung P4"
  fi
  if command -v sqlite3 >/dev/null; then
    sqlite3 -header -column "$DB" \
      "SELECT stage, operation, COUNT(*) AS ops, CAST(AVG(duration_ms) AS INT) AS avg_ms, CAST(AVG(queue_wait_ms) AS INT) AS avg_queue_ms
       FROM run_operation_observations
       WHERE job_id LIKE '$JOB_ID%' AND component IN ('chronon','render')
       GROUP BY stage, operation ORDER BY stage, operation" \
      > "$RES/queue-service-$LABEL.tsv" || true
  fi
done

echo "[p4-benchmark] run R2 riuscite:${SUCCEEDED_LABELS:- nessuna}"
echo "[p4-benchmark] fine. Evidenze: $RES/ ; summary per-run: $GOLDEN_DIR/results/<label>/summary.json"
COUNT=$(echo "$SUCCEEDED_LABELS" | wc -w)
[ "$COUNT" -ge 3 ] || warn "meno di 3 run riuscite: il confronto con la mediana A5 richiede 3 run comparabili"

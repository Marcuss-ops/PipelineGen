#!/usr/bin/env bash
# Resubmit the four Garlasco videos (r10) one at a time.
#
# r10 differs from r9 only in the protected intro: the three 5s clips are the
# Drive stock clips (Garlasco_TG_Interviste_Stock_20x60s_5s), referenced by
# Drive file id and registered as drive-only catalog assets. Nothing is
# downloaded or copied on the master ahead of the run: the canonical
# materializer fetches them from Drive when the job needs them.
#
# Payloads also carry a -r10 identity (correlation_id, item id, Drive render
# subfolder) so the run never overwrites an earlier artifact.
#
# Usage: run_r10.sh [name ...]      (default: all four, in delivery order)
#   LOG=/path/r13.log TIMEOUT_SECONDS=5400 run_r10.sh 02_procedimento
set -Eeuo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/../../../.." && pwd)"
LOG="${LOG:-$HERE/r13.log}"
TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-5400}"
PROJECT="garlasco-batch-10-introfix-r13"

if (($# > 0)); then
  ORDER=("$@")
else
  ORDER=(01_cronologia 02_procedimento 03_stasi_sentenza 04_nuova_indagine)
fi

for name in "${ORDER[@]}"; do
  payload="$HERE/$name.r13.json"
  [[ -r "$payload" ]] || { echo "payload mancante: $payload" | tee -a "$LOG" >&2; exit 2; }
  idem="garlasco-introfix-r13-$name-$(date -u +%Y%m%dT%H%M%SZ)"
  echo "=== $(date -u +%FT%TZ) submit $name (idem=$idem)" | tee -a "$LOG"
  if "$ROOT/scripts/submit-and-poll.sh" "$payload" "$PROJECT" "$idem" "$TIMEOUT_SECONDS" >>"$LOG" 2>&1; then
    echo "=== $(date -u +%FT%TZ) $name terminal OK" | tee -a "$LOG"
  else
    echo "=== $(date -u +%FT%TZ) $name FAILED (continuing)" | tee -a "$LOG"
  fi
done
echo "=== $(date -u +%FT%TZ) resubmission r10 complete for: ${ORDER[*]}" | tee -a "$LOG"

#!/usr/bin/env bash
# Resubmit the four Garlasco intro-clip videos (r9), one at a time.
#
# Same operator path used for the r7/r8 resubmissions: POST /api/script/generate
# through scripts/submit-and-poll.sh, sequential, so the render worker never runs
# two composite renders at once.
#
# Payloads are byte-identical to the r7/r8 runs except for:
#   correlation_id / items[0].id                -> -r9 suffix (new job identity)
#   items[0].output.render.drive_subfolder_name -> -r9 suffix (no overwrite)
#   force_refresh                               -> false (reuse cached script/audio)
#
# Usage: run_r9.sh [name ...]      (default: all four, in delivery order)
#   LOG=/path/r9.log TIMEOUT_SECONDS=5400 run_r9.sh 02_procedimento 03_stasi_sentenza
set -Eeuo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/../../../.." && pwd)"
LOG="${LOG:-$HERE/r9.log}"
TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-5400}"
PROJECT="garlasco-batch-10-introfix-r9"

if (($# > 0)); then
  ORDER=("$@")
else
  ORDER=(01_cronologia 02_procedimento 03_stasi_sentenza 04_nuova_indagine)
fi

for name in "${ORDER[@]}"; do
  payload="$HERE/$name.r9.json"
  [[ -r "$payload" ]] || { echo "payload mancante: $payload" | tee -a "$LOG" >&2; exit 2; }
  idem="garlasco-introfix-r9-$name-$(date -u +%Y%m%dT%H%M%SZ)"
  echo "=== $(date -u +%FT%TZ) submit $name (idem=$idem)" | tee -a "$LOG"
  if "$ROOT/scripts/submit-and-poll.sh" "$payload" "$PROJECT" "$idem" "$TIMEOUT_SECONDS" >>"$LOG" 2>&1; then
    echo "=== $(date -u +%FT%TZ) $name terminal OK" | tee -a "$LOG"
  else
    echo "=== $(date -u +%FT%TZ) $name FAILED (continuing)" | tee -a "$LOG"
  fi
done
echo "=== $(date -u +%FT%TZ) resubmission r9 complete for: ${ORDER[*]}" | tee -a "$LOG"

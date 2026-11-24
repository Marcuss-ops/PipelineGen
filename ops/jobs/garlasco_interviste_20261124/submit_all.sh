#!/usr/bin/env bash
# Submit the Garlasco interview payloads sequentially and wait for each job.
#
# Usage:
#   ./submit_all.sh [--dry-run] [payload ...]
#
# By default every payloads/*.process.json is submitted in filename order
# (01..10). Pass one or more payload paths to run a subset; useful for the
# canary:
#   ./submit_all.sh payloads/06_YJ6NTtHPEt8.process.json
#
# Environment:
#   PROJECT       idempotency namespace (default garlasco-interviste-20261124)
#   TIMEOUT       per-job poll timeout (default 2h)
#   INTERVAL      poll interval (default 10s)
#   LOG           log file (default submit.log)
set -Eeuo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
PROJECT="${PROJECT:-garlasco-interviste-20261124}"
TIMEOUT="${TIMEOUT:-2h}"
INTERVAL="${INTERVAL:-10s}"
LOG="${LOG:-$HERE/submit.log}"
VELOX="$ROOT/scripts/velox"

DRY_RUN=0
PAYLOADS=()
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
    *) PAYLOADS+=("$arg") ;;
  esac
done

if ((${#PAYLOADS[@]} == 0)); then
  mapfile -t PAYLOADS < <(find "$HERE/payloads" -maxdepth 1 -name '*.process.json' | sort)
fi
if ((${#PAYLOADS[@]} == 0)); then
  echo "nessun payload in $HERE/payloads; esegui prima build_payloads.py" >&2
  exit 2
fi

mkdir -p "$(dirname "$LOG")"
for payload in "${PAYLOADS[@]}"; do
  [[ -r "$payload" ]] || { echo "payload non leggibile: $payload" >&2; exit 2; }
  name="$(basename "$payload" .process.json)"
  if ((DRY_RUN)); then
    echo "DRY-RUN $name $payload"
    continue
  fi
  echo "=== $(date -u +%FT%TZ) submit $name" | tee -a "$LOG"
  response="$("$VELOX" submit clips-process --key "$PROJECT" --payload "$payload" --json)"
  printf '%s\n' "$response" | tee -a "$LOG"
  job_id="$(printf '%s' "$response" | jq -r '.job_id // empty')"
  if [[ -z "$job_id" ]]; then
    echo "=== $name: submit senza job_id, mi fermo" | tee -a "$LOG" >&2
    exit 1
  fi
  echo "=== $(date -u +%FT%TZ) poll $name job=$job_id" | tee -a "$LOG"
  if "$VELOX" poll "$job_id" --timeout "$TIMEOUT" --interval "$INTERVAL" >>"$LOG" 2>&1; then
    echo "=== $(date -u +%FT%TZ) $name SUCCEEDED" | tee -a "$LOG"
  else
    echo "=== $(date -u +%FT%TZ) $name FAILED (job=$job_id, vedi $LOG)" | tee -a "$LOG" >&2
    exit 1
  fi
done
echo "=== $(date -u +%FT%TZ) batch completato" | tee -a "$LOG"

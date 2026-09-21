#!/usr/bin/env bash
set -Eeuo pipefail

# Drain active jobs before restarting PipelineGen. Queued jobs remain durable
# and are picked up after boot; the helper only waits for in-flight handlers so
# a binary restart does not interrupt an active generation/render operation.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASE_URL="${VELOX_BASE_URL:-http://127.0.0.1:8000}"
SERVICE="${PIPELINEGEN_SERVICE:-pipelinegen}"
TIMEOUT_SECONDS="${1:-1800}"
INTERVAL_SECONDS="${VELOX_DRAIN_INTERVAL_SECONDS:-5}"

[[ "$TIMEOUT_SECONDS" =~ ^[0-9]+$ ]] || { echo "timeout non valido: $TIMEOUT_SECONDS" >&2; exit 2; }
started=$(date +%s)

"$ROOT/scripts/with-velox-auth" bash -c '
  set -Eeuo pipefail
  base="$1"
  timeout="$2"
  interval="$3"
  start=$(date +%s)
  while :; do
    body=$(curl -fsS --max-time 10 -H "X-Velox-Admin-Token: $VELOX_ADMIN_TOKEN" "$base/api/jobs/stats")
    running=$(printf "%s" "$body" | jq -r ".stats.by_status.RUNNING // 0")
    queued=$(printf "%s" "$body" | jq -r ".stats.by_status.QUEUED // 0")
    printf "drain running=%s queued=%s\n" "$running" "$queued"
    if [[ "$running" == "0" ]]; then
      break
    fi
    if (( $(date +%s) - start >= timeout )); then
      echo "drain timeout: $running job ancora RUNNING" >&2
      exit 2
    fi
    sleep "$interval"
  done
' _ "$BASE_URL" "$TIMEOUT_SECONDS" "$INTERVAL_SECONDS"

echo "riavvio servizio $SERVICE dopo drain"
if systemctl restart "$SERVICE"; then
  echo "restart richiesto"
else
  # On deployments where polkit blocks a user-level systemctl restart, signal
  # only the drained MainPID. Restart=always then starts the new binary without
  # touching queued work. Refuse the fallback when ownership is unclear.
  main_pid=$(systemctl show -p MainPID --value "$SERVICE" 2>/dev/null || true)
  owner=$(ps -o user= -p "$main_pid" 2>/dev/null | awk '{print $1}')
  if [[ "$main_pid" =~ ^[1-9][0-9]*$ ]] && [[ "$owner" == "$(id -un)" ]]; then
    echo "systemctl restart negato; invio TERM al MainPID drenato=$main_pid"
    kill -TERM "$main_pid"
  else
    echo "restart pipelinegen fallito e fallback non sicuro" >&2
    exit 1
  fi
fi

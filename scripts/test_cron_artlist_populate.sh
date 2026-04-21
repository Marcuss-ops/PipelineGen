#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
API_URL="${API_URL:-http://127.0.0.1:8080}"
LOG_FILE="${LOG_FILE:-$ROOT_DIR/server_test_cron_artlist.log}"
TIMEOUT_HEALTH_SEC="${TIMEOUT_HEALTH_SEC:-180}"

SERVER_PID=""
TERMS_FILE="/tmp/test_cron_artlist_terms.txt"

cleanup() {
  rm -f "$TERMS_FILE"
  if [[ -n "${SERVER_PID}" ]] && ps -p "${SERVER_PID}" >/dev/null 2>&1; then
    kill "${SERVER_PID}" >/dev/null 2>&1 || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
}
trap cleanup EXIT

echo "== Build server =="
(cd "$ROOT_DIR/src/go-master" && go build -o "$ROOT_DIR/bin/server" ./cmd/server)

echo "== Start server =="
rm -f "$LOG_FILE"
(cd "$ROOT_DIR" && nohup ./bin/server > "$LOG_FILE" 2>&1 & echo $! > /tmp/test_cron_server_pid.txt)
SERVER_PID="$(cat /tmp/test_cron_server_pid.txt)"
rm -f /tmp/test_cron_server_pid.txt
echo "Server PID: $SERVER_PID"
echo "Log file: $LOG_FILE"

echo "== Wait health =="
start_ts="$(date +%s)"
while true; do
  if curl -sS "$API_URL/health" >/dev/null 2>&1; then
    echo "Health OK"
    break
  fi
  now_ts="$(date +%s)"
  if (( now_ts - start_ts > TIMEOUT_HEALTH_SEC )); then
    echo "ERROR: server not healthy within ${TIMEOUT_HEALTH_SEC}s"
    tail -n 150 "$LOG_FILE" || true
    exit 1
  fi
  sleep 2
done

cat > "$TERMS_FILE" <<'TERMS'
Nikola Tesla technology innovations and electricity
artificial intelligence and robotics automation
TERMS

echo "== Run cron populate cycle =="
TERMS_FILE="$TERMS_FILE" MAX_REQUEST_TIME_SEC=900 SLEEP_BETWEEN_SEC=1 \
  "$ROOT_DIR/scripts/cron_artlist_populate.sh"

echo "== Validate server logs =="
declare -a REQUIRED_PATTERNS=(
  "Dynamic clip uploaded in keyword folder|Skipping upload for already downloaded Artlist source clip"
  "Post-cycle DriveSync completed|Post-cycle DriveSync skipped \\(already running\\)"
  "Post-cycle ArtlistSync completed|Post-cycle ArtlistSync skipped \\(already running\\)"
  "Post-cycle CatalogSync completed|Post-cycle CatalogSync skipped \\(already running\\)"
  "Post-cycle DB sync completed"
)

for pattern in "${REQUIRED_PATTERNS[@]}"; do
  if ! rg -n "$pattern" "$LOG_FILE" >/dev/null; then
    echo "ERROR: missing log pattern: $pattern"
    tail -n 200 "$LOG_FILE" || true
    exit 1
  fi
done

echo "== Key log lines =="
rg -n "Dynamic clip uploaded in keyword folder|Skipping upload for already downloaded Artlist source clip|Post-cycle DriveSync (completed|skipped \\(already running\\))|Post-cycle ArtlistSync (completed|skipped \\(already running\\))|Post-cycle CatalogSync (completed|skipped \\(already running\\))|Post-cycle DB sync completed" "$LOG_FILE" | tail -n 30

echo "SUCCESS: cron populate cycle test passed."

#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
API_URL="${API_URL:-http://127.0.0.1:8080}"
LOG_FILE="${LOG_FILE:-$ROOT_DIR/server_test_tesla_postcycle.log}"
RESP_FILE="${RESP_FILE:-/tmp/tesla_scriptdocs_response.json}"
TIMEOUT_HEALTH_SEC="${TIMEOUT_HEALTH_SEC:-180}"
MAX_REQUEST_TIME_SEC="${MAX_REQUEST_TIME_SEC:-900}"

SERVER_PID=""

cleanup() {
  if [[ -n "${SERVER_PID}" ]] && ps -p "${SERVER_PID}" > /dev/null 2>&1; then
    kill "${SERVER_PID}" >/dev/null 2>&1 || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
}
trap cleanup EXIT

echo "== Build server =="
(cd "$ROOT_DIR/src/go-master" && go build -o "$ROOT_DIR/bin/server" ./cmd/server)

echo "== Start server =="
rm -f "$LOG_FILE"
(cd "$ROOT_DIR" && nohup ./bin/server > "$LOG_FILE" 2>&1 & echo $! > /tmp/tesla_server_pid.txt)
SERVER_PID="$(cat /tmp/tesla_server_pid.txt)"
rm -f /tmp/tesla_server_pid.txt
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
    tail -n 120 "$LOG_FILE" || true
    exit 1
  fi
  sleep 2
done

echo "== Call /api/script-docs/generate (Tesla EN) =="
curl -sS --max-time "$MAX_REQUEST_TIME_SEC" \
  -X POST "$API_URL/api/script-docs/generate" \
  -H "Content-Type: application/json" \
  --data '{"topic":"Nikola Tesla technology innovations and electricity","duration":80,"languages":["en"],"template":"documentary"}' \
  > "$RESP_FILE"

python3 - "$RESP_FILE" <<'PY'
import json, sys
path = sys.argv[1]
with open(path, "r", encoding="utf-8") as f:
    data = json.load(f)
print("Response keys:", ", ".join(sorted(data.keys())))
print("ok:", data.get("ok"))
print("doc_url:", data.get("doc_url"))
print("artlist_matches:", data.get("artlist_matches"))
print("avg_confidence:", data.get("avg_confidence"))
PY

if rg -n "file:///" "$RESP_FILE" >/dev/null; then
  echo "ERROR: response contains unsupported file:/// URLs"
  cat "$RESP_FILE"
  exit 1
fi

echo "== Validate logs =="
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

echo "== Extract key log lines =="
rg -n "Dynamic clip uploaded in keyword folder|Skipping upload for already downloaded Artlist source clip|Post-cycle DriveSync (completed|skipped \\(already running\\))|Post-cycle ArtlistSync (completed|skipped \\(already running\\))|Post-cycle CatalogSync (completed|skipped \\(already running\\))|Post-cycle DB sync completed" "$LOG_FILE" | tail -n 20

echo "SUCCESS: Tesla test passed with post-cycle sync + keyword-folder upload."

#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
API_URL="${API_URL:-http://127.0.0.1:8080}"
LOG_FILE="${LOG_FILE:-$ROOT_DIR/server_test_clipsearch_e2e_fast.log}"
RESP_FILE="${RESP_FILE:-/tmp/clipsearch_e2e_fast_response.json}"
TIMEOUT_HEALTH_SEC="${TIMEOUT_HEALTH_SEC:-90}"
MAX_REQUEST_TIME_SEC="${MAX_REQUEST_TIME_SEC:-240}"
MAX_REQUEST_ATTEMPTS="${MAX_REQUEST_ATTEMPTS:-2}"

# Load shared yt-dlp defaults for YouTube.
source "$ROOT_DIR/scripts/env_ytdlp_defaults.sh"

SERVER_PID=""
cleanup() {
  if [[ -n "${SERVER_PID}" ]] && ps -p "${SERVER_PID}" >/dev/null 2>&1; then
    kill "${SERVER_PID}" >/dev/null 2>&1 || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
}
trap cleanup EXIT

echo "== Build server =="
(cd "$ROOT_DIR/src/go-master" && go build -o "$ROOT_DIR/bin/server" ./cmd/server)

echo "== Ensure port 8080 is free =="
if fuser -n tcp 8080 >/dev/null 2>&1; then
  PIDS="$(fuser -n tcp 8080 2>/dev/null || true)"
  for pid in $PIDS; do
    kill "$pid" >/dev/null 2>&1 || true
  done
  sleep 1
fi

echo "== Start server =="
rm -f "$LOG_FILE"
(cd "$ROOT_DIR" && nohup ./bin/server > "$LOG_FILE" 2>&1 & echo $! > /tmp/clipsearch_fast_server_pid.txt)
SERVER_PID="$(cat /tmp/clipsearch_fast_server_pid.txt)"
rm -f /tmp/clipsearch_fast_server_pid.txt

echo "== Wait health =="
start_ts="$(date +%s)"
while true; do
  if curl -sS "$API_URL/health" >/dev/null 2>&1; then
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

echo "== Call /api/script-docs/generate (fast e2e) =="
echo "== Warmup text model =="
curl -sS --max-time 60 \
  -X POST "$API_URL/api/text/generate" \
  -H "Content-Type: application/json" \
  --data '{"topic":"technology","duration":20}' >/dev/null || true

attempt=1
while (( attempt <= MAX_REQUEST_ATTEMPTS )); do
  echo "-- generate attempt $attempt/$MAX_REQUEST_ATTEMPTS"
  if curl -sS --max-time "$MAX_REQUEST_TIME_SEC" \
    -X POST "$API_URL/api/script-docs/generate" \
    -H "Content-Type: application/json" \
    --data '{"topic":"Nikola Tesla technology innovations and electricity","duration":40,"languages":["en"],"template":"documentary"}' \
    > "$RESP_FILE"; then
    break
  fi
  if (( attempt == MAX_REQUEST_ATTEMPTS )); then
    echo "ERROR: script-docs generate timed out/failed after $MAX_REQUEST_ATTEMPTS attempts"
    tail -n 200 "$LOG_FILE" || true
    exit 1
  fi
  attempt=$((attempt + 1))
  sleep 2
done

if rg -n "file:///" "$RESP_FILE" >/dev/null; then
  echo "ERROR: response contains unsupported file:/// URLs"
  cat "$RESP_FILE"
  exit 1
fi

python3 - "$RESP_FILE" <<'PY'
import json, sys
with open(sys.argv[1], 'r', encoding='utf-8') as f:
    data = json.load(f)
print('ok=', data.get('ok'), 'artlist_matches=', data.get('artlist_matches'), 'avg_confidence=', data.get('avg_confidence'))
if not data.get('ok'):
    raise SystemExit(1)
PY

# We only need one successful dynamic cycle with post-cycle sync markers.
if rg -n "Dynamic clip uploaded in keyword folder" "$LOG_FILE" >/dev/null; then
  echo "mode=upload_path"
  declare -a REQUIRED_PATTERNS=(
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
else
  echo "mode=dedup_path"
  if ! rg -n "Skipping upload for already downloaded Artlist source clip|Found clip in DB cache" "$LOG_FILE" >/dev/null; then
    echo "ERROR: no upload and no dedup evidence found"
    tail -n 200 "$LOG_FILE" || true
    exit 1
  fi
fi

echo "== Key lines =="
rg -n "Dynamic clip uploaded in keyword folder|Skipping upload for already downloaded Artlist source clip|Post-cycle DriveSync (completed|skipped \\(already running\\))|Post-cycle ArtlistSync (completed|skipped \\(already running\\))|Post-cycle CatalogSync (completed|skipped \\(already running\\))|Post-cycle DB sync completed" "$LOG_FILE" | tail -n 20

echo "SUCCESS: fast e2e clipsearch test passed"

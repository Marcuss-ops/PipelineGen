#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
API_URL="${API_URL:-http://127.0.0.1:8080}"
TERMS_FILE="${TERMS_FILE:-$ROOT_DIR/scripts/artlist_seed_terms.txt}"
LANGUAGE="${LANGUAGE:-en}"
DURATION="${DURATION:-80}"
TEMPLATE="${TEMPLATE:-documentary}"
MAX_REQUEST_TIME_SEC="${MAX_REQUEST_TIME_SEC:-900}"
SLEEP_BETWEEN_SEC="${SLEEP_BETWEEN_SEC:-2}"
LOG_DIR="${LOG_DIR:-$ROOT_DIR/logs}"

mkdir -p "$LOG_DIR"
RUN_TS="$(date -u +%Y%m%dT%H%M%SZ)"
RUN_LOG="$LOG_DIR/cron_artlist_populate_${RUN_TS}.log"

echo "== cron_artlist_populate start: ${RUN_TS} ==" | tee -a "$RUN_LOG"
echo "API_URL=$API_URL" | tee -a "$RUN_LOG"
echo "TERMS_FILE=$TERMS_FILE" | tee -a "$RUN_LOG"

if [[ ! -f "$TERMS_FILE" ]]; then
  echo "ERROR: terms file not found: $TERMS_FILE" | tee -a "$RUN_LOG"
  exit 1
fi

if ! curl -sS "$API_URL/health" >/dev/null; then
  echo "ERROR: server not healthy at $API_URL" | tee -a "$RUN_LOG"
  exit 1
fi

total=0
ok_count=0
fail_count=0

while IFS= read -r raw_term || [[ -n "$raw_term" ]]; do
  term="${raw_term%%#*}"
  term="$(echo "$term" | xargs)"
  if [[ -z "$term" ]]; then
    continue
  fi

  total=$((total + 1))
  resp_file="/tmp/cron_artlist_populate_${RUN_TS}_${total}.json"

  payload="$(printf '{"topic":"%s","duration":%s,"languages":["%s"],"template":"%s"}' "$term" "$DURATION" "$LANGUAGE" "$TEMPLATE")"

  echo "-- [$total] topic: $term" | tee -a "$RUN_LOG"
  if ! curl -sS --max-time "$MAX_REQUEST_TIME_SEC" \
    -X POST "$API_URL/api/script-docs/generate" \
    -H "Content-Type: application/json" \
    --data "$payload" > "$resp_file"; then
    echo "   FAIL: request error" | tee -a "$RUN_LOG"
    fail_count=$((fail_count + 1))
    continue
  fi

  if rg -n "file:///" "$resp_file" >/dev/null; then
    echo "   FAIL: found unsupported file:/// url" | tee -a "$RUN_LOG"
    fail_count=$((fail_count + 1))
    continue
  fi

  if python3 - "$resp_file" <<'PY'
import json, sys
path = sys.argv[1]
with open(path, 'r', encoding='utf-8') as f:
    data = json.load(f)
ok = bool(data.get('ok'))
print(f"   ok={ok} artlist_matches={data.get('artlist_matches')} avg_confidence={data.get('avg_confidence')} doc_url={data.get('doc_url')}")
sys.exit(0 if ok else 1)
PY
  then
    ok_count=$((ok_count + 1))
  else
    fail_count=$((fail_count + 1))
  fi

  sleep "$SLEEP_BETWEEN_SEC"
done < "$TERMS_FILE"

echo "== completed total=$total ok=$ok_count fail=$fail_count ==" | tee -a "$RUN_LOG"

if (( fail_count > 0 )); then
  exit 1
fi

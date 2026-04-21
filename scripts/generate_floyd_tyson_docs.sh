#!/usr/bin/env bash
set -euo pipefail

API_URL="${API_URL:-http://127.0.0.1:8080}"
TOPIC="${TOPIC:-Floyd Mayweather to Mike Tyson}"
DURATION="${DURATION:-120}"
SCRIPT_LANG="${SCRIPT_LANG:-en}"
TEMPLATE="${TEMPLATE:-biography}"
ASSOCIATION_MODE="${ASSOCIATION_MODE:-default}"
OUT_JSON="${OUT_JSON:-/tmp/floyd_tyson_scriptdocs_response.json}"
MAX_TIME="${MAX_TIME:-420}"
ATTEMPTS="${ATTEMPTS:-2}"

echo "== ScriptDocs Generator =="
echo "api=$API_URL"
echo "topic=$TOPIC"
echo "duration=$DURATION lang=$SCRIPT_LANG template=$TEMPLATE mode=$ASSOCIATION_MODE"

if ! curl -sS "$API_URL/health" >/dev/null 2>&1; then
  echo "ERROR: server non raggiungibile su $API_URL"
  exit 1
fi

payload="$(printf '{"topic":"%s","duration":%s,"languages":["%s"],"template":"%s","association_mode":"%s"}' \
  "$TOPIC" "$DURATION" "$SCRIPT_LANG" "$TEMPLATE" "$ASSOCIATION_MODE")"

attempt=1
while (( attempt <= ATTEMPTS )); do
  echo "-- attempt $attempt/$ATTEMPTS"
  if curl -sS --max-time "$MAX_TIME" \
    -X POST "$API_URL/api/script-docs/generate" \
    -H "Content-Type: application/json" \
    --data "$payload" \
    > "$OUT_JSON"; then
    break
  fi
  if (( attempt == ATTEMPTS )); then
    echo "ERROR: request failed after $ATTEMPTS attempts"
    exit 1
  fi
  attempt=$((attempt + 1))
  sleep 2
done

python3 - "$OUT_JSON" <<'PY'
import json, sys

path = sys.argv[1]
with open(path, "r", encoding="utf-8") as f:
    data = json.load(f)

if not data.get("doc_url"):
    print("ERROR: doc_url mancante")
    raise SystemExit(1)

print("ok:", bool(data.get("ok", True)))
print("doc_id:", data.get("doc_id"))
print("doc_url:", data.get("doc_url"))
print("title:", data.get("title"))
print("languages:", len(data.get("languages", [])))
PY

echo "SUCCESS: risposta salvata in $OUT_JSON"

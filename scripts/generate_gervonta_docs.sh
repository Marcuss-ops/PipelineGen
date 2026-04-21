#!/usr/bin/env bash
set -euo pipefail

API_URL="${API_URL:-http://127.0.0.1:8080}"
TOPIC="${TOPIC:-Gervonta Davis rise to fame, career milestones, legal issues, and legacy}"
DURATION="${DURATION:-80}"
SCRIPT_LANG="${SCRIPT_LANG:-en}"
TEMPLATE="${TEMPLATE:-documentary}"
MAX_TIME="${MAX_TIME:-420}"
ATTEMPTS="${ATTEMPTS:-2}"
OUT_JSON="${OUT_JSON:-/tmp/gervonta_scriptdocs_response.json}"

echo "== Gervonta ScriptDocs Generator =="
echo "api=$API_URL"
echo "topic=$TOPIC"
echo "duration=$DURATION lang=$SCRIPT_LANG template=$TEMPLATE"

if ! curl -sS "$API_URL/health" >/dev/null 2>&1; then
  echo "ERROR: server non raggiungibile su $API_URL"
  exit 1
fi

payload="$(printf '{"topic":"%s","duration":%s,"languages":["%s"],"template":"%s"}' "$TOPIC" "$DURATION" "$SCRIPT_LANG" "$TEMPLATE")"

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
ok = bool(data.get("ok"))
print("ok:", ok)
print("doc_url:", data.get("doc_url"))
print("artlist_matches:", data.get("artlist_matches"))
print("avg_confidence:", data.get("avg_confidence"))
if not ok:
    print("error:", data.get("error"))
    raise SystemExit(1)
PY

if rg -n "file:///" "$OUT_JSON" >/dev/null; then
  echo "ERROR: risposta contiene URL file:/// non supportati"
  exit 1
fi

echo "SUCCESS: risposta salvata in $OUT_JSON"

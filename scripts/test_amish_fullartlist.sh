#!/usr/bin/env bash
set -euo pipefail

API_URL="${API_URL:-http://localhost:8080}"
TOPIC="${TOPIC:-Amish community lifestyle and traditions}"
RESP_FILE="${RESP_FILE:-/tmp/amish_fullartlist_resp.json}"
MAX_TIME="${MAX_TIME:-240}"

echo "== Amish FullArtlist endpoint test =="
echo "API_URL: $API_URL"
echo "TOPIC:   $TOPIC"

curl -sS --max-time "$MAX_TIME" \
  -X POST "$API_URL/api/script-docs/generate/fullartlist" \
  -H "Content-Type: application/json" \
  --data "{\"topic\":\"$TOPIC\",\"duration\":90,\"languages\":[\"en\"],\"template\":\"documentary\"}" \
  > "$RESP_FILE"

python3 - "$RESP_FILE" <<'PY'
import json, sys
path = sys.argv[1]
with open(path, "r", encoding="utf-8") as f:
    data = json.load(f)

assert data.get("ok") is True, data
assert data.get("mode") == "fullartlist", data
langs = data.get("languages") or []
assert len(langs) >= 1, data

for i, lang in enumerate(langs, start=1):
    assoc = int(lang.get("associations", 0))
    art = int(lang.get("artlist_matches", 0))
    non_art = int(lang.get("non_artlist_associations", -1))
    timeline = int(lang.get("timeline_entries", 0))
    assert assoc >= 1, f"language #{i}: expected at least 1 association, got {assoc}"
    assert art == assoc, f"language #{i}: expected all associations to be artlist ({assoc}), got {art}"
    assert non_art == 0, f"language #{i}: expected 0 non-artlist, got {non_art}"
    assert timeline >= 1, f"language #{i}: expected timeline entries >=1, got {timeline}"

print("OK fullartlist:", data.get("doc_url"))
PY

echo "Response saved to: $RESP_FILE"

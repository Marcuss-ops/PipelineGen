#!/usr/bin/env bash
set -euo pipefail

# Bounded search certification. Provider errors are never converted to an
# empty successful search. Completeness is authoritative only when total or
# has_next_page is present; otherwise the report says bounded_exhaustion.
BASE="${BASE:-http://127.0.0.1:9123}"
QUERY="${QUERY:-electricity}"
LIMIT="${LIMIT:-50}"
MAX_PAGES="${MAX_PAGES:-20}"
MODE="${MODE:-catalog_first}"
WORK_DIR="${WORK_DIR:-$(mktemp -d /tmp/artlist-certification.XXXXXX)}"
REPORT="${REPORT:-$WORK_DIR/report.json}"
mkdir -p "$WORK_DIR"
trap 'rm -rf "$WORK_DIR"' EXIT

raw_count=0
pages=0
failed=0
: > "$WORK_DIR/clips.ndjson"

for page in $(seq 1 "$MAX_PAGES"); do
  pages=$page
  request=$(jq -nc --arg q "$QUERY" --arg mode "$MODE" --argjson page "$page" --argjson limit "$LIMIT" \
    '{query:$q,page:$page,limit:$limit,mode:$mode}')
  response="$WORK_DIR/page-$page.json"
  if ! curl -fsS --max-time "${CURL_TIMEOUT:-120}" -X POST "$BASE/v1/clips/search" \
    -H 'Content-Type: application/json' -d "$request" >"$response"; then
    failed=1
    break
  fi
  if [[ "$(jq -r '.ok // false' "$response")" != true ]]; then
    failed=1
    break
  fi
  count=$(jq '.clips // [] | length' "$response")
  raw_count=$((raw_count + count))
  jq -c '.clips[]?' "$response" >> "$WORK_DIR/clips.ndjson"
  has_next=$(jq -r 'if .has_next_page == null then "unknown" else (.has_next_page|tostring) end' "$response")
  if [[ "$count" -eq 0 || "$has_next" == false || ("$count" -lt "$LIMIT" && "$has_next" == unknown) ]]; then break; fi
done

unique_count=$(if [[ -s "$WORK_DIR/clips.ndjson" ]]; then jq -r '.clip_id // .id // .clip_page_url // .page_url' "$WORK_DIR/clips.ndjson" | sort -u | wc -l; else echo 0; fi)
unique_count=$(tr -d ' ' <<< "$unique_count")
first="$WORK_DIR/page-1.json"
source=$(jq -r '.source // "unknown"' "$first" 2>/dev/null || echo unknown)
provider_contacted=$(jq -r '.provider_contacted // false' "$first" 2>/dev/null || echo false)
browser_launched=$(jq -r '.browser_launched // false' "$first" 2>/dev/null || echo false)
total=$(jq -r '.total // empty' "$first" 2>/dev/null || true)
if [[ "$total" =~ ^[0-9]+$ ]]; then
  ratio=$(awk -v u="$unique_count" -v t="$total" 'BEGIN { if (t == 0) print 1; else printf "%.6f", u/t }')
  completeness_status=$([[ "$unique_count" -eq "$total" ]] && echo PASS || echo FAIL)
else
  ratio=null
  completeness_status=BOUNDED_EXHAUSTION
fi

jq -n --arg query "$QUERY" --arg mode "$MODE" --arg source "$source" \
  --arg status "$completeness_status" --argjson pages "$pages" --argjson raw "$raw_count" \
  --argjson unique "$unique_count" --argjson contacted "$provider_contacted" \
  --argjson browser "$browser_launched" --argjson ratio "$ratio" --argjson failed "$failed" \
  '{certification:"artlist",query:$query,mode:$mode,failed:($failed==1),search:{source:$source,pages:$pages,raw_count:$raw,unique_count:$unique},provenance:{provider_contacted:$contacted,browser_launched:$browser},completeness:{status:$status,ratio:$ratio}}' > "$REPORT"
cat "$REPORT"
printf '\nreport=%s\n' "$REPORT"
exit "$failed"

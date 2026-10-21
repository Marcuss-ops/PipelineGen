#!/usr/bin/env bash
# Build and run the overlay plan of an existing script payload without paying
# for the per-item overlay renders.
#
#   scripts/audit-plan.sh PAYLOAD.json [OUT_DIR] [--dry-run]
#
# It derives a plan-only payload (render enabled -> false, docs disabled), keeps
# the narration/voiceover inputs untouched (the plan needs certified speech
# timing), submits it, waits for the terminal state and writes:
#   OUT/plan-only.json   the derived payload
#   OUT/full.json        the full job body
#   OUT/plan.json        just the overlay_plan (what an audit scores)
#
# The admin token comes from scripts/with-velox-auth and is never printed.
set -uo pipefail

PAYLOAD=${1:?usage: audit-plan.sh PAYLOAD.json [OUT_DIR] [--dry-run]}
OUT=${2:-/tmp/garlasco-plan-audit}
DRY_RUN=0
[[ "${3:-}" == "--dry-run" ]] && DRY_RUN=1
[[ "${2:-}" == "--dry-run" ]] && { DRY_RUN=1; OUT=/tmp/garlasco-plan-audit; }

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASE_URL=${VELOX_BASE_URL:-http://127.0.0.1:8000}
TIMEOUT=${AUDIT_PLAN_TIMEOUT_SECONDS:-1800}
IDENTITY=${AUDIT_PLAN_IDENTITY:-audit-plan-$(basename "$PAYLOAD" .json)-$(date -u +%Y%m%dT%H%M%SZ)}

[[ -r "$PAYLOAD" ]] || { echo "payload non leggibile: $PAYLOAD" >&2; exit 2; }
mkdir -p "$OUT"
PLAN_PAYLOAD="$OUT/plan-only.json"

# The plan is compiled in the audio stage, before any overlay render: turning
# render off removes the expensive fan-out without changing the semantic plan.
# Write through a temp file: rerunning with OUT pointing at the same directory
# must not truncate the payload jq is about to read (PAYLOAD may equal
# PLAN_PAYLOAD).
jq --arg id "$IDENTITY" '
  .correlation_id = $id
  | .force_refresh = false
  | .final_job = false
  | .items[0].id = $id
  | .items[0].output.render.enabled = false
  | .items[0].output.docs.enabled = false
' "$PAYLOAD" > "$PLAN_PAYLOAD.tmp" || { rm -f "$PLAN_PAYLOAD.tmp"; echo "payload non valido: $PAYLOAD" >&2; exit 2; }
mv "$PLAN_PAYLOAD.tmp" "$PLAN_PAYLOAD"

echo "plan-only payload: $PLAN_PAYLOAD"
echo "identity: $IDENTITY"
if (( DRY_RUN )); then
  exit 0
fi

cd "$ROOT" || exit 1

resp=$(./scripts/with-velox-auth bash -c '
  set -euo pipefail
  curl -fsS --retry 2 --max-time 60 -X POST "$0/api/script/generate" \
    -H "X-Velox-Admin-Token: $VELOX_ADMIN_TOKEN" \
    -H "Content-Type: application/json" \
    -H "Idempotency-Key: $1" \
    --data-binary @"$2"
' "$BASE_URL" "$IDENTITY" "$PLAN_PAYLOAD") || { echo "submit fallito: $resp" >&2; exit 1; }

job_id=$(printf '%s' "$resp" | jq -r '.job_id // empty')
[[ -n "$job_id" ]] || { echo "nessun job_id: $resp" >&2; exit 1; }
printf '%s\n' "$job_id" > "$OUT/job_id"
echo "job_id=$job_id"

deadline=$(( $(date +%s) + TIMEOUT ))
misses=0
while (( $(date +%s) < deadline )); do
  body=$(./scripts/with-velox-auth bash -c '
    curl -fsS -m 30 -H "X-Velox-Admin-Token: $VELOX_ADMIN_TOKEN" "$0/api/jobs/$1/full"
  ' "$BASE_URL" "$job_id" 2>/dev/null) || {
    misses=$((misses + 1))
    (( misses >= 5 )) && { echo "poll fallito 5 volte" >&2; exit 2; }
    sleep 5
    continue
  }
  misses=0
  status=$(printf '%s' "$body" | jq -r '.status // "UNKNOWN"')
  case "$status" in
    SUCCEEDED|COMPLETED|SUCCESS|DONE|FAILED|ERROR|CANCELED|CANCELLED)
      printf '%s' "$body" > "$OUT/full.json"
      if [[ "$status" != SUCCEEDED && "$status" != COMPLETED && "$status" != SUCCESS && "$status" != DONE ]]; then
        # A stale server binary rejects payload keys its source tree already
        # accepts; say so instead of failing with "no overlay_plan".
        echo "job $status: $(printf '%s' "$body" | jq -r '.error // .current_detail // "unknown"')" >&2
        echo "full body: $OUT/full.json" >&2
        exit 1
      fi
      # The plan can be nested in the job envelope; take the first complete one.
      printf '%s' "$body" \
        | jq '[.. | objects | .overlay_plan? | select(.items? != null)][0] // empty' \
        > "$OUT/plan.json"
      if [[ ! -s "$OUT/plan.json" ]]; then
        echo "status=$status ma nessun overlay_plan in $OUT/full.json" >&2
        exit 1
      fi
      jq '{
        status: $status,
        plan_id: .plan_id,
        items: (.items | length),
        maps: ([.items[] | select(.kind == "map")] | length),
        image_groups: ([.items[] | select((.image_layers // []) | length >= 2)] | length),
        bullets: ([.items[] | select((.params.editorial_role // "") | test("bullet"))] | length),
        chart_cards: ([.items[] | select(.params.chart_card == true)] | length),
        phrases: ([.items[] | select(.kind == "text_phrase")] | length)
      }' --arg status "$status" "$OUT/plan.json"
      echo "plan: $OUT/plan.json"
      exit 0
      ;;
  esac
  sleep 10
done
echo "timeout dopo ${TIMEOUT}s per $job_id" >&2
exit 3

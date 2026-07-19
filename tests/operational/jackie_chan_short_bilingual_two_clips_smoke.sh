#!/usr/bin/env bash
# Live smoke: short Jackie Chan script, two real clips, EN -> ES translation.
#
# The test exercises the canonical /api/script/generate pipeline. Translation
# is requested through output.translate_to, not through a second ad-hoc route.
# It intentionally stops at the persisted script/specscene boundary: a video
# renderer cannot consume these Drive-backed clips until a render worker has
# materialized local media paths.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"
smoke_require sqlite3 curl jq

ROOT_DIR=$(cd "$DIR/../.." && pwd)
FIXTURE="$ROOT_DIR/examples/scripts/jackie_chan_short_bilingual_two_clips.json"
SMOKE_DB="${SMOKE_DB:-$ROOT_DIR/data/media/media.db.sqlite}"
REQ_ID="jackie_short_bilingual_$(date +%s)_$$"
SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-300}"

[[ -f "$FIXTURE" ]] || { echo "fixture missing: $FIXTURE" >&2; exit 2; }
[[ -f "$SMOKE_DB" ]] || { echo "database missing: $SMOKE_DB" >&2; exit 2; }

sqlite_q() { sqlite3 -noheader -separator $'\x1f' "$SMOKE_DB" "$1"; }

smoke_log_section "Jackie Chan short bilingual smoke: preflight"
smoke_curl GET "/health" >/dev/null
smoke_assert_http_2xx "GET /health" || exit 2

CLIP_IDS=$(jq -r '.items[0].source.clip_ids[]' "$FIXTURE")
CLIP_COUNT=$(printf '%s\n' "$CLIP_IDS" | wc -l | tr -d ' ')
[[ "$CLIP_COUNT" == "2" ]] || { echo "fixture must contain exactly 2 clips" >&2; exit 2; }

while IFS= read -r clip_id; do
    row=$(sqlite_q "SELECT COALESCE(title, name, ''), COALESCE(drive_link, ''), COALESCE(local_path, '') FROM media_assets WHERE id='$clip_id'")
    [[ -n "$row" ]] || { echo "clip not present in SQLite: $clip_id" >&2; exit 2; }
    printf '  clip=%s present\n' "$clip_id"
done <<< "$CLIP_IDS"

PAYLOAD=$(jq --arg req "$REQ_ID" '. + {force_refresh: true, correlation_id: $req, idempotency_key: $req}' "$FIXTURE")
smoke_log_section "Generate EN and translate to ES"
smoke_curl POST "/api/script/generate" \
    -H "Idempotency-Key: $REQ_ID" \
    --data "$PAYLOAD" >/dev/null
smoke_assert_http_2xx "POST /api/script/generate" || exit 2

JOB_ID=$(jq -r '.job_id // .id // empty' "$SMOKE_LAST_BODY")
[[ -n "$JOB_ID" ]] || { echo "response has no job_id" >&2; exit 2; }
smoke_poll_terminal "$JOB_ID" || exit 1
case "$SMOKE_LAST_STATUS" in
    completed|SUCCEEDED|succeeded) ;;
    *) echo "job ended with status=$SMOKE_LAST_STATUS" >&2; exit 1 ;;
esac

ROWS=$(sqlite_q "SELECT language || char(31) || COALESCE(specscene, '') FROM scripts WHERE idempotency_key='$REQ_ID' AND language IN ('en','es') ORDER BY language")
[[ -n "$ROWS" ]] || { echo "no persisted EN/ES script rows for $REQ_ID" >&2; exit 1; }

EN_SCENE=$(printf '%s\n' "$ROWS" | awk -F $'\x1f' '$1=="en" {print $2; exit}')
ES_SCENE=$(printf '%s\n' "$ROWS" | awk -F $'\x1f' '$1=="es" {print $2; exit}')
[[ -n "$EN_SCENE" && -n "$ES_SCENE" ]] || { echo "expected both EN and ES rows" >&2; exit 1; }

for scene in "$EN_SCENE" "$ES_SCENE"; do
    jq -e --arg a "$(printf '%s' "$CLIP_IDS" | sed -n '1p')" --arg b "$(printf '%s' "$CLIP_IDS" | sed -n '2p')" '
      (.version == 1) and (.scenes | length == 2) and
      ([.scenes[].bindings.clip.clip_id] == [$a, $b]) and
      (all(.scenes[]; (.text | length) > 0))
    ' <<< "$scene" >/dev/null
done

EN_TEXT=$(jq -r '[.scenes[].text] | join(" ")' <<< "$EN_SCENE")
ES_TEXT=$(jq -r '[.scenes[].text] | join(" ")' <<< "$ES_SCENE")
[[ "$EN_TEXT" != "$ES_TEXT" ]] || { echo "translated text is identical to source" >&2; exit 1; }
printf '\nPASS: Jackie Chan short script generated and translated\n'
printf 'job_id=%s\nrequest_id=%s\nscene_count=2\nlanguages=en,es\n' "$JOB_ID" "$REQ_ID"

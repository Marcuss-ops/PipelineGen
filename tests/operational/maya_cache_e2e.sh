#!/usr/bin/env bash
# maya_cache_e2e.sh — MY-01 cold generation + MY-02 idempotent replay.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
SMOKE_TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-900}"
SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-900}"
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"
smoke_require curl jq sha256sum

CASE_PREFIX="maya-cache-$(smoke_gen_uuid)"
IDEMPOTENCY_KEY="$CASE_PREFIX-cold-001"

PAYLOAD=$(jq -n --arg marker "$CASE_PREFIX" --arg item_id "$CASE_PREFIX-item" ' {
  version: 2,
  preset: "custom",
  force_refresh: false,
  items: [{
    id: $item_id,
    title: ("Maya cache verification " + $marker),
    language: "it",
    tone: "documentary",
    source: {
      type: "text",
      topic: ("Maya cache verification " + $marker),
      source_text: ("Una spedizione documenta una scoperta archeologica in una valle remota. " +
        "Gli studiosi confrontano reperti, mappe e testimonianze prima di formulare una nuova ipotesi.\n\n" +
        "Il gruppo organizza il lavoro sul campo, registra ogni passaggio e conserva i dati per verifiche successive.\n\n" +
        "La ricostruzione finale distingue le osservazioni confermate dalle interpretazioni ancora aperte.\n\n" +
        "Caso di verifica: " + $marker + ".")
    },
    script_params: {
      target_words: 360,
      skip_quality_gate: true,
      use_memory: true,
      prompt_version: "vidrush-maya-cache-v1"
    },
    output: {
      extract_entities: true,
      generate_metadata: true,
      save_to_db: true
    },
    media_plan: {
      mode: "hybrid",
      provider_policy: {
        artlist: "enabled",
        internet_images: "enabled"
      },
      extraction: { enabled: true }
    }
  }]
}')

dispatch() {
    export SMOKE_IDEMPOTENCY_KEY="$IDEMPOTENCY_KEY"
    smoke_curl POST "/api/script/generate" --data "${DISPATCH_PAYLOAD:-$PAYLOAD}" >/dev/null
    unset SMOKE_IDEMPOTENCY_KEY
    [[ "$SMOKE_LAST_HTTP" == "202" ]] || { echo "FAIL: dispatch HTTP=$SMOKE_LAST_HTTP" >&2; exit 1; }
    JOB_ID=$(jq -r '.job_id // empty' "$SMOKE_LAST_BODY")
    [[ -n "$JOB_ID" ]] || { echo "FAIL: missing job_id" >&2; exit 1; }
    jq -e --arg expected "/api/jobs/$JOB_ID/full" '.ok == true and .status_url == $expected' "$SMOKE_LAST_BODY" >/dev/null
}

smoke_log_section "MY-01 cold run"
dispatch
FIRST_JOB_ID="$JOB_ID"
smoke_poll_terminal "$FIRST_JOB_ID"
[[ "$SMOKE_LAST_STATUS" == "completed" || "$SMOKE_LAST_STATUS" == "SUCCEEDED" ]] || {
    echo "FAIL: cold job ended with $SMOKE_LAST_STATUS" >&2
    exit 1
}
smoke_curl GET "/api/jobs/$FIRST_JOB_ID/full" >/dev/null
FIRST_RESULT=$(jq -c '.result.data.items[0].result // .result.items[0].result // .result.output // .result // empty' "$SMOKE_LAST_BODY")
[[ -n "$FIRST_RESULT" && "$FIRST_RESULT" != "null" ]] || { echo "FAIL: cold result missing" >&2; exit 1; }

jq -e '
  (.script_id | type == "number" and . > 0)
  and (.output.text | type == "string" and length > 0)
  and (.segments | length >= 3)
  and (.cache.hit == false)
  and (.cache.script == "MISS")
  and (all(.segments[]; .cache.extraction == "MISS" and .cache.artlist == "MISS" and .cache.internet_images == "MISS" and .cache.binding == "MISS"))
' <<<"$FIRST_RESULT" >/dev/null || {
    echo "FAIL: cold cache contract failed" >&2
    jq '{script_id,cache,segment_count:(.segments|length)}' <<<"$FIRST_RESULT" >&2
    exit 1
}

FIRST_SCRIPT_HASH=$(jq -r '.output.text' <<<"$FIRST_RESULT" | sha256sum | awk '{print $1}')
printf 'cold job=%s script_hash=%s segments=%s\n' "$FIRST_JOB_ID" "$FIRST_SCRIPT_HASH" "$(jq '.segments|length' <<<"$FIRST_RESULT")"

# MY-03 uses the same payload with a different idempotency key. This must be
# a distinct HTTP job while the application-level generation and media caches
# are warm.
smoke_log_section "MY-03 warm cache with a new key"
COLD_IDEMPOTENCY_KEY="$IDEMPOTENCY_KEY"
IDEMPOTENCY_KEY="$CASE_PREFIX-warm-001"
dispatch
WARM_JOB_ID="$JOB_ID"
[[ "$WARM_JOB_ID" != "$FIRST_JOB_ID" ]] || { echo "FAIL: warm request reused the cold job" >&2; exit 1; }
smoke_poll_terminal "$WARM_JOB_ID"
[[ "$SMOKE_LAST_STATUS" == "completed" || "$SMOKE_LAST_STATUS" == "SUCCEEDED" ]] || {
    echo "FAIL: warm job ended with $SMOKE_LAST_STATUS" >&2
    exit 1
}
smoke_curl GET "/api/jobs/$WARM_JOB_ID/full" >/dev/null
WARM_RESULT=$(jq -c '.result.data.items[0].result // .result.items[0].result // .result.output // .result // empty' "$SMOKE_LAST_BODY")
jq -e '
  (.cache.hit == true)
  and (.cache.status == "exact_hit")
  and (.cache.script == "HIT_EXACT")
  and (all(.segments[]; .cache.extraction == "HIT_EXACT" and .cache.artlist == "HIT_EXACT" and .cache.internet_images == "HIT_EXACT" and .cache.binding == "HIT_EXACT"))
' <<<"$WARM_RESULT" >/dev/null || {
    echo "FAIL: warm cache contract failed" >&2
    jq '{cache,segment_count:(.segments|length)}' <<<"$WARM_RESULT" >&2
    exit 1
}
WARM_SCRIPT_HASH=$(jq -r '.output.text' <<<"$WARM_RESULT" | sha256sum | awk '{print $1}')
[[ "$WARM_SCRIPT_HASH" == "$FIRST_SCRIPT_HASH" ]] || { echo "FAIL: warm cache changed generated text" >&2; exit 1; }
printf 'warm job=%s script_hash=%s\n' "$WARM_JOB_ID" "$WARM_SCRIPT_HASH"

# MY-04 bypasses the application cache and must enqueue a fresh generation.
smoke_log_section "MY-04 force refresh"
IDEMPOTENCY_KEY="$CASE_PREFIX-refresh-001"
DISPATCH_PAYLOAD=$(jq '.force_refresh = true' <<<"$PAYLOAD")
dispatch
REFRESH_JOB_ID="$JOB_ID"
[[ "$REFRESH_JOB_ID" != "$FIRST_JOB_ID" && "$REFRESH_JOB_ID" != "$WARM_JOB_ID" ]] || {
    echo "FAIL: force_refresh reused an existing job" >&2
    exit 1
}
smoke_poll_terminal "$REFRESH_JOB_ID"
[[ "$SMOKE_LAST_STATUS" == "completed" || "$SMOKE_LAST_STATUS" == "SUCCEEDED" ]] || {
    echo "FAIL: force-refresh job ended with $SMOKE_LAST_STATUS" >&2
    exit 1
}
smoke_curl GET "/api/jobs/$REFRESH_JOB_ID/full" >/dev/null
REFRESH_RESULT=$(jq -c '.result.data.items[0].result // .result.items[0].result // .result.output // .result // empty' "$SMOKE_LAST_BODY")
jq -e '
  (.script_id | type == "number" and . > 0)
  and (.output.text | type == "string" and length > 0)
  and (.cache.hit == false)
  and (.cache.script == "MISS")
  and (.cache.status == "generated")
' <<<"$REFRESH_RESULT" >/dev/null || {
    echo "FAIL: force_refresh did not produce a fresh generation" >&2
    jq '{script_id,cache,segment_count:(.segments|length)}' <<<"$REFRESH_RESULT" >&2
    exit 1
}
printf 'force-refresh job=%s cache=%s\n' "$REFRESH_JOB_ID" "$(jq -c '.cache' <<<"$REFRESH_RESULT")"
unset DISPATCH_PAYLOAD

smoke_log_section "MY-02 identical replay"
IDEMPOTENCY_KEY="$COLD_IDEMPOTENCY_KEY"
REPLAY_HEADERS="$WORK_DIR/maya-replay.headers"
REPLAY_BODY="$WORK_DIR/maya-replay.json"
REPLAY_HTTP=$(curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
    -D "$REPLAY_HEADERS" -o "$REPLAY_BODY" -X POST \
    -H "Authorization: Bearer $SMOKE_TOKEN" \
    -H "Idempotency-Key: $IDEMPOTENCY_KEY" \
    -H 'Content-Type: application/json' --data "$PAYLOAD" \
    -w '%{http_code}' "http://${SMOKE_API_BASE}/api/script/generate")
[[ "$REPLAY_HTTP" == "202" ]] || { echo "FAIL: replay HTTP=$REPLAY_HTTP" >&2; exit 1; }
[[ "$(jq -r '.job_id // empty' "$REPLAY_BODY")" == "$FIRST_JOB_ID" ]] || { echo "FAIL: replay returned a different job" >&2; exit 1; }
grep -Eiq '^X-Idempotency-Replay:[[:space:]]*true' "$REPLAY_HEADERS" || { echo "FAIL: missing replay header" >&2; exit 1; }

printf 'replay job=%s header=true\n' "$FIRST_JOB_ID"
printf '%sMY-01/MY-03/MY-02 passed%s\n' "$GREEN" "$RESET"

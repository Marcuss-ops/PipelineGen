#!/usr/bin/env bash
# Shared HTTP idempotency assertions.

generate_assert_idempotency() {
    local payload="$1" idem_key="$2" assertions="$3"
    export SMOKE_IDEMPOTENCY_KEY="$idem_key"
    smoke_curl POST "/api/script/generate" --data "$payload" >/dev/null
    unset SMOKE_IDEMPOTENCY_KEY
    [[ "$SMOKE_LAST_HTTP" == "202" ]] || { echo "FAIL: idempotency replay HTTP=$SMOKE_LAST_HTTP" >&2; return 1; }
    [[ "$(jq -r '.job_id // empty' "$SMOKE_LAST_BODY")" == "$GENERATE_JOB_ID" ]] || { echo "FAIL: idempotency replay returned a different job" >&2; return 1; }

    local replay_http
    replay_http=$(curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" -D "$WORK_DIR/replay.headers" -o /dev/null -X POST \
        -H "Authorization: Bearer $SMOKE_TOKEN" -H "Idempotency-Key: $idem_key" \
        -H 'Content-Type: application/json' --data "$payload" \
        -w '%{http_code}' "http://${SMOKE_API_BASE}/api/script/generate")
    [[ "$replay_http" == "202" ]] || { echo "FAIL: replay header probe HTTP=$replay_http" >&2; return 1; }
    grep -Eiq '^X-Idempotency-Replay:[[:space:]]*true' "$WORK_DIR/replay.headers" || { echo "FAIL: missing X-Idempotency-Replay: true" >&2; return 1; }

    local conflict_title conflict_http
    conflict_title=$(jq -r '.conflict_title // "Payload differente"' <<<"$assertions")
    conflict_http=$(jq --arg title "$conflict_title" '.items[0].title = $title' <<<"$payload" | curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" -X POST \
        -H "Authorization: Bearer $SMOKE_TOKEN" -H "Idempotency-Key: $idem_key" \
        -H 'Content-Type: application/json' --data-binary @- -o "$WORK_DIR/conflict.json" \
        -w '%{http_code}' "http://${SMOKE_API_BASE}/api/script/generate")
    [[ "$conflict_http" == "409" ]] || { echo "FAIL: idempotency conflict HTTP=$conflict_http" >&2; return 1; }
    jq -e '.code == "IDEMPOTENCY_KEY_CONFLICT"' "$WORK_DIR/conflict.json" >/dev/null || { echo "FAIL: missing IDEMPOTENCY_KEY_CONFLICT" >&2; return 1; }

    if jq -e '.missing_key == true' <<<"$assertions" >/dev/null; then
        local missing_key_http
        missing_key_http=$(curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" -X POST \
            -H "Authorization: Bearer $SMOKE_TOKEN" -H 'Content-Type: application/json' \
            --data "$payload" -o "$WORK_DIR/missing-key.json" -w '%{http_code}' \
            "http://${SMOKE_API_BASE}/api/script/generate")
        [[ "$missing_key_http" == "400" ]] || { echo "FAIL: missing Idempotency-Key HTTP=$missing_key_http" >&2; return 1; }
        jq -e '.code == "IDEMPOTENCY_KEY_REQUIRED"' "$WORK_DIR/missing-key.json" >/dev/null || { echo "FAIL: missing IDEMPOTENCY_KEY_REQUIRED" >&2; return 1; }
    fi
}

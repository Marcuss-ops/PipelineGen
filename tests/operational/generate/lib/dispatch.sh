#!/usr/bin/env bash
# Shared dispatch/polling primitives for the script-generation suite.

generate_dispatch() {
    local payload="$1" idem_key="$2"
    export SMOKE_IDEMPOTENCY_KEY="$idem_key"
    smoke_curl POST "/api/script/generate" --data "$payload" >/dev/null
    unset SMOKE_IDEMPOTENCY_KEY

    [[ "$SMOKE_LAST_HTTP" == "202" ]] || {
        printf '%sFAIL: dispatch returned HTTP %s (expected 202)%s\n' "$RED" "$SMOKE_LAST_HTTP" "$RESET" >&2
        smoke_echo_safe "$(head -c 1200 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        return 1
    }
    GENERATE_DISPATCH_BODY="$SMOKE_LAST_BODY"
    GENERATE_JOB_ID=$(jq -r '.job_id // empty' "$GENERATE_DISPATCH_BODY")
    [[ -n "$GENERATE_JOB_ID" && "$GENERATE_JOB_ID" != "null" ]] || {
        echo "FAIL: dispatch did not return a non-empty job_id" >&2
        return 1
    }
    jq -e --arg expected "/api/jobs/${GENERATE_JOB_ID}/full" '
        .ok == true
        and (.status | type == "string" and length > 0)
        and .status_url == $expected
        and (.current_stage | type == "string" and length > 0)
    ' "$GENERATE_DISPATCH_BODY" >/dev/null || {
        echo "FAIL: dispatch missing canonical async fields or current_stage" >&2
        return 1
    }
}

generate_poll_and_fetch() {
    smoke_poll_terminal "$GENERATE_JOB_ID" || {
        printf '%sFAIL: polling did not reach terminal state (last_status=%s)%s\n' "$RED" "${SMOKE_LAST_STATUS:-?}" "$RESET" >&2
        return 1
    }
    [[ "$SMOKE_LAST_STATUS" == "completed" || "$SMOKE_LAST_STATUS" == "SUCCEEDED" ]] || {
        printf '%sFAIL: job ended with status %s%s\n' "$RED" "$SMOKE_LAST_STATUS" "$RESET" >&2
        smoke_echo_safe "$(head -c 1200 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        return 1
    }
    smoke_curl GET "/api/jobs/${GENERATE_JOB_ID}/full" >/dev/null
    GENERATE_FULL_BODY="$SMOKE_LAST_BODY"
}

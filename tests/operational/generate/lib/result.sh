#!/usr/bin/env bash
# Shared result extraction for GenerationEnvelopeV2 jobs.

generate_extract_result() {
    GENERATE_RESULT=$(jq -c '.result.data.items[0].result // .result.items[0].result // .result.output // .result // empty' "$GENERATE_FULL_BODY")
    [[ -n "$GENERATE_RESULT" && "$GENERATE_RESULT" != "null" ]] || {
        echo "FAIL: missing canonical generation result" >&2
        return 1
    }
    GENERATE_SCRIPT=$(jq -r '.output.text // .script.text // .script // .text // .content // empty' <<<"$GENERATE_RESULT")
    [[ -n "$GENERATE_SCRIPT" && "$GENERATE_SCRIPT" != "null" ]] || {
        echo "FAIL: script text is empty" >&2
        smoke_echo_safe "$(head -c 2000 "$GENERATE_FULL_BODY" 2>/dev/null || true)" >&2
        return 1
    }
    GENERATE_SCRIPT_ID=$(jq -r '.script_id // empty' <<<"$GENERATE_RESULT")
}

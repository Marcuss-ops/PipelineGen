#!/usr/bin/env bash
# Shared result extraction for GenerationEnvelopeV2 jobs.

generate_extract_result() {
    # Canonical jobs-plane results are the artifact-manifest envelope with
    # the generated payload under result.result. Keep the older async
    # envelope paths for deployed clients during the transition.
    GENERATE_RESULT=$(jq -c '.result.data.result // .result.data.data // .result.data.items[0].result // .result.items[0].result // .result.result // .result.output // .result // empty' "$GENERATE_FULL_BODY")
    [[ -n "$GENERATE_RESULT" && "$GENERATE_RESULT" != "null" ]] || {
        echo "FAIL: missing canonical generation result" >&2
        return 1
    }
    GENERATE_SCRIPT=$(jq -r '.output.text // .script.text // .script // .text // .content // empty' <<<"$GENERATE_RESULT")
    if [[ "${GENERATE_REQUIRE_SCRIPT:-1}" != "0" && ( -z "$GENERATE_SCRIPT" || "$GENERATE_SCRIPT" == "null" ) ]]; then
        echo "FAIL: script text is empty" >&2
        smoke_echo_safe "$(head -c 2000 "$GENERATE_FULL_BODY" 2>/dev/null || true)" >&2
        return 1
    fi
    GENERATE_SCRIPT_ID=$(jq -r '.script_id // empty' <<<"$GENERATE_RESULT")
}

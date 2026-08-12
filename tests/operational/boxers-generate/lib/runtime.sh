#!/usr/bin/env bash
# tests/operational/boxers-generate/lib/runtime.sh — polling and payload helpers.
# Source-only library. Functions intentionally use the caller's global runner state.

refresh_parent_full_until_terminal() {
    local job_id="$1"
    local destination="$2"
    local deadline=$(( $(date +%s) + SMOKE_POLL_TIMEOUT_SECONDS ))
    local status=""
    while (( $(date +%s) < deadline )); do
        smoke_wallclock_check
        smoke_curl GET "/api/jobs/$job_id/full" >/dev/null
        if [[ "$SMOKE_LAST_HTTP" != "200" ]]; then
            return 1
        fi
        cp "$SMOKE_LAST_BODY" "$destination"
        status=$(jq -r '.status // .job.status // .result.status // .job.result.status // ""' "$destination")
        case "$status" in
            completed|SUCCEEDED|SUCCEEDED_WITH_WARNINGS|failed|FAILED|cancelled|CANCELLED|dead_letter|DEAD_LETTER)
                return 0
                ;;
        esac
        sleep "$SMOKE_POLL_INTERVAL_SECONDS"
    done
    return 124
}

refresh_parent_full_until_children() {
    local job_id="$1"
    local destination="$2"
    local deadline=$(( $(date +%s) + SMOKE_POLL_TIMEOUT_SECONDS ))
    local child_ids=""
    while (( $(date +%s) < deadline )); do
        smoke_wallclock_check
        smoke_curl GET "/api/jobs/$job_id/full" >/dev/null
        if [[ "$SMOKE_LAST_HTTP" != "200" ]]; then
            return 1
        fi
        cp "$SMOKE_LAST_BODY" "$destination"
        child_ids=$(jq -r '
            (.result.data.child_job_ids // .result.child_job_ids
             // .job.result.data.child_job_ids // .job.result.child_job_ids // [])
            | map(select(type == "string" and length > 0)) | .[]
        ' "$destination")
        if [[ -n "$child_ids" ]]; then
            return 0
        fi
        sleep "$SMOKE_POLL_INTERVAL_SECONDS"
    done
    return 124
}

# Helper function to preprocess JSON scenarios.
prepare_payload() {
    local scenario_file="$1"
    local temp_json="$WORK_DIR/payload.json"

    if ! python3 "$DIR/stock_registry.py" materialize \
        --resolved "$RESOLVED_STOCK_FILE" \
        --input "$scenario_file" \
        --output "$temp_json"; then
        printf '%ssetup error: scenario stock binding is not present in resolved registry: %s%s\n' \
            "$RED" "$scenario_file" "$RESET" >&2
        return 1
    fi

    # Perform string replacements for Tyson clip placeholders.
    for i in {0..5}; do
        local idx=$((i + 1))
        local placeholder="TYSON_CLIP_ID_${idx}"
        sed -i "s/$placeholder/${TYSON_CLIPS[$i]}/g" "$temp_json"
    done

    # Inject the sole runtime voiceover folder value into every payload declaration.
    local runtime_payload="$temp_json.runtime"
    jq --arg folder "$VOICEOVER_FOLDER" '
        walk(
            if type == "object" then
                with_entries(
                    if .key == "voiceover_folder_id" or .key == "folder_id"
                    then .value = $folder
                    else .
                    end
                )
            else .
            end
        )
    ' "$temp_json" > "$runtime_payload"
    mv "$runtime_payload" "$temp_json"

    if ! python3 "$DIR/runner_policy.py" validate-folder \
        --payload "$temp_json" \
        --folder-id "$VOICEOVER_FOLDER"; then
        printf '%ssetup error: payload voiceover folder is inconsistent with BOXERS_VOICEOVER_FOLDER_ID%s\n' \
            "$RED" "$RESET" >&2
        return 1
    fi

    # Scenario metadata is runner-only and must never be sent to the API.
    # runner_policy.py is also the testable source of the mode contract.
    local payload_json="$temp_json.payload"
    if ! python3 "$DIR/runner_policy.py" prepare \
        --mode "$RUNNER_MODE" \
        --input "$temp_json" \
        --output "$payload_json"; then
        printf '%ssetup error: invalid %s runner payload: %s%s\n' \
            "$RED" "$RUNNER_MODE" "$scenario_file" "$RESET" >&2
        return 1
    fi
    mv "$payload_json" "$temp_json"

    cat "$temp_json"
}
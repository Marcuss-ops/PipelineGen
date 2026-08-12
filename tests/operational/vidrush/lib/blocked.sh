#!/usr/bin/env bash
# tests/operational/vidrush/lib/blocked.sh — explicit external prerequisites.
# Returning BLOCKED is intentional: these scenarios cannot safely manufacture
# provider/worker failures and must never be reported as successful.

run_external_prerequisite_block() {
    local reason required_json
    case "$SCENARIO_TYPE" in
        failure_injection)
            reason="requires explicit provider/Drive/Qdrant/Ollama failure injection; no chaos hook is configured"
            required_json=$(jq -c '[.failure_cases[]?.case_id] // []' "$SCENARIO_FILE")
            ;;
        render_handoff)
            reason="requires a connected scene.composite.v1@1 worker, jobs.submit M2M credentials and render destination"
            required_json=$(jq -c '[.render_pipeline_steps[]?.name] // []' "$SCENARIO_FILE")
            ;;
        *)
            reason="external prerequisite is not configured"
            required_json='[]'
            ;;
    esac
    printf '%sBLOCKED%s %s: %s\n' "$YELLOW" "$RESET" "$SCENARIO_ID" "$reason"
    report_json "BLOCKED" "" "" "{\"reason\":$(jq -Rn --arg v "$reason" '$v | @json'),\"required_steps\":$required_json}" | jq '.'
    return 1
}

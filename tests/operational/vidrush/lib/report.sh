#!/usr/bin/env bash
# tests/operational/vidrush/lib/report.sh — scenario report lifecycle helpers.
# Source-only library. The runner provides SCENARIO_ID, GIT_SHA,
# TIMESTAMP_START and SCENARIO_FILE before sourcing this file.

report_json() {
    local status="$1" job_id="${2:-}" cache_mode="${3:-}" extra="${4:-}"
    local now_ms
    now_ms=$(date +%s%3N 2>/dev/null || date +%s000)
    local total_ms=$(( now_ms - TIMESTAMP_START ))

    jq -n \
        --arg scenario_id "$SCENARIO_ID" \
        --arg git_sha "$GIT_SHA" \
        --arg job_id "$job_id" \
        --arg input_hash "$(jq -c '.payload // .checks' "$SCENARIO_FILE" | md5sum | cut -d' ' -f1)" \
        --arg status "$status" \
        --arg cache_mode "$cache_mode" \
        --argjson total_ms "$total_ms" \
        --arg extra_json "$extra" \
    '{
        scenario_id: $scenario_id,
        git_sha: $git_sha,
        job_id: $job_id,
        input_hash: $input_hash,
        status: $status,
        cache_mode: $cache_mode,
        timing_ms: { total: $total_ms },
        counts: { segments: 0, entities: 0, provider_requests: 0, bindings: 0, unresolved: 0 },
        resources: { cpu_peak_pct: 0, rss_peak_mb: 0, goroutines_peak: 0 },
        artifacts: { sqlite_verified: false, qdrant_verified: false, drive_verified: false, render_verified: false }
    } * (($extra_json | fromjson?) // {})'
}

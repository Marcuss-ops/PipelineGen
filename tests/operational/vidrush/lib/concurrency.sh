#!/usr/bin/env bash
# tests/operational/vidrush/lib/concurrency.sh — measured concurrency phase.
# Requires the runner contract from run_scenario.sh plus common.sh helpers:
# SCENARIO_ID, SCENARIO_FILE, SMOKE_API_BASE, SMOKE_TOKEN,
# SMOKE_HTTP_TIMEOUT_SECONDS, SMOKE_POLL_TIMEOUT_SECONDS, WORK_DIR,
# report_json and smoke_gen_uuid.

vidrush_metrics_snapshot() {
    local metrics_url="${METRICS_URL:-http://${SMOKE_API_BASE}/metrics}"
    local metrics_body

    # The admin token is intentionally not accepted here. /metrics has its
    # own fail-closed credential so a missing scrape is evidence of an
    # unobservable run, not a reason to silently skip stop conditions.
    if [[ -z "${METRICS_AUTH_TOKEN:-}" ]]; then
        jq -c -n '{available:false,reason:"METRICS_AUTH_TOKEN is not configured"}'
        return 0
    fi
    metrics_body=$(curl -fsS --max-time 8 \
        -H "Authorization: Bearer ${METRICS_AUTH_TOKEN}" \
        "$metrics_url" 2>/dev/null) || {
        jq -c -n '{available:false,reason:"metrics endpoint unavailable"}'
        return 0
    }

    awk '
    BEGIN {
        artlist_requests = 0; image_requests = 0
        artlist_failures = 0; image_failures = 0
        sqlite_busy = 0; queue_depth = 0
        rss_bytes = 0; goroutines = 0
    }
    $1 ~ /^vidrush_provider_requests_total\{/ && $1 ~ /provider="artlist"/ { artlist_requests = $2 }
    $1 ~ /^vidrush_provider_requests_total\{/ && $1 ~ /provider="internet_images"/ { image_requests = $2 }
    $1 ~ /^vidrush_provider_failures_total\{/ && $1 ~ /provider="artlist"/ { artlist_failures = $2 }
    $1 ~ /^vidrush_provider_failures_total\{/ && $1 ~ /provider="internet_images"/ { image_failures = $2 }
    $1 ~ /^sqlite_busy_total\{/ { sqlite_busy += $2 }
    $1 ~ /^jobs_queue_depth\{/ { queue_depth += $2 }
    $1 == "process_resident_memory_bytes" { rss_bytes = $2 }
    $1 == "go_goroutines" { goroutines = $2 }
    END {
        printf "{\"available\":true,\"artlist_requests\":%s,\"image_requests\":%s,\"artlist_failures\":%s,\"image_failures\":%s,\"sqlite_busy\":%s,\"queue_depth\":%s,\"rss_bytes\":%s,\"goroutines\":%s}\n", \
            artlist_requests, image_requests, artlist_failures, image_failures, sqlite_busy, queue_depth, rss_bytes, goroutines
    }' <<<"$metrics_body"
}

run_concurrency_wave() {
    local payload="$1" width="$2" label="$3" wave_dir="$4" metrics_before="$5"
    local base_url="http://${SMOKE_API_BASE}"
    local wave_start_ms wave_end_ms
    wave_start_ms=$(date +%s%3N 2>/dev/null || date +%s000)
    local -a pids=() jobs=()
    local i
    for ((i=0; i<width; i++)); do
        local response_file="$wave_dir/${label}-${i}.body"
        local code_file="$wave_dir/${label}-${i}.code"
        local error_file="$wave_dir/${label}-${i}.err"
        local idem_key="vidrush-${SCENARIO_ID}-${label}-${i}-$(smoke_gen_uuid)"
        (
            curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
                -X POST -o "$response_file" -w '%{http_code}' \
                -H "Authorization: Bearer ${SMOKE_TOKEN}" \
                -H "Idempotency-Key: ${idem_key}" \
                -H 'Content-Type: application/json' \
                --data "$payload" "$base_url/api/script/generate" >"$code_file" 2>"$error_file" || true
        ) &
        pids+=("$!")
    done
    local pid
    for pid in "${pids[@]}"; do
        wait "$pid" || true
    done

    local failures=0
    for ((i=0; i<width; i++)); do
        local code job_id
        code=$(cat "$wave_dir/${label}-${i}.code" 2>/dev/null || echo 0)
        job_id=$(jq -r '.job_id // empty' "$wave_dir/${label}-${i}.body" 2>/dev/null || true)
        if [[ "$code" != "202" || -z "$job_id" ]]; then
            failures=$((failures + 1))
            continue
        fi
        jobs+=("$job_id")
    done
    if (( failures > 0 )); then
        wave_end_ms=$(date +%s%3N 2>/dev/null || date +%s000)
        local metrics_after
        metrics_after=$(vidrush_metrics_snapshot)
        jq -c -n --arg wave_label "$label" --argjson width "$width" --argjson dispatch_failures "$failures" \
            --argjson wall_ms "$((wave_end_ms - wave_start_ms))" \
            --argjson metrics_before "$metrics_before" --argjson metrics_after "$metrics_after" \
            '{label:$wave_label,width:$width,dispatch_failures:$dispatch_failures,terminal:[],wall_ms:$wall_ms,metrics_before:$metrics_before,metrics_after:$metrics_after}'
        return 1
    fi

    local -a terminal=()
    local job_id
    local failed_terminal=0
    for job_id in "${jobs[@]}"; do
        local deadline=$(( $(date +%s) + SMOKE_POLL_TIMEOUT_SECONDS ))
        local status="timeout"
        while (( $(date +%s) < deadline )); do
            local body_file="$wave_dir/${label}-${job_id}.poll"
            local code
            code=$(curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
                -o "$body_file" -w '%{http_code}' \
                -H "Authorization: Bearer ${SMOKE_TOKEN}" \
                "$base_url/api/jobs/${job_id}" 2>/dev/null || echo 0)
            if [[ "$code" == "200" ]]; then
                status=$(jq -r '.status // .job.status // "unknown"' "$body_file" 2>/dev/null || echo unknown)
                case "$status" in
                    completed|SUCCEEDED|failed|cancelled|dead_letter|FAILED) break ;;
                esac
            fi
            sleep 1
        done
        if [[ "$status" != "completed" && "$status" != "SUCCEEDED" ]]; then
            failed_terminal=$((failed_terminal + 1))
        fi
        terminal+=("$status")
    done
    wave_end_ms=$(date +%s%3N 2>/dev/null || date +%s000)
    local metrics_after
    metrics_after=$(vidrush_metrics_snapshot)
    jq -c -n --arg wave_label "$label" --argjson width "$width" --argjson dispatch_failures "$failures" \
        --argjson terminal "$(printf '%s\n' "${terminal[@]}" | jq -R . | jq -s .)" \
        --argjson wall_ms "$((wave_end_ms - wave_start_ms))" \
        --argjson metrics_before "$metrics_before" --argjson metrics_after "$metrics_after" \
        '{label:$wave_label,width:$width,dispatch_failures:$dispatch_failures,terminal:$terminal,failed_terminal:'"$failed_terminal"',wall_ms:$wall_ms,metrics_before:$metrics_before,metrics_after:$metrics_after}'
    (( failed_terminal == 0 ))
}

run_concurrency() (
    # Keep the wave workspace private to this phase and guarantee cleanup even
    # when a strict-mode failure exits before the normal path reaches rm -rf.
    echo "=== VidRush concurrency: $SCENARIO_ID ==="
    local payload wave_dir levels_json level wave_json waves_json='[]' failed=0 metrics_before
    payload=$(jq -c '.payload' "$SCENARIO_FILE")
    wave_dir=$(mktemp -d "${TMPDIR:-/tmp}/vidrush-concurrency.XXXXXX")
    trap 'rm -rf -- "$wave_dir"' EXIT
    metrics_before=$(vidrush_metrics_snapshot)
    if ! jq -e '.available == true' <<<"$metrics_before" >/dev/null; then
        local metrics_reason
        metrics_reason=$(jq -r '.reason // "metrics unavailable"' <<<"$metrics_before")
        rm -rf "$wave_dir"
        report_json "BLOCKED" "" "" "{\"reason\":$(jq -Rn --arg v "$metrics_reason" '$v | @json'),\"required\":\"METRICS_AUTH_TOKEN and /metrics\"}" | jq '.'
        return 1
    fi
    levels_json=$(jq -c '[.concurrency_levels[] | select(.concurrent_jobs <= 5) | .concurrent_jobs]' "$SCENARIO_FILE")
    for level in $(jq -r '.[]' <<<"$levels_json"); do
        echo "  → bounded wave: ${level} job(s)"
        wave_json=$(run_concurrency_wave "$payload" "$level" "measured-${level}" "$wave_dir" "$metrics_before" | tail -1)
        echo "  $(jq -c '.' <<<"$wave_json")"
        waves_json=$(jq -c --argjson wave "$wave_json" '. + [$wave]' <<<"$waves_json")
        if ! jq -e '(.failed_terminal // 0) == 0 and (.dispatch_failures // 0) == 0 and (.metrics_after.available == true) and ((.metrics_after.sqlite_busy - .metrics_before.sqlite_busy) == 0) and ((.metrics_after.artlist_failures - .metrics_before.artlist_failures) == 0) and ((.metrics_after.image_failures - .metrics_before.image_failures) == 0)' <<<"$wave_json" >/dev/null; then
            failed=1
        fi
        metrics_before=$(jq -c '.metrics_after' <<<"$wave_json")
    done
    rm -rf "$wave_dir"
    if (( failed != 0 )); then
        report_json "FAILED" "" "cold" "{\"concurrency\":{\"levels\":$waves_json,\"verdict\":\"FAILED\"}}" | jq '.'
        return 1
    fi
    report_json "SUCCEEDED" "" "warm" "{\"concurrency\":{\"levels\":$waves_json,\"verdict\":\"PASS\",\"provider_enabled\":true}}" | jq '.'
)

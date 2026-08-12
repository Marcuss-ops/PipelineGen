#!/usr/bin/env bash
# tests/operational/vidrush/lib/idempotency.sh — idempotency phase.
# Requires report_json and the scenario globals supplied by run_scenario.sh.

idempotency_post() {
    local payload="$1" key="$2" label="$3" dir="$4"
    curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
        -D "$dir/${label}.headers" \
        -o "$dir/${label}.body" -w '%{http_code}' \
        -H "Authorization: Bearer ${SMOKE_TOKEN}" \
        -H "Idempotency-Key: ${key}" \
        -H 'Content-Type: application/json' \
        --data "$payload" "http://${SMOKE_API_BASE}/api/script/generate" \
        >"$dir/${label}.code" 2>"$dir/${label}.err" || true
}

run_idempotency() (
    # Keep the idempotency workspace private to this phase and guarantee
    # cleanup if strict mode exits before the normal path reaches rm -rf.
    echo "=== VidRush idempotency: $SCENARIO_ID ==="
    local payload payload_conflict test_dir first_job replay_job conflict_code
    local replay_header_same="false" concurrent_ok="true" conflict_ok="false"
    payload=$(jq -c '.payload' "$SCENARIO_FILE")
    payload_conflict=$(jq -c '.payload | .items[0].source.source_text += " conflict-body"' "$SCENARIO_FILE")
    test_dir=$(mktemp -d "${TMPDIR:-/tmp}/vidrush-idempotency.XXXXXX")
    trap 'rm -rf -- "$test_dir"' EXIT

    local key1="vidrush-idem-k1-$(smoke_gen_uuid)"
    idempotency_post "$payload" "$key1" same-1 "$test_dir"
    first_job=$(jq -r '.job_id // empty' "$test_dir/same-1.body" 2>/dev/null || true)
    idempotency_post "$payload" "$key1" same-2 "$test_dir"
    replay_job=$(jq -r '.job_id // empty' "$test_dir/same-2.body" 2>/dev/null || true)
    if [[ -n "$first_job" && "$first_job" == "$replay_job" ]] && grep -Eqi '^X-Idempotency-Replay:[[:space:]]*true' "$test_dir/same-2.headers"; then
        replay_header_same="true"
    fi

    local key2="vidrush-idem-k2-$(smoke_gen_uuid)"
    idempotency_post "$payload" "$key2" conflict-1 "$test_dir"
    idempotency_post "$payload_conflict" "$key2" conflict-2 "$test_dir"
    conflict_code=$(cat "$test_dir/conflict-2.code" 2>/dev/null || echo 0)
    if [[ "$conflict_code" == "409" ]] && jq -e '((.code // .error.code // "") | tostring | test("IDEMPOTENCY_KEY_CONFLICT")) or ((.error // "") | tostring | test("IDEMPOTENCY_KEY_CONFLICT"))' "$test_dir/conflict-2.body" >/dev/null 2>&1; then
        conflict_ok="true"
    fi

    local key3="vidrush-idem-k3-$(smoke_gen_uuid)"
    local -a pids=()
    local i
    for ((i=0; i<3; i++)); do
        idempotency_post "$payload" "$key3" "concurrent-${i}" "$test_dir" &
        pids+=("$!")
    done
    local pid
    for pid in "${pids[@]}"; do wait "$pid" || true; done
    local concurrent_jobs=""
    for ((i=0; i<3; i++)); do
        local code job
        code=$(cat "$test_dir/concurrent-${i}.code" 2>/dev/null || echo 0)
        job=$(jq -r '.job_id // empty' "$test_dir/concurrent-${i}.body" 2>/dev/null || true)
        if [[ ! "$code" =~ ^2[0-9][0-9]$ || -z "$job" ]]; then
            concurrent_ok="false"
        fi
        concurrent_jobs+="${job},"
    done
    local distinct_jobs
    distinct_jobs=$(printf '%s\n' "$concurrent_jobs" | tr ',' '\n' | sed '/^$/d' | sort -u | wc -l)
    if [[ "$distinct_jobs" != "1" ]]; then concurrent_ok="false"; fi

    rm -rf "$test_dir"
    if [[ "$replay_header_same" != "true" || "$conflict_ok" != "true" || "$concurrent_ok" != "true" ]]; then
        report_json "FAILED" "" "" "{\"idempotency\":{\"replay\":$replay_header_same,\"conflict\":$conflict_ok,\"concurrent\":$concurrent_ok}}" | jq '.'
        return 1
    fi
    report_json "SUCCEEDED" "" "warm" "{\"idempotency\":{\"replay\":true,\"conflict\":true,\"concurrent\":true}}" | jq '.'
)

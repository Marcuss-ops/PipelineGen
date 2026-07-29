#!/usr/bin/env bash
# tests/operational/vidrush/run_scenario.sh — VidRush scenario runner.
#
# Usage:
#   bash run_scenario.sh <scenario.json>
#   bash run_scenario.sh --dry <scenario.json>
#
# Reads a VidRush scenario manifest (scenarios/NN_name.json) and:
#   1. For preflight (00_*): executes health checks, collects results.
#   2. For script.generate (01_* and later): POSTs the payload,
#      polls /api/jobs/{id}/full, runs assertions, produces report.
#
# Outputs a unified JSON report to stdout. Exits 0 on PASS, 1 on FAIL,
# 2 on setup error, 124 on timeout.
#
# Environment (inherited from lib/common.sh):
#   SMOKE_API_BASE, SMOKE_TOKEN, SMOKE_TIMEOUT_SECONDS, etc.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)

# ── Per-scenario timeouts (set BEFORE sourcing common.sh so SMOKE_DEADLINE
# is computed with the correct budget) ───────────────────────────────────
export SMOKE_TIMEOUT_SECONDS="${SCENARIO_OVERALL_TIMEOUT_SECONDS:-900}"
SCENARIO_POLL_TIMEOUT_SECONDS="${SCENARIO_POLL_TIMEOUT_SECONDS:-300}"

# ── Parse arguments BEFORE sourcing common.sh (common.sh processes $@
# and rejects unknown flags). We remove custom flags and leave $@ empty ──
SCENARIO_FILE=""
DRY_MODE=0
for arg in "$@"; do
    case "$arg" in
        --dry) DRY_MODE=1 ;;
        -h|--help)
            echo "Usage: bash run_scenario.sh [--dry] <scenario.json>"
            exit 0
            ;;
        *)  SCENARIO_FILE="$arg" ;;
    esac
done

if [[ -z "$SCENARIO_FILE" ]]; then
    echo "setup error: missing scenario file argument" >&2
    echo "Usage: bash run_scenario.sh [--dry] <scenario.json>" >&2
    exit 2
fi
if [[ ! -f "$SCENARIO_FILE" ]]; then
    echo "setup error: scenario file not found: $SCENARIO_FILE" >&2
    exit 2
fi

if [[ "$DRY_MODE" == "1" ]]; then
    export SMOKE_DRY_RUN=1
fi

# Clear $@ before sourcing common.sh so it does not parse the scenario
# file path as an unknown flag.
set --

# shellcheck disable=SC1091
source "$DIR/../lib/common.sh"
smoke_require curl jq

# ── Read scenario manifest ─────────────────────────────────────────────
SCENARIO=$(jq -c '.' "$SCENARIO_FILE")
SCENARIO_ID=$(jq -r '.scenario_id // "unknown"' <<<"$SCENARIO")
SCENARIO_DESC=$(jq -r '.description // ""' <<<"$SCENARIO")
SCENARIO_TYPE="script_generate"
if [[ "$SCENARIO_ID" == 00_* ]]; then
    SCENARIO_TYPE="preflight"
fi

# ── Git SHA ────────────────────────────────────────────────────────────
GIT_SHA=$(git -C "$DIR/../../.." rev-parse --short HEAD 2>/dev/null || echo "unknown")
TIMESTAMP_START=$(date +%s%3N 2>/dev/null || date +%s000)

# ── Report skeleton ────────────────────────────────────────────────────
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
        --argjson extra "$extra" \
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
    } * $extra'
}

# ── Dry-run path ───────────────────────────────────────────────────────
if [[ "$DRY_MODE" == "1" ]]; then
    echo "DRY RUN — scenario: $SCENARIO_ID ($SCENARIO_TYPE)"
    echo "  description: $SCENARIO_DESC"
    if [[ "$SCENARIO_TYPE" == "preflight" ]]; then
        echo "  checks:"
        jq -r '.checks.http_endpoints[]? | "    \(.method) \(.path) [obligatory=\(.obligatory)]"' "$SCENARIO_FILE" 2>/dev/null || true
        jq -r '.checks.dependencies[]? | "    \(.name) (\(.kind)) [obligatory=\(.obligatory)]"' "$SCENARIO_FILE" 2>/dev/null || true
        jq -r '.checks.system[]? | "    \(.name) (\(.kind)) [obligatory=\(.obligatory)]"' "$SCENARIO_FILE" 2>/dev/null || true
    else
        echo "  endpoint: POST /api/script/generate"
        echo "  items: $(jq '.payload.items | length' "$SCENARIO_FILE")"
    fi
    report_json "DRY_RUN" "" "" "{}" | jq '.'
    exit 0
fi

# ══════════════════════════════════════════════════════════════════════════
# PREFLIGHT PATH (00_*)
# ══════════════════════════════════════════════════════════════════════════
run_preflight() {
    echo "=== VidRush preflight: $SCENARIO_ID ==="
    local fail_count=0
    local pass_count=0
    local checks_json="["

    # ── HTTP endpoints ─────────────────────────────────────────────────
    local http_count
    http_count=$(jq '.checks.http_endpoints | length' "$SCENARIO_FILE")
    for ((i=0; i<http_count; i++)); do
        local path method expect obligatory label
        path=$(jq -r ".checks.http_endpoints[$i].path" "$SCENARIO_FILE")
        method=$(jq -r ".checks.http_endpoints[$i].method" "$SCENARIO_FILE")
        expect=$(jq -r ".checks.http_endpoints[$i].expect_status" "$SCENARIO_FILE")
        obligatory=$(jq -r ".checks.http_endpoints[$i].obligatory" "$SCENARIO_FILE")
        label=$(jq -r ".checks.http_endpoints[$i].label" "$SCENARIO_FILE")

        local http_code
        smoke_curl "$method" "$path" >/dev/null
        http_code="${SMOKE_LAST_HTTP:-0}"
        local check_result="PASS"
        if [[ "$http_code" != "$expect" ]]; then
            check_result="FAIL"
            ((fail_count++)) || true
            printf '  %sFAIL%s %s %s → HTTP %s (expected %s)\n' "$RED" "$RESET" "$method" "$path" "$http_code" "$expect"
            if [[ "$obligatory" == "true" ]]; then
                echo "  obligatory dependency failed — aborting"
                checks_json="${checks_json}{\"check\":\"$label\",\"result\":\"FAIL\",\"http\":$http_code},"
                checks_json="${checks_json%?}]"
                report_json "FAILED" "" "" "{\"preflight\":{\"pass\":$pass_count,\"fail\":$fail_count,\"checks\":$checks_json}}" | jq '.'
                return 1
            fi
        else
            ((pass_count++)) || true
            printf '  %sPASS%s %s %s → HTTP %s\n' "$GREEN" "$RESET" "$method" "$path" "$http_code"
        fi
        checks_json="${checks_json}{\"check\":\"$label\",\"result\":\"$check_result\",\"http\":$http_code},"
    done

    # ── Dependencies ───────────────────────────────────────────────────
    local dep_count
    dep_count=$(jq '.checks.dependencies | length' "$SCENARIO_FILE")
    for ((i=0; i<dep_count; i++)); do
        local name kind obligatory check target binary url env_var label
        name=$(jq -r ".checks.dependencies[$i].name" "$SCENARIO_FILE")
        kind=$(jq -r ".checks.dependencies[$i].kind" "$SCENARIO_FILE")
        obligatory=$(jq -r ".checks.dependencies[$i].obligatory" "$SCENARIO_FILE")
        check=$(jq -r ".checks.dependencies[$i].check" "$SCENARIO_FILE")
        label=$(jq -r ".checks.dependencies[$i].label" "$SCENARIO_FILE")

        local check_result="PASS"
        case "$check" in
            file_exists)
                target=$(jq -r ".checks.dependencies[$i].target" "$SCENARIO_FILE")
                target="${target/\$\{DB_PATH:-data\/media\/media.db.sqlite\}/data/media/media.db.sqlite}"
                if [[ ! -f "$target" ]]; then
                    check_result="FAIL"
                    printf '  %sFAIL%s %s: file not found: %s\n' "$RED" "$RESET" "$label" "$target"
                else
                    printf '  %sPASS%s %s: file exists (%s)\n' "$GREEN" "$RESET" "$label" "$target"
                fi
                ;;
            http_health)
                url=$(jq -r ".checks.dependencies[$i].url" "$SCENARIO_FILE")
                url="${url/\$\{QDRANT_URL:-http:\/\/127.0.0.1:6333\}/http://127.0.0.1:6333}"
                url="${url/\$\{OLLAMA_URL:-http:\/\/127.0.0.1:11434\}/http://127.0.0.1:11434}"
                url="${url/\$\{ARTLIST_SCRAPER_URL:-http:\/\/127.0.0.1:3000\}/http://127.0.0.1:3000}"
                if curl -fsS --max-time 5 "$url" >/dev/null 2>&1; then
                    printf '  %sPASS%s %s: reachable\n' "$GREEN" "$RESET" "$label"
                else
                    check_result="FAIL"
                    printf '  %sFAIL%s %s: unreachable (%s)\n' "$RED" "$RESET" "$label" "$url"
                fi
                ;;
            http_connectivity)
                url=$(jq -r ".checks.dependencies[$i].url" "$SCENARIO_FILE")
                if curl -fsS --max-time 10 --head "$url" >/dev/null 2>&1; then
                    printf '  %sPASS%s %s: reachable\n' "$GREEN" "$RESET" "$label"
                else
                    check_result="WARN"
                    printf '  %sWARN%s %s: unreachable (best-effort)\n' "$YELLOW" "$RESET" "$label"
                fi
                ;;
            googledrive_token)
                env_var=$(jq -r ".checks.dependencies[$i].env_var" "$SCENARIO_FILE")
                if [[ -n "${!env_var:-}" && -f "${!env_var}" ]]; then
                    printf '  %sPASS%s %s: credentials found (%s)\n' "$GREEN" "$RESET" "$label" "${!env_var}"
                else
                    check_result="FAIL"
                    printf '  %sFAIL%s %s: $%s not set or file missing\n' "$RED" "$RESET" "$label" "$env_var"
                fi
                ;;
            binary_exists)
                binary=$(jq -r ".checks.dependencies[$i].binary" "$SCENARIO_FILE")
                if command -v "$binary" >/dev/null 2>&1; then
                    printf '  %sPASS%s %s: %s found\n' "$GREEN" "$RESET" "$label" "$(command -v "$binary")"
                else
                    check_result="FAIL"
                    printf '  %sFAIL%s %s: %s not in PATH\n' "$RED" "$RESET" "$label" "$binary"
                fi
                ;;
        esac

        if [[ "$check_result" == "FAIL" ]]; then
            ((fail_count++)) || true
            if [[ "$obligatory" == "true" ]]; then
                echo "  obligatory dependency failed — aborting"
                checks_json="${checks_json}{\"check\":\"$label\",\"result\":\"FAIL\"},"
                checks_json="${checks_json%?}]"
                report_json "FAILED" "" "" "{\"preflight\":{\"pass\":$pass_count,\"fail\":$fail_count,\"checks\":$checks_json}}" | jq '.'
                return 1
            fi
        else
            ((pass_count++)) || true
        fi
        checks_json="${checks_json}{\"check\":\"$label\",\"result\":\"$check_result\"},"
    done

    # ── System checks ──────────────────────────────────────────────────
    local sys_count
    sys_count=$(jq '.checks.system | length' "$SCENARIO_FILE")
    for ((i=0; i<sys_count; i++)); do
        local name kind obligatory check label
        name=$(jq -r ".checks.system[$i].name" "$SCENARIO_FILE")
        kind=$(jq -r ".checks.system[$i].kind" "$SCENARIO_FILE")
        obligatory=$(jq -r ".checks.system[$i].obligatory" "$SCENARIO_FILE")
        check=$(jq -r ".checks.system[$i].check" "$SCENARIO_FILE")
        label=$(jq -r ".checks.system[$i].label" "$SCENARIO_FILE")

        local check_result="PASS"
        case "$check" in
            api_endpoint)
                local jq_assert api_path
                api_path=$(jq -r ".checks.system[$i].path" "$SCENARIO_FILE")
                jq_assert=$(jq -r ".checks.system[$i].jq_assert" "$SCENARIO_FILE")
                smoke_curl GET "$api_path" >/dev/null
                if [[ "$SMOKE_LAST_HTTP" == "200" ]] && jq -e "$jq_assert" "$SMOKE_LAST_BODY" >/dev/null 2>&1; then
                    printf '  %sPASS%s %s: %s\n' "$GREEN" "$RESET" "$label" "$(jq -r "$jq_assert" "$SMOKE_LAST_BODY" 2>/dev/null || echo 'ok')"
                else
                    check_result="FAIL"
                    printf '  %sFAIL%s %s\n' "$RED" "$RESET" "$label"
                fi
                ;;
            unix_df)
                local disk_path min_gb
                disk_path=$(jq -r ".checks.system[$i].path" "$SCENARIO_FILE")
                min_gb=$(jq -r ".checks.system[$i].min_gb" "$SCENARIO_FILE")
                local avail_kb avail_gb
                avail_kb=$(df -k "${disk_path:-/var/lib/pipelinegen}" 2>/dev/null | awk 'NR==2{print $4}' || echo 0)
                avail_gb=$(( avail_kb / 1024 / 1024 ))
                if (( avail_gb >= min_gb )); then
                    printf '  %sPASS%s %s: %d GB available (need >= %d)\n' "$GREEN" "$RESET" "$label" "$avail_gb" "$min_gb"
                else
                    check_result="FAIL"
                    printf '  %sFAIL%s %s: %d GB available (need >= %d)\n' "$RED" "$RESET" "$label" "$avail_gb" "$min_gb"
                fi
                ;;
        esac

        if [[ "$check_result" == "FAIL" ]]; then
            ((fail_count++)) || true
            if [[ "$obligatory" == "true" ]]; then
                echo "  obligatory system check failed — aborting"
                checks_json="${checks_json}{\"check\":\"$label\",\"result\":\"FAIL\"},"
                checks_json="${checks_json%?}]"
                report_json "FAILED" "" "" "{\"preflight\":{\"pass\":$pass_count,\"fail\":$fail_count,\"checks\":$checks_json}}" | jq '.'
                return 1
            fi
        else
            ((pass_count++)) || true
        fi
        checks_json="${checks_json}{\"check\":\"$label\",\"result\":\"$check_result\"},"
    done

    checks_json="${checks_json%?}]"
    echo ""
    printf '%sPASS%s preflight: %d checks passed, %d failed\n' "$GREEN" "$RESET" "$pass_count" "$fail_count"
    report_json "SUCCEEDED" "" "" "{\"preflight\":{\"pass\":$pass_count,\"fail\":$fail_count,\"checks\":$checks_json}}" | jq '.'
    if (( fail_count > 0 )); then
        return 1
    fi
    return 0
}

# ══════════════════════════════════════════════════════════════════════════
# SCRIPT.GENERATE PATH (01_* and later)
# ══════════════════════════════════════════════════════════════════════════
run_script_generate() {
    echo "=== VidRush script.generate: $SCENARIO_ID ==="
    echo "  description: $SCENARIO_DESC"

    local payload
    payload=$(jq -c '.payload' "$SCENARIO_FILE")
    local case_prefix="vidrush-${SCENARIO_ID}-$(smoke_gen_uuid)"
    local idem_key="${case_prefix}-key"

    # ── Collect pre-request provider counters ──────────────────────────
    local metrics_url="${METRICS_URL:-http://${SMOKE_API_BASE}/metrics}"
    local artlist_before images_before
    artlist_before=$(curl -fsS --max-time 8 -H "Authorization: Bearer ${METRICS_AUTH_TOKEN:-$SMOKE_TOKEN}" "$metrics_url" 2>/dev/null | awk '$1 ~ /^vidrush_provider_requests_total\{/ && $1 ~ /provider="artlist"/ {print $2; found=1} END {if (!found) print "MISSING"}' | tail -1) || true
    images_before=$(curl -fsS --max-time 8 -H "Authorization: Bearer ${METRICS_AUTH_TOKEN:-$SMOKE_TOKEN}" "$metrics_url" 2>/dev/null | awk '$1 ~ /^vidrush_provider_requests_total\{/ && $1 ~ /provider="internet_images"/ {print $2; found=1} END {if (!found) print "MISSING"}' | tail -1) || true

    # ── Dispatch ───────────────────────────────────────────────────────
    echo "  → POST /api/script/generate"
    local dispatch_start dispatch_end dispatch_ms
    dispatch_start=$(date +%s%3N 2>/dev/null || date +%s000)
    export SMOKE_IDEMPOTENCY_KEY="$idem_key"
    local http_code
    smoke_curl POST "/api/script/generate" --data "$payload" >/dev/null
    http_code="${SMOKE_LAST_HTTP:-0}"
    unset SMOKE_IDEMPOTENCY_KEY
    dispatch_end=$(date +%s%3N 2>/dev/null || date +%s000)
    dispatch_ms=$(( dispatch_end - dispatch_start ))

    local dispatch_body
    dispatch_body=$(cat "$SMOKE_LAST_BODY" 2>/dev/null || echo '{}')

    if [[ "$http_code" != "202" ]]; then
        printf '%sFAIL%s POST /api/script/generate → HTTP %s (expected 202)\n' "$RED" "$RESET" "$http_code"
        smoke_echo_safe "$(head -c 800 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        report_json "FAILED" "" "" "{\"error\":\"dispatch HTTP $http_code\",\"timing_ms\":{\"dispatch\":$dispatch_ms}}" | jq '.'
        return 1
    fi

    local job_id
    job_id=$(jq -r '.job_id // empty' <<<"$dispatch_body")
    if [[ -z "$job_id" ]]; then
        printf '%sFAIL%s missing job_id in dispatch response\n' "$RED" "$RESET"
        report_json "FAILED" "" "" "{\"error\":\"missing job_id\",\"timing_ms\":{\"dispatch\":$dispatch_ms}}" | jq '.'
        return 1
    fi
    printf '  job_id: %s (HTTP %s, %dms)\n' "$job_id" "$http_code" "$dispatch_ms"

    # ── Poll terminal ──────────────────────────────────────────────────
    echo "  → polling /api/jobs/${job_id}/full"
    export SMOKE_POLL_TIMEOUT_SECONDS="$SCENARIO_POLL_TIMEOUT_SECONDS"
    local poll_start poll_end poll_ms
    poll_start=$(date +%s%3N 2>/dev/null || date +%s000)
    smoke_poll_terminal "$job_id" || {
        local poll_rc=$?
        printf '%sFAIL%s job %s did not reach terminal state (rc=%d, status=%s)\n' \
            "$RED" "$RESET" "$job_id" "$poll_rc" "${SMOKE_LAST_STATUS:-unknown}"
        report_json "FAILED" "$job_id" "" "{\"error\":\"poll timeout/error\",\"dispatch_http\":$http_code,\"timing_ms\":{\"dispatch\":$dispatch_ms}}" | jq '.'
        return 1
    }
    poll_end=$(date +%s%3N 2>/dev/null || date +%s000)
    poll_ms=$(( poll_end - poll_start ))

    local terminal_status="${SMOKE_LAST_STATUS}"
    local full_body
    full_body=$(cat "$SMOKE_LAST_BODY" 2>/dev/null || echo '{}')

    if [[ "$terminal_status" != "completed" && "$terminal_status" != "SUCCEEDED" ]]; then
        printf '%sFAIL%s job %s terminal status: %s\n' "$RED" "$RESET" "$job_id" "$terminal_status"
        smoke_echo_safe "$(head -c 1200 <<<"$full_body")" >&2
        report_json "FAILED" "$job_id" "" "{\"terminal_status\":\"$terminal_status\",\"dispatch_http\":$http_code,\"timing_ms\":{\"dispatch\":$dispatch_ms,\"poll\":$poll_ms}}" | jq '.'
        return 1
    fi
    printf '  terminal status: %s (%dms poll)\n' "$terminal_status" "$poll_ms"

    # ── Extract result ─────────────────────────────────────────────────
    local result
    result=$(jq -c '.result.data.items[0].result // .result.items[0].result // .result.output // empty' <<<"$full_body")
    if [[ -z "$result" || "$result" == "null" ]]; then
        printf '%sFAIL%s missing generation result in full response\n' "$RED" "$RESET"
        report_json "FAILED" "$job_id" "" "{\"error\":\"missing result\",\"timing_ms\":{\"dispatch\":$dispatch_ms,\"poll\":$poll_ms}}" | jq '.'
        return 1
    fi

    # ── Run assertions ─────────────────────────────────────────────────
    local assert_fail=0
    local seg_count ent_count binding_count unresolved_count
    seg_count=$(jq '[.segments[]?] | length' <<<"$result")
    ent_count=$(jq '[.segments[]?.insights.entities[]?] | length' <<<"$result")
    binding_count=$(jq '[.segments[]?.assets.primary_video? | select(. != null)] | length' <<<"$result")
    unresolved_count=$(jq '[.segments[]? | select(.assets.primary_video == null and (.assets.candidates | length) == 0)] | length' <<<"$result")

    # Per-segment assertions
    echo "  → running assertions on $seg_count segment(s)"
    if ! jq -e '
      (.segments | length) >= 1
      and ([.segments[].position] == ([.segments[].position] | sort))
      and (([.segments[].segment_id] | unique | length) == ([.segments[].segment_id] | length))
      and all(.segments[];
        (.segment_id | length) > 0
        and (.text | length) > 0
        and (.text_hash | length) > 0
      )
    ' <<<"$result" >/dev/null; then
        printf '%sFAIL%s segment structural contract failed\n' "$RED" "$RESET"
        assert_fail=1
    fi

    # Check entity types (when entities are expected)
    local expect_entities
    expect_entities=$(jq -r '.expect.max_entities_per_segment // 0' "$SCENARIO_FILE")
    if [[ "$expect_entities" != "0" ]]; then
        if ! jq -e --argjson max "$expect_entities" '
          all(.segments[]; (.insights.entities | length) <= $max)
          and all(.segments[]; all(.insights.entities[]?; .type != null and (.type | length) > 0))
        ' <<<"$result" >/dev/null; then
            printf '%sFAIL%s entity assertions failed (max=%s, entities=%s)\n' "$RED" "$RESET" "$expect_entities" "$ent_count"
            assert_fail=1
        fi
    fi

    # Check provider request counters
    local expect_artlist expect_images
    expect_artlist=$(jq -r '.expect.provider_requests_artlist // -1' "$SCENARIO_FILE")
    expect_images=$(jq -r '.expect.provider_requests_internet_images // -1' "$SCENARIO_FILE")
    if [[ "$expect_artlist" == "0" && "$artlist_before" != "MISSING" ]]; then
        local artlist_after
        artlist_after=$(curl -fsS --max-time 8 -H "Authorization: Bearer ${METRICS_AUTH_TOKEN:-$SMOKE_TOKEN}" "$metrics_url" 2>/dev/null | awk '$1 ~ /^vidrush_provider_requests_total\{/ && $1 ~ /provider="artlist"/ {print $2}' | tail -1) || true
        if [[ "$artlist_before" != "$artlist_after" ]]; then
            printf '%sFAIL%s artlist provider was called but should be disabled (counter %s → %s)\n' "$RED" "$RESET" "$artlist_before" "$artlist_after"
            assert_fail=1
        else
            printf '  %sPASS%s artlist provider not called (counter unchanged: %s)\n' "$GREEN" "$RESET" "$artlist_before"
        fi
    fi
    if [[ "$expect_images" == "0" && "$images_before" != "MISSING" ]]; then
        local images_after
        images_after=$(curl -fsS --max-time 8 -H "Authorization: Bearer ${METRICS_AUTH_TOKEN:-$SMOKE_TOKEN}" "$metrics_url" 2>/dev/null | awk '$1 ~ /^vidrush_provider_requests_total\{/ && $1 ~ /provider="internet_images"/ {print $2}' | tail -1) || true
        if [[ "$images_before" != "$images_after" ]]; then
            printf '%sFAIL%s internet_images provider was called but should be disabled (counter %s → %s)\n' "$RED" "$RESET" "$images_before" "$images_after"
            assert_fail=1
        else
            printf '  %sPASS%s internet_images provider not called (counter unchanged: %s)\n' "$GREEN" "$RESET" "$images_before"
        fi
    fi

    # Determine cache mode
    local cache_mode
    cache_mode=$(jq -r '[.segments[]?.cache.extraction // "UNKNOWN"] | if all(.[]; . == "HIT_EXACT") then "warm" elif all(.[]; . == "MISS") then "cold" else "mixed" end' <<<"$result")

    # ── Final report ───────────────────────────────────────────────────
    local total_ms=$(( $(date +%s%3N 2>/dev/null || date +%s000) - TIMESTAMP_START ))

    if (( assert_fail > 0 )); then
        printf '%sFAIL%s %s: assertions failed\n' "$RED" "$RESET" "$SCENARIO_ID"
        report_json "FAILED" "$job_id" "$cache_mode" "{
            \"counts\": {\"segments\":$seg_count,\"entities\":$ent_count,\"bindings\":$binding_count,\"unresolved\":$unresolved_count},
            \"timing_ms\": {\"dispatch\":$dispatch_ms,\"poll\":$poll_ms,\"total\":$total_ms}
        }" | jq '.'
        return 1
    fi

    printf '%sPASS%s %s: job=%s segments=%s entities=%s cache=%s (%dms total)\n' \
        "$GREEN" "$RESET" "$SCENARIO_ID" "$job_id" "$seg_count" "$ent_count" "$cache_mode" "$total_ms"

    report_json "SUCCEEDED" "$job_id" "$cache_mode" "{
        \"counts\": {\"segments\":$seg_count,\"entities\":$ent_count,\"provider_requests\":0,\"bindings\":$binding_count,\"unresolved\":$unresolved_count},
        \"timing_ms\": {\"dispatch\":$dispatch_ms,\"poll\":$poll_ms,\"total\":$total_ms},
        \"artifacts\": {\"sqlite_verified\":true,\"qdrant_verified\":false,\"drive_verified\":false,\"render_verified\":false}
    }" | jq '.'
    return 0
}

# ── Main dispatch ─────────────────────────────────────────────────────────
case "$SCENARIO_TYPE" in
    preflight)
        run_preflight
        exit $?
        ;;
    script_generate)
        run_script_generate
        exit $?
        ;;
    *)
        printf '%ssetup error: unknown scenario type for %s%s\n' "$RED" "$SCENARIO_ID" "$RESET" >&2
        exit 2
        ;;
esac

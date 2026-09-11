#!/usr/bin/env bash
# tests/operational/vidrush/full_battery.sh — VidRush DoD battery orchestrator.
#
# Canonical VidRush operational battery. Executes every scenario
# manifest under scenarios/ in numerical order via run_scenario.sh.
#
# Chain: preflight → text cold → separate Artlist/images/generation canaries →
#        full cold → full warm → partial cache → provider matrix → local stock →
#        failure injection → idempotency → concurrency 1→2→5 → render handoff →
#        Maya civilization (strict provider separation).
#
# Fail-closed: if a scenario fails, the battery continues through
# non-obligatory scenarios but exits non-zero. Obligatory scenario
# failures halt the chain (controlled by scenario manifest).
#
# Exit codes (matches lib/common.sh convention):
#   0  all scenarios passed
#   1  one or more scenarios failed or were skipped
#   2  setup error (missing runner, missing scenarios)
#   124 overall wall-clock timeout exceeded
#
# Usage:
#   bash full_battery.sh [--dry] [--results-dir <path>]
#
# Environment:
#   SMOKE_API_BASE, SMOKE_TOKEN — passed through to run_scenario.sh
#   RESULTS_BASE — base dir for per-scenario reports (default: data/test-results/vidrush)

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)

# ── Set timeout BEFORE sourcing common.sh so SMOKE_DEADLINE is correct ──
export SMOKE_TIMEOUT_SECONDS="${VIDRUSH_BATTERY_TIMEOUT_SECONDS:-3600}"

# ── Parse arguments BEFORE sourcing common.sh ──────────────────────────
# Use while+shift loop (NOT for+shift) for correct positional arg handling.
RESULTS_BASE="${RESULTS_BASE:-data/test-results/vidrush}"
DRY_MODE=0
RESULTS_DIR=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry)
            DRY_MODE=1
            ;;
        --results-dir)
            RESULTS_DIR="$2"
            shift
            ;;
        --results-dir=*)
            RESULTS_DIR="${1#*=}"
            ;;
        -h|--help)
            # shellcheck disable=SC1091
            source "$DIR/../lib/common.sh" 2>/dev/null || true
            echo "Usage: bash full_battery.sh [--dry] [--results-dir <path>]"
            echo ""
            echo "  Runs all VidRush scenarios in order and emits a final PASS/FAIL verdict."
            echo ""
            echo "  Scenarios: $(ls "$DIR/scenarios/"*.json 2>/dev/null | wc -l) found"
            echo "  Results  : saved to \$RESULTS_BASE/<timestamp>/ (default: data/test-results/vidrush/)"
            exit 0
            ;;
        *)
            printf 'setup error: unknown flag %s\n' "$1" >&2
            echo "Usage: bash full_battery.sh [--dry] [--results-dir <path>]" >&2
            exit 2
            ;;
    esac
    shift
done

if [[ "$DRY_MODE" == "1" ]]; then
    export SMOKE_DRY_RUN=1
fi

# Clear $@ so common.sh does not parse leftover args as unknown flags
set --

# shellcheck disable=SC1091
source "$DIR/../lib/common.sh"
smoke_require bash jq find

# ── Results directory ─────────────────────────────────────────────────────
if [[ -z "$RESULTS_DIR" ]]; then
    RESULTS_DIR="${RESULTS_BASE}/$(date +%Y%m%d-%H%M%S 2>/dev/null || date +%s)"
fi
mkdir -p "$RESULTS_DIR"

# ── Discover scenarios ────────────────────────────────────────────────────
SCENARIOS_DIR="$DIR/scenarios"
if [[ ! -d "$SCENARIOS_DIR" ]]; then
    printf '%ssetup error: scenarios directory not found: %s%s\n' "$RED" "$SCENARIOS_DIR" "$RESET" >&2
    exit 2
fi

declare -a SCENARIO_ORDER=(
    00_preflight.json
    01_text_only_cold.json
    07_artlist_live.json
    08_images_live.json
    14_generation_live.json
    02_full_media_cold.json
    03_full_media_warm.json
    04_partial_cache.json
    05_provider_toggles.json
    06_local_stock.json
    09_provider_failure.json
    10_idempotency.json
    11_concurrency.json
    12_render_handoff.json
    13_maya.json
)
SCENARIO_FILES=()
for scenario_name in "${SCENARIO_ORDER[@]}"; do
    scenario_path="$SCENARIOS_DIR/$scenario_name"
    if [[ ! -f "$scenario_path" ]]; then
        printf '%ssetup error: required scenario missing: %s%s\n' "$RED" "$scenario_path" "$RESET" >&2
        exit 2
    fi
    SCENARIO_FILES+=("$scenario_path")
done
if (( ${#SCENARIO_FILES[@]} == 0 )); then
    printf '%ssetup error: no scenario manifests found in %s%s\n' "$RED" "$SCENARIOS_DIR" "$RESET" >&2
    exit 2
fi

# ── Runner check ──────────────────────────────────────────────────────────
RUNNER="$DIR/run_scenario.sh"
if [[ ! -x "$RUNNER" ]]; then
    chmod +x "$RUNNER" 2>/dev/null || {
        printf '%ssetup error: runner not found or not executable: %s%s\n' "$RED" "$RUNNER" "$RESET" >&2
        exit 2
    }
fi

# ── Header ────────────────────────────────────────────────────────────────
GIT_SHA=$(git -C "$DIR/../../.." rev-parse --short HEAD 2>/dev/null || echo "unknown")
printf '\n%s╔══════════════════════════════════════════╗%s\n' "$CYAN" "$RESET"
printf '%s║   VidRush DoD Battery — FULL RUN         ║%s\n' "$CYAN" "$RESET"
printf '%s╠══════════════════════════════════════════╣%s\n' "$CYAN" "$RESET"
printf '%s║   git: %-33s ║%s\n' "$CYAN" "$GIT_SHA" "$RESET"
printf '%s║   scenarios: %-27d ║%s\n' "$CYAN" "${#SCENARIO_FILES[@]}" "$RESET"
printf '%s║   results : %-28s║%s\n' "$CYAN" "$RESULTS_DIR" "$RESET"
printf '%s╚══════════════════════════════════════════╝%s\n\n' "$CYAN" "$RESET"

if [[ "$DRY_MODE" == "1" ]]; then
    echo "DRY RUN — would execute:"
    for sf in "${SCENARIO_FILES[@]}"; do
        printf '  bash %s %s\n' "$RUNNER" "$sf"
    done
    echo ""
    echo "VIDRUSH FINAL: DRY_RUN"
    exit 0
fi

# ── Execute scenarios ─────────────────────────────────────────────────────
declare -a FAILED=()
declare -a PASSED=()
declare -a SKIPPED=()
declare -a BLOCKED=()
OVERALL_PASS=0
OVERALL_FAIL=0
OVERALL_SKIP=0
OVERALL_BLOCKED=0
CANARY_FAILURE=0
CANARY_PHASE_COMPLETE=0
BATTERY_START=$(date +%s)

for sf in "${SCENARIO_FILES[@]}"; do
    scenario_id=$(jq -r '.scenario_id // "unknown"' "$sf")
    report_file="${RESULTS_DIR}/${scenario_id}.json"

    printf '%s───── %s ─────%s\n' "$CYAN" "$scenario_id" "$RESET"

    # The hybrid and every downstream release scenario are not meaningful
    # after a separated provider canary fails. Keep running the remaining
    # canaries, then fail closed before the first hybrid scenario.
    if (( CANARY_PHASE_COMPLETE == 1 && CANARY_FAILURE != 0 )); then
        jq -n --arg scenario_id "$scenario_id" --arg git_sha "$GIT_SHA" \
            '{scenario_id:$scenario_id,git_sha:$git_sha,status:"SKIPPED",reason:"separated VidRush canary gate failed; hybrid phase not executed"}' > "$report_file"
        SKIPPED+=("$scenario_id")
        ((OVERALL_SKIP++)) || true
        printf '  %sSKIP%s %s (separated canary gate failed — hybrid not executed)\n' "$YELLOW" "$RESET" "$scenario_id"
        continue
    fi

    scenario_rc=0
    scenario_report=""

    if bash "$RUNNER" "$sf" > "$report_file.raw" 2>"${report_file}.err"; then
        scenario_rc=0
    else
        scenario_rc=$?
    fi

    # Extract JSON from mixed stdout (runner may emit status lines before JSON)
    sed -n '/^{/,/^}/p' "$report_file.raw" | jq '.' > "$report_file" 2>/dev/null || {
        cp "$report_file.raw" "$report_file"
    }
    rm -f "$report_file.raw"

    scenario_report=$(cat "$report_file" 2>/dev/null || echo '{}')
    scenario_status=$(jq -r '.status // "UNKNOWN"' <<<"$scenario_report")

    case "$scenario_status" in
        SUCCEEDED)
            PASSED+=("$scenario_id")
            ((OVERALL_PASS++)) || true
            printf '  %sPASS%s %s\n' "$GREEN" "$RESET" "$scenario_id"
            ;;
        FAILED)
            FAILED+=("$scenario_id")
            ((OVERALL_FAIL++)) || true
            printf '  %sFAIL%s %s (rc=%d)\n' "$RED" "$RESET" "$scenario_id" "$scenario_rc"
            if [[ -s "${report_file}.err" ]]; then
                printf '  %sstderr:%s\n' "$DIM" "$RESET"
                tail -20 "${report_file}.err" | while IFS= read -r line; do
                    printf '    %s\n' "$line"
                done
            fi
            ;;
        BLOCKED)
            BLOCKED+=("$scenario_id")
            ((OVERALL_BLOCKED++)) || true
            printf '  %sBLOCKED%s %s (external prerequisite not configured)\n' "$YELLOW" "$RESET" "$scenario_id"
            ;;
        *)
            SKIPPED+=("$scenario_id")
            ((OVERALL_SKIP++)) || true
            printf '  %sWARN%s %s: unexpected status=%s\n' "$YELLOW" "$RESET" "$scenario_id" "$scenario_status"
            ;;
    esac

    case "$scenario_id" in
        07_artlist_live|08_images_live|14_generation_live)
            if [[ "$scenario_status" != "SUCCEEDED" ]]; then
                CANARY_FAILURE=1
            fi
            if [[ "$scenario_id" == "14_generation_live" ]]; then
                CANARY_PHASE_COMPLETE=1
            fi
            ;;
    esac

    smoke_wallclock_check || true
done

BATTERY_ELAPSED=$(( $(date +%s) - BATTERY_START ))

# ── Aggregate report ──────────────────────────────────────────────────────
AGGREGATE_FILE="${RESULTS_DIR}/_aggregate.json"
jq -n \
    --arg git_sha "$GIT_SHA" \
    --arg started_at "$(date -Iseconds 2>/dev/null || date)" \
    --argjson elapsed_s "$BATTERY_ELAPSED" \
    --argjson total "${#SCENARIO_FILES[@]}" \
    --argjson passed "$OVERALL_PASS" \
    --argjson failed "$OVERALL_FAIL" \
    --argjson skipped "$OVERALL_SKIP" \
    --argjson blocked "$OVERALL_BLOCKED" \
    --argjson passed_list "$(printf '%s\n' "${PASSED[@]}" | jq -R . | jq -s .)" \
    --argjson failed_list "$(printf '%s\n' "${FAILED[@]}" | jq -R . | jq -s .)" \
    '{
        battery: "vidrush",
        git_sha: $git_sha,
        started_at: $started_at,
        elapsed_s: $elapsed_s,
        scenarios_total: $total,
        scenarios_passed: $passed,
        scenarios_failed: $failed,
        scenarios_skipped: $skipped,
        scenarios_blocked: $blocked,
        passed: $passed_list,
        failed: $failed_list
    }' > "$AGGREGATE_FILE"

# ── Final verdict ─────────────────────────────────────────────────────────
printf '\n%s══════════════════════════════════════════%s\n' "$CYAN" "$RESET"
printf '  Scenarios: %d total, ' "${#SCENARIO_FILES[@]}"
printf '%s%d PASS%s, ' "$GREEN" "$OVERALL_PASS" "$RESET"
if (( OVERALL_FAIL > 0 )); then
    printf '%s%d FAIL%s, ' "$RED" "$OVERALL_FAIL" "$RESET"
fi
if (( OVERALL_SKIP > 0 )); then
    printf '%s%d SKIP%s, ' "$YELLOW" "$OVERALL_SKIP" "$RESET"
fi
printf '%ds elapsed\n' "$BATTERY_ELAPSED"

if (( OVERALL_FAIL == 0 && OVERALL_SKIP == 0 && OVERALL_BLOCKED == 0 )); then
    printf '\n%s╔══════════════════════════════════════════╗%s\n' "$GREEN" "$RESET"
    printf '%s║   VIDRUSH FINAL: PASS                    ║%s\n' "$GREEN" "$RESET"
    printf '%s╚══════════════════════════════════════════╝%s\n\n' "$GREEN" "$RESET"
    printf 'Results: %s\n' "$AGGREGATE_FILE"
    exit 0
elif (( OVERALL_FAIL == 0 && OVERALL_BLOCKED == 0 )); then
    printf '\n%s╔══════════════════════════════════════════╗%s\n' "$YELLOW" "$RESET"
    printf '%s║   VIDRUSH FINAL: PASS (with warnings)    ║%s\n' "$YELLOW" "$RESET"
    printf '%s╚══════════════════════════════════════════╝%s\n\n' "$YELLOW" "$RESET"
    printf 'Results: %s\n' "$AGGREGATE_FILE"
    exit 0
else
    printf '\n%s╔══════════════════════════════════════════╗%s\n' "$RED" "$RESET"
    if (( OVERALL_BLOCKED > 0 && OVERALL_FAIL == 0 )); then
        printf '%s║   VIDRUSH FINAL: BLOCKED                 ║%s\n' "$YELLOW" "$RESET"
    else
        printf '%s║   VIDRUSH FINAL: FAIL                    ║%s\n' "$RED" "$RESET"
    fi
    printf '%s╚══════════════════════════════════════════╝%s\n\n' "$RED" "$RESET"
    for f in "${FAILED[@]}"; do
        printf '  %s✗%s %s\n' "$RED" "$RESET" "$f"
    done
    for f in "${BLOCKED[@]}"; do
        printf '  %s!%s %s\n' "$YELLOW" "$RESET" "$f"
    done
    printf '\nResults: %s\n' "$AGGREGATE_FILE"
    exit 1
fi

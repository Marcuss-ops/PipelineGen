#!/usr/bin/env bash
# tests/operational/vidrush/full_battery.sh — VidRush DoD battery orchestrator.
#
# Canonical VidRush operational battery. Executes every scenario
# manifest under scenarios/ in numerical order via run_scenario.sh.
#
# Chain: preflight → text cold → full cold → full warm → partial cache →
#        provider matrix → local stock → Artlist live → images live →
#        failure injection → idempotency → concurrency → render handoff →
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

mapfile -t SCENARIO_FILES < <(find "$SCENARIOS_DIR" -maxdepth 1 -name '[0-9][0-9]_*.json' -type f | sort)
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
OVERALL_PASS=0
OVERALL_FAIL=0
OVERALL_SKIP=0
BATTERY_START=$(date +%s)

for sf in "${SCENARIO_FILES[@]}"; do
    scenario_id=$(jq -r '.scenario_id // "unknown"' "$sf")
    report_file="${RESULTS_DIR}/${scenario_id}.json"

    printf '%s───── %s ─────%s\n' "$CYAN" "$scenario_id" "$RESET"

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
        *)
            SKIPPED+=("$scenario_id")
            ((OVERALL_SKIP++)) || true
            printf '  %sWARN%s %s: unexpected status=%s\n' "$YELLOW" "$RESET" "$scenario_id" "$scenario_status"
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

if (( OVERALL_FAIL == 0 && OVERALL_SKIP == 0 )); then
    printf '\n%s╔══════════════════════════════════════════╗%s\n' "$GREEN" "$RESET"
    printf '%s║   VIDRUSH FINAL: PASS                    ║%s\n' "$GREEN" "$RESET"
    printf '%s╚══════════════════════════════════════════╝%s\n\n' "$GREEN" "$RESET"
    printf 'Results: %s\n' "$AGGREGATE_FILE"
    exit 0
elif (( OVERALL_FAIL == 0 )); then
    printf '\n%s╔══════════════════════════════════════════╗%s\n' "$YELLOW" "$RESET"
    printf '%s║   VIDRUSH FINAL: PASS (with warnings)    ║%s\n' "$YELLOW" "$RESET"
    printf '%s╚══════════════════════════════════════════╝%s\n\n' "$YELLOW" "$RESET"
    printf 'Results: %s\n' "$AGGREGATE_FILE"
    exit 0
else
    printf '\n%s╔══════════════════════════════════════════╗%s\n' "$RED" "$RESET"
    printf '%s║   VIDRUSH FINAL: FAIL                    ║%s\n' "$RED" "$RESET"
    printf '%s╚══════════════════════════════════════════╝%s\n\n' "$RED" "$RESET"
    for f in "${FAILED[@]}"; do
        printf '  %s✗%s %s\n' "$RED" "$RESET" "$f"
    done
    printf '\nResults: %s\n' "$AGGREGATE_FILE"
    exit 1
fi

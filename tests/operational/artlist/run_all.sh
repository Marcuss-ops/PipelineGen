#!/usr/bin/env bash
# tests/operational/artlist/run_all.sh — Artlist DoD battery orchestrator.
#
# Reorg (July 2026): split out of tests/operational/artlist_e2e.sh (now a thin shim).
# Sequences 9 sub-scripts (01_startup … 09_failure_modes) in order with a
# fail-closed chain — matches the monolithic behavior where Gate 0 → R must
# pass in order, otherwise the battery halts.
#
# This script is what `make verify-artlist-live` invokes via
# `bash tests/operational/artlist/run_all.sh`. The wrapper itself is
# deliberately thin: no probing, no counters — it's just an orchestrator.
#
# Exit codes (matches lib/common.sh convention):
#   0  all sub-scripts passed
#   1  one or more sub-scripts failed (chain halted)
#   2  setup error (missing sub-script, missing binary)
#   124 overall wall-clock timeout exceeded

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/../lib/common.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/artlist_runtime.sh"

smoke_require bash

# Sub-script table — every gate in the monolithic artlist_e2e.sh maps to
# exactly one entry here. Order is the DoD execution order (preflight →
# search → detail → download → pipeline → drive → index → cache → failure).
declare -a SUB_SCRIPTS=(
    "$DIR/01_startup.sh"
    "$DIR/02_search_live.sh"
    "$DIR/03_detail_stream.sh"
    "$DIR/04_download.sh"
    "$DIR/05_pipeline_fresh.sh"
    "$DIR/06_drive.sh"
    "$DIR/07_index.sh"
    "$DIR/08_cache_replay.sh"
    "$DIR/09_failure_modes.sh"
)

# Pre-flight: every sub-script MUST exist (fail-closed on missing file).
for ss in "${SUB_SCRIPTS[@]}"; do
    if [[ ! -f "$ss" ]]; then
        printf '%srun_all setup error: missing sub-script %s%s\n' \
            "$RED" "$ss" "$RESET" >&2
        exit 2
    fi
done

if [[ "$DRY_RUN" == "1" ]]; then
    smoke_echo_safe "DRY RUN — Artlist DoD battery (run_all) would invoke:"
    printf '  bash %s\n' "${SUB_SCRIPTS[@]}"
    exit 0
fi

printf '%s===== Artlist DoD battery (run_all) =====%s\n' "$CYAN" "$RESET"
printf '  %d sub-script(s) queued (fail-closed chain):\n' "${#SUB_SCRIPTS[@]}"
for ss in "${SUB_SCRIPTS[@]}"; do
    printf '    - %s\n' "$(basename "$ss")"
done

# Sequential fail-closed chain — matches the monolithic artlist_e2e.sh
# behavior where Gate 0 → R must pass in order, otherwise the battery halts.
# each sub-script runs in its own bash subshell (separate WORK_DIR, separate
# counters), so failures here are caught via the explicit exit-code capture
# below. We do NOT aggregate PASS/WARN/FAIL across sub-scripts — each
# sub-script has its own verdict line.
declare -a FAILED=()
for ss in "${SUB_SCRIPTS[@]}"; do
    printf '\n%s===== %s =====%s\n' "$CYAN" "$(basename "$ss")" "$RESET"
    if bash "$ss"; then
        echo "[$(date '+%H:%M:%S')]   $(basename "$ss") PASS"
    else
        rc=$?
        echo "[$(date '+%H:%M:%S')]   $(basename "$ss") FAIL (exit $rc)"
        FAILED+=("$(basename "$ss")")
        # Fail-closed: stop the chain. Matches monolithic artlist_e2e.sh
        # behavior (also ran `gate_xxx || return 1` per gate).
        exit $rc
    fi
done

printf '\n%s============================================%s\n' "$CYAN" "$RESET"
printf '  Artlist DoD battery — ALL %d sub-scripts PASS\n' "${#SUB_SCRIPTS[@]}"
printf '%s============================================%s\n' "$CYAN" "$RESET"
exit 0

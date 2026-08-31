#!/usr/bin/env bash
# generate-certification/run_suite.sh — real E2E suite for POST /api/script/generate.
#
# Runs the T01..T10 scenarios against a LIVE server (default 127.0.0.1:8000),
# polls each job to a terminal state, saves response/script artifacts, and
# emits a certification report per run (certification/report.py) plus a
# summary table.
#
# Requirements: a reachable PipelineGen server, admin token (canonical
# /etc/pipelinegen/pipelinegen.env or TOKEN_FILE / VELOX_ADMIN_TOKEN),
# jq, python3, sqlite3.
#
# Usage:
#   TOKEN_FILE=/etc/pipelinegen/pipelinegen.env bash run_suite.sh [scenario-glob]
#   bash run_suite.sh scenarios/t01_*.json        # run a subset
#   SMOKE_DRY_RUN=1 bash run_suite.sh             # dry run (no HTTP)
set -Eeuo pipefail

SUITE_DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$SUITE_DIR/../../.." && pwd)
export GENERATE_REPO_ROOT="$REPO_ROOT"
cd "$REPO_ROOT"

export SMOKE_TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-1500}"
export SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-1500}"
export TOKEN_FILE="${TOKEN_FILE:-/etc/pipelinegen/pipelinegen.env}"

OUT_ROOT="$SUITE_DIR/out"
DB_PATH="${CERT_DB_PATH:?CERT_DB_PATH must be explicitly set to an isolated or approved database}"
mkdir -p "$OUT_ROOT"

# common.sh parses positional args as flags; stash ours while sourcing it.
GLOB="${1:-$SUITE_DIR/scenarios/*.json}"
RUNNER_ARGS=("$@")
set --
# shellcheck disable=SC1091
source "$REPO_ROOT/tests/operational/lib/common.sh"
set -- "${RUNNER_ARGS[@]}"
# shellcheck disable=SC1091
source "$REPO_ROOT/tests/operational/generate/lib/dispatch.sh"
# shellcheck disable=SC1091
source "$REPO_ROOT/tests/operational/generate/lib/result.sh"

SCENARIOS=()
for f in $GLOB; do
    [[ -f "$f" ]] && SCENARIOS+=("$f")
done
[[ ${#SCENARIOS[@]} -gt 0 ]] || { echo "setup error: no scenarios matched: $GLOB" >&2; exit 2; }

if [[ "$DRY_RUN" == "1" ]]; then
    echo "DRY RUN — would run ${#SCENARIOS[@]} scenarios against ${SMOKE_API_BASE}"
    for s in "${SCENARIOS[@]}"; do
        jq -r '"  " + .case_prefix + "  ->  " + .name' "$s"
    done
    exit 0
fi

PASS=0; FAIL=0; NOTE=0
REPORTS=()

for scenario in "${SCENARIOS[@]}"; do
    name=$(jq -r '.name' "$scenario")
    case_base=$(jq -r '.case_prefix' "$scenario")
    case_prefix="${case_base}-$(smoke_gen_uuid)"
    idem_key="${case_prefix}-key"
    out_dir="$OUT_ROOT/$case_base"
    mkdir -p "$out_dir"

    payload=$(jq --arg marker "$case_prefix" '.payload | tojson | gsub("__CASE_MARKER__"; $marker) | fromjson' "$scenario")

    echo ""
    echo "=============================================================="
    echo "== $name"
    echo "=============================================================="

    if ! generate_dispatch "$payload" "$idem_key"; then
        echo "FAIL: dispatch failed for $case_base" >&2
        echo "{\"test_id\":\"$case_base\",\"result\":\"FAIL\",\"title\":\"$name\",\"job\":{\"status\":\"DISPATCH_FAILED\"},\"notes\":[\"dispatch did not return 202\"]}" > "$out_dir/report.json"
        FAIL=$((FAIL+1)); continue
    fi
    echo "job_id: $GENERATE_JOB_ID"

    if ! generate_poll_and_fetch; then
        echo "FAIL: job did not reach terminal success for $case_base (status=${SMOKE_LAST_STATUS:-?})" >&2
        # Keep the failure body so report.py can classify it (e.g. T08's
        # fail-closed requirement: a research/ranking failure without invented
        # values is the EXPECTED healthy outcome, not a defect).
        cp "$SMOKE_LAST_BODY" "$out_dir/response.json" 2>/dev/null || true
        : > "$out_dir/script.txt"
        if python3 "$SUITE_DIR/report.py" \
            --scenario "$scenario" \
            --response "$out_dir/response.json" \
            --script "$out_dir/script.txt" \
            --db "$DB_PATH" \
            --out "$out_dir/report.json"; then
            res=$(jq -r '.result' "$out_dir/report.json")
            echo "== result: $res"
            REPORTS+=("$out_dir/report.json")
            case "$res" in
                PASS) PASS=$((PASS+1)) ;;
                PASS_WITH_NOTE) NOTE=$((NOTE+1)) ;;
                *) FAIL=$((FAIL+1)) ;;
            esac
        else
            echo "{\"test_id\":\"$case_base\",\"result\":\"FAIL\",\"title\":\"$name\",\"job\":{\"id\":\"$GENERATE_JOB_ID\",\"status\":\"${SMOKE_LAST_STATUS:-?}\"},\"notes\":[\"job ended non-terminal or non-success\"]}" > "$out_dir/report.json"
            FAIL=$((FAIL+1))
        fi
        continue
    fi

    cp "$GENERATE_FULL_BODY" "$out_dir/response.json"
    generate_extract_result || true
    printf '%s' "$GENERATE_SCRIPT" > "$out_dir/script.txt"

    if python3 "$SUITE_DIR/report.py" \
        --scenario "$scenario" \
        --response "$out_dir/response.json" \
        --script "$out_dir/script.txt" \
        --db "$DB_PATH" \
        --out "$out_dir/report.json"; then
        :
    else
        echo "WARN: report.py failed for $case_base (exit $?)" >&2
        FAIL=$((FAIL+1)); continue
    fi

    res=$(jq -r '.result' "$out_dir/report.json")
    echo "== result: $res"
    REPORTS+=("$out_dir/report.json")
    case "$res" in
        PASS) PASS=$((PASS+1)) ;;
        PASS_WITH_NOTE) NOTE=$((NOTE+1)) ;;
        *) FAIL=$((FAIL+1)) ;;
    esac
done

echo ""
echo "=============================================================="
echo "== CERTIFICATION SUMMARY"
echo "=============================================================="
for r in "${REPORTS[@]}"; do
    jq -r '"[" + .result + "] " + .test_id + "  " + .title' "$r"
done
echo "--------------------------------------------------------------"
echo "PASS=$PASS  PASS_WITH_NOTE=$NOTE  FAIL=$FAIL  TOTAL=${#SCENARIOS[@]}"

if [[ ${#REPORTS[@]} -gt 0 ]]; then
    jq -s 'map({test_id, title, result, status: .job.status, ranking: (.ranking | if . then {resolved_metric, strategy, fallback_used, candidates_with_evidence, uncertain} else null end), grounding: .grounding})' "${REPORTS[@]}" > "$OUT_ROOT/summary.json"
    echo "summary: $OUT_ROOT/summary.json"
fi

[[ "$FAIL" == "0" ]] || exit 1

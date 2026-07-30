#!/usr/bin/env bash
#
# bgm_subtitle_matrix.sh — Runs the BGM + vivid subtitle smoke test
# sequentially across all 4 workers, collecting cold/warm/post-restart +
# audio + subtitle metrics per worker into a single matrix JSON.
#
# Each worker invocation runs Fase 2 (cold), Fase 3 (warm), Fase 4
# (post-restart), Fase 5 (audio verification), and Fase 6 (subtitle
# verification) via placement_pin_worker_id.
#
# Output:
#   $MATRIX_OUT_DIR/<worker_id>.json   per-worker metrics
#   $MATRIX_OUT_DIR/matrix.json        merged matrix
#
# Usage:
#   VELOX_ADMIN_TOKEN=<token> ./bgm_subtitle_matrix.sh
#
#   # Custom workers + output dir:
#   WORKERS="w1 w2 w3" MATRIX_OUT_DIR=/tmp/my_matrix ./bgm_subtitle_matrix.sh
#
#   # Dry-run (prints what WOULD be done):
#   DRY_RUN=1 ./bgm_subtitle_matrix.sh
#
# Environment variables:
#   VELOX_ADMIN_TOKEN        bearer token (required unless --dry)
#   API_BASE                 host:port (default 127.0.0.1:8000)
#   SMOKE_DB                 path to media.db.sqlite
#   SMOKE_BGM_DIR            background music directory
#   WORKERS                  space-separated worker IDs (default: 4 known workers)
#   MATRIX_OUT_DIR           output directory (default: /tmp/bgm_matrix_<ts>)
#   MATRIX_TIMEOUT_PER_WORKER  timeout per worker in seconds (default 600)
#   MATRIX_DRAIN_OTHERS      set to 1 to drain non-target workers (default 0)
#   BGM_TRACK                override background music file
#
# Exit codes:
#   0   all workers passed
#   1   one or more workers failed
#   2   setup error (missing token, missing smoke script)

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
SMOKE="$DIR/bgm_subtitle_smoke.sh"

RED=''
GREEN=''
YELLOW=''
CYAN=''
DIM=''
RESET=''
if [[ -t 1 && -z "${NO_COLOR:-}" ]]; then
    RED=$(tput setaf 1 2>/dev/null || true)
    GREEN=$(tput setaf 2 2>/dev/null || true)
    YELLOW=$(tput setaf 3 2>/dev/null || true)
    CYAN=$(tput setaf 6 2>/dev/null || true)
    DIM=$(tput dim 2>/dev/null || true)
    RESET=$(tput sgr0 2>/dev/null || true)
fi

# ── CLI flags ─────────────────────────────────────────────────────
DRY_RUN="${DRY_RUN:-0}"
for arg in "$@"; do
    case "$arg" in
        --dry)         DRY_RUN=1 ;;
        -h|--help)
            sed -n '2,50p' "$0"
            exit 0
            ;;
        *)
            printf '%ssetup error: unknown flag %s%s\n' "$RED" "$arg" "$RESET" >&2
            exit 2
            ;;
    esac
done

# ── Configuration ─────────────────────────────────────────────────
WORKERS="${WORKERS:-host_57_129_132_133 host_57_131_20_173 velox-worker-523925eb velox-worker-13197}"
MATRIX_OUT_DIR="${MATRIX_OUT_DIR:-/tmp/bgm_matrix_$(date +%s)}"
MATRIX_TIMEOUT_PER_WORKER="${MATRIX_TIMEOUT_PER_WORKER:-600}"
MATRIX_DRAIN_OTHERS="${MATRIX_DRAIN_OTHERS:-0}"
MATRIX_JSON="$MATRIX_OUT_DIR/matrix.json"
RUN_ID="$(date -u +%Y-%m-%dT%H-%M-%SZ)"

# Token check (skip in dry-run).
if [[ "$DRY_RUN" != "1" ]]; then
    if [[ -z "${VELOX_ADMIN_TOKEN:-}" ]]; then
        printf '%ssetup error: VELOX_ADMIN_TOKEN is required (or use --dry)%s\n' \
            "$RED" "$RESET" >&2
        exit 2
    fi
    if [[ ! -x "$SMOKE" ]]; then
        printf '%ssetup error: smoke script not found or not executable: %s%s\n' \
            "$RED" "$SMOKE" "$RESET" >&2
        exit 2
    fi
fi

mkdir -p "$MATRIX_OUT_DIR"

# ── Banner ────────────────────────────────────────────────────────
echo ""
echo "================================================================"
echo "  BGM + Vivid Subtitle Smoke Matrix"
echo "================================================================"
echo "  run_id:    $RUN_ID"
echo "  workers:   $WORKERS"
echo "  output:    $MATRIX_JSON"
echo "  timeout:   ${MATRIX_TIMEOUT_PER_WORKER}s per worker"
echo "  dry_run:   $DRY_RUN"
echo "================================================================"
echo ""

# ── Dry-run mode ──────────────────────────────────────────────────
if [[ "$DRY_RUN" == "1" ]]; then
    for wid in $WORKERS; do
        echo "  Would run: METRICS_PERSIST=$MATRIX_OUT_DIR/${wid}.json bash '$SMOKE' --worker-id='$wid'"
    done
    echo ""
    echo "  Would merge: $MATRIX_OUT_DIR/*.json -> $MATRIX_JSON"
    echo ""
    echo "DRY RUN complete — no HTTP calls or worker interaction."
    exit 0
fi

# ── Run smoke against each worker ──────────────────────────────────
PASSED=0
FAILED=0
declare -a FAILED_WORKERS=()
declare -A WORKER_RCS=()

run_single_worker() {
    local wid="$1"
    local metrics="$MATRIX_OUT_DIR/${wid}.json"
    local drain_flag=""
    if [[ "$MATRIX_DRAIN_OTHERS" == "1" ]]; then
        drain_flag="--drain-others"
    fi

    printf '%s=== Worker: %s ===%s\n' "$CYAN" "$wid" "$RESET"

    local rc=0
    # Run the smoke test. Pass through env vars so the child inherits
    # VELOX_ADMIN_TOKEN, SMOKE_DB, SMOKE_BGM_DIR, BGM_TRACK, etc.
    METRICS_PERSIST="$metrics" \
        SMOKE_TIMEOUT_SECONDS="$MATRIX_TIMEOUT_PER_WORKER" \
        SMOKE_POLL_TIMEOUT_SECONDS="$((MATRIX_TIMEOUT_PER_WORKER - 60))" \
        bash "$SMOKE" --worker-id="$wid" $drain_flag 2>&1 | \
        while IFS= read -r line; do
            printf '  %s[%s]%s %s\n' "$DIM" "$wid" "$RESET" "$line"
        done
    rc=${PIPESTATUS[0]}

    WORKER_RCS["$wid"]="$rc"

    if [[ "$rc" == "0" ]]; then
        printf '  %sRESULT: PASS%s (exit %s)\n' "$GREEN" "$RESET" "$rc"
        PASSED=$((PASSED + 1))
        return 0
    else
        printf '  %sRESULT: FAIL%s (exit %s)\n' "$RED" "$RESET" "$rc"
        FAILED=$((FAILED + 1))
        FAILED_WORKERS+=("$wid")
        # Write a failure stub so the matrix merge works even when
        # the smoke test didn't produce a metrics file.
        if [[ ! -s "$metrics" ]]; then
            jq -n \
                --arg worker_id "$wid" \
                --arg rc "$rc" \
                --arg run_id "$RUN_ID" \
                '{
                    run_id: $run_id,
                    worker_id: $worker_id,
                    error: ("smoke exited with code " + $rc),
                    cold_cache: {},
                    warm_cache: {},
                    post_restart: {},
                    audio: {},
                    subtitles: {}
                }' > "$metrics"
        fi
        return 1
    fi
}

# Iterate workers sequentially.
for wid in $WORKERS; do
    run_single_worker "$wid" || true  # continue to next worker on failure
    echo ""
done

# ── Merge per-worker metrics into matrix JSON ─────────────────────
echo "--- Merging per-worker metrics ---"

merge_worker_results() {
    local out="$1"
    # Pipe each worker's wrapped JSON object into jq -s add.
    # Missing/unparseable files become stubs so one failure doesn't
    # discard all other worker metrics.
    {
        for wid in $WORKERS; do
            local f="$MATRIX_OUT_DIR/${wid}.json"
            if [[ -s "$f" ]]; then
                jq -c --arg wid "$wid" '{($wid): .}' "$f" 2>/dev/null || \
                    printf '{"%s":{"error":"jq parse failed"}}\n' "$wid"
            else
                printf '{"%s":{"error":"metrics file missing"}}\n' "$wid"
            fi
        done
    } | jq -s 'add' > "$out"
}

merge_worker_results "$MATRIX_OUT_DIR/_merged.json"
workers_json=$(cat "$MATRIX_OUT_DIR/_merged.json" 2>/dev/null || echo "{}")

# Build exit_codes map from the per-worker rc array.
exit_codes_json=$(for wid in $WORKERS; do
    printf '%s:%s\n' "$wid" "${WORKER_RCS[$wid]:-?}"
done | jq -R 'split(":") | {(.[0]): (.[1] | tonumber? // -1)}' | jq -s 'add')

jq -n \
    --arg run_id "$RUN_ID" \
    --argjson summary "{\"total_workers\": $(echo "$WORKERS" | wc -w), \"passed\": $PASSED, \"failed\": $FAILED}" \
    --argjson failed_workers "$(printf '%s\n' "${FAILED_WORKERS[@]}" | jq -R . | jq -s .)" \
    --argjson exit_codes "$exit_codes_json" \
    --argjson workers "$workers_json" \
    '{
        run_id: $run_id,
        summary: $summary,
        failed_worker_ids: $failed_workers,
        exit_codes: $exit_codes,
        workers: $workers
    }' > "$MATRIX_JSON"

# ── Final report ──────────────────────────────────────────────────
echo ""
echo "================================================================"
printf '  %sVERDICT:%s ' "$([[ $FAILED -eq 0 ]] && echo "$GREEN" || echo "$RED")" "$RESET"
if [[ $FAILED -eq 0 ]]; then
    echo "ALL $PASSED WORKERS PASSED"
else
    echo "$FAILED WORKER(S) FAILED"
fi
echo "================================================================"
printf '  passed:  %s\n' "$PASSED"
printf '  failed:  %s\n' "$FAILED"
if [[ ${#FAILED_WORKERS[@]} -gt 0 ]]; then
    printf '  failures: %s\n' "${FAILED_WORKERS[*]}"
fi
printf '  matrix:  %s\n' "$MATRIX_JSON"
echo "================================================================"
echo ""

# Print the matrix JSON (compact, for log capture).
printf '%s=== MATRIX JSON ===%s\n' "$CYAN" "$RESET"
cat "$MATRIX_JSON"
printf '%s=== END MATRIX ===%s\n' "$CYAN" "$RESET"

if [[ $FAILED -gt 0 ]]; then
    exit 1
fi
exit 0

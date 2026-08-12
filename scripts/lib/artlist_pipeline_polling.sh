#!/usr/bin/env bash
# Source-only Artlist pipeline live-test helper: artlist_pipeline_polling.sh.
# shellcheck shell=bash
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    echo "[ERROR] artlist_pipeline_polling.sh must be sourced, not executed directly." >&2
    exit 1
fi

artlist_pipeline_polling() {
# ─── STEP 4: Poll all 10 jobs to terminal SUCCEEDED ───────────────────────
log ""
log "── STEP 4/12  Poll ${NUM_QUERIES} jobs to terminal (max ${JOB_POLL_TIMEOUT}s) ──"
declare -A JOB_STATUS
ELAPSED=0
ALL_TERMINAL=0
while [ "$ELAPSED" -lt "$JOB_POLL_TIMEOUT" ]; do
    sleep "$JOB_POLL_INTERVAL"
    ELAPSED=$((ELAPSED + JOB_POLL_INTERVAL))
    ALL_TERMINAL=1
    IDX=0
    for TERM in "${QUERIES[@]}"; do
        IDX=$((IDX+1))
        JID="${JOB_IDS[$((IDX-1))]}"
        [ -z "$JID" ] && continue
        POLL_FILE="$OUT_DIR/step4_query${IDX}_poll_${ELAPSED}s.json"
        P_HTTP=$(http_get "$BASE/api/artlist/runs/$JID" "$POLL_FILE")
        ST=$(jq -r '.status // "?"' "$POLL_FILE" 2>/dev/null || echo "?")
        JOB_STATUS[$IDX]="$ST"
        if [ "$ST" != "SUCCEEDED" ] && [ "$ST" != "FAILED" ] && [ "$ST" != "DEAD_LETTERED" ]; then
            ALL_TERMINAL=0
        fi
    done
    log "   ${ELAPSED}s statuses: $(for i in $(seq 1 ${NUM_QUERIES}); do printf 'q%d=%s ' "$i" "${JOB_STATUS[$i]:-?}"; done)"
    if [ "$ALL_TERMINAL" = "1" ]; then
        break
    fi
done
IDX=0
SUCCEEDED=0
for TERM in "${QUERIES[@]}"; do
    IDX=$((IDX+1))
    if [ "${JOB_STATUS[$IDX]:-?}" = "SUCCEEDED" ]; then
        SUCCEEDED=$((SUCCEEDED+1))
    fi
done
if [ "$SUCCEEDED" -ge "$NUM_QUERIES" ]; then
    pass "all ${SUCCEEDED}/${NUM_QUERIES} jobs reached SUCCEEDED in ${ELAPSED}s"
elif [ "$SUCCEEDED" -ge 1 ]; then
    fail "only ${SUCCEEDED}/${NUM_QUERIES} jobs reached SUCCEEDED in ${ELAPSED}s"
else
    fail "0/${NUM_QUERIES} jobs reached SUCCEEDED in ${ELAPSED}s"
fi

}


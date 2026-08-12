#!/usr/bin/env bash
# Source-only Artlist pipeline live-test helper: artlist_pipeline_payload.sh.
# shellcheck shell=bash
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    echo "[ERROR] artlist_pipeline_payload.sh must be sourced, not executed directly." >&2
    exit 1
fi

artlist_pipeline_payload() {
# ─── STEP 3: Enqueue 10 artlist/run jobs (async) ──────────────────────────
log ""
log "── STEP 3/12  POST /api/artlist/run × ${NUM_QUERIES} (async) ──"
IDX=0
for TERM in "${QUERIES[@]}"; do
    IDX=$((IDX+1))
    if [ -n "$ROOT_FOLDER_ID" ]; then
        RUN_BODY=$(jq -nc --arg term "$TERM" --argjson limit "$LIMIT_PER_QUERY" --arg rid "$ROOT_FOLDER_ID" \
            '{term: $term, limit: $limit, dry_run: false, root_folder_id: $rid}')
    else
        RUN_BODY=$(jq -nc --arg term "$TERM" --argjson limit "$LIMIT_PER_QUERY" \
            '{term: $term, limit: $limit, dry_run: false}')
    fi
    RUN_FILE="$OUT_DIR/step3_query${IDX}_${TERM}_response.json"
    RUN_HTTP=$(http_post "$BASE/api/artlist/run" "$RUN_FILE" "$RUN_BODY")
    RUN_ID=$(jq -r '.run_id // .job_id // ""' "$RUN_FILE" 2>/dev/null || echo "")
    if [ -z "$RUN_ID" ] || [ "$RUN_ID" = "null" ]; then
        fail "query#${IDX} term='${TERM}': no run_id (HTTP ${RUN_HTTP}, body=$(head -c 200 "$RUN_FILE" 2>/dev/null))"
        JOB_IDS+=("")
    else
        log "   query#${IDX} '${TERM}' → run_id=${RUN_ID} (HTTP ${RUN_HTTP})"
        JOB_IDS+=("$RUN_ID")
    fi
done
ENQUEUED=$(printf '%s\n' "${JOB_IDS[@]}" | grep -c -v '^$' || true)
if [ "$ENQUEUED" -lt "$NUM_QUERIES" ]; then
    fail "only ${ENQUEUED}/${NUM_QUERIES} queries enqueued successfully"
else
    pass "enqueued ${ENQUEUED}/${NUM_QUERIES} artlist/run jobs (async)"
fi

}


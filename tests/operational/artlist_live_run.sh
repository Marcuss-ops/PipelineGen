#!/usr/bin/env bash
# artlist_live_run.sh — run phase for artlist_live_e2e_verify.sh
#
# Sourced by the main shim AFTER prep.sh. Enqueues the real Artlist
# job, polls until terminal state (SUCCEEDED / FAILED), and extracts
# the produced asset_ids for the per-asset assertion phase.
#
# Cross-phase reads (from env.sh + prep.sh sourced state):
#   TOKEN / BASE_URL / CURL_TIMEOUT / SEARCH_TERM / LIMIT /
#   ROOT_FOLDER_ID / POLL_INTERVAL / POLL_MAX
#
# Cross-phase writes (consumed by assert.sh + teardown.sh):
#   JID             — job_id returned by /api/artlist/run
#   JSTATUS         — terminal status (SUCCEEDED / FAILED / ?)
#   JRESP           — last poll response (full job record)
#   ASSET_IDS       — newline-separated clip_id values from result.items
#   ASSET_COUNT     — count of produced asset_ids

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    echo "[ERROR] artlist_live_run.sh must be sourced, not executed directly." >&2
    exit 1
fi

# ============================================================
# STEP 1: enqueue media.artlist job
# ============================================================
log_info "=== STEP 1: POST /api/artlist/run ==="

if [[ -n "${ROOT_FOLDER_ID}" ]]; then
    RUN_BODY=$(jq -nc \
        --arg term "${SEARCH_TERM}" \
        --argjson limit "${LIMIT}" \
        --arg rid "${ROOT_FOLDER_ID}" \
        '{term: $term, limit: $limit, dry_run: false, root_folder_id: $rid}')
else
    RUN_BODY=$(jq -nc \
        --arg term "${SEARCH_TERM}" \
        --argjson limit "${LIMIT}" \
        '{term: $term, limit: $limit, dry_run: false}')
fi

JID=$(curl -s --max-time "${CURL_TIMEOUT}" -X POST "${BASE_URL}/api/artlist/run" \
    -H "$(auth_header)" \
    -H 'Content-Type: application/json' \
    -d "${RUN_BODY}" | jq -r '.run_id // ""')

if [[ -z "${JID}" || "${JID}" == "null" ]]; then
    log_fail "No run_id in POST /api/artlist/run response"
    exit 2
fi
log_pass "Job enqueued: ${JID}"

# ============================================================
# STEP 2: poll until terminal state
# ============================================================
log_info "=== STEP 2: Poll ${JID} until SUCCEEDED/FAILED (max ~$((POLL_INTERVAL * POLL_MAX))s) ==="
JSTATUS="?"
JRESP="{}"
i=0
for i in $(seq 1 ${POLL_MAX}); do
    sleep "${POLL_INTERVAL}"
    JRESP=$(curl -s --max-time "${CURL_TIMEOUT}" "${BASE_URL}/api/jobs/${JID}/full" \
        -H "$(auth_header)" || true)
    JSTATUS=$(echo "${JRESP}" | jq -r '.status // "?"' 2>/dev/null || echo "?")
    log_info "  poll #${i}/${POLL_MAX}: status=${JSTATUS}"
    if [[ "${JSTATUS}" == "SUCCEEDED" || "${JSTATUS}" == "FAILED" ]]; then
        break
    fi
done

if [[ "${JSTATUS}" != "SUCCEEDED" ]]; then
    log_fail "Job did not SUCCEED within timeout — final status=${JSTATUS}"
else
    log_pass "Job SUCCEEDED in ~$((POLL_INTERVAL * i))s"
fi

# ============================================================
# STEP 3: extract produced asset_ids
# ============================================================
ASSET_IDS=$(echo "${JRESP}" | jq -r '.result.items[]?.clip_id // empty' 2>/dev/null || true)
ASSET_COUNT=0
if [[ -n "${ASSET_IDS}" ]]; then
    ASSET_COUNT=$(echo "${ASSET_IDS}" | wc -l | tr -d ' ')
    log_pass "Driver returned ${ASSET_COUNT} asset_id(s)"
else
    log_fail "No assets in job result.items (job may have failed before processing)"
fi

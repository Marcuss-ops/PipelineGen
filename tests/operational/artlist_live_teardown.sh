#!/usr/bin/env bash
# artlist_live_teardown.sh — teardown phase for artlist_live_e2e_verify.sh
#
# Sourced by the main shim AFTER assert.sh. Prints the verdict
# summary, builds the machine-readable JSON verdict via `jq -n
# --arg ...`, writes it to ${LAST_JSON}, and applies the final
# exit-code policy:
#
#   FAIL > 0  → exit 1 (pipeline Artlist LIVE broken)
#   WARN > 0  → exit 0 (operator review recommended; all hard PASS)
#   all PASS  → exit 0
#
# Cross-phase reads (from env + prep + run + assert sourced state):
#   PASS / WARN / FAIL counters, ASSET_VERDICTS array,
#   SEARCH_TERM / LIMIT / ROOT_FOLDER_ID / JID / JSTATUS / ASSET_COUNT,
#   LAST_JSON.
#
# The teardown phase does NOT run if DRY_RUN is set (env.sh exits 0
# before prep is sourced). Fail-closed preflight (prep.sh) exits 2
# BEFORE the run/assert/teardown chain is reached, so LAST_JSON is
# only written when the live cycle completed.

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    echo "[ERROR] artlist_live_teardown.sh must be sourced, not executed directly." >&2
    exit 1
fi

# ============================================================
# Verdict summary + machine-readable JSON
# ============================================================
echo
echo "============================================"
echo "  Artlist LIVE E2E Verification"
echo "  PASS=${PASS}  WARN=${WARN}  FAIL=${FAIL}"
echo "============================================"

# Build per-asset JSON array via jq (handles empty ASSET_VERDICTS gracefully).
if [[ "${#ASSET_VERDICTS[@]}" -gt 0 ]]; then
    ASSETS_JSON=$(printf '%s\n' "${ASSET_VERDICTS[@]}" \
        | jq -R 'split("|") | {id: .[0], pass: (.[1]|tonumber), warn: (.[2]|tonumber), fail: (.[3]|tonumber)}' \
        | jq -s .)
else
    ASSETS_JSON="[]"
fi

jq -n \
    --arg ts           "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" \
    --arg term         "${SEARCH_TERM}" \
    --argjson limit    "${LIMIT}" \
    --arg rid          "${ROOT_FOLDER_ID}" \
    --arg jid          "${JID}" \
    --arg jstatus      "${JSTATUS}" \
    --argjson acount   "${ASSET_COUNT}" \
    --argjson pass     "${PASS}" \
    --argjson warn     "${WARN}" \
    --argjson fail     "${FAIL}" \
    --argjson assets   "${ASSETS_JSON}" \
    '{
       timestamp: $ts,
       search_term: $term,
       limit: $limit,
       root_folder_id: $rid,
       job_id: $jid,
       job_status: $jstatus,
       asset_count: $acount,
       totals: { pass: $pass, warn: $warn, fail: $fail },
       assets: $assets
     }' > "${LAST_JSON}"

log_info "Wrote machine-readable verdict: ${LAST_JSON}"

if [[ "${FAIL}" -gt 0 ]]; then
    echo "VERDICT: ${FAIL} CHECK(S) FAILED — pipeline Artlist LIVE broken"
    exit 1
elif [[ "${WARN}" -gt 0 ]]; then
    echo "VERDICT: ALL PASS, ${WARN} WARNINGS (operator review recommended)"
    exit 0
else
    echo "VERDICT: PIPELINE ARTLIST LIVE CORRETTA"
    exit 0
fi

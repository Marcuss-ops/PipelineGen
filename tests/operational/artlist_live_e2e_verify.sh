#!/usr/bin/env bash
# artlist_live_e2e_verify.sh — Artlist LIVE End-to-End Verification (main shim)
#
# Live E2E verification of the Artlist acquisition pipeline. Real
# scrape, real download (consumes Artlist quota — keep LIMIT=1),
# real Drive upload, real Qdrant indexing, real /api/media/search hit.
#
# Companion to:
#   - tests/operational/artlist_preflight_smoke.sh     (read-only preflight)
#   - tests/operational/artlist_multi_query_smoke.sh   (multi-keyword dry_run)
#   - tests/operational/artlist_qdrant_failure_smoke.sh
#   - tests/operational/artlist_drive_failure_smoke.sh
#   - tests/operational/artlist_scraper_failure_smoke.sh
#
# 9 verification points (mapped to action-plan DoD):
#   1.  Artlist scraper (/search) returns > 0 candidates
#   2.  media.artlist job enqueued via POST /api/artlist/run and terminal SUCCEEDED
#   3.  artlist_download_audit.status = 'succeeded' for the resulting asset
#   4.  media_assets row: source='artlist'
#                              + lifecycle_state = 'PUBLISHED'
#                              + drive_file_id non-empty
#                              + drive_link non-empty
#                              + download_link non-empty
#                              + file_hash non-empty
#                              + source_version non-empty
#                              + index_state = 'INDEXED'
#   5.  POST /api/drive/resolve-by-id (canonical Files.Get wrapper,
#                                       body {ids:[fileID]}):
#                                  file exists + not trashed + name non-empty
#                                  + size > 0
#   6.  outbox_events for asset_id: status IN ('completed', 'superseded')
#   7.  Qdrant scroll on alias ${COLLECTION}: at least one point with
#                                  payload.asset_id == ${ASSET_ID}
#   8.  Qdrant payload: source='artlist', media_type='video',
#                                  lifecycle_state = 'PUBLISHED'
#   9.  POST /api/media/search with sources=['artlist'] returns the asset
#
# Architecture (LAYER SPLIT, July 2026): the 867-LOC monolith is split
# into 5 phase sub-scripts (env / prep / run / assert / teardown)
# sourced by this main shim in strict order. All phase sub-scripts
# live side-by-side in tests/operational/ with the `artlist_live_`
# prefix. Each phase sub-script has a source-only guard
# (`[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit 1`) so direct
# execution of a phase fails closed rather than silently missing
# tally wiring + set -euo pipefail.
#
# Phase dispatch order (source-only, NOT invoked):
#   1. env.sh       — usage() + CLI args + config defaults + tally
#                     counters + tally helpers (log_*) + auth_header +
#                     append_asset_verdict + require_tool + DRY_RUN
#                     early-exit. Sourced first so every other phase
#                     sees the counters / helpers / set -euo.
#   2. prep.sh      — fail-closed preflight (jq / sqlite3 / curl on
#                     PATH, TOKEN non-empty, SCROLL_TIMEOUT bound,
#                     /ready, /health, /search connect-probe,
#                     /collections, DB present, ROOT_FOLDER_ID warn)
#                     + § 1 live-search probe + HERMETIC GATES +
#                     QHEADERS bash array.
#   3. run.sh       — STEP 1 job enqueue (POST /api/artlist/run →
#                     run_id) + STEP 2 poll (loop until SUCCEEDED /
#                     FAILED with POLL_INTERVAL × POLL_MAX budget) +
#                     STEP 3 asset_ids extraction.
#   4. assert.sh    — per-asset 9-step verification (audit status,
#                     media_assets row, Drive resolve-by-id,
#                     outbox_events status, Qdrant scroll + payload)
#                     + STEP 9 unified /api/media/search round-trip.
#                     Per-asset verdicts accumulate in ASSET_VERDICTS.
#   5. teardown.sh  — verbose verdict summary, jq -n machine-readable
#                     JSON verdict written to LAST_JSON, final
#                     exit-code policy (FAIL > 0 → 1; WARN/ALL_PASS → 0).
#
# Cross-phase state (preserved across `source` boundaries):
#   TOKEN, BASE_URL, SCRAPER_URL, QDRANT_URL, QDRANT_API_KEY, COLLECTION,
#   DB_PATH, SEARCH_TERM, LIMIT, ROOT_FOLDER_ID, POLL_INTERVAL, POLL_MAX,
#   SCROLL_TIMEOUT, SCRAPER_CONNECT_TIMEOUT_SECONDS, CURL_TIMEOUT,
#   EXPECTED_GATE_MATCHES, LAST_JSON, PASS, WARN, FAIL, ASSET_VERDICTS,
#   QHEADERS, SCRAPER_PROBE / SCRAPER_OK / SCRAPER_CLIPS, JID, JSTATUS,
#   JRESP, ASSET_IDS, ASSET_COUNT.
#
# Prerequisites (fail-closed):
#   - VELOX_ADMIN_TOKEN env var set (PipelineGen refuses default tokens in prod)
#   - features.artlist_enabled=true, features.drive_enabled=true
#   - qdrant.enabled=true, clip_indexer.enabled=true
#   - VELOX_ARTLIST_SCRAPER_SERVER_URL=http://127.0.0.1:9123
#     (PR-ARTLIST-CONFIG-PREFIX July 2026: renamed from bare
#     ARTLIST_SCRAPER_SERVER_URL; both this script + the loader
#     config struct in internal/platform/config/types_external.go
#     read the VELOX_-prefixed name now)
#   - ARTLIST_ACQUISITION_MODE=authorized_api + ARTLIST_DAILY_DOWNLOAD_LIMIT > 0
#   - VELOX_DRIVE_ARTLIST_ROOT set (or pass ROOT_FOLDER_ID=)
#   - sqlite3 + curl + jq + go on PATH

set -euo pipefail

# Fail-closed: refuse direct invocation as a non-source script. The
# source-only guard inside each phase sub-script is the primary
# defense; this one is a belt-and-suspenders for the main shim itself.
if [[ -z "${ARTLIST_LIVE_MAIN_SHIM:-}" ]]; then
    echo "[ERROR] artlist_live_e2e_verify.sh is the main shim — invoke it directly with 'bash \$0'." >&2
    echo "[ERROR] Phase sub-scripts (env/prep/run/assert/teardown.sh) MUST be sourced, not executed." >&2
    exit 1
fi

# Source the 5 phase sub-scripts in order. Each defines a single
# responsibility + cross-phase invariants (token / counters / vars
# documented above). Sourcing (NOT invoking) keeps PASS/WARN/FAIL +
# ASSET_VERDICTS in the same shell scope.
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${DIR}/artlist_live_env.sh"      "$@"
source "${DIR}/artlist_live_prep.sh"
source "${DIR}/artlist_live_run.sh"
source "${DIR}/artlist_live_assert.sh"
source "${DIR}/artlist_live_teardown.sh"

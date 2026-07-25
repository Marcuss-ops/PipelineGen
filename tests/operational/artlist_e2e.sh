#!/usr/bin/env bash
# tests/operational/artlist_e2e.sh — DEPRECATED backward-compat shim.
#
# Reorg (July 2026): the Artlist DoD battery has been split into
# tests/operational/artlist/{01_startup, 02_search_live, 03_detail_stream,
# 04_download, 05_pipeline_fresh, 06_drive, 07_index, 08_cache_replay,
# 09_failure_modes, run_all}.sh. The canonical orchestrator is run_all.sh;
# this file forwards to it for backward compat with manual `bash artlist_e2e.sh`
# invocations and any external caller that hasn't migrated to verify-artlist-live
# in the Makefile.
#
# DO NOT add new logic here. New gates go into the sub-scripts under
# tests/operational/artlist/. This shim will be removed at the next major
# version bump (post-reorg, July 2026+1 cycle).
#
# Gate-to-file map (for the extraction roadmap, July 2026+1 cycle):
#   Gate 0 clean reproducible env  → 01_startup.sh       (preflight + no-tamper fingerprint snapshot)
#   Gate 1 POST /detail hard gate   → 03_detail_stream.sh (happy + STREAM_NOT_FOUND)
#   Gate 2 POST /download + ffprobe → 04_download.sh      (DoD-exact ffprobe contract)
#   Gate 3 /api/artlist/search/live × 3 → 02_search_live.sh
#   Gate 4 first fresh run 3/3      → 05_pipeline_fresh.sh
#   Gate 5 per-clip DB+file checks  → split across 05/06/07
#   Gate 6 Drive resolve-by-id      → 06_drive.sh
#   Gate 7 SQLite + outbox          → 07_index.sh
#   Gate 8 Qdrant + media search    → 07_index.sh
#   Gate 9 cache replay             → 08_cache_replay.sh
#   Gate 10 negative tests          → 09_failure_modes.sh
#   Restart test (post-Gate 0 anti-tampering) → also enforced by run_all.sh
#   post-chain smoke_no_tampering_verify
#
# at extraction time this shim is replaced by tests/operational/artlist/run_all.sh
# (which already exists — `make verify-artlist-live` funnels through it).

set -euo pipefail
DIR=$(cd "$(dirname "$0")" && pwd)
exec bash "$DIR/artlist/run_all.sh"

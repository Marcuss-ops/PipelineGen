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

set -euo pipefail
DIR=$(cd "$(dirname "$0")" && pwd)
exec bash "$DIR/artlist/run_all.sh"

#!/usr/bin/env bash
# ─── scripts/artlist_pipeline_live_test.sh ───────────────────────────────
# Artlist pipeline live battery — operator-only, manual run.
# Fail-closed: every check is a real probe; no fake succeeds. Hits the
# real scraper (node-scraper/artlist_server.js on cfg.artlist_scraper_server_url),
# the real Drive API, the real Qdrant, and the real /api/media/search.
#
# godlike/07 NO-FAKE-AVAILABILITY: a real Artlist download IS consumed per
# query (10 queries → 10 downloads). Operator must budget quota and run
# sparingly.
#
# godlike/06 SSOT: this file is the SOLE canonical owner of the artlist
# live battery script. The byte-equivalent copy at
# scripts/tests/artlist_pipeline_live_test.sh is regenerated via
# `cp -p` (per ci-architectural-checks.sh Check 70).
#
# Mandatory invariants (Fase 14 / DoD §25-27):
#   - >= 10 real Artlist queries (one per term, deduped by asset_id)
#   - >= 20 candidates discovered (post-dedup)
#   - >= 10 clips downloaded with valid ffprobe
#   - >= 10 valid FFmpeg outputs
#   - >= 10 Drive files verified via POST /api/drive/resolve-by-id
#   - >= 10 PUBLISHED in SQLite (media_assets.lifecycle_state='PUBLISHED')
#   - >= 10 outbox completed (outbox_events.status='completed')
#   - >= 10 Qdrant points (filter source='artlist' AND asset_id in our set)
#   - >= 10 found via POST /api/media/search with sources=['artlist']
#   - ZERO false success / duplicates / corrupted files / orphan uploads
#
# Env contract (all read):
#   VELOX_ADMIN_TOKEN         Bearer (else extracted from .env)
#   VELOX_PORT                int      (default 8000)
#   DB_PATH                   file     (default data/media/media.db.sqlite)
#   QDRANT_URL                url      (default http://127.0.0.1:6333)
#   QDRANT_COLLECTION         alias    (default media_assets_current)
#   SCRAPER_URL               url      (default http://127.0.0.1:9123)
#   ROOT_FOLDER_ID            id       (Drive destination folder; required
#                                          for the run — server falls back
#                                          to the configured default if empty)
#   ARTLIST_QUERIES           csv      (default 10 diverse terms)
#   LIMIT_PER_QUERY           int      (default 3 → ~30 candidates, well
#                                          above the >=20 threshold)
#   MIN_ASSETS                int      (default 20 — DoD §25)
#   MIN_DOWNLOADS             int      (default 10 — DoD §25)
#   MIN_PUBLISHED             int      (default 10 — DoD §25)
#   MIN_OUTBOX_COMPLETED      int      (default 10 — DoD §25)
#   MIN_QDRANT_POINTS         int      (default 10 — DoD §25)
#   MIN_SEARCH_HITS           int      (default 10 — DoD §25)
#   MIN_MP4_BYTES             int      (default 65536)
#   JOB_POLL_TIMEOUT          int      (default 600 — 10min wall clock cap)
#   JOB_POLL_INTERVAL         int      (default 10)
#   REQUIRE_QDRANT            0|1      (default 1)
#
# Usage:
#   VELOX_ADMIN_TOKEN='...' ROOT_FOLDER_ID='<drive_folder_id>' \
#     ./scripts/artlist_pipeline_live_test.sh
#
# Verdict at end: VERDICT pass=<n> fail=<n> queries=<n> assets=<n> ...
#   Followed by a PASS/FAIL summary line. Exit 0 on all PASS; exit 1
#   on any FAIL.
# ──────────────────────────────────────────────────────────────────────────

set -uo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib/artlist_pipeline_preflight.sh"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib/artlist_pipeline_payload.sh"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib/artlist_pipeline_polling.sh"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib/artlist_pipeline_report.sh"

artlist_pipeline_preflight
artlist_pipeline_payload
artlist_pipeline_polling
artlist_pipeline_report

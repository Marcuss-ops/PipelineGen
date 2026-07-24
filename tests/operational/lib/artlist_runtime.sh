#!/usr/bin/env bash
# tests/operational/lib/artlist_runtime.sh — Artlist-battery runtime config + counters.
#
# Source-able library. Every arlist/*.sh sub-script does:
#   DIR=$(cd "$(dirname "$0")" && pwd)
#   # shellcheck disable=SC1091
#   source "$DIR/../lib/common.sh"
#   # shellcheck disable=SC1091
#   source "$DIR/../lib/artlist.sh"
#   # shellcheck disable=SC1091
#   source "$DIR/../lib/artlist_runtime.sh"
#
# Contract:
#   - Exposes HOST / PIPELINE_PORT / BASE_URL / DB_PATH / SCRAPER_URL /
#     QDRANT_URL / QDRANT_COLLECTION / ARTLIST_ROOT_FOLDER / ARTLIST_TERM
#     (all env-overridable; canonical DoD defaults).
#   - Exposes PASS / WARN / FAIL counters + log_pass / log_warn /
#     log_fail / log_info helpers (verbatim-identical across every battery).
#
# Environment variables (all overridable; defaults shown):
#   VELOX_HOST                            host (default 127.0.0.1)
#   VELOX_PORT                            pipeline port (default 8000)
#   PIPELINE_PORT                         port, higher precedence than VELOX_PORT
#   VELOX_DATA_DIR                        parent of media.db.sqlite (default ./data)
#   VELOX_ARTLIST_SCRAPER_SERVER_URL      scraper base URL (default http://127.0.0.1:9123)
#   QDRANT_URL                            Qdrant base URL (default http://127.0.0.1:6333)
#   QDRANT_COLLECTION                     Qdrant collection (default media_assets_current)
#   VELOX_DRIVE_ARTLIST_ROOT              Artlist Drive root folder (no default)
#   ROOT_FOLDER_ID                        alt source for VELOX_DRIVE_ARTLIST_ROOT
#   ARTLIST_TERM                          default search term (default "business …").
#
# DoD refactor (July 2026, post-verify-* split): replaces ~225 lines of
# copy-pasted boilerplate across 9 arlist sub-scripts + run_all.sh with
# a single ~60-line canonical source.

set -euo pipefail

# ── Runtime config (env-overridable, canonical DoD defaults) ──────────────
HOST="${VELOX_HOST:-127.0.0.1}"
PIPELINE_PORT="${PIPELINE_PORT:-${VELOX_PORT:-8000}}"
BASE_URL="http://${HOST}:${PIPELINE_PORT}"
DB_PATH="${VELOX_DATA_DIR:-./data}/media/media.db.sqlite"
SCRAPER_URL="${VELOX_ARTLIST_SCRAPER_SERVER_URL:-http://127.0.0.1:9123}"
QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"
QDRANT_COLLECTION="${QDRANT_COLLECTION:-media_assets_current}"
ARTLIST_ROOT_FOLDER="${VELOX_DRIVE_ARTLIST_ROOT:-${ROOT_FOLDER_ID:-}}"
ARTLIST_TERM="${ARTLIST_TERM:-business team working in modern office}"
export HOST BASE_URL DB_PATH SCRAPER_URL \
       QDRANT_URL QDRANT_COLLECTION \
       ARTLIST_ROOT_FOLDER ARTLIST_TERM PIPELINE_PORT

# ── Per-battery counters + log_* helpers (verbatim-identical per battery)
# Counters stay unexported on purpose: bumping inside $(…) subshells would not
# propagate to the parent shell anyway, and keeping them script-local matches
# the original inline definition. log_* functions inherit PASS/WARN/FAIL via
# sourcing scope (the lib's body is injected into the sub-script at source).
PASS=0; WARN=0; FAIL=0

log_pass() { printf '[PASS]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; PASS=$((PASS + 1)); }
log_warn() { printf '[WARN]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; WARN=$((WARN + 1)); }
log_fail() { printf '[FAIL]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; FAIL=$((FAIL + 1)); }
log_info() { printf '[INFO]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; }

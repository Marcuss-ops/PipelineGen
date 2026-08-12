#!/usr/bin/env bash
# Source-only Artlist pipeline live-test helper: artlist_pipeline_preflight.sh.
# shellcheck shell=bash
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    echo "[ERROR] artlist_pipeline_preflight.sh must be sourced, not executed directly." >&2
    exit 1
fi

artlist_pipeline_preflight() {

# ─── 0. Defaults + env validation ─────────────────────────────────────────
ARTLIST_QUERIES="${ARTLIST_QUERIES:-boxing,drone,mountains,ocean,city,forest,desert,river,snow,fire}"
LIMIT_PER_QUERY="${LIMIT_PER_QUERY:-3}"
MIN_ASSETS="${MIN_ASSETS:-20}"
MIN_DOWNLOADS="${MIN_DOWNLOADS:-10}"
MIN_PUBLISHED="${MIN_PUBLISHED:-10}"
MIN_OUTBOX_COMPLETED="${MIN_OUTBOX_COMPLETED:-10}"
MIN_QDRANT_POINTS="${MIN_QDRANT_POINTS:-10}"
MIN_SEARCH_HITS="${MIN_SEARCH_HITS:-10}"
MIN_MP4_BYTES="${MIN_MP4_BYTES:-65536}"
JOB_POLL_TIMEOUT="${JOB_POLL_TIMEOUT:-600}"
JOB_POLL_INTERVAL="${JOB_POLL_INTERVAL:-10}"
REQUIRE_QDRANT="${REQUIRE_QDRANT:-1}"

VELOX_PORT="${VELOX_PORT:-8000}"
BASE="http://127.0.0.1:${VELOX_PORT}"
DB_PATH="${DB_PATH:-data/media/media.db.sqlite}"
QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"
QDRANT_COLLECTION="${QDRANT_COLLECTION:-media_assets_current}"
SCRAPER_URL="${SCRAPER_URL:-${VELOX_ARTLIST_SCRAPER_SERVER_URL:-http://127.0.0.1:9123}}"
ROOT_FOLDER_ID="${ROOT_FOLDER_ID:-${VELOX_DRIVE_ARTLIST_ROOT:-}}"
# Per fix(scraper) PR + docs/operations/stock-e2e-runbook.md §11.0:
SCROLL_TIMEOUT="${SCROLL_TIMEOUT:-120}"
SCRAPER_CONNECT_TIMEOUT_SECONDS="${SCRAPER_CONNECT_TIMEOUT_SECONDS:-5}"

OUT_DIR="/tmp/artlist-pipeline-live-test"
mkdir -p "$OUT_DIR"

# Per the AGENTS.md secrets policy: never echo the token. Keep it in a var
# whose name is unlikely to appear in unrelated envs.
TOKEN="${VELOX_ADMIN_TOKEN:-}"
if [ -z "$TOKEN" ] && [ -f .env ]; then
    TOKEN=$(grep '^VELOX_ADMIN_TOKEN=' .env 2>/dev/null | head -n1 | cut -d= -f2- | tr -d '"' | tr -d "'" || true)
fi
if [ -z "$TOKEN" ]; then
    echo "FATAL: VELOX_ADMIN_TOKEN undefined (also: .env lookup failed)." >&2
    echo "  Remediation: export VELOX_ADMIN_TOKEN=... or populate .env." >&2
    exit 2
fi

# Required tools
MISSING_TOOLS=()
for t in jq curl sqlite3 ffprobe awk sed; do
    command -v "$t" >/dev/null 2>&1 || MISSING_TOOLS+=("$t")
done
if [ "${#MISSING_TOOLS[@]}" -gt 0 ]; then
    echo "FATAL: missing required tools on PATH: ${MISSING_TOOLS[*]}" >&2
    exit 2
fi

# Required files
[ -f "$DB_PATH" ] || {
    echo "FATAL: DB_PATH file not found: $DB_PATH" >&2
    exit 2
}
[ "${REQUIRE_QDRANT:-1}" = "1" ] && [ -z "${QDRANT_URL:-}" ] && {
    echo "FATAL: QDRANT_URL is required (REQUIRE_QDRANT=1)" >&2
    exit 2
}

# Expand ARTLIST_QUERIES into a bash array (CSV → array).
IFS=',' read -ra QUERIES <<< "$ARTLIST_QUERIES"
NUM_QUERIES="${#QUERIES[@]}"
if [ "$NUM_QUERIES" -lt 10 ]; then
    echo "FATAL: ARTLIST_QUERIES has $NUM_QUERIES term(s) — DoD §25 requires >= 10." >&2
    echo "  Set ARTLIST_QUERIES='term1,term2,...,term10' (default has 10)." >&2
    exit 2
fi

# Helpers
log()  { printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*"; }
fail() { log "  [FAIL] $*"; FAIL=$((FAIL+1)); }
pass() { log "  [PASS] $*"; PASS=$((PASS+1)); }
note() { log "  [NOTE] $*"; }
art()  { printf '%s' "$OUT_DIR/$1"; }

# Tally
PASS=0
FAIL=0
JOB_IDS=()
ASSET_IDS=()
RUN_START_ISO="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

log "── Configuration snapshot ──"
log "   BASE=$BASE"
log "   SCRAPER_URL=$SCRAPER_URL"
log "   DB=$DB_PATH"
log "   QDRANT=$QDRANT_URL/$QDRANT_COLLECTION"
log "   ROOT_FOLDER_ID=${ROOT_FOLDER_ID:-<unset — server uses default>}"
log "   ARTLIST_QUERIES=$ARTLIST_QUERIES (count=$NUM_QUERIES)"
log "   LIMIT_PER_QUERY=$LIMIT_PER_QUERY  MIN_ASSETS=$MIN_ASSETS  MIN_DOWNLOADS=$MIN_DOWNLOADS"
log "   MIN_PUBLISHED=$MIN_PUBLISHED  MIN_OUTBOX_COMPLETED=$MIN_OUTBOX_COMPLETED"
log "   MIN_QDRANT_POINTS=$MIN_QDRANT_POINTS  MIN_SEARCH_HITS=$MIN_SEARCH_HITS"
log "   MIN_MP4_BYTES=$MIN_MP4_BYTES"
log "   JOB_POLL_TIMEOUT=${JOB_POLL_TIMEOUT}s  JOB_POLL_INTERVAL=${JOB_POLL_INTERVAL}s"
log "   REQUIRE_QDRANT=$REQUIRE_QDRANT"
log "   RUN_START=$RUN_START_ISO"

# ─── curl wrappers (DO NOT swallow non-2xx) ──────────────────────────────
http_post() {
    local url="$1" out="$2" body="${3:-}" extra="${4:-}"
    local args=(-sS --max-time 30 -X POST -H "X-Velox-Admin-Token: $TOKEN"
                -H 'Content-Type: application/json' -o "$out" -w '%{http_code}')
    [ -n "$body"  ] && args+=(-d "$body")
    [ -n "$extra" ] && args+=($extra)
    curl "${args[@]}" "$url" 2>/dev/null
}
http_get() {
    local url="$1" out="$2" extra="${3:-}"
    local args=(-sS --max-time 30 -H "X-Velox-Admin-Token: $TOKEN"
                -o "$out" -w '%{http_code}')
    [ -n "$extra" ] && args+=($extra)
    curl "${args[@]}" "$url" 2>/dev/null
}

# ─── STEP 1: Health probe (fast-fail on a dead server) ────────────────────
log ""
log "── STEP 1/12  GET $BASE/health ──"
HEALTH_HTTP=$(curl -s -o "$OUT_DIR/step1_health.txt" -w '%{http_code}' \
    --max-time 5 "$BASE/health" 2>/dev/null || echo "000")
if [ "$HEALTH_HTTP" != "200" ]; then
    echo "FATAL: Server at $BASE returned HTTP $HEALTH_HTTP on /health." >&2
    echo "  Remediation: VELOX_FEATURE_ARTLIST_ENABLED=true ./pipelinegen --mode all" >&2
    exit 2
fi
pass "/health returned 200"

# ─── STEP 2: Scraper reachable (real Artlist search probe) ──────────────
log ""
log "── STEP 2/12  Scraper /search probe (term='${QUERIES[0]}', limit=$LIMIT_PER_QUERY) ──"
SCRAPER_PROBE=$(curl -sS --connect-timeout "${SCRAPER_CONNECT_TIMEOUT_SECONDS:-5}" --max-time "${SCROLL_TIMEOUT:-120}" -X POST "$SCRAPER_URL/search" \
    -H 'Content-Type: application/json' \
    -d "$(jq -nc --arg q "${QUERIES[0]}" --argjson n "$LIMIT_PER_QUERY" '{term: $q, limit: $n}')" \
    2>/dev/null || echo '{}')
SCRAPER_OK=$(echo "$SCRAPER_PROBE" | jq -r '.ok // false' 2>/dev/null || echo "false")
SCRAPER_CLIPS=$(echo "$SCRAPER_PROBE" | jq -r '.clips | length // 0' 2>/dev/null || echo "0")
if [ "$SCRAPER_OK" != "true" ]; then
    SCRAPER_ERR=$(echo "$SCRAPER_PROBE" | jq -r '.error // "<no .error field>"' 2>/dev/null)
    fail "scraper /search returned ok=false: $SCRAPER_ERR"
elif [ "$SCRAPER_CLIPS" -ge 1 ]; then
    pass "scraper /search returned ${SCRAPER_CLIPS} candidate(s) for term='${QUERIES[0]}'"
else
    fail "scraper /search returned 0 candidates for term='${QUERIES[0]}'"
fi

}


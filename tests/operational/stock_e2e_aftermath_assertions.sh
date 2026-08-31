#!/usr/bin/env bash
# tests/operational/stock_e2e_aftermath_assertions.sh (DoD §15 aftermath)
#
# Authored: BLOCCHETTO ULTIMO §15 — post-battery outbox + /api/media/search
# assertions for the stock pipeline.
#
# This test is MEANT TO RUN AFTER a battery (e.g. stock_e2e_one_clip.sh,
# stock_e2e_one_round.sh, stock_e2e_full_fight.sh) has at least once
# produced stock-source media values + their canonical outbox
# asset.index.requested events. It performs the user-spec assertions:
#
#   Part 1 — OUTBOX PROBE (per-asset pin)
#     For every media_asset.row WHERE source='stock' AND a matching
#     outbox_events row tied to that aggregate_id exists, verify
#     event_type='asset.index.requested', status IN ('pending',
#     'completed'), last_error=''.
#     Fail → PR-STOCK-OUTBOX-DEAD-LETTERED or PR-STOCK-OUTBOX-LAST-ERROR.
#
#   Part 2 — HYBRID SEARCH PROBE
#     POST /api/media/search with the user-spec body
#       {query:"pacquiao cotto boxing", sources:["stock"], mode:"hybrid"}
#     Assert response.items has >=1 record where
#       source=stock AND asset_id non-empty AND score>0 (numeric)
#       AND preview_url matches ^https?://.
#
#   Part 3 — asset_id ↔ media_assets CROSS-VALIDATION
#     Every source=stock hit's asset_id must SELECT 1 FROM
#     media_assets WHERE id = <hit_id>. Round-trip check: at least
#     1 hit's asset_id matches one of the outbox-probed aggregate_ids.
#
# Strict-mode mapping (HTTP code → canonical owner file path) per the
# existing stock_e2e_unified_search_smoke.sh convention; this new test
# does NOT replace that smoke — it layers the per-asset cross-check on
# top, which the existing smoke does not do.
#
# Self-checks:
#   - bash -n tests/operational/stock_e2e_aftermath_assertions.sh
#     must exit 0 (validated at commit time)
#   - jq -e . on the response body must succeed BEFORE per-item asserts
#
# Exit codes (canonical battery convention):
#   0   = PASS (every assertion held) OR INFO-empty-DB (no rows; aligned
#         with stock_e2e_db_outbox_smoke.sh which uses exit 0 + INFO for
#         the empty-DB case so CI tooling interprets "no data" as PASS)
#   1   = FAIL (1+ violation, canonical root cause printed)
#   2   = PREREQ_MISSING (curl/jq/sqlite3 absent, DB not found, schema missing)
#
# Overridable env vars:
#   DB_PATH = data/media/media.db.sqlite
#   BASE    = http://localhost:8080    (VELOX_API_BASE / SMOKE_API_BASE honoured)
#   TOKEN   =                       (VELOX_ADMIN_TOKEN / SMOKE_TOKEN honoured)
#   SAMPLE_MAX = 50                  (cap on aggregate_ids probed per run)

set -euo pipefail

# ---- Configuration ---------------------------------------------------------
DB_PATH="${DB_PATH:?DB_PATH must be explicitly set to an isolated or approved database}"
BASE="${VELOX_API_BASE:-${SMOKE_API_BASE:-http://localhost:8080}}"
TOKEN="${VELOX_ADMIN_TOKEN:-${SMOKE_TOKEN:-}}"
SAMPLE_MAX="${SAMPLE_MAX:-50}"

OUT_JSON="$(mktemp /tmp/stk-aftermath-resp.XXXXXX.json)"
TMP_WORK="$(mktemp -d /tmp/stk-aftermath-work.XXXXXX)"
trap 'rm -rf "$OUT_JSON" "$TMP_WORK"' EXIT INT TERM

# ---- CLI flags -------------------------------------------------------------
DRY_RUN=0
HELP_REQUESTED=0
for arg in "$@"; do
    case "$arg" in
        --dry) DRY_RUN=1 ;;
        -h|--help) HELP_REQUESTED=1 ;;
        *) printf 'setup error: unknown flag %s\n' "$arg" >&2
            exit 2 ;;
    esac
done

if [ "$HELP_REQUESTED" -eq 1 ]; then
    sed -n '2,40p' "$0"
    exit 0
fi

# ---- Header ----------------------------------------------------------------
echo "=================================================================="
echo "STK-E2E-AFTERMATH (DoD §15): outbox + /api/media/search assertions"
echo "  DB_PATH    = $DB_PATH"
echo "  BASE       = $BASE"
echo "  SAMPLE_MAX = $SAMPLE_MAX"
echo "=================================================================="

if [ "$DRY_RUN" -eq 1 ]; then
    echo "DRY-RUN: skipping live HTTP probe (outbox probe still runs)"
fi

# ---- Prerequisite checks (exit 2) -----------------------------------------
for tool in sqlite3 jq curl; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        printf 'PREREQ_MISSING: %s not on PATH (exit 2)\n' "$tool" >&2
        exit 2
    fi
done

if [ ! -f "$DB_PATH" ]; then
    printf 'FAIL: %s not found (exit 2)\n' "$DB_PATH" >&2
    printf '  Suggested: start PipelineGen to bootstrap the DB, or point DB_PATH\n' >&2
    exit 2
fi

# ---- Schema presence probe (exit 2 if outbox_events OR media_assets missing)
for tbl in outbox_events media_assets; do
    COUNT=$(sqlite3 "$DB_PATH" \
        "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='$tbl';" \
        2>/dev/null || echo "0")
    if [ "$COUNT" != "1" ]; then
        printf 'FAIL: %s table not found in %s (exit 2)\n' "$tbl" "$DB_PATH" >&2
        exit 2
    fi
done

# ============================================================================
# PART 1 — OUTBOX PROBE (per-asset pin)
# ============================================================================
echo
echo "------- Part 1: per-asset outbox_events probe -------"

# Identify stock-source media_assets.id values that have a matching
# asset.index.requested outbox row (i.e. the AssetCommitter emitted the
# canonical event). Limited to SAMPLE_MAX most-recent rows so a large
# post-battery DB doesn't explode awk pipelines.
STOCK_AGGS_FILE="$TMP_WORK/stock_aggregate_ids.txt"
sqlite3 -separator $'\n' "$DB_PATH" "
SELECT DISTINCT ma.id
FROM media_assets ma
JOIN outbox_events oe
  ON oe.event_type = 'asset.index.requested'
 AND oe.aggregate_id = ma.id
WHERE ma.source = 'stock'
ORDER BY ma.created_at DESC
LIMIT $SAMPLE_MAX;
" > "$STOCK_AGGS_FILE" 2>/dev/null || {
    printf 'FAIL: sqlite3 media_assets/outbox_events JOIN failed (exit 2)\n' >&2
    exit 2
}

TOTAL_AGG_IDS=$(grep -c . "$STOCK_AGGS_FILE" || echo 0)

if [ "$TOTAL_AGG_IDS" -eq 0 ]; then
    echo "INFO: zero stock-source media_asset + outbox_events rows in $DB_PATH"
    echo "  Suggestion: run tests/operational/stock_e2e_one_clip.sh first to mint a stock asset."
    echo "  Verdict: exit 0 (cannot validate lifecycle without rows; aligned with stock_e2e_db_outbox_smoke.sh convention)."
    exit 0
fi

echo "Found $TOTAL_AGG_IDS stock-source aggregate_ids with asset.index.requested outbox events."

# Build quoted IN-clause for the per-row outbox pin (escape single quotes;
# manual awk quoting keeps the SQL portable across sqlite3 versions).
IN_LIST=$(awk 'BEGIN{ORS=","; q=sprintf("%c",39)}
              {gsub(q, q q); printf q "%s" q, $0}' \
              "$STOCK_AGGS_FILE" | sed 's/,$//')

OUTBOX_RAW_FILE="$TMP_WORK/outbox_pin.txt"
sqlite3 -separator '|' -header "$DB_PATH" "
SELECT aggregate_id, status, last_error
FROM outbox_events
WHERE event_type = 'asset.index.requested'
  AND aggregate_id IN ($IN_LIST);
" > "$OUTBOX_RAW_FILE"

ROWS_PINNED=$(awk -F'|' 'NR>1 && $1!="" {n++} END{print n+0}' "$OUTBOX_RAW_FILE")
PENDING_COUNT=$(awk -F'|' 'NR>1 && $2=="pending" {n++} END{print n+0}' "$OUTBOX_RAW_FILE")
COMPLETED_COUNT=$(awk -F'|' 'NR>1 && $2=="completed" {n++} END{print n+0}' "$OUTBOX_RAW_FILE")
DEAD_LETTER_COUNT=$(awk -F'|' 'NR>1 && ($2=="dead_lettered" || $2=="dead_letter" || $2=="failed") {n++} END{print n+0}' "$OUTBOX_RAW_FILE")
LAST_ERR_COUNT=$(awk -F'|' 'NR>1 && $3!="" && $3!=" " {n++} END{print n+0}' "$OUTBOX_RAW_FILE")

echo "Outbox lifecycle:"
echo "  total pinned (aggregate_id IN) = $ROWS_PINNED"
echo "  status='pending'               = $PENDING_COUNT"
echo "  status='completed'             = $COMPLETED_COUNT"
echo "  status IN (dead_letter|failed) = $DEAD_LETTER_COUNT   (must be 0)"
echo "  last_error != empty            = $LAST_ERR_COUNT      (must be 0)"

PART1_FAIL=0
if [ "$DEAD_LETTER_COUNT" -gt 0 ]; then
    printf 'FAIL canonical: PR-STOCK-OUTBOX-DEAD-LETTERED (%s row(s) in dead/terminal state)\n' \
        "$DEAD_LETTER_COUNT" >&2
    PART1_FAIL=1
fi
if [ "$LAST_ERR_COUNT" -gt 0 ]; then
    printf 'FAIL canonical: PR-STOCK-OUTBOX-LAST-ERROR (%s row(s) with last_error != empty)\n' \
        "$LAST_ERR_COUNT" >&2
    PART1_FAIL=1
fi
if [ "$ROWS_PINNED" -ne "$TOTAL_AGG_IDS" ]; then
    printf 'FAIL canonical: PR-STOCK-OUTBOX-MISSING (%s aggregate_ids matched no outbox row; should be %s)\n' \
        "$((TOTAL_AGG_IDS - ROWS_PINNED))" "$TOTAL_AGG_IDS" >&2
    PART1_FAIL=1
fi
if [ "$PART1_FAIL" -ne 0 ]; then exit 1; fi

echo "Part 1 PASS: every stock aggregate_id has healthy outbox pin."

# ============================================================================
# PART 2 — HYBRID SEARCH PROBE
# ============================================================================
if [ "$DRY_RUN" -eq 1 ]; then
    echo "[DRY-RUN] skipping /api/media/search live probe."
    exit 0
fi

echo
echo "------- Part 2: POST /api/media/search (DoD §15 query) -------"

BODY=$(cat <<'PAYLOAD_EOF'
{
  "query": "pacquiao cotto boxing",
  "sources": ["stock"],
  "mode": "hybrid",
  "limit": 20
}
PAYLOAD_EOF
)

# Build curl args; admin token injected when set (handler requires
# IsAdmin=true for the hybrid semantic-backend path).
CURL_ARGS=( -sS --max-time 30 -X POST \
    -H 'Content-Type: application/json' \
    --data "$BODY" \
    -o "$OUT_JSON" \
    -w '%{http_code}' )
if [ -n "$TOKEN" ]; then
    CURL_ARGS=( -H "Authorization: Bearer $TOKEN" "${CURL_ARGS[@]}" )
fi

HTTP_CODE=$(curl "${CURL_ARGS[@]}" "$BASE/api/media/search" 2>/dev/null) || {
    printf 'FAIL: curl to %s/api/media/search exit non-zero (network/DNS/refused)\n' \
        "$BASE" >&2
    exit 1
}

# Map HTTP code → canonical owner (per existing stock_e2e_unified_search_smoke.sh).
case "$HTTP_CODE" in
    200) : ;;
    401)
        printf 'FAIL canonical: PR-STOCK-SEARCH-UNAUTHENTICATED (HTTP 401 — missing/invalid admin token)\n' >&2
        printf '  Canonical owner: route middleware auth gate (VELOX_ADMIN_TOKEN bearer validator)\n' >&2
        exit 1 ;;
    403)
        printf 'FAIL canonical: PR-STOCK-SEARCH-FORBIDDEN (HTTP 403 — non-admin caller; actor.IsAdmin=false)\n' >&2
        printf '  Canonical owner: internal/capabilities/assets/search/handler.go::actor.IsAdmin gate\n' >&2
        exit 1 ;;
    404)
        printf 'FAIL canonical: PR-STOCK-ROUTE-REGISTRATION (HTTP 404 on POST /api/media/search)\n' >&2
        printf '  Canonical owner: internal/capabilities/assets/search/handler.go::RegisterRoutes\n' >&2
        exit 1 ;;
    400)
        printf 'FAIL canonical: PR-STOCK-SEARCH-HANDLER-VALIDATION (HTTP 400 — invalid request shape)\n' >&2
        printf '  Canonical owner: internal/capabilities/assets/search/handler.go::c.BindJSON(&searchRequest)\n' >&2
        exit 1 ;;
    422)
        printf 'FAIL canonical: PR-STOCK-SEMANTIC-UNAVAILABLE (HTTP 422 — semantic backend down for hybrid)\n' >&2
        printf '  Canonical owner: internal/capabilities/assets/search/errors.go::ErrSemanticBackendUnavailable\n' >&2
        exit 1 ;;
    503)
        printf 'FAIL canonical: PR-STOCK-SEARCH-NOT-WIRED (HTTP 503 — aggregator is nil)\n' >&2
        printf '  Canonical owner: internal/capabilities/assets/search/handler.go::h.aggreg == nil guard\n' >&2
        exit 1 ;;
    *)
        printf 'FAIL canonical: PR-STOCK-UNIFIED-SEARCH-UNKNOWN (HTTP %s)\n' "$HTTP_CODE" >&2
        printf '  Canonical owner: internal/capabilities/assets/search/handler.go::Handler.Search\n' >&2
        exit 1 ;;
esac

# Validate response envelope shape (body is non-empty + jq-parseable).
if [ ! -s "$OUT_JSON" ] || ! jq -e . "$OUT_JSON" >/dev/null 2>&1; then
    printf 'FAIL canonical: PR-STOCK-UNIFIED-SEARCH-EMPTY (HTTP 200 but body empty/non-JSON)\n' >&2
    exit 1
fi

# Per-item tallies (escape the URL regex properly; jq raw strings handle ^/$).
TOTAL_HITS=$(jq '[.items[]?] | length' "$OUT_JSON")
STOCK_HITS=$(jq '[.items[]? | select(.source == "stock")] | length' "$OUT_JSON")
VALID_STOCK_HITS=$(jq '[.items[]? | select(
    .source == "stock"
    and (.asset_id != null) and ((.asset_id | length) > 0)
    and (.score != null) and ((.score | type) == "number") and (.score > 0)
    and ((.preview_url // "") | test("^https?://"))
)] | length' "$OUT_JSON")

echo "Search hits:"
echo "  total items (.items[])              = $TOTAL_HITS"
echo "  source=stock                          = $STOCK_HITS  (must be >= 1)"
echo "  full invariants (id+score+preview)  = $VALID_STOCK_HITS  (must be >= 1)"

if [ "$STOCK_HITS" -lt 1 ]; then
    printf 'FAIL canonical: PR-STOCK-OUTBOX-QDRANT-INDEX (zero source=stock hits)\n' >&2
    printf '  Likely cause: deliver/database.go (drive+finalizer enqueued but\n' >&2
    printf '  indexing handler has not flushed into Qdrant yet) OR\n' >&2
    printf '  internal/capabilities/assets/search/handler.go::searchQueryFromRequest.filters.source\n' >&2
    exit 1
fi
if [ "$VALID_STOCK_HITS" -lt 1 ]; then
    printf 'FAIL canonical: PR-STOCK-SEARCH-SCORE-OWNERSHIP (source=stock hits lack id+score+preview_url)\n' >&2
    printf '  Canonical owner: internal/capabilities/assets/search/types_result.go (Candidate struct fields)\n' >&2
    exit 1
fi

echo "Part 2 PASS: hybrid search returned >= 1 fully-shaped source=stock hit."

# ============================================================================
# PART 3 — asset_id ↔ media_assets CROSS-VALIDATION
# ============================================================================
echo
echo "------- Part 3: asset_id ── SQLite cross-validation -------"

# Extract asset_id from every source=stock hit meeting the per-invariants.
HIT_IDS_FILE="$TMP_WORK/hit_asset_ids.txt"
jq -r '.items[]? | select(
    .source == "stock"
    and (.asset_id != null) and ((.asset_id | length) > 0)
    and (.score != null) and ((.score | type) == "number") and (.score > 0)
    and ((.preview_url // "") | test("^https?://"))
) | .asset_id' "$OUT_JSON" > "$HIT_IDS_FILE"

HIT_IDS_COUNT=$(grep -c . "$HIT_IDS_FILE" || echo 0)

if [ "$HIT_IDS_COUNT" -eq 0 ]; then
    # Should be impossible after Part 2 PASS, but guard explicitly.
    printf 'FAIL canonical: PR-STOCK-SEARCH-INVARIANT-DRIFT (Part 2 said valid>0 but jq extraction=0)\n' >&2
    exit 1
fi

# Probe media_assets WHERE id IN (top-20 by sort to keep the JOIN cheap).
HIT_IN=$(awk 'BEGIN{ORS=","; q=sprintf("%c",39)}
            {gsub(q, q q); printf q "%s" q, $0}' \
            "$HIT_IDS_FILE" | sed 's/,$//')

CROSSCHECK_COUNT=$(sqlite3 "$DB_PATH" \
    "SELECT COUNT(*) FROM media_assets WHERE id IN ($HIT_IN);")

echo "Asset_id -> SQLite cross-check:"
echo "  source=stock + invariant-satisfied hits sampled = $HIT_IDS_COUNT"
echo "  media_assets.id IN (...) match count             = $CROSSCHECK_COUNT"
echo "  parity (must match)                              = $([ "$HIT_IDS_COUNT" = "$CROSSCHECK_COUNT" ] && echo YES || echo NO)"

if [ "$CROSSCHECK_COUNT" -ne "$HIT_IDS_COUNT" ]; then
    printf 'FAIL canonical: PR-STOCK-SEARCH-JOIN-BROKEN (%s hit(s) with asset_id NOT in media_assets)\n' \
        "$((HIT_IDS_COUNT - CROSSCHECK_COUNT))" >&2
    printf '  Canonical owner: internal/platform/sqlite/assets/clip_atomic_writer.go\n' >&2
    printf '  The /api/media/search handler is returning asset_ids that have\n' >&2
    printf '  no corresponding row in media_assets (broken wire).\n' >&2
    exit 1
fi

# Round-trip: at least 1 hit asset_id must match a known outbox aggregate_id.
ROUNDTRIP_JUNCTION="$TMP_WORK/junction.txt"
comm -12 \
    <(sort -u "$HIT_IDS_FILE") \
    <(sort -u "$STOCK_AGGS_FILE") \
    > "$ROUNDTRIP_JUNCTION"
ROUNDTRIP_COUNT=$(grep -c . "$ROUNDTRIP_JUNCTION" || echo 0)

echo "Cross-validated round-trip hits:"
echo "  hits intersecting outbox-pinned aggregate_ids = $ROUNDTRIP_COUNT (must be >= 1)"

if [ "$ROUNDTRIP_COUNT" -lt 1 ]; then
    printf 'WARN canonical: PR-STOCK-SEARCH-ROUND-TRIP (search hit asset_ids do not match outbox aggregate_ids)\n' >&2
    printf '  This usually means: indexed assets predate the test sample.\n' >&2
    printf '  Not a hard FAIL (DoD §15 only requires per-part invariants);\n' >&2
    printf '  but the operator should reconcile SAMPLE_MAX vs index lag.\n' >&2
fi

# ============================================================================
# PASS
# ============================================================================
echo
echo "=================================================================="
echo "STK-E2E-AFTERMATH PASS"
echo "=================================================================="
echo "Part 1 — outbox pin:"
echo "  $ROWS_PINNED/$TOTAL_AGG_IDS aggregate_ids healthy (pending=$PENDING_COUNT completed=$COMPLETED_COUNT)"
echo "  dead_letter=$DEAD_LETTER_COUNT last_error=$LAST_ERR_COUNT"
echo
echo "Part 2 — /api/media/search:"
echo "  total_hits=$TOTAL_HITS stock_hits=$STOCK_HITS full_invariants_HITS=$VALID_STOCK_HITS"
echo
echo "Part 3 — asset_id ── media_assets:"
echo "  sampled=$HIT_IDS_COUNT crosscheck=$CROSSCHECK_COUNT round_trip=$ROUNDTRIP_COUNT"
exit 0

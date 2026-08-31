#!/usr/bin/env bash
# tests/operational/stock_e2e_unified_search_smoke.sh (STK-E2E-F, STOCK-E2E-BATTERY-2026-07-05)
#
# Hermetic shell-smoke for the unified media-search route (POST /api/media/search).
# Spec (per user literal request 2026-07-05):
#   Body: { "query": "boxing training gym stock footage", "sources": ["stock"],
#           "mode": "hybrid", "limit": 10 }
#   Assertion: response contains >= 1 record with source=stock AND score > 0.
#   Output: jq-formatted (formatted_response.json).
#   Tally: F=2 (per Phase H aggregator stock_e2e_full_battery.sh).
#
# Per godlike/06 SSOT (one-canonical-owner-per-fact): handler lives at
#   internal/api/assets/search/handler.go::Handler.Search (POST /api/media/search).
#   DTO: query/sources/mode/filters/limit/cursor per searchRequest struct.
#   SearchCandidate JSON tags (per internal/application/search/types_result.go):
#     {asset_id, source, source_ref, media_type, title, name, thumbnail_url,
#      preview_url, score, hash}  --  score is float64 (non-omitempty).
#
# Per godlike/07 NO-FAKE-AVAILABILITY: every FAIL pattern maps to a canonical
# owner file path with rg-verified SSOT (per `docs/operations/stock-e2e-runbook.md§3`
# diagnosis decision tree).
#
# Exit codes (canonical battery convention):
#   0 = PASS (>= 1 source=stock record with score > 0)
#   1 = FAIL (zero matches / wrong source / route 404 / invalid-shape 400)
#   2 = PREREQ_MISSING (curl/jq missing or $BASE unreachable)
#   3 = DATABASE_REQUIRED (registry SQLite query needed; fall through to PR-STOCK-SEARCH-DB-OWNERSHIP-REQUIRED)

set -euo pipefail

# -------------------------------------------------------------------
# Pre-flight: required tools
# -------------------------------------------------------------------
for tool in curl jq sqlite3; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "PREREQ_MISSING: $tool not found in PATH" >&2
    exit 2
  fi
done

# -------------------------------------------------------------------
# Configuration (env-overridable; defaults match action plan §3)
# -------------------------------------------------------------------
BASE="${VELOX_API_BASE:-${SMOKE_API_BASE:-http://localhost:8080}}"
ADMIN_TOKEN="${VELOX_ADMIN_TOKEN:-}"
DB_PATH="${VELOX_DB_PATH:?VELOX_DB_PATH must be explicitly set to an isolated or approved database}"
OUT_JSON="${TMPDIR:-/tmp}/stk-e2e-f-response.$$.json"
FORMATTED="${TMPDIR:-/tmp}/stk-e2e-f-formatted.$$.txt"
JQ_ERR_LOG="${TMPDIR:-/tmp}/stk-e2e-f-jq-err.$$.log"
WARN_JQ=0

cleanup() {
  # Preserve OUT_JSON + JQ_ERR_LOG on FAIL (exit 1) so operator can inspect; remove on PASS (exit 0).
  local exit_code=$?
  if [ "$exit_code" -eq 0 ]; then
    rm -f "$OUT_JSON" "$FORMATTED" 2>/dev/null || true
  fi
}
trap cleanup EXIT

# -------------------------------------------------------------------
# Probe: POST /api/media/search (canonical route per internal/api/assets/search/handler.go)
# -------------------------------------------------------------------
REQ_BODY=$(cat <<'PAYLOAD_EOF'
{
  "query": "boxing training gym stock footage",
  "sources": ["stock"],
  "mode": "hybrid",
  "limit": 10
}
PAYLOAD_EOF
)

CURL_ARGS=(-sS -w '\n%{http_code}' --max-time 30 -X POST \
  -H "Content-Type: application/json" \
  --data "$REQ_BODY" \
  "$BASE/api/media/search")

# Inject admin token if set (some canonical deployments require it).
if [ -n "$ADMIN_TOKEN" ]; then
  CURL_ARGS=(-H "Authorization: Bearer $ADMIN_TOKEN" "${CURL_ARGS[@]}")
fi

CURL_OUTPUT=$(curl "${CURL_ARGS[@]}" 2>&1) || {
  echo "FAIL: curl exit non-zero (network / DNS / refused)" >&2
  echo "Canonical owner: internal/api/assets/search/handler.go::Handler.Search" >&2
  echo "Forward-pointer: PR-STOCK-ROUTE-REGISTRATION if 404 / connection-refused on /api/media/search" >&2
  exit 1
}

HTTP_CODE=$(echo "$CURL_OUTPUT" | tail -n 1)
RESP_BODY=$(echo "$CURL_OUTPUT" | sed '$d')

# -------------------------------------------------------------------
# FAIL-to-PR mapping (per docs/operations/stock-e2e-runbook.md§3 + canonical errors.go)
# -------------------------------------------------------------------

# 404: route not registered at composition root
if [ "$HTTP_CODE" = "404" ]; then
  echo "FAIL: HTTP 404 on POST /api/media/search" >&2
  echo "Canonical owner: internal/api/assets/search/handler.go (NOT registered in composition root)" >&2
  echo "Forward-pointer: PR-STOCK-ROUTE-REGISTRATION (compose-time wiring missing)" >&2
  exit 1
fi

# 422: semantic backend unavailable (hybrid mode but Qdrant disconnected)
# Canonical owner: internal/application/search/errors.go::ErrSemanticBackendUnavailable
# (handlers map this to HTTP 422 per errors.go godoc).
if [ "$HTTP_CODE" = "422" ]; then
  echo "FAIL: HTTP 422 (semantic backend unavailable for hybrid mode)" >&2
  echo "Canonical owner: internal/application/search/errors.go::ErrSemanticBackendUnavailable" >&2
  echo "Forward-pointer: PR-STOCK-SEMANTIC-UNAVAILABLE (Qdrant v3 disconnected or hybrid unsupported)" >&2
  exit 1
fi

# 400: invalid request shape (searchRequest binding failed)
if [ "$HTTP_CODE" = "400" ]; then
  echo "FAIL: HTTP 400 (invalid request shape)" >&2
  echo "Canonical owner: internal/api/assets/search/handler.go::c.BindJSON(&searchRequest)" >&2
  echo "Forward-pointer: PR-STOCK-SEARCH-HANDLER-VALIDATION (binding tag mismatch / missing field)" >&2
  exit 1
fi

# 502/503/500: backend failure
if echo "$HTTP_CODE" | grep -qE '^(502|503|500)$'; then
  echo "FAIL: HTTP $HTTP_CODE (backend failure)" >&2
  echo "Canonical owner: internal/application/search/ports.go (BackendRegistry)" >&2
  echo "Forward-pointer: PR-STOCK-SEARCH-BACKEND-DOWN (semantic + lexical backends offline)" >&2
  exit 1
fi

# 200 but empty body or non-JSON
if [ "$HTTP_CODE" != "200" ]; then
  echo "FAIL: HTTP $HTTP_CODE (unexpected)" >&2
  echo "Canonical owner: internal/api/assets/search/handler.go::Handler.Search" >&2
  echo "Forward-pointer: PR-STOCK-UNIFIED-SEARCH-UNKNOWN (unanticipated status code)" >&2
  exit 1
fi

if [ -z "$RESP_BODY" ] || ! echo "$RESP_BODY" | jq -e . >/dev/null 2>&1; then
  echo "FAIL: HTTP 200 but response body is empty or non-JSON" >&2
  echo "Canonical owner: internal/api/assets/search/handler.go::mapSearchResponse" >&2
  echo "Forward-pointer: PR-STOCK-UNIFIED-SEARCH-EMPTY (handler shipped empty body)" >&2
  exit 1
fi

# Persist response envelope for jq-assertion phase (preserved on FAIL per cleanup()).
printf '%s' "$RESP_BODY" > "$OUT_JSON"

# -------------------------------------------------------------------
# jq assertions (per SearchCandidate JSON tags above)
# -------------------------------------------------------------------

# Primary tally F=1: item count >= 1 with source=stock.
STOCK_COUNT=$(jq '[.items[]? | select(.source == "stock")] | length' "$OUT_JSON" 2>>"$JQ_ERR_LOG" || { echo "0"; WARN_JQ=1; })
TOTAL_COUNT=$(jq '[.items[]?] | length' "$OUT_JSON" 2>>"$JQ_ERR_LOG" || { echo "0"; WARN_JQ=1; })

if [ "$STOCK_COUNT" -lt 1 ]; then
  if [ "$TOTAL_COUNT" -lt 1 ]; then
    echo "FAIL: zero search results returned" >&2
    echo "Canonical owner: internal/application/jobs/outbox/delivery.go (outbox_best_effort may not have flushed)" >&2
    echo "Forward-pointer: PR-STOCK-OUTBOX-QDRANT-INDEX (outbox event for stock clip never reached Qdrant)" >&2
  else
    echo "FAIL: $TOTAL_COUNT total results but zero with source=stock" >&2
    echo "Canonical owner: internal/api/assets/search/handler.go::searchQueryFromRequest.filters.source" >&2
    echo "Forward-pointer: PR-STOCK-SEARCH-SOURCE-FILTER (source filter bypassed or media_assets.source='stock' missing)" >&2
  fi
  exit 1
fi

# Primary tally F=2: source=stock record has score > 0 (per godlike/07 NO-FAKE-AVAILABILITY).
SCORED_COUNT=$(jq '[.items[]? | select(.source == "stock" and .score != null and (.score | type) == "number" and .score > 0)] | length' "$OUT_JSON" 2>>"$JQ_ERR_LOG" || { echo "0"; WARN_JQ=1; })

if [ "$SCORED_COUNT" -lt 1 ]; then
  echo "FAIL: $STOCK_COUNT source=stock records but zero with valid score > 0" >&2
  echo "Canonical owner: internal/application/search/types_result.go::Candidate.Score (float64, non-omitempty)" >&2
  echo "Forward-pointer: PR-STOCK-SEARCH-SCORE-OWNERSHIP (scoring port wired to silent-zero default)" >&2
  exit 1
fi

# -------------------------------------------------------------------
# PASS: emit jq-formatted verbose output (per user literal: output jq formattato)
# -------------------------------------------------------------------
{
  echo "============================================="
  echo "STK-E2E-F (Phase F: unified_search) PASS"
  echo "============================================="
  echo "Endpoint:    POST $BASE/api/media/search"
  echo "Query:       'boxing training gym stock footage' (sources=[stock], mode=hybrid, limit=10)"
  echo "Total hits:  $TOTAL_COUNT"
  echo "Source=stock: $STOCK_COUNT (tally F=1 OK)"
  echo "Scored:      $SCORED_COUNT (tally F=2 OK)"
  echo "============================================="
  echo "Top-3 results (.results[:3]):"
  echo "--------------------------------------------"
  jq '.items[:3]' "$OUT_JSON"
  echo "============================================="
} > "$FORMATTED"
cat "$FORMATTED"
if [ "$WARN_JQ" -eq 1 ] || [ -s "$JQ_ERR_LOG" ]; then
  echo "" >&2
  echo "WARN: jq stderr captured during assertions:" >&2
  cat "$JQ_ERR_LOG" >&2 || true
fi

exit 0

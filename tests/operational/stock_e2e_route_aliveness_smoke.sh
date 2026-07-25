#!/usr/bin/env bash
# stock_e2e_route_aliveness_smoke.sh
#
# STK-E2E-A probe for architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05
# Single POST /api/stock-pipeline/run with EMPTY payload {} asserting HTTP 400
# (NOT 404). The empty payload is the canonical BindJSON validation trigger:
#   - HTTP 400 means: route IS registered + BindJSON IS wired + validation IS
#     enforced (the canonical healthy state)
#   - HTTP 404 means: stock module NOT mounted in gateway (PR-STOCK-ROUTE-REGISTRATION)
#   - HTTP 200 silently accepting empty payload means: route mounted but
#     BindJSON validation bypassed (godlike/07 NO-FAKE-AVAILABILITY silent-success
#     anti-pattern; PR-STOCK-BINDJSON-VALIDATION-BYPASS)
#   - HTTP 503 means: handler exists but composition root not wired
#     (PR-STOCK-COMPOSITION-WIRE)
#   - HTTP 401/403 means: bearer token wrong or admin route misconfigured
#     (PR-STOCK-AUTH-CHECK)
#
# Per godlike/06 SSOT one-canonical-owner-per-fact: the
# /api/stock-pipeline/run route canonical owner is
# `internal/api/assets/stock/handler.go::HandleRun` (per STK-E2E-C closure).
#
# **Idempotent**: re-runnable without side-effects. Pure GET-equiv smoke:
# no DB writes, no job submission, no resource mutation. Safe to run
# repeatedly (and recommended per godlike/06 SSOT monitoring pattern).
#
# Exit codes per action-plan §5:
#   0 = PASS (HTTP 400 with BindJSON validation diagnostic in body)
#   1 = FAIL (HTTP 404 / 503 / 200 silent-success / 401-403 / unexpected)
#   2 = prereq missing (server down / token wrong / curl/jq absent)
#
# Self-checks: `bash -n tests/operational/stock_e2e_route_aliveness_smoke.sh`
# must exit 0 (validated at commit time per §5).
#
# Overridable env vars:
#   BASE  = http://127.0.0.1:8000   (PipelineGen API root)
#   AUTH  = "Authorization: Bearer ${VELOX_ADMIN_TOKEN:-}"

set -euo pipefail


# ─── Fail-closed auth gate (AGENTS.md "no-fake-availability") ───────────
# If VELOX_ADMIN_TOKEN is unset or empty, refuse to run. The canonical
# loader is `scripts/with-velox-auth`; the Makefile-level auth-check
# target runs the same loader against /api/artlist/job-consumer as a
# pre-flight gate. The historical placeholder `test-admin-token-12345`
# is forbidden by AGENTS.md and must never appear in this script or any
# other operational surface again — see AGENTS.md "Authentication SSOT".
: "${VELOX_ADMIN_TOKEN:?❌ VELOX_ADMIN_TOKEN unset — source scripts/with-velox-auth (or export manually before rerunning).}"

# ---- Configuration --------------------------------------------------------
BASE="${BASE:-http://127.0.0.1:8000}"
AUTH="${AUTH:-Authorization: Bearer ${VELOX_ADMIN_TOKEN:-}}"

# ---- Prerequisite checks (exit 2) ----------------------------------------
command -v curl >/dev/null 2>&1 || { echo "FAIL: curl not on PATH (exit 2)" >&2; exit 2; }
command -v jq >/dev/null 2>&1   || { echo "FAIL: jq not on PATH (exit 2)" >&2; exit 2; }

# ---- Server reachability pre-flight (exit 2) ------------------------------
# Per code-reviewer round 1 (probe C): explicit endpoint logging + canonical
# route probe; bumped --max-time 5 -> 10 to avoid false down-flagging on
# slow warm-up. Includes the canonical /api/stock-pipeline/run route probe
# so the "server reachable but stock module not mounted" regression is
# caught at pre-flight (was previously masked by /healthz generic probe).

PROBE_ENDPOINTS=(
    "$BASE/health"
    "$BASE/api/stock-pipeline/run"
)

PREFLIGHT_OK=0
for endpoint in "${PROBE_ENDPOINTS[@]}"; do
    HTTP=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 \
        -X GET "$endpoint" 2>/dev/null || echo "000")
    case "$HTTP" in
        2*|3*|400|405)
            echo "PRE-FLIGHT: $endpoint -> HTTP $HTTP (reachable + route mounted)"
            PREFLIGHT_OK=1
            break
            ;;
        000)
            echo "PRE-FLIGHT: $endpoint -> unreachable (curl failed)"
            ;;
        *)
            echo "PRE-FLIGHT: $endpoint -> HTTP $HTTP (unusual, continuing)"
            ;;
    esac
done

if [ "$PREFLIGHT_OK" -eq 0 ]; then
    echo
    echo "FAIL: PipelineGen server at $BASE unreachable on all pre-flight endpoints (exit 2)" >&2
    echo "FAIL canonical: server down or VELOX_PORT misconfigured (per action plan §4)" >&2
    exit 2
fi

# ---- Header / logging ------------------------------------------------------
REQ_TAG="stock-route-aliveness-$(date +%s)"
OUT_JSON="/tmp/stock-tests/${REQ_TAG}.json"

echo "=================================================================="
echo "STK-E2E-A: stock route-aliveness smoke (single POST + assert HTTP 400)"
echo "  BASE    = $BASE"
echo "  REQ_TAG = $REQ_TAG"
echo "  payload = {} (empty JSON; idempotent GET-equiv smoke)"
echo "=================================================================="

# ---- Single POST /api/stock-pipeline/run with empty JSON body -------------
# Build payload via jq @json (heredoc-injection-safe even for empty {}).
PAYLOAD=$(jq -n '{}')

HTTP=$(curl -sS -X POST "$BASE/api/stock-pipeline/run" \
    -H "$AUTH" \
    -H "Content-Type: application/json" \
    --data "$PAYLOAD" \
    -o "$OUT_JSON" \
    -w '%{http_code}' \
    --max-time 30)

echo
echo "POST $BASE/api/stock-pipeline/run (empty payload) -> HTTP $HTTP"
echo "--- response body ---"
if [ -s "$OUT_JSON" ]; then
    jq . "$OUT_JSON" 2>/dev/null || cat "$OUT_JSON"
else
    echo "(empty body)"
fi
echo "--- end response body ---"

# ---- Assert / canonical PR-STOCK-* failure handling -----------------------
case "$HTTP" in
    400)
        echo
        echo "PASS: HTTP 400 with empty payload = BindJSON validation correctly wired"
        echo "Receipt: $OUT_JSON"
        echo "Idempotency: pure GET-equiv smoke; re-runnable without side-effects"
        exit 0
        ;;
    404)
        echo
        echo "FAIL: $BASE/api/stock-pipeline/run returned HTTP 404" >&2
        echo "FAIL canonical: PR-STOCK-ROUTE-REGISTRATION" >&2
        echo "  Suggested fix: register /api/stock-pipeline/run route in gateway" >&2
        echo "  Canonical owner: internal/api/assets/stock/handler.go::HandleRun (handler exists but" >&2
        echo "    not mounted in app/registry.go -> registerInternalModules / registerArtlist)" >&2
        echo "  Likely root causes:" >&2
        echo "    1. feature flag disabled (VELOX_FEATURE_STOCK=false)" >&2
        echo "    2. routing typo (handler at /api/stock-pipeline/run vs /api/stock/run)" >&2
        echo "    3. registry import missing (internal/api/assets/stock/handler.go not in composition root)" >&2
        exit 1
        ;;
    503)
        echo
        echo "FAIL: $BASE/api/stock-pipeline/run returned HTTP 503" >&2
        echo "FAIL canonical: PR-STOCK-COMPOSITION-WIRE" >&2
        echo "  Suggested fix: wire jobs.Service into stock handler composition root" >&2
        echo "  Canonical owner: internal/app/build_bundles_stock.go::WireAssets" >&2
        exit 1
        ;;
    200)
        # Per godlike/07 NO-FAKE-AVAILABILITY: a silent-accept of empty
        # payload is the canonical anti-pattern (route mounted, validation
        # bypassed). FAIL-CLOSED hard to surface the regression.
        echo
        echo "FAIL: HTTP 200 silently accepted empty payload (godlike/07 NO-FAKE-AVAILABILITY silent-success anti-pattern)" >&2
        echo "FAIL canonical: PR-STOCK-BINDJSON-VALIDATION-BYPASS" >&2
        echo "  Suggested fix: enable BindJSON required-tag on StockRunPayload" >&2
        echo "  Canonical owner: internal/api/assets/stock/handler.go::HandleRun + types.go" >&2
        echo "  Body preserved: $OUT_JSON" >&2
        exit 1
        ;;
    401|403)
        echo
        echo "FAIL: $BASE/api/stock-pipeline/run returned HTTP $HTTP (auth failure)" >&2
        echo "FAIL canonical: PR-STOCK-AUTH-CHECK (wrong bearer token or admin route misconfigured)" >&2
        echo "  Suggested fix: confirm VELOX_ADMIN_TOKEN=${AUTH##* } matches server cfg.AdminToken" >&2
        exit 1
        ;;
    *)
        echo
        echo "FAIL: $BASE/api/stock-pipeline/run returned unexpected HTTP $HTTP" >&2
        echo "FAIL canonical: PR-STOCK-ROUTE-REGISTRATION-or-other (investigate gating layer)" >&2
        echo "Receipt: $OUT_JSON" >&2
        exit 1
        ;;
esac

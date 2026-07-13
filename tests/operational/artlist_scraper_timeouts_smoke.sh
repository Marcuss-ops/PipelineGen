#!/usr/bin/env bash
# artlist_scraper_timeouts_smoke.sh — fail-loud first-packet probe for artlist-scraper.
#
# Per fix(scraper) PR (split connect/total timeout, raise SCROLL_TIMEOUT to 120):
# assert that the scraper's /health endpoint completes a TCP connect within
# TARGET_FIRST_PACKET_S seconds. Connect-timeout is enforced via --connect-timeout
# (= SCRAPER_CONNECT_TIMEOUT_SECONDS, default 5) and the failing condition is a
# 1-second slack envelope so a slow-but-OK connect is not a hard false-positive.
#
# Per godlike/07 NO-FAKE-AVAILABILITY + minimum-blast-radius: this test ONLY
# asserts the timing surface (time_connect = TCP-handshake-only). HTTP semantics
# (healthy=true vs false) are owned by tests/operational/artlist_scraper_failure_smoke.sh.
#
# Usage:
#   bash tests/operational/artlist_scraper_timeouts_smoke.sh
#
# Exit codes (per AGENTS.md "Fail-closed" rule):
#   0 — scraper reachable; TCP-connect time_connect < TARGET_FIRST_PACKET_S (default 6s).
#   1 — scraper unreachable OR time_connect >= TARGET_FIRST_PACKET_S OR HTTP code out of range.
#
# Canonical references:
#   - docs/operations/stock-e2e-runbook.md §11.0 (env contract SSOT).
#   - tests/operational/artlist_live_e2e_verify.sh (uses the same env var names).

set -uo pipefail

SCRAPER_URL="${VELOX_ARTLIST_SCRAPER_SERVER_URL:-http://127.0.0.1:9123}"
CONNECT_BUDGET_S="${SCRAPER_CONNECT_TIMEOUT_SECONDS:-5}"     # §11.0 doc-public default
TARGET_S="${TARGET_FIRST_PACKET_S:-6}"                       # connect-budget + 1s slack (godlike/07)
HEALTH_PATH="${SCRAPER_HEALTH_PATH:-/health}"

echo "[INFO] Probing ${SCRAPER_URL}${HEALTH_PATH} (target time_connect < ${TARGET_S}s; connect budget ${CONNECT_BUDGET_S}s)"

# Probe 1: split connect/total timeout. `--write-out '%{http_code} %{time_connect}'`
# emits the TCP-connect time only (NOT body TTFB). Don't use time_starttransfer —
# the scraper warm-up can take >30s and isn't what this probe measures.
PROBE_OUT="$(mktemp)"
trap 'rm -f "${PROBE_OUT}"' EXIT

set +e
HTTP_CODE_TIME=$(curl -sS \
    --connect-timeout "${CONNECT_BUDGET_S}" \
    --max-time 30 \
    -o "${PROBE_OUT}" \
    -w '%{http_code} %{time_connect}\n' \
    "${SCRAPER_URL}${HEALTH_PATH}" 2>/dev/null)
CURL_EXIT=$?
set -e

# Fail-loud: classify the curl exit code into a stable error kind.
if [[ "${CURL_EXIT}" -ne 0 ]]; then
    case "${CURL_EXIT}" in
        7)  ERR_KIND='connection-refused' ;;
        28) ERR_KIND='timeout-total-or-connect' ;;
        35) ERR_KIND='ssl-handshake-failed' ;;
        6)  ERR_KIND='host-unresolved' ;;
        *)  ERR_KIND="curl-exit-code-${CURL_EXIT}" ;;
    esac
    echo "[FAIL] scraper unreachable: ${ERR_KIND} (curl exit=${CURL_EXIT})" >&2
    echo "[INFO] probe target: ${SCRAPER_URL}${HEALTH_PATH}" >&2
    exit 1
fi

HTTP_CODE=$(echo "${HTTP_CODE_TIME}"  | awk '{print $1}')
TIME_CONNECT=$(echo "${HTTP_CODE_TIME}" | awk '{print $2}')

# Probe 2: HTTP code shape sanity (any 1xx/2xx/3xx/4xx/5xx is fine; this probe is timing-only).
if [[ "${HTTP_CODE}" -lt 100 || "${HTTP_CODE}" -ge 600 ]]; then
    echo "[FAIL] scraper unreachable: HTTP code out of range (${HTTP_CODE})" >&2
    exit 1
fi

# Probe 3: time_connect < TARGET_S (default 6s). Strictly less so we catch
# "exactly at the budget" as a fail-loud per godlike/07 fail-closed surface.
if awk -v t="${TIME_CONNECT}" -v lim="${TARGET_S}" 'BEGIN { exit !(t+0 < lim+0) }'; then
    echo "[PASS] scraper reachable: HTTP=${HTTP_CODE} time_connect=${TIME_CONNECT}s < ${TARGET_S}s"
    exit 0
fi

echo "[FAIL] scraper unreachable: time_connect=${TIME_CONNECT}s >= ${TARGET_S}s budget" >&2
echo "[INFO] probe target: ${SCRAPER_URL}${HEALTH_PATH}; connect-budget=${CONNECT_BUDGET_S}s" >&2
exit 1

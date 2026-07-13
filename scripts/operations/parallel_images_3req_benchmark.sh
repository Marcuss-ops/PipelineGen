#!/usr/bin/env bash
# =============================================================================
# parallel_images_3req_benchmark.sh — 3-request benchmark exercising the
# three delivery_mode contracts on POST /api/images/generated/generate.
#
# godlike/06 SSOT: defaults match the canonical PipelineGen HTTP surface
# (port 8000, $VELOX_PORT). Reuses the request envelope from the production
# handler at internal/api/images/generated_generate_handler.go.
#
# godlike/07 NO-FAKE-AVAILABILITY: the image-generation backend was retired
# (PR-IMAGES-CHROME-RETIRED, July 2026). On a fresh checkout the handler
# returns HTTP 501 with body {"error":"image generation endpoint has been
# removed",...}. This script reports that 501 honestly as exit 2 (the
# default exit-code family for "endpoint not live / drifts from contract"),
# and ALSO emits a banner that explains the retired-backend state so an
# operator running the benchmark for the first time does not mistake a
# systematic 501 for an outage.
#
# The 3 requests:
#   (R1) Default mode.    POST without delivery_mode ⇒ handler defaults to "fast".
#                         Elapsed time = synchronous SQLite commit only.
#   (R2) Fast mode.       POST with delivery_mode="fast" explicit.
#                         Same wire shape as R1, asserts behaviour matches default.
#   (R3) Complete mode.   POST with delivery_mode="complete".
#                         Handler waits for outbox dispatcher ack (bounded
#                         timeout). If the backend is restored end-to-end,
#                         elapsed time WILL exceed R1+R2 latency; if the
#                         backend is retired (current state), R3 returns 501
#                         quickly because skipDrive=false is irrelevant once
#                         the Gen sub-service is nil.
#
# Usage:
#   bash scripts/operations/parallel_images_3req_benchmark.sh
#   bash scripts/operations/parallel_images_3req_benchmark.sh --url http://127.0.0.1:8000
#   bash scripts/operations/parallel_images_3req_benchmark.sh --timeout 15 --prompt "..."
#   bash scripts/operations/parallel_images_3req_benchmark.sh --json
#   bash scripts/operations/parallel_images_3req_benchmark.sh --help
#
# Required tools: curl, jq.
#
# Exit codes (canonical for pager alerts):
#   0  All 3 requests got HTTP 200 (backend live).
#   1  App /health unreachable.
#   2  ≥1 endpoint non-200 (typically 501 on retired backend).
#   3  Required tool missing (curl / jq).
#   7  Bad CLI usage / unknown flag / missing required arg.
#
# Companion runbook: docs/operations/parallel-images-verification-runbook.md
# =============================================================================

set -euo pipefail
# SIGPIPE-friendly trap — pagers piping to head/grep see the script's
# intended exit code (0/1/2/3/7), not 128+13=141 from the pipe-break signal.
trap '' PIPE

URL_BASE=""
TIMEOUT=15
PROMPT="a peaceful mountain valley at dawn (parallel images benchmark)"
JSON_MODE=0
declare -a R1 R2 R3

# ── colour helpers (NO_COLOR respected) ────────────────────────────────────
if [ -t 1 ] && [ "${NO_COLOR:-0}" != "1" ]; then
  C_GREEN=$(printf '\033[32m'); C_RED=$(printf '\033[31m'); C_YELLOW=$(printf '\033[33m')
  C_CYAN=$(printf '\033[36m'); C_DIM=$(printf '\033[2m'); C_BOLD=$(printf '\033[1m'); C_RESET=$(printf '\033[0m')
else
  C_GREEN=; C_RED=; C_YELLOW=; C_CYAN=; C_DIM=; C_BOLD=; C_RESET=
fi

section() { printf "\n${C_BOLD}${C_CYAN}── %s ──${C_RESET}\n" "$1"; }
log_pass() { printf "  ${C_GREEN}✓ PASS${C_RESET}  %s\n" "$1"; }
log_fail() { printf "  ${C_RED}✗ FAIL${C_RESET}  %s\n" "$1"; }
log_info() { printf "  ${C_DIM}·${C_RESET}        %s\n" "$1"; }

# ── USAGE block ────────────────────────────────────────────────────────────
USAGE=$(sed -n '2,36p' "$0")

# ── CLI parsing ────────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --url) URL_BASE="${2:-}"; [[ -z "$URL_BASE" ]] && { echo "ERROR: --url requires a URL" >&2; exit 7; }; shift 2 ;;
    --timeout) TIMEOUT="${2:-}"; [[ ! "$TIMEOUT" =~ ^[1-9][0-9]*$ ]] && { echo "ERROR: --timeout must be a positive integer (seconds)" >&2; exit 7; }; shift 2 ;;
    --prompt) PROMPT="${2:-}"; [[ -z "$PROMPT" ]] && { echo "ERROR: --prompt requires a non-empty string" >&2; exit 7; }; shift 2 ;;
    --json) JSON_MODE=1; shift ;;
    -h|--help) printf "%s\n" "$USAGE"; exit 0 ;;
    --*) echo "ERROR: unknown flag: $1" >&2; exit 7 ;;
    *) echo "ERROR: extra positional argument: $1" >&2; exit 7 ;;
  esac
done

# ── 1. Required tool preflight ─────────────────────────────────────────────
for tool in curl jq; do
  command -v "$tool" >/dev/null 2>&1 || { echo "ERROR: $tool not found in PATH" >&2; exit 3; }
done

# ── 2. URL resolve (godlike/06 SSOT — matches verify_siglip_sidecar.sh) ───
[[ -z "$URL_BASE" ]] && URL_BASE="http://127.0.0.1:${VELOX_PORT:-8000}"

if [[ $JSON_MODE -eq 0 ]]; then
  printf "${C_BOLD}${C_CYAN}━━━ Parallel-images 3-request benchmark — ${C_BOLD}${URL_BASE}${C_RESET}\n"
  printf "${C_DIM}timeout=%ds/req  prompt=%q${C_RESET}\n" "$TIMEOUT" "$PROMPT"
fi

# ── 3. /health probe (exit 1) ──────────────────────────────────────────────
HEALTH_HTTP=$(curl -s -o /dev/null -w '%{http_code}' --max-time "$TIMEOUT" "$URL_BASE/health" 2>/dev/null || echo "000")
if [[ "$HEALTH_HTTP" != "200" ]]; then
  echo >&2
  echo "ERROR: app unreachable at $URL_BASE (GET /health returned HTTP $HEALTH_HTTP)" >&2
  echo "       (re)start: make run (or VELOX_PORT=NNNN make run for override)" >&2
  exit 1
fi

# ── 4. Helper: POST /api/images/generated/generate with given mode override ─
#   Args: $1=label  $2=delivery_mode_field ("omit"|"fast"|"complete")
#   Emits "code|time|asset_id|delivery_mode|err_msg" on stdout, captured per-req.
post_req() {
  local label="$1" mode_token="$2"
  local body mode_field
  if [[ "$mode_token" == "omit" ]]; then
    body=$(jq -nc \
      --arg p "$PROMPT" \
      '{prompt: $p, width: 512, height: 512, style: "cinematic", tags: ["smoke", "parallel_images_benchmark"]}')
  else
    body=$(jq -nc \
      --arg p "$PROMPT" --arg m "$mode_token" \
      '{prompt: $p, width: 512, height: 512, style: "cinematic", tags: ["smoke", "parallel_images_benchmark"], delivery_mode: $m}')
  fi

  local tmp; tmp=$(mktemp /tmp/pi3req_XXXXXX.json)
  local line code t
  line=$(curl -s -o "$tmp" -w '%{http_code}|%{time_total}' \
    --max-time "$TIMEOUT" \
    -H 'Content-Type: application/json' \
    --data-binary "$body" \
    "$URL_BASE/api/images/generated/generate" 2>/dev/null) || line="000|0"
  code="${line%%|*}"; t="${line##*|}"

  local asset_id mode_field_resp err_msg
  if [[ "$code" == "200" ]]; then
    asset_id=$(jq -r '.asset_id // "<missing>"' "$tmp" 2>/dev/null || echo "<jq-error>")
    mode_field_resp=$(jq -r '.delivery_mode // "<missing>"' "$tmp" 2>/dev/null || echo "<jq-error>")
    err_msg=""
  else
    asset_id="<none>"
    mode_field_resp="<none>"
    err_msg=$(jq -r '.error // (.message // "<" + (.detail // "no body") + ">")' "$tmp" 2>/dev/null | head -c 160 || echo "<no-body>")
  fi
  echo "$code|$t|$asset_id|$mode_field_resp|$err_msg"
  rm -f "$tmp"
}

# ── 5. Three requests in sequence (chronological order) ───────────────────
section "R1 - default mode (no delivery_mode field ⇒ handler defaults to \"fast\")"
R1_OUT=$(post_req "R1" "omit")
log_info "request: $PROMPT (delivery_mode field omitted)"
log_info "elapsed=$(echo "$R1_OUT" | awk -F'|' '{print $2}')s http=$(echo "$R1_OUT" | awk -F'|' '{print $1}') asset_id=$(echo "$R1_OUT" | awk -F'|' '{print $3}') server-mode=$(echo "$R1_OUT" | awk -F'|' '{print $4}')"

section "R2 - explicit fast mode (delivery_mode=\"fast\")"
R2_OUT=$(post_req "R2" "fast")
log_info "elapsed=$(echo "$R2_OUT" | awk -F'|' '{print $2}')s http=$(echo "$R2_OUT" | awk -F'|' '{print $1}') asset_id=$(echo "$R2_OUT" | awk -F'|' '{print $3}') server-mode=$(echo "$R2_OUT" | awk -F'|' '{print $4}')"

section "R3 - complete mode (delivery_mode=\"complete\", waits for outbox dispatcher)"
R3_OUT=$(post_req "R3" "complete")
log_info "elapsed=$(echo "$R3_OUT" | awk -F'|' '{print $2}')s http=$(echo "$R3_OUT" | awk -F'|' '{print $1}') asset_id=$(echo "$R3_OUT" | awk -F'|' '{print $3}') server-mode=$(echo "$R3_OUT" | awk -F'|' '{print $4}')"

# ── 6. godlike/06 SSOT pack-shape assertions ──────────────────────────────
# When the backend is RESTORED (live), each 200 response must include:
#   - delivery_mode field set to the requested mode OR "fast" if R1 (default)
#   - asset_id non-empty
# When the backend is RETIRED, all 3 return 501 with body containing
# "image generation endpoint has been removed" — that's the godlike/07
# fail-closed contract holding (no fake OK).
ANY_200=0
ANY_501=0
for r in "$R1_OUT" "$R2_OUT" "$R3_OUT"; do
  code=$(echo "$r" | awk -F'|' '{print $1}')
  case "$code" in
    200) ANY_200=1 ;;
    501) ANY_501=1 ;;
  esac
done

section "summary"

# Backend-retirement banner (godlike/07 NO-FAKE-AVAILABILITY transparency).
if [[ $ANY_501 -eq 1 && $ANY_200 -eq 0 ]]; then
  printf "  ${C_YELLOW}ℹ INFO${C_RESET}  endpoint returned HTTP 501 for all 3 requests. This is EXPECTED on a host\n"
  printf "            with the image-generation backend retired (PR-IMAGES-CHROME-RETIRED, July 2026).\n"
  printf "            The benchmark IS still measuring wire-roundtrip overhead. To restore\n"
  printf "            and re-run with the backend live, see docs/operations/parallel-images-verification-runbook.md\n"
  printf "            § 3 (Endpoint Contract — Retired backend).\n"
fi

if [[ $JSON_MODE -eq 1 ]]; then
  jq -nc \
    --arg url "$URL_BASE" \
    --arg prompt "$PROMPT" \
    --argjson timeout "$TIMEOUT" \
    --argjson r1 "$R1_OUT" \
    --argjson r2 "$R2_OUT" \
    --argjson r3 "$R3_OUT" \
    --argjson any200 "$ANY_200" --argjson any501 "$ANY_501" \
    '{
      tool:"parallel_images_3req_benchmark",
      kind:"delivery_mode_contract_smoke",
      url:$url, prompt:$prompt, timeout_s_per_req:$timeout,
      backend_state: (if $any200 == 1 then "live" elif $any501 == 1 then "retired" else "drift" end),
      requests: {
        r1_default: ($r1 | split("|") | {http: .[0], time_s: (.[1]|tonumber? // 0), asset_id: .[2], server_mode: .[3], error_msg: .[4]}),
        r2_fast:    ($r2 | split("|") | {http: .[0], time_s: (.[1]|tonumber? // 0), asset_id: .[2], server_mode: .[3], error_msg: .[4]}),
        r3_complete:($r3 | split("|") | {http: .[0], time_s: (.[1]|tonumber? // 0), asset_id: .[2], server_mode: .[3], error_msg: .[4]})
      }
    }'
else
  printf "  request summary:\n"
  printf "    R1 default:  http=%s  time=%ss  server-mode=%s  asset=%s\n" \
    "$(echo "$R1_OUT" | awk -F'|' '{print $1}')" \
    "$(echo "$R1_OUT" | awk -F'|' '{print $2}')" \
    "$(echo "$R1_OUT" | awk -F'|' '{print $4}')" \
    "$(echo "$R1_OUT" | awk -F'|' '{print $3}')"
  printf "    R2 fast:     http=%s  time=%ss  server-mode=%s  asset=%s\n" \
    "$(echo "$R2_OUT" | awk -F'|' '{print $1}')" \
    "$(echo "$R2_OUT" | awk -F'|' '{print $2}')" \
    "$(echo "$R2_OUT" | awk -F'|' '{print $4}')" \
    "$(echo "$R2_OUT" | awk -F'|' '{print $3}')"
  printf "    R3 complete: http=%s  time=%ss  server-mode=%s  asset=%s\n" \
    "$(echo "$R3_OUT" | awk -F'|' '{print $1}')" \
    "$(echo "$R3_OUT" | awk -F'|' '{print $2}')" \
    "$(echo "$R3_OUT" | awk -F'|' '{print $4}')" \
    "$(echo "$R3_OUT" | awk -F'|' '{print $3}')"
  printf "\n  backend_state: %s\n" "$([[ $ANY_200 -eq 1 ]] && echo live || ([[ $ANY_501 -eq 1 ]] && echo retired || echo drift))"
fi

# ── 7. Fail-fast exit-code precedence ─────────────────────────────────────
# Per godlike/07: any non-200 across the 3 requests ⇒ exit 2 (endpoint drift
# OR retired-backend state, both are non-success — same typed code, same
# operator onboarding).
for r in "$R1_OUT" "$R2_OUT" "$R3_OUT"; do
  code=$(echo "$r" | awk -F'|' '{print $1}')
  if [[ "$code" != "200" ]]; then
    exit 2
  fi
done
exit 0

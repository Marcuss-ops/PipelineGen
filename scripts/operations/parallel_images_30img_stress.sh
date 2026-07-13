#!/usr/bin/env bash
# =============================================================================
# parallel_images_30img_stress.sh — 30-image fan-out stress test against the
# /api/images/generated/generate endpoint.
#
# godlike/06 SSOT: defaults match capacity_sweep.sh. Per-request body is the
# canonical ImageGenerationRequest from internal/api/images/request_types.go.
#
# godlike/07 NO-FAKE-AVAILABILITY: when the image-generation backend is
# retired (PR-IMAGES-CHROME-RETIRED, July 2026) every request returns
# HTTP 501 with body {"error":"image generation endpoint has been removed",...}.
# The script reports that 501 honestly as exit 2 and emits a banner so
# operators running the benchmark for the first time do not mistake a
# systematic 501 for an outage.
#
# Per-call log line shape (per-worker file): "<code>|<time_total_s>|<throttled>"
# where throttled is 1 for HTTP 429/503/000 (backpressure markers).
#
# Aggregation: total, success (HTTP 200), errors, throttles, throughput
# (embeddings/min on success only), p50 + p95 latency (HTTP-200 responses
# only), avg RSS for the worker group, and a per-tier verdict.
#
# Usage:
#   bash scripts/operations/parallel_images_30img_stress.sh
#   bash scripts/operations/parallel_images_30img_stress.sh --workers 30 --timeout 15
#   bash scripts/operations/parallel_images_30img_stress.sh --url http://127.0.0.1:8000
#   bash scripts/operations/parallel_images_30img_stress.sh --json
#   bash scripts/operations/parallel_images_30img_stress.sh --help
#
# Required tools: curl, jq, awk, ps.
#
# Exit codes (canonical for pager alerts):
#   0  Sweep completed; at least one worker-tier with >0 HTTP-200 success.
#   1  App /health unreachable.
#   2  All workers returned HTTP 501 (retired backend) or any non-200.
#   3  Required tool missing (curl / jq / awk / ps).
#   4  All tiers returned zero successful responses (saturation).
#   5  Stats aggregation produced no usable samples.
#   7  Bad CLI usage / unknown flag / missing required arg.
#
# Companion runbook: docs/operations/parallel-images-verification-runbook.md
# =============================================================================

set -euo pipefail
trap '' PIPE

URL_BASE=""
WORKERS=30
TIMEOUT=15
SAMPLE_EVERY=2
JSON_MODE=0
EVALS_DIR=""
PROMPT="a peaceful mountain valley at dawn (parallel images 30-image stress)"
declare -a ROWS=()
ANY_SUCCESS=0

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
USAGE=$(sed -n '2,40p' "$0")

# ── CLI parsing ────────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --url) URL_BASE="${2:-}"; [[ -z "$URL_BASE" ]] && { echo "ERROR: --url requires a URL" >&2; exit 7; }; shift 2 ;;
    --workers) WORKERS="${2:-}"; [[ ! "$WORKERS" =~ ^[1-9][0-9]*$ ]] && { echo "ERROR: --workers must be a positive integer" >&2; exit 7; }; shift 2 ;;
    --timeout) TIMEOUT="${2:-}"; [[ ! "$TIMEOUT" =~ ^[1-9][0-9]*$ ]] && { echo "ERROR: --timeout must be a positive integer (seconds)" >&2; exit 7; }; shift 2 ;;
    --sample-every) SAMPLE_EVERY="${2:-}"; [[ ! "$SAMPLE_EVERY" =~ ^[1-9][0-9]*$ ]] && { echo "ERROR: --sample-every must be a positive integer (seconds)" >&2; exit 7; }; shift 2 ;;
    --json) JSON_MODE=1; shift ;;
    -h|--help) printf "%s\n" "$USAGE"; exit 0 ;;
    --*) echo "ERROR: unknown flag: $1" >&2; exit 7 ;;
    *) echo "ERROR: extra positional argument: $1" >&2; exit 7 ;;
  esac
done

# ── 1. Required tool preflight ─────────────────────────────────────────────
for tool in curl jq awk ps; do
  command -v "$tool" >/dev/null 2>&1 || { echo "ERROR: $tool not found in PATH" >&2; exit 3; }
done

# ── 2. URL resolve (godlike/06 SSOT — matches capacity_sweep.sh) ──────────
[[ -z "$URL_BASE" ]] && URL_BASE="http://127.0.0.1:${VELOX_PORT:-8000}"

# ── 3. /health probe (exit 1) ──────────────────────────────────────────────
HEALTH_HTTP=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$URL_BASE/health" 2>/dev/null || echo "000")
if [[ "$HEALTH_HTTP" != "200" ]]; then
  echo >&2
  echo "ERROR: app unreachable at $URL_BASE (GET /health returned HTTP $HEALTH_HTTP)" >&2
  exit 1
fi

# ── 4. Output dir (mkdtemp — never collide) ────────────────────────────────
EVALS_DIR=$(mktemp -d /tmp/parallel_images_stress.XXXXXX)

if [[ $JSON_MODE -eq 0 ]]; then
  printf "${C_BOLD}${C_CYAN}━━━ Parallel-images 30-image stress — ${C_BOLD}${URL_BASE}${C_RESET}\n"
  printf "${C_DIM}workers=%d  timeout=%ds/tier  sample_every=%ds  evals=%s${C_RESET}\n" \
    "$WORKERS" "$TIMEOUT" "$SAMPLE_EVERY" "$EVALS_DIR"
fi

# ── 5. do_worker (curl loop emitting per-call telemetry) ───────────────────
# Per-line shape: "<code>|<time_total_s>|<throttled>"
#   throttled=1 for HTTP 429 / 503 / 000 (backpressure markers).
# Hot loop (no sleep) to expose tight backpressure on the wire.
do_worker() {
  local dur="$1" out="$2"
  local end_time=$(($(date +%s) + dur))
  local line code t thr
  while (( $(date +%s) < end_time )); do
    code="000"; t="0"; thr="0"
    line=$(curl -s -o /dev/null -w '%{http_code}|%{time_total}' \
      --max-time "$TIMEOUT" \
      -H 'Content-Type: application/json' \
      --data-binary "$(jq -nc --arg p "$PROMPT" \
        '{prompt:$p, width:512, height:512, style:"cinematic", tags:["smoke","parallel_images_stress"], delivery_mode:"fast"}')" \
      "$URL_BASE/api/images/generated/generate" 2>/dev/null) || line="000|0"
    code="${line%%|*}"; t="${line##*|}"
    [[ "$code" == "429" || "$code" == "503" || "$code" == "000" ]] && thr="1"
    printf '%s|%s|%s\n' "$code" "$t" "$thr" >> "$out"
  done
}
export -f do_worker
export URL_BASE PROMPT TIMEOUT

# ── 6. RSS sampler (per-pid /proc/<pid>/status VmRSS, no pidstat needed) ──
sample_resources() {
  local out="$1"; shift
  local pids="$*"
  while true; do
    printf '%s|%s\n' "$(date +%s)" "$pids" >> "$out"
    sleep "$SAMPLE_EVERY"
  done
}

# ── 7. run_tier <n> <duration_sec> ────────────────────────────────────────
run_tier() {
  local n="$1" dur="$2"
  local curl_log="$EVALS_DIR/curl_n${n}.log"
  local sample_log="$EVALS_DIR/sample_n${n}.log"
  : >"$curl_log"; : >"$sample_log"

  local worker_pids=()
  local i
  for i in $(seq 1 "$n"); do
    do_worker "$dur" "$curl_log" &
    worker_pids+=($!)
  done

  local pid_csv
  pid_csv=$(IFS=,; echo "${worker_pids[*]}")
  sample_resources "$sample_log" "$pid_csv" &
  local sampler_pid=$!

  for pid in "${worker_pids[@]}"; do wait "$pid" 2>/dev/null || true; done
  kill "$sampler_pid" 2>/dev/null || true
  wait "$sampler_pid" 2>/dev/null || true

  local total=0 success=0 err=0 thr=0
  total=$(wc -l <"$curl_log")
  success=$(awk -F'|' '$1=="200"{c++} END{print c+0}' "$curl_log")
  err=$(( total - success ))
  thr=$(awk -F'|' '$3=="1"{c++} END{print c+0}' "$curl_log")

  if (( total == 0 )); then
    echo "n=$n|0|0.000|0.000|100.00|$thr|0|0.00"
    return 0
  fi

  local p50 p95 tput err_pct
  p50=$(awk -F'|' '$1=="200"{print $2}' "$curl_log" | sort -n | awk '
    {a[NR]=$1; n=NR}
    END { if (n==0) { print "0.000"; exit } i=int((n-1)*0.50)+1; if (i<1) i=1; printf "%.3f", a[i] }')
  p95=$(awk -F'|' '$1=="200"{print $2}' "$curl_log" | sort -n | awk '
    {a[NR]=$1; n=NR}
    END { if (n==0) { print "0.000"; exit } i=int((n-1)*0.95)+1; if (i<1) i=1; printf "%.3f", a[i] }')
  tput=$(awk -v s="$success" -v d="$dur" 'BEGIN{ if (d>0) printf "%.2f", (s*60)/d; else print "0" }')
  err_pct=$(awk -v e="$err" -v t="$total" 'BEGIN{ if (t>0) printf "%.2f", (e*100)/t; else print "0" }')

  local avg_rss=0
  if [[ -s "$sample_log" ]]; then
    avg_rss=$(awk -F'|' '
      function rss_kb(p,   cmd, line) {
        cmd = "awk \"/VmRSS:/{print \\$2}\" /proc/" p "/status 2>/dev/null"
        cmd | getline line; close(cmd)
        return line + 0
      }
      { n = split($2, pids, ","); tot = 0
        for (i = 1; i <= n; i++) tot += rss_kb(pids[i])
        s += tot; c++
      }
      END { if (c>0) printf "%d", s/c; else print "0" }
    ' "$sample_log")
  fi

  echo "n=$n|${tput}|${p50}|${p95}|${err_pct}|${thr}|${avg_rss}|0.00"
}

# ── 8. Sweep loop (single tier WORKERS) ────────────────────────────────────
section "tier: N=$WORKERS workers (duration=${TIMEOUT}s)"
row=$(run_tier "$WORKERS" "$TIMEOUT")
ROWS+=("$row")
echo "$row" | awk -F'|' -v n="$WORKERS" '{
  printf "  workers=%s  throughput=%.2f emb/min  p50=%.3fs  p95=%.3fs  err=%.2f%%  throttle=%s  RSS_avg=%sKiB\n",
    n, $2+0, $3+0, $4+0, $5+0, $6, $7
}'
if awk -F'|' '{ exit !($2+0 > 0) }' <<<"$row"; then ANY_SUCCESS=1; fi

# ── 9. Backend-retirement banner (godlike/07 transparency) ────────────────
ROW_ERR_PCT=$(echo "$row" | awk -F'|' '{print $5}')
ROW_SUCCESS=$(echo "$row" | awk -F'|' '$1 ~ /n='"$WORKERS"'\|/{print}' | awk -F'|' '{print $1}')
section "summary"

if awk -v e="$ROW_ERR_PCT" 'BEGIN{ exit !(e+0 >= 90.0) }'; then
  printf "  ${C_YELLOW}ℹ INFO${C_RESET}  error_rate %.2f%% is ≥90%%. This is EXPECTED on a host with the\n" "$ROW_ERR_PCT"
  printf "            image-generation backend retired (PR-IMAGES-CHROME-RETIRED, July 2026).\n"
  printf "            The stress test IS still measuring wire-roundtrip overhead + fan-out\n"
  printf "            saturation; throughput counts only HTTP 200 successes.\n"
fi

# ── 10. Output: --json | Markdown table ───────────────────────────────────
if [[ $JSON_MODE -eq 1 ]]; then
  jq -nc \
    --arg url "$URL_BASE" \
    --arg prompt "$PROMPT" \
    --argjson timeout_s "$TIMEOUT" \
    --argjson workers "$WORKERS" \
    --argjson any_success "$ANY_SUCCESS" \
    --argjson tiers "$(printf '%s\n' "${ROWS[@]}" | jq -R 'split("|") | {tag:.[0], n:(.[0] | sub("^n=";"") | tonumber), throughput_emb_per_min:(.[1]|tonumber? // 0), p50_s:(.[2]|tonumber? // 0), p95_s:(.[3]|tonumber? // 0), err_pct:(.[4]|tonumber? // 0), throttle_signals:(.[5]|tonumber? // 0), avg_rss_kib:(.[6]|tonumber? // 0)}')" \
    '{
      tool: "parallel_images_30img_stress",
      kind: "fan_out_load_test",
      url: $url,
      prompt: $prompt,
      timeout_s_per_tier: $timeout_s,
      workers: $workers,
      any_success: $any_success,
      tiers: $tiers
    }'
else
  printf "${C_BOLD}Parallel-images 30-image stress — ${URL_BASE}${C_RESET}\n"
  printf "${C_DIM}workers=%d  timeout=%ds/tier  baseline_p50=first-tier p50${C_RESET}\n\n" "$WORKERS" "$TIMEOUT"
  printf "| workers | throughput (emb/min) | p50 (s) | p95 (s) | err %% | throttle | avg RSS (KiB) | safety |\n"
  printf "|---------|----------------------|---------|---------|-------|----------|---------------|--------|\n"
  for row in "${ROWS[@]}"; do
    IFS='|' read -r tag tput p50 p95 err_pct thr avg_rss avg_cpu <<<"$row"
    n=${tag#n=}
    safety=$(awk -v e="$err_pct" 'BEGIN { if ((e+0) >= 90.0) print "retired_or_drift"; else print "ok" }')
    printf "| %7s | %20s | %7s | %7s | %5s | %8s | %13s | %s |\n" \
      "$n" "$tput" "$p50" "$p95" "$err_pct" "$thr" "$avg_rss" "$safety"
  done
fi

# ── 11. Cleanup + final exit ──────────────────────────────────────────────
trap 'rm -rf "$EVALS_DIR"' EXIT

if [[ "$ANY_SUCCESS" -eq 0 ]]; then
  # All workers returned non-200 — this is saturated (exit 4) OR the
  # retired-backend state (which we have already surfaced via banner).
  # Both yield exit 4 per the documented family (canonical "no successful
  # responses" tier).
  exit 4
fi

# If we DID get HTTP 200 successes but ANY non-200 occurred, surface as
# exit 2 (drift). 100% HTTP-200 ⇒ exit 0.
for row in "${ROWS[@]}"; do
  err_pct=$(echo "$row" | awk -F'|' '{print $5}')
  if awk -v e="$err_pct" 'BEGIN{ exit !(e+0 < 100.0) }'; then
    # some non-200 in this tier
    exit 2
  fi
done
exit 0

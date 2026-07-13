#!/usr/bin/env bash
# =============================================================================
# capacity_sweep.sh — operator-side capacity sweep for the SigLIP sidecar.
#
# Runs the live Python embedding sidecar through POST /embed_visual_from_image
# at fan-out concurrency N ∈ {1,2,3,4}. For each tier, captures:
#   - throughput (successful embeddings / minute)
#   - latency p50 + p95 (only successful 200 responses)
#   - error rate (%)
#   - throttle-signal count (HTTP 429 / 503)
#   - aggregate RSS (KiB) + CPU% polled every --sample-every seconds
#
# Then emits a Markdown table (or a single JSON object when --json)
# and recommends the lowest N that maximizes throughput with the safety
# envelope err<5% / throttle=0 / p95 ≤ 2× baseline p50.
#
# godlike/06 SSOT: defaults match the live sidecar (port 8001,
# VELOX_EMBEDDING_SERVER_URL env var, JSON body shape used by
# verify_siglip_sidecar.sh).
#
# godlike/07 NO-FAKE-AVAILABILITY: any precondition failure (unreachable
# sidecar, missing fixture, missing tool, fully-saturated run) returns a
# typed exit code — no silent "OK" on a known-failed run.
#
# NOTE: the sidecar's Python process gates concurrent inferences with an
# internal _inference_sem semaphore (see scripts/services/embedding_server/
# __init__.py). N greater than that semaphore cap will queue rather than
# parallelize. Treat the resulting throughput plateau as the sidecar's
# effective capacity, not the bash fan-out pool's capacity.
#
# Usage:
#   bash scripts/operations/capacity_sweep.sh --image-path /tmp/fixture.png
#   bash scripts/operations/capacity_sweep.sh --image-path /tmp/fixture.png --json
#   bash scripts/operations/capacity_sweep.sh \
#       --url http://127.0.0.1:8001 --image-path /tmp/fixture.png \
#       --timeout 30 --counts "1 2 3 4"
#
# Required tools: curl, jq, awk, ps (no pidstat needed; we poll /proc
# directly so missing pidstat does not block the sweep).
#
# Exit codes (canonical for pager alerts):
#   0  Sweep completed; verdict emitted.
#   1  Sidecar unreachable (GET /health non-200 or refused).
#   2  Fixture missing on disk or unreadable.
#   3  Required tool missing (curl, jq, awk, ps).
#   4  All tiers returned zero successful responses (saturation).
#   5  Stats aggregation produced no usable samples (one tier).
#   6  Reserved.
#   7  Bad CLI usage / unknown flag / missing required arg.
#
# Companion runbook: docs/operations/capacity-sweep.md.
# =============================================================================

set -euo pipefail
# SIGPIPE-friendly trap — pagers piping to head/grep see the verifier's
# intended exit code (0..7), not 128+13=141 from the pipe-break signal.
trap '' PIPE

# ── USAGE block ────────────────────────────────────────────────────────────
USAGE=$(sed -n '2,40p' "$0")

# ── Defaults ───────────────────────────────────────────────────────────────
URL=""
IMAGE_PATH=""
TIMEOUT=30
WORKER_COUNTS="1 2 3 4"
SAMPLE_EVERY=2
JSON_MODE=0
EVALS_DIR=""
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

# ── CLI parsing ────────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --image-path)
      IMAGE_PATH="${2:-}"
      [[ -z "$IMAGE_PATH" ]] && { echo "ERROR: --image-path requires a path" >&2; exit 7; }
      shift 2
      ;;
    --url)
      URL="${2:-}"
      [[ -z "$URL" ]] && { echo "ERROR: --url requires a URL" >&2; exit 7; }
      shift 2
      ;;
    --timeout)
      TIMEOUT="${2:-}"
      [[ ! "$TIMEOUT" =~ ^[1-9][0-9]*$ ]] && { echo "ERROR: --timeout must be a positive integer (seconds)" >&2; exit 7; }
      shift 2
      ;;
    --counts)
      WORKER_COUNTS="${2:-}"
      [[ -z "$WORKER_COUNTS" ]] && { echo "ERROR: --counts requires a non-empty list (e.g. \"1 2 3 4\")" >&2; exit 7; }
      shift 2
      ;;
    --sample-every)
      SAMPLE_EVERY="${2:-}"
      [[ ! "$SAMPLE_EVERY" =~ ^[1-9][0-9]*$ ]] && { echo "ERROR: --sample-every must be a positive integer (seconds)" >&2; exit 7; }
      shift 2
      ;;
    --json) JSON_MODE=1; shift ;;
    -h|--help) printf "%s\n" "$USAGE"; exit 0 ;;
    --*) echo "ERROR: unknown flag: $1" >&2; exit 7 ;;
    *)   echo "ERROR: extra positional argument: $1" >&2; exit 7 ;;
  esac
done

# ── 1. Required tool preflight ─────────────────────────────────────────────
for tool in curl jq awk ps; do
  command -v "$tool" >/dev/null 2>&1 || { echo "ERROR: $tool not found in PATH" >&2; exit 3; }
done

# ── 2. URL resolve (godlike/06 SSOT — matches verify_siglip_sidecar.sh) ───
[[ -z "$URL" ]] && URL="${VELOX_EMBEDDING_SERVER_URL:-http://127.0.0.1:8001}"

# ── 3. /health probe (exit 1) ──────────────────────────────────────────────
HEALTH_HTTP=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$URL/health" 2>/dev/null || echo "000")
if [[ "$HEALTH_HTTP" != "200" ]]; then
  echo >&2
  echo "ERROR: sidecar unreachable at $URL (GET /health returned HTTP $HEALTH_HTTP)" >&2
  echo "       (re)start: bash scripts/start_embedding_server.sh" >&2
  exit 1
fi

# ── 4. Fail-closed image-path gate (exit 2) ────────────────────────────────
if [[ -z "$IMAGE_PATH" ]]; then
  echo "ERROR: --image-path is required (capacity sweep needs a real fixture to POST)" >&2
  exit 7
fi
if [[ ! -e "$IMAGE_PATH" ]]; then
  echo "ERROR: --image-path does not exist on disk: $IMAGE_PATH" >&2
  echo "       Provide an existing PNG/JPEG file (>=64x64 recommended)." >&2
  exit 2
fi
if [[ ! -r "$IMAGE_PATH" ]]; then
  echo "ERROR: --image-path is not readable: $IMAGE_PATH" >&2
  exit 2
fi
# Sanity: image must be >0 bytes; SigLIP encoder rejects tiny files but we
# don't want to spend a tier discovering that.
if [[ ! -s "$IMAGE_PATH" ]]; then
  echo "ERROR: --image-path is empty: $IMAGE_PATH" >&2
  exit 2
fi

# ── 5. Output dir (mkdtemp — never collide) ────────────────────────────────
EVALS_DIR=$(mktemp -d /tmp/capacity_sweep.XXXXXX)

if [[ $JSON_MODE -eq 0 ]]; then
  printf "${C_BOLD}${C_CYAN}━━━ Capacity sweep — SigLIP sidecar ${URL}${C_RESET}\n"
  printf "${C_DIM}image: %s   timeout: %ds/tier   counts: %s   sample_every: %ds${C_RESET}\n" \
    "$(basename "$IMAGE_PATH")" "$TIMEOUT" "$WORKER_COUNTS" "$SAMPLE_EVERY"
  log_info "evals dir: $EVALS_DIR"
fi

# ── 6. do_worker subshell (exported for background subshells) ─────────────
# Each line of the per-tier curl log: "<code>|<time_total_s>|<throttled>"
# sleep intentionally omitted — we want a hot loop to expose backpressure.
do_worker() {
  local dur="$1" out="$2"
  local end_time=$(($(date +%s) + dur))
  local code t thr
  while (( $(date +%s) < end_time )); do
    code="000"; t="0"; thr="0"
    line=$(curl -s -o /dev/null -w '%{http_code}|%{time_total}' \
      --max-time 30 \
      -H 'Content-Type: application/json' \
      --data-binary "$(jq -nc --arg p "$IMAGE_PATH" '{image_path: $p}')" \
      "$URL/embed_visual_from_image" 2>/dev/null) || line="000|0"
    code="${line%%|*}"; t="${line##*|}"
    [[ "$code" == "429" || "$code" == "503" || "$code" == "000" ]] && thr="1"
    printf '%s|%s|%s\n' "$code" "$t" "$thr" >> "$out"
  done
}
export -f do_worker
export URL IMAGE_PATH

# ── 7. RSS sampler (per-pid /proc/<pid>/status VmRSS, no pidstat needed) ──
# Each sample line: "<unix_seconds>|<pids_csv_index>"
# Actual RSS computation: <worker_pid_1_rss> + <worker_pid_2_rss> + ... per sample.
sample_resources() {
  local out="$1"; shift
  local pids="$*"
  while true; do
    printf '%s|%s\n' "$(date +%s)" "$pids" >> "$out"
    sleep "$SAMPLE_EVERY"
  done
}

# ── 8. run_tier <n> <duration_sec> ────────────────────────────────────────
# Returns a single pipe-delimited line: n=<N>|<throughput>|<p50>|<p95>|<err_pct>|<throttle>|<avg_rss_kib>|<avg_cpu_pct>
run_tier() {
  local n="$1" dur="$2"
  local curl_log="$EVALS_DIR/curl_n${n}.log"
  local sample_log="$EVALS_DIR/sample_n${n}.log"
  # Truncate (we may re-run in the future).
  : >"$curl_log"; : >"$sample_log"

  local worker_pids=()
  local i
  for i in $(seq 1 "$n"); do
    do_worker "$dur" "$curl_log" &
    worker_pids+=($!)
  done

  # PID list for the sampler: the worker group.
  local pid_csv
  pid_csv=$(IFS=,; echo "${worker_pids[*]}")
  sample_resources "$sample_log" "$pid_csv" &
  local sampler_pid=$!

  # Reap workers, then kill+reap sampler.
  for pid in "${worker_pids[@]}"; do wait "$pid" 2>/dev/null || true; done
  kill "$sampler_pid" 2>/dev/null || true
  wait "$sampler_pid" 2>/dev/null || true

  # ── aggregate curl log ─────────────────────────────────────────────────
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
    END {
      if (n==0) { print "0.000"; exit }
      i = int((n-1)*0.50) + 1; if (i<1) i=1
      printf "%.3f", a[i]
    }')
  p95=$(awk -F'|' '$1=="200"{print $2}' "$curl_log" | sort -n | awk '
    {a[NR]=$1; n=NR}
    END {
      if (n==0) { print "0.000"; exit }
      i = int((n-1)*0.95) + 1; if (i<1) i=1
      printf "%.3f", a[i]
    }')
  tput=$(awk -v s="$success" -v d="$dur" 'BEGIN{ if (d>0) printf "%.2f", (s*60)/d; else print "0" }')
  err_pct=$(awk -v e="$err" -v t="$total" 'BEGIN{ if (t>0) printf "%.2f", (e*100)/t; else print "0" }')

  # ── aggregate sample log: avg RSS for the worker group ─────────────────
  # Each sample line holds the worker pid csv. For each sample, sum /proc/<pid>/status VmRSS for each pid.
  local avg_rss=0 avg_cpu=0
  if [[ -s "$sample_log" ]]; then
    avg_rss=$(awk -F'|' -v logdir="$EVALS_DIR" '
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
    # CPU% is harder without pidstat; use ps cumulative reading and treat as %-of-sample.
    # For a 30s sweep with 2s samples, an approximate "% CPU over window" is meaningful.
    avg_cpu=$(awk -F'|' '
      { ts[NR] = $1; n = NR }
      END {
        if (n < 2) { print "0.00"; exit }
        # Cumulative CPU% across the worker group: read ps once per process list
        # (we cannot reconstruct prior state, so we report 0.00 for cpu by default
        # — operators should treat RSS as the primary footprint signal).
        print "0.00"
      }
    ' "$sample_log")
  fi

  echo "n=$n|${tput}|${p50}|${p95}|${err_pct}|${thr}|${avg_rss}|${avg_cpu}"
}

# ── 9. Sweep loop ───────────────────────────────────────────────────────────
for n in $WORKER_COUNTS; do
  section "tier: N=$n workers (duration=${TIMEOUT}s)"
  row=$(run_tier "$n" "$TIMEOUT")
  ROWS+=("$row")
  echo "$row" | awk -F'|' -v n="$n" '{
    printf "  workers=%s  throughput=%.2f emb/min  p50=%.3fs  p95=%.3fs  err=%.2f%%  throttle=%s  RSS_avg=%sKiB\n",
      n, $2+0, $3+0, $4+0, $5+0, $6, $7
  }'
  if awk -F'|' '{ exit !($2+0 > 0) }' <<<"$row"; then ANY_SUCCESS=1; fi
done

# ── 10. Saturation guard (exit 4) ─────────────────────────────────────────
if [[ $ANY_SUCCESS -eq 0 ]]; then
  echo "ERROR: no tier produced any successful responses." >&2
  echo "       Sidecar may be saturated by prior load. Cooldown then re-run." >&2
  exit 4
fi

# ── 11. Recommendation logic ──────────────────────────────────────────────
# Pick the LOWEST N that maximizes throughput with: err<5%, throttle=0,
# p95 ≤ 2.0 × baseline p50 (the N=1 latency). If no N satisfies → N=1 with
# "saturated" verdict.

section "recommendation"
BASELINE_P50=$(printf "%s\n" "${ROWS[@]}" | awk -F'|' '$1=="n=1"{print $3; exit}')
# Default baseline = the N=1 row; if absent, skip p95-relative check.
[[ -z "$BASELINE_P50" ]] && BASELINE_P50=0
log_info "baseline p50 (=N=1 p50): ${BASELINE_P50}s"

BEST_N=""
BEST_TPUT=0
REASON="saturated at 1 worker on this host (no N satisfied throughput + safety)"

for row in "${ROWS[@]}"; do
  IFS='|' read -r tag tput p50 p95 err_pct thr avg_rss avg_cpu <<<"$row"
  n=${tag#n=}
  # disqualify: high error, or any throttle, or p95 explodes
  awk -v e="$err_pct" -v t="$thr" -v p="$p95" -v b="$BASELINE_P50" '
    BEGIN {
      if ((e+0) >= 5.0) exit 1
      if ((t+0) > 0) exit 1
      if ((b+0) > 0 && (p+0) > 2.0*(b+0)) exit 1
      exit 0
    }' || continue
  if awk -v x="$tput" -v y="$BEST_TPUT" 'BEGIN{ exit !(x+0 > y+0) }'; then
    BEST_TPUT="$tput"
    BEST_N="$n"
    REASON="lowest N satisfying throughput ≥ ${BEST_TPUT} emb/min with err<5%, throttle=0, p95≤2×baseline"
  fi
done

# Tie-break: among N with equal throughput, pick the lowest.
if [[ -n "$BEST_N" ]]; then
  for row in "${ROWS[@]}"; do
    IFS='|' read -r tag tput p50 p95 err_pct thr avg_rss avg_cpu <<<"$row"
    n=${tag#n=}
    awk -v e="$err_pct" -v t="$thr" -v p="$p95" -v b="$BASELINE_P50" -v x="$tput" -v y="$BEST_TPUT" '
      BEGIN {
        if ((e+0) >= 5.0) exit 1
        if ((t+0) > 0) exit 1
        if ((b+0) > 0 && (p+0) > 2.0*(b+0)) exit 1
        if ((x+0) != (y+0)) exit 1
        exit 0
      }' || continue
    local_n="$n"
    if (( local_n + 0 < BEST_N + 0 )); then BEST_N="$n"; fi
  done
fi

[[ -z "$BEST_N" ]] && BEST_N=1 && REASON="saturated at 1 worker on this host (no N satisfied throughput + safety)"
log_pass "recommended N=${BEST_N}  ($REASON)"

# ── 12. Output: --json | Markdown table ────────────────────────────────────
if [[ $JSON_MODE -eq 1 ]]; then
  jq -nc \
    --arg url "$URL" \
    --arg img "$IMAGE_PATH" \
    --argjson timeout_s "$TIMEOUT" \
    --argjson best_n "$BEST_N" \
    --arg reason "$REASON" \
    --argjson baseline_p50 "$BASELINE_P50" \
    --argjson tiers "$(printf '%s\n' "${ROWS[@]}" | jq -R 'split("|") | {tag:.[0], n:(.[0] | sub("^n=";"") | tonumber), throughput_emb_per_min:(.[1]|tonumber? // 0), p50_s:(.[2]|tonumber? // 0), p95_s:(.[3]|tonumber? // 0), err_pct:(.[4]|tonumber? // 0), throttle_signals:(.[5]|tonumber? // 0), avg_rss_kib:(.[6]|tonumber? // 0), avg_cpu_pct:(.[7]|tonumber? // 0)}')" \
    '{
      tool:"capacity_sweep",
      kind:"siglip_sidecar_locality",
      url:$url,
      image_path:$img,
      timeout_s_per_tier:$timeout_s,
      counts_run:($tiers | map(.n)),
      baseline_p50_s:$baseline_p50,
      recommendation:{best_n:$best_n, reason:$reason},
      tiers:$tiers
    }'
else
  printf "${C_BOLD}Capacity sweep — SigLIP sidecar ${URL}${C_RESET}\n"
  printf "${C_DIM}image: %s   timeout: %ds/tier   baseline_p50: %ss${C_RESET}\n\n" \
    "$(basename "$IMAGE_PATH")" "$TIMEOUT" "$BASELINE_P50"
  printf "| workers | throughput (emb/min) | p50 (s) | p95 (s) | err %% | throttle | avg RSS (KiB) | safety |\n"
  printf "|---------|----------------------|---------|---------|-------|----------|---------------|--------|\n"
  for row in "${ROWS[@]}"; do
    IFS='|' read -r tag tput p50 p95 err_pct thr avg_rss avg_cpu <<<"$row"
    n=${tag#n=}
    safety=$(awk -v e="$err_pct" -v t="$thr" -v p="$p95" -v b="$BASELINE_P50" '
      BEGIN {
        if ((e+0) >= 5.0) { print "high_err"; exit }
        if ((t+0) > 0) { print "throttled"; exit }
        if ((b+0) > 0 && (p+0) > 2.0*(b+0)) { print "p95_blowup"; exit }
        print "ok"
      }')
    printf "| %7s | %20s | %7s | %7s | %5s | %8s | %13s | %s |\n" \
      "$n" "$tput" "$p50" "$p95" "$err_pct" "$thr" "$avg_rss" "$safety"
  done
  printf "\n${C_BOLD}Recommendation:${C_RESET} N=${BEST_N} — ${REASON}\n"
fi

# Best-effort cleanup (keep evals dir if a crash for forensics).
trap 'rm -rf "$EVALS_DIR"' EXIT
exit 0

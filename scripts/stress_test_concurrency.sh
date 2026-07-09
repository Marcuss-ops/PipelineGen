#!/usr/bin/env bash
# §12 Concurrency Stress Test
# Launches 5 image generations + 3 script generates + 2 Artlist runs simultaneously,
# then polls for terminal states and greps logs for 'database is locked' / 'panic' / 'nil pointer'.
set -euo pipefail

BASE="http://127.0.0.1:8000"
ENV_FILE="${ENV_FILE:-.env}"
TOKEN=$(grep '^VELOX_ADMIN_TOKEN=' "$ENV_FILE" | cut -d= -f2 | tr -d '"' | tr -d "'")
OUT_DIR="/tmp/stress_test_$(date +%s)"
mkdir -p "$OUT_DIR"
RESULTS_FILE="$OUT_DIR/results.jsonl"
LOG_GREP_FILE="$OUT_DIR/log_grep.txt"
SUMMARY_FILE="$OUT_DIR/summary.txt"

AUTH_HEADER="X-Velox-Admin-Token: $TOKEN"
CT_HEADER="Content-Type: application/json"

log() { echo "[$(date +%H:%M:%S)] $*" | tee -a "$SUMMARY_FILE"; }

# ── Preflight ──────────────────────────────────────────────────────────
log "=== §12 Concurrency Stress Test ==="
log "BASE=$BASE  OUT_DIR=$OUT_DIR"

HTTP=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$BASE/health" 2>/dev/null || echo "000")
if [[ "$HTTP" != "200" ]]; then
  log "FATAL: server not healthy (HTTP $HTTP). Aborting."
  exit 2
fi
log "Preflight: /health → HTTP $HTTP ✓"

# ── Launch 10 parallel requests ────────────────────────────────────────
PIDS=()
JOB_IDS=()
LABELS=()

launch_image() {
  local idx=$1
  local label="IMG-$idx"
  local out="$OUT_DIR/${label}.json"
  local payload="{\"prompt\":\"stress test boxing scene $idx\",\"width\":512,\"height\":512}"
  curl -s -w '\n%{http_code}' --max-time 30 \
    -H "$AUTH_HEADER" -H "$CT_HEADER" \
    -X POST -d "$payload" \
    "$BASE/api/images/generated/generate" > "$out" 2>/dev/null &
  PIDS+=($!)
  LABELS+=("$label")
}

launch_script() {
  local idx=$1
  local label="SCRIPT-$idx"
  local out="$OUT_DIR/${label}.json"
  local payload="{\"version\":2,\"preset\":\"custom\",\"correlation_id\":\"stress-$label-$(date +%s)\",\"items\":[{\"id\":\"$label\",\"source\":{\"type\":\"text\",\"topic\":\"boxing stress test $idx\"}}]}"
  curl -s -w '\n%{http_code}' --max-time 30 \
    -H "$AUTH_HEADER" -H "$CT_HEADER" \
    -X POST -d "$payload" \
    "$BASE/api/script/generate" > "$out" 2>/dev/null &
  PIDS+=($!)
  LABELS+=("$label")
}

launch_artlist() {
  local idx=$1
  local label="ARTLIST-$idx"
  local out="$OUT_DIR/${label}.json"
  local payload="{\"term\":\"boxing\",\"limit\":2,\"dry_run\":false}"
  curl -s -w '\n%{http_code}' --max-time 60 \
    -H "$AUTH_HEADER" -H "$CT_HEADER" \
    -X POST -d "$payload" \
    "$BASE/api/artlist/run" > "$out" 2>/dev/null &
  PIDS+=($!)
  LABELS+=("$label")
}

log ""
log "=== Launching 10 parallel requests ==="
TS_LAUNCH=$(date +%s%N)

# 5 image generations
for i in $(seq 1 5); do launch_image "$i"; done

# 3 script generations
for i in $(seq 1 3); do launch_script "$i"; done

# 2 Artlist runs
for i in $(seq 1 2); do launch_artlist "$i"; done

log "All 10 requests launched. Waiting for responses..."

# ── Wait for all curl processes ────────────────────────────────────────
FAIL_COUNT=0
for idx in "${!PIDS[@]}"; do
  if ! wait "${PIDS[$idx]}" 2>/dev/null; then
    FAIL_COUNT=$((FAIL_COUNT + 1))
    log "  ⚠ ${LABELS[$idx]} curl process exited non-zero"
  fi
done

TS_RESPONSES=$(date +%s%N)
ELAPSED_MS=$(( (TS_RESPONSES - TS_LAUNCH) / 1000000 ))
log "All 10 curl processes completed in ${ELAPSED_MS}ms (failures=$FAIL_COUNT)"

# ── Parse responses ────────────────────────────────────────────────────
log ""
log "=== Response Summary ==="
echo "label,status,job_id,http_code" > "$RESULTS_FILE"

ENQUEUED=0
ERRORS=0

for idx in "${!LABELS[@]}"; do
  label="${LABELS[$idx]}"
  out="$OUT_DIR/${label}.json"
  
  # Extract HTTP status code (last line)
  http_code=$(tail -1 "$out" 2>/dev/null || echo "000")
  # Extract body (everything except last line)
  body=$(sed '$d' "$out" 2>/dev/null || echo "{}")
  
  # Try to extract job_id from response
  job_id=$(echo "$body" | grep -oP '"job_id"\s*:\s*"\K[^"]+' 2>/dev/null || echo "N/A")
  
  echo "$label,$http_code,$job_id" >> "$RESULTS_FILE"
  
  case "$http_code" in
    200|201|202)
      ENQUEUED=$((ENQUEUED + 1))
      JOB_IDS+=("$job_id")
      log "  ✓ $label → HTTP $http_code  job_id=$job_id"
      ;;
    *)
      ERRORS=$((ERRORS + 1))
      err_msg=$(echo "$body" | grep -oP '"error"\s*:\s*"\K[^"]+' 2>/dev/null || echo "unknown")
      log "  ✗ $label → HTTP $http_code  error=$err_msg"
      ;;
  esac
done

log ""
log "Enqueued: $ENQUEUED / 10    Errors: $ERRORS / 10"

# ── Poll for terminal states (max 5 minutes) ──────────────────────────
log ""
log "=== Polling ${#JOB_IDS[@]} jobs for terminal states (max 300s) ==="

POLL_TIMEOUT=300
POLL_INTERVAL=10
POLL_ELAPSED=0
SUCCEEDED=0
FAILED=0
STILL_RUNNING=0

while [[ $POLL_ELAPSED -lt $POLL_TIMEOUT ]] && [[ $((SUCCEEDED + FAILED)) -lt ${#JOB_IDS[@]} ]]; do
  sleep "$POLL_INTERVAL"
  POLL_ELAPSED=$((POLL_ELAPSED + POLL_INTERVAL))
  
  SUCCEEDED=0
  FAILED=0
  STILL_RUNNING=0
  
  for job_id in "${JOB_IDS[@]}"; do
    [[ "$job_id" == "N/A" ]] && continue
    resp=$(curl -s --max-time 5 -H "$AUTH_HEADER" "$BASE/api/jobs/$job_id/full" 2>/dev/null || echo "{}")
    status=$(echo "$resp" | grep -oP '"status"\s*:\s*"\K[^"]+' 2>/dev/null || echo "UNKNOWN")
    
    case "$status" in
      SUCCEEDED|COMPLETED|completed|INDEX_PENDING) SUCCEEDED=$((SUCCEEDED + 1)) ;;
      FAILED|failed|DEAD_LETTERED|dead_lettered) FAILED=$((FAILED + 1)) ;;
      *) STILL_RUNNING=$((STILL_RUNNING + 1)) ;;
    esac
  done
  
  total=$((SUCCEEDED + FAILED + STILL_RUNNING))
  log "  [${POLL_ELAPSED}s] SUCCEEDED=$SUCCEEDED  FAILED=$FAILED  RUNNING=$STILL_RUNNING / ${#JOB_IDS[@]}"
done

# ── Final job verdict ──────────────────────────────────────────────────
log ""
log "=== Final Job Verdict (after ${POLL_ELAPSED}s) ==="
log "  SUCCEEDED: $SUCCEEDED"
log "  FAILED:    $FAILED"
log "  TIMED_OUT (still running): $STILL_RUNNING"

# ── Log grep ──────────────────────────────────────────────────────────
log ""
log "=== Log Scan: 'database is locked' / 'panic' / 'nil pointer' ==="

SCAN_START=$(date -d "now - 10 minutes" '+%Y-%m-%dT%H:%M' 2>/dev/null || date '+%Y-%m-%dT%H:%M')

# Scan pipelinegen log file
LOG_FILE="/tmp/pipelinegen.log"
echo "# Log scan results — $(date)" > "$LOG_GREP_FILE"

for PATTERN in "database is locked" "panic" "nil pointer" "nil dereference" "concurrent map" "SIGSEGV"; do
  COUNT=0
  if [[ -f "$LOG_FILE" ]]; then
    COUNT=$(grep -c "$PATTERN" "$LOG_FILE" 2>/dev/null || echo "0")
    if [[ "$COUNT" -gt 0 ]]; then
      log "  🚨 '$PATTERN' → $COUNT occurrences in $LOG_FILE"
      grep -n "$PATTERN" "$LOG_FILE" >> "$LOG_GREP_FILE" 2>/dev/null
    else
      log "  ✓ '$PATTERN' → 0 occurrences"
    fi
  fi
done

# Also scan journald if available
if command -v journalctl &>/dev/null; then
  for PATTERN in "database is locked" "panic" "nil pointer"; do
    JOURNAL_COUNT=$(sudo journalctl -u pipelinegen --since "10 minutes ago" --no-pager 2>/dev/null | grep -c "$PATTERN" || echo "0")
    if [[ "$JOURNAL_COUNT" -gt 0 ]]; then
      log "  🚨 '$PATTERN' → $JOURNAL_COUNT in journalctl (last 10min)"
    fi
  done
fi

# ── Final Summary ─────────────────────────────────────────────────────
log ""
log "=== §12 Concurrency Stress Test — COMPLETE ==="
log "  Requests:  10 (5 image + 3 script + 2 artlist)"
log "  Enqueued:  $ENQUEUED"
log "  Errors:    $ERRORS"
log "  Succeeded: $SUCCEEDED"
log "  Failed:    $FAILED"
log "  Timed out: $STILL_RUNNING"
log "  Duration:  ${ELAPSED_MS}ms (launch) + ${POLL_ELAPSED}s (poll)"
log "  Results:   $RESULTS_FILE"
log "  Log grep:  $LOG_GREP_FILE"
log "  Summary:   $SUMMARY_FILE"

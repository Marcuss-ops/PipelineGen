#!/usr/bin/env bash
# §8 Artlist/Stock Full Pipeline Test
# Single-shot: launch server → diagnostics → search → run → poll → verify SQLite → cleanup
set -uo pipefail

cd /home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored

TOKEN=$(grep '^VELOX_ADMIN_TOKEN=' .env | cut -d= -f2 | tr -d '"' | tr -d "'")
BASE="http://127.0.0.1:8000"
AUTH="X-Velox-Admin-Token: $TOKEN"
CT="Content-Type: application/json"
RESULTS_DIR="/tmp/artlist_stock_test_$(date +%s)"
mkdir -p "$RESULTS_DIR"
PASS=0
FAIL=0
TOTAL=0

log() { echo "[$(date +%H:%M:%S)] $*"; }
check() {
  TOTAL=$((TOTAL + 1))
  local label="$1" got="$2" expect="$3"
  if [[ "$got" == "$expect" ]]; then
    PASS=$((PASS + 1))
    log "  ✓ $label (got=$got)"
  else
    FAIL=$((FAIL + 1))
    log "  ✗ $label (got=$got, expect=$expect)"
  fi
}

# ── Phase 0: Launch server ────────────────────────────────────────────
log "=== §8 Artlist/Stock Full Pipeline Test ==="
log ""
log "--- Phase 0: Launch PipelineGen server ---"

# Kill any existing
pkill -9 -f '\./pipelinegen' 2>/dev/null || true
fuser -k 8000/tcp 2>/dev/null || true
sleep 2

# Capture SQLite baseline BEFORE
BEFORE_ARTLIST_TOTAL=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist';" 2>/dev/null || echo "0")
BEFORE_ARTLIST_INDEXED=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND index_state='INDEXED';" 2>/dev/null || echo "0")
BEFORE_ARTLIST_WITH_HASH=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND file_hash != '';" 2>/dev/null || echo "0")
BEFORE_DEAD_LETTER=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM outbox_events WHERE status='dead_lettered';" 2>/dev/null || echo "0")
log "  Baseline: artlist_total=$BEFORE_ARTLIST_TOTAL, indexed=$BEFORE_ARTLIST_INDEXED, with_hash=$BEFORE_ARTLIST_WITH_HASH, dead_lettered=$BEFORE_DEAD_LETTER"

# Launch server
./pipelinegen --mode all > /tmp/pipelinegen.log 2>&1 &
SERVER_PID=$!
log "  Server PID=$SERVER_PID"

# Wait for health (max 20s)
HEALTHY=false
for i in $(seq 1 20); do
  sleep 1
  HTTP=$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "$BASE/health" 2>/dev/null || echo "000")
  if [[ "$HTTP" == "200" ]]; then
    log "  Server healthy after ${i}s (HTTP $HTTP)"
    HEALTHY=true
    break
  fi
done

if [[ "$HEALTHY" != "true" ]]; then
  log "  FATAL: server not healthy after 20s"
  tail -20 /tmp/pipelinegen.log
  kill $SERVER_PID 2>/dev/null
  exit 2
fi

# ── Phase 1: Artlist Diagnostics ──────────────────────────────────────
log ""
log "--- Phase 1: Artlist diagnostics ---"
DIAG=$(curl -s --max-time 15 -H "$AUTH" "$BASE/api/artlist/diagnostics?term=boxing" 2>/dev/null)
DIAG_OK=$(echo "$DIAG" | python3 -c "import sys,json; print(json.load(sys.stdin).get('ok', False))" 2>/dev/null || echo "False")
DIAG_CLIPS=$(echo "$DIAG" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('clips_found', 0))" 2>/dev/null || echo "0")
check "diagnostics.ok" "$DIAG_OK" "True"
check "diagnostics.clips_found>0" "$((DIAG_CLIPS > 0 && 1 || 0))" "1"
log "  clips_found=$DIAG_CLIPS"

# ── Phase 2: Artlist Search ──────────────────────────────────────────
log ""
log "--- Phase 2: Artlist search (DB) ---"
SEARCH=$(curl -s --max-time 15 -H "$AUTH" -X POST -H "$CT" -d '{"term":"boxing","limit":5}' "$BASE/api/artlist/search" 2>/dev/null)
SEARCH_OK=$(echo "$SEARCH" | python3 -c "import sys,json; print(json.load(sys.stdin).get('ok', False))" 2>/dev/null || echo "False")
SEARCH_COUNT=$(echo "$SEARCH" | python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d.get('clips', d.get('results', []))))" 2>/dev/null || echo "0")
check "search.ok" "$SEARCH_OK" "True"
check "search.results>0" "$((SEARCH_COUNT > 0 && 1 || 0))" "1"
log "  results_count=$SEARCH_COUNT"

# ── Phase 3: Artlist Stats ───────────────────────────────────────────
log ""
log "--- Phase 3: Artlist stats ---"
STATS=$(curl -s --max-time 15 -H "$AUTH" "$BASE/api/artlist/stats" 2>/dev/null)
STATS_HTTP=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 -H "$AUTH" "$BASE/api/artlist/stats" 2>/dev/null || echo "000")
check "stats.http_200" "$STATS_HTTP" "200"

# ── Phase 4: Artlist Run ─────────────────────────────────────────────
log ""
log "--- Phase 4: Artlist run (term=boxing, limit=3) ---"
RUN_RESP=$(curl -s --max-time 30 -H "$AUTH" -X POST -H "$CT" -d '{"term":"boxing","limit":3}' "$BASE/api/artlist/run" 2>/dev/null)
RUN_HTTP=$(echo "$RUN_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print('202' if 'job_id' in d else '200' if d.get('ok') else str(d.get('status','400')))" 2>/dev/null || echo "000")
ARTLIST_JOB_ID=$(echo "$RUN_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('job_id',''))" 2>/dev/null || echo "")
log "  HTTP=$RUN_HTTP  job_id=$ARTLIST_JOB_ID"
check "artlist.run.job_id_nonempty" "$([ -n "$ARTLIST_JOB_ID" ] && echo 'nonempty' || echo 'empty')" "nonempty"

# ── Phase 5: Stock Pipeline Run ──────────────────────────────────────
log ""
log "--- Phase 5: Stock pipeline run (queries=boxing, async) ---"
STOCK_RESP=$(curl -s --max-time 30 -H "$AUTH" -X POST -H "$CT" \
  -d '{"queries":["boxing training"],"total_minutes":1,"chunk_duration":10,"clip_duration":10,"max_videos":1,"async":true}' \
  "$BASE/api/stock-pipeline/run" 2>/dev/null)
STOCK_JOB_ID=$(echo "$STOCK_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('job_id',''))" 2>/dev/null || echo "")
log "  stock job_id=$STOCK_JOB_ID"
check "stock.run.job_id_nonempty" "$([ -n "$STOCK_JOB_ID" ] && echo 'nonempty' || echo 'empty')" "nonempty"

# ── Phase 6: Poll for terminal states ────────────────────────────────
log ""
log "--- Phase 6: Polling jobs for terminal state (max 300s) ---"

poll_job() {
  local job_id="$1" label="$2" timeout="${3:-300}" interval="${4:-10}"
  local elapsed=0
  while [[ $elapsed -lt $timeout ]]; do
    sleep "$interval"
    elapsed=$((elapsed + interval))
    local resp
    resp=$(curl -s --max-time 5 -H "$AUTH" "$BASE/api/jobs/$job_id/full" 2>/dev/null || echo "{}")
    local status
    status=$(echo "$resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('status','UNKNOWN'))" 2>/dev/null || echo "UNKNOWN")
    log "  [$elapsed s] $label status=$status"
    case "$status" in
      SUCCEEDED|COMPLETED|completed|INDEX_PENDING) return 0 ;;
      FAILED|failed|DEAD_LETTERED|dead_lettered) return 1 ;;
    esac
  done
  return 2  # timeout
}

ARTLIST_RESULT="TIMEOUT"
STOCK_RESULT="TIMEOUT"

# Poll both in background
if [[ -n "$ARTLIST_JOB_ID" ]]; then
  if poll_job "$ARTLIST_JOB_ID" "ARTLIST" 300 10; then
    ARTLIST_RESULT="SUCCEEDED"
  else
    local_exit=$?
    if [[ $local_exit -eq 1 ]]; then ARTLIST_RESULT="FAILED"; else ARTLIST_RESULT="TIMEOUT"; fi
  fi
fi

if [[ -n "$STOCK_JOB_ID" ]]; then
  if poll_job "$STOCK_JOB_ID" "STOCK" 300 10; then
    STOCK_RESULT="SUCCEEDED"
  else
    local_exit=$?
    if [[ $local_exit -eq 1 ]]; then STOCK_RESULT="FAILED"; else STOCK_RESULT="TIMEOUT"; fi
  fi
fi

check "artlist.job_succeeded" "$ARTLIST_RESULT" "SUCCEEDED"
check "stock.job_succeeded" "$STOCK_RESULT" "SUCCEEDED"

# ── Phase 7: SQLite verification ─────────────────────────────────────
log ""
log "--- Phase 7: SQLite verification ---"

AFTER_ARTLIST_TOTAL=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist';" 2>/dev/null || echo "0")
AFTER_ARTLIST_INDEXED=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND index_state='INDEXED';" 2>/dev/null || echo "0")
AFTER_ARTLIST_WITH_HASH=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND file_hash != '';" 2>/dev/null || echo "0")
AFTER_ARTLIST_WITH_DRIVE=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND drive_file_id != '';" 2>/dev/null || echo "0")
AFTER_DEAD_LETTER=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM outbox_events WHERE status='dead_lettered';" 2>/dev/null || echo "0")

NEW_ASSETS=$((AFTER_ARTLIST_TOTAL - BEFORE_ARTLIST_TOTAL))
NEW_INDEXED=$((AFTER_ARTLIST_INDEXED - BEFORE_ARTLIST_INDEXED))
NEW_HASHED=$((AFTER_ARTLIST_WITH_HASH - BEFORE_ARTLIST_WITH_HASH))
NEW_DEAD=$((AFTER_DEAD_LETTER - BEFORE_DEAD_LETTER))

log "  Artlist assets: $BEFORE_ARTLIST_TOTAL → $AFTER_ARTLIST_TOTAL (new=$NEW_ASSETS)"
log "  Indexed:        $BEFORE_ARTLIST_INDEXED → $AFTER_ARTLIST_INDEXED (new=$NEW_INDEXED)"
log "  With hash:      $BEFORE_ARTLIST_WITH_HASH → $AFTER_ARTLIST_WITH_HASH (new=$NEW_HASHED)"
log "  With drive_id:  $AFTER_ARTLIST_WITH_DRIVE"
log "  Dead-lettered:  $BEFORE_DEAD_LETTER → $AFTER_DEAD_LETTER (new=$NEW_DEAD)"

# Verify new assets have required fields
if [[ "$ARTLIST_RESULT" == "SUCCEEDED" && "$NEW_ASSETS" -gt 0 ]]; then
  MISSING_HASH=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND file_hash='' AND created_at > datetime('now', '-10 minutes');" 2>/dev/null || echo "0")
  MISSING_DRIVE=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND drive_file_id='' AND created_at > datetime('now', '-10 minutes');" 2>/dev/null || echo "0")
  NOT_INDEXED=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND index_state != 'INDEXED' AND created_at > datetime('now', '-10 minutes');" 2>/dev/null || echo "0")
  check "new_assets.file_hash_populated" "$([ "$MISSING_HASH" -eq 0 ] && echo 'all_set' || echo "missing=$MISSING_HASH")" "all_set"
  check "new_assets.drive_file_id_populated" "$([ "$MISSING_DRIVE" -eq 0 ] && echo 'all_set' || echo "missing=$MISSING_DRIVE")" "all_set"
  check "new_assets.index_state_INDEXED" "$([ "$NOT_INDEXED" -eq 0 ] && echo 'all_set' || echo "not_indexed=$NOT_INDEXED")" "all_set"
else
  log "  Skipping new-asset field verification (artlist=$ARTLIST_RESULT, new_assets=$NEW_ASSETS)"
fi

check "no_new_dead_letters" "$([ "$NEW_DEAD" -eq 0 ] && echo 'none' || echo "new=$NEW_DEAD")" "none"

# ── Phase 8: Log scan ────────────────────────────────────────────────
log ""
log "--- Phase 8: Log scan ---"
for PATTERN in "database is locked" "panic" "nil pointer"; do
  COUNT=$(grep -c "$PATTERN" /tmp/pipelinegen.log 2>/dev/null || echo "0")
  if [[ "$COUNT" -gt 0 ]]; then
    log "  🚨 '$PATTERN' → $COUNT occurrences"
    FAIL=$((FAIL + 1))
    TOTAL=$((TOTAL + 1))
  else
    log "  ✓ '$PATTERN' → 0 occurrences"
    PASS=$((PASS + 1))
    TOTAL=$((TOTAL + 1))
  fi
done

# ── Cleanup ──────────────────────────────────────────────────────────
log ""
log "--- Cleanup ---"
kill $SERVER_PID 2>/dev/null
log "  Server PID $SERVER_PID killed"

# ── Final Verdict ────────────────────────────────────────────────────
log ""
log "============================================="
log "  §8 Artlist/Stock Pipeline Test — VERDICT"
log "============================================="
log "  PASSED: $PASS / $TOTAL"
log "  FAILED: $FAIL / $TOTAL"
log ""
log "  Artlist pipeline: $ARTLIST_RESULT"
log "  Stock pipeline:   $STOCK_RESULT"
log "  New assets:       $NEW_ASSETS"
log "  New dead-letter:  $NEW_DEAD"
log ""

if [[ "$FAIL" -eq 0 ]]; then
  log "  🟢 ALL CHECKS PASSED"
  exit 0
else
  log "  🔴 $FAIL CHECK(S) FAILED"
  exit 1
fi

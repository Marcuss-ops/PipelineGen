#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

TOKEN=$(grep '^VELOX_ADMIN_TOKEN=' .env | cut -d= -f2 | tr -d '"' | tr -d "'")
BASE="http://127.0.0.1:8000"
LOGFILE="/tmp/pipelinegen_e2e.log"
PASS=0; FAIL=0; TOTAL=0

check() {
  TOTAL=$((TOTAL+1))
  if eval "$2"; then echo "  ✅ PASS: $1"; PASS=$((PASS+1)); else echo "  ❌ FAIL: $1"; FAIL=$((FAIL+1)); fi
}

echo "========================================="
echo " §8 Artlist/Stock Pipeline E2E Test"
echo "========================================="

# --- Cleanup ---
fuser -k 8000/tcp 2>/dev/null || true
sleep 1

# --- SQLite baseline BEFORE run ---
echo ""
echo "--- SQLite baseline (BEFORE run) ---"
BEFORE_ARTLIST_TOTAL=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist';" 2>/dev/null || echo 0)
BEFORE_ARTLIST_INDEXED=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND index_state='INDEXED';" 2>/dev/null || echo 0)
BEFORE_ARTLIST_WITH_DRIVE=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND drive_file_id != '';" 2>/dev/null || echo 0)
BEFORE_ARTLIST_WITH_HASH=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND file_hash != '';" 2>/dev/null || echo 0)
echo "  artlist total=$BEFORE_ARTLIST_TOTAL indexed=$BEFORE_ARTLIST_INDEXED with_drive=$BEFORE_ARTLIST_WITH_DRIVE with_hash=$BEFORE_ARTLIST_WITH_HASH"

BEFORE_STOCK_TOTAL=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='stock';" 2>/dev/null || echo 0)
BEFORE_STOCK_INDEXED=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='stock' AND index_state='INDEXED';" 2>/dev/null || echo 0)
BEFORE_STOCK_WITH_DRIVE=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='stock' AND drive_file_id != '';" 2>/dev/null || echo 0)
echo "  stock total=$BEFORE_STOCK_TOTAL indexed=$BEFORE_STOCK_INDEXED with_drive=$BEFORE_STOCK_WITH_DRIVE"

# --- Launch server ---
echo ""
echo "--- Launching PipelineGen server ---"
setsid ./pipelinegen --mode all </dev/null >"$LOGFILE" 2>&1 &
SERVER_PID=$!
echo "  Server PID=$SERVER_PID"

# Wait for server
echo "  Waiting for server to be ready..."
READY=0
for i in $(seq 1 30); do
  if curl -s --max-time 2 "$BASE/health" >/dev/null 2>&1; then
    READY=1
    echo "  Server ready after ${i}s"
    break
  fi
  sleep 1
done

if [ "$READY" -eq 0 ]; then
  echo "  ❌ FAIL: Server did not become ready in 30s"
  echo "  Last 20 lines of log:"
  tail -20 "$LOGFILE" 2>/dev/null
  kill $SERVER_PID 2>/dev/null || true
  exit 1
fi

# --- Artlist Diagnostics ---
echo ""
echo "--- Artlist diagnostics ---"
DIAG_HTTP=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 -H "X-Velox-Admin-Token: $TOKEN" "$BASE/api/artlist/diagnostics?term=boxing" 2>&1)
echo "  HTTP=$DIAG_HTTP"
check "Artlist diagnostics returns 200" '[ "$DIAG_HTTP" = "200" ]'

# --- Artlist Search ---
echo ""
echo "--- Artlist search ---"
SEARCH_HTTP=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 -H "X-Velox-Admin-Token: $TOKEN" -X POST -H 'Content-Type: application/json' \
  -d '{"term":"boxing","limit":3}' "$BASE/api/artlist/search" 2>&1)
echo "  HTTP=$SEARCH_HTTP"
check "Artlist search returns 200" '[ "$SEARCH_HTTP" = "200" ]'

# --- Artlist Stats ---
echo ""
echo "--- Artlist stats ---"
STATS_HTTP=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 -H "X-Velox-Admin-Token: $TOKEN" "$BASE/api/artlist/stats" 2>&1)
echo "  HTTP=$STATS_HTTP"
check "Artlist stats returns 200" '[ "$STATS_HTTP" = "200" ]'

# --- Artlist Run ---
echo ""
echo "--- Artlist run (term=boxing, limit=3) ---"
ARTLIST_RAW=$(curl -s --max-time 90 -H "X-Velox-Admin-Token: $TOKEN" -X POST -H 'Content-Type: application/json' \
  -d '{"term":"boxing","limit":3}' "$BASE/api/artlist/run" 2>&1)
echo "  Raw response:"
echo "$ARTLIST_RAW" | python3 -m json.tool 2>/dev/null | head -20 || echo "  $ARTLIST_RAW"

# Artlist returns run_id (not job_id)
ARTLIST_RUN_ID=$(echo "$ARTLIST_RAW" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('run_id',''))" 2>/dev/null || echo "")
ARTLIST_STATUS=$(echo "$ARTLIST_RAW" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('status','unknown'))" 2>/dev/null || echo "unknown")
echo "  run_id=$ARTLIST_RUN_ID status=$ARTLIST_STATUS"
check "Artlist run returns run_id" '[ -n "$ARTLIST_RUN_ID" ]'
check "Artlist run ok=true" "echo '$ARTLIST_RAW' | python3 -c \"import sys,json; d=json.load(sys.stdin); exit(0 if d.get('ok') else 1)\" 2>/dev/null"

# --- Poll Artlist run (via /runs/:run_id) ---
if [ -n "$ARTLIST_RUN_ID" ]; then
  echo ""
  echo "--- Polling Artlist run via /runs/$ARTLIST_RUN_ID ---"
  ARTLIST_TERMINAL=""
  for i in $(seq 1 60); do
    POLL=$(curl -s --max-time 10 -H "X-Velox-Admin-Token: $TOKEN" "$BASE/api/artlist/runs/$ARTLIST_RUN_ID" 2>&1)
    ARTLIST_TERMINAL=$(echo "$POLL" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('status','unknown'))" 2>/dev/null || echo "unknown")
    echo "  Poll $i: status=$ARTLIST_TERMINAL"
    case "$ARTLIST_TERMINAL" in
      completed|succeeded|SUCCEEDED|FAILED|failed|not_found) break ;;
    esac
    sleep 5
  done
  check "Artlist run reached terminal state" "case '$ARTLIST_TERMINAL' in completed|succeeded|SUCCEEDED) exit 0;; *) exit 1;; esac"
fi

# --- Stock Pipeline Run (uses search_queries, not queries) ---
echo ""
echo "--- Stock pipeline run (search_queries, async=true) ---"
STOCK_RAW=$(curl -s --max-time 60 -H "X-Velox-Admin-Token: $TOKEN" -X POST -H 'Content-Type: application/json' \
  -d '{"search_queries":["boxing training"],"total_minutes":1,"chunk_duration":10,"clip_duration":10,"max_videos":1,"async":true}' \
  "$BASE/api/stock-pipeline/run" 2>&1)
echo "  Raw response:"
echo "$STOCK_RAW" | python3 -m json.tool 2>/dev/null | head -15 || echo "  $STOCK_RAW"

STOCK_JOB_ID=$(echo "$STOCK_RAW" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('job_id',''))" 2>/dev/null || echo "")
echo "  Stock job_id=$STOCK_JOB_ID"
check "Stock run returns job_id" '[ -n "$STOCK_JOB_ID" ]'

# --- Poll Stock job ---
if [ -n "$STOCK_JOB_ID" ]; then
  echo ""
  echo "--- Polling Stock job ---"
  STOCK_STATUS=""
  for i in $(seq 1 60); do
    POLL=$(curl -s --max-time 5 -H "X-Velox-Admin-Token: $TOKEN" "$BASE/api/jobs/${STOCK_JOB_ID}/full" 2>&1)
    STOCK_STATUS=$(echo "$POLL" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('status','unknown'))" 2>/dev/null || echo "unknown")
    echo "  Poll $i: status=$STOCK_STATUS"
    if [ "$STOCK_STATUS" = "SUCCEEDED" ] || [ "$STOCK_STATUS" = "FAILED" ] || [ "$STOCK_STATUS" = "DEAD_LETTERED" ]; then
      break
    fi
    sleep 5
  done
  check "Stock job reached SUCCEEDED" '[ "$STOCK_STATUS" = "SUCCEEDED" ]'
fi

# --- SQLite verification AFTER run ---
echo ""
echo "--- SQLite verification (AFTER run) ---"
AFTER_ARTLIST_TOTAL=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist';" 2>/dev/null || echo 0)
AFTER_ARTLIST_INDEXED=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND index_state='INDEXED';" 2>/dev/null || echo 0)
AFTER_ARTLIST_WITH_DRIVE=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND drive_file_id != '';" 2>/dev/null || echo 0)
AFTER_ARTLIST_WITH_HASH=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND file_hash != '';" 2>/dev/null || echo 0)
echo "  artlist total=$AFTER_ARTLIST_TOTAL (was $BEFORE_ARTLIST_TOTAL)"
echo "  artlist indexed=$AFTER_ARTLIST_INDEXED (was $BEFORE_ARTLIST_INDEXED)"
echo "  artlist with_drive=$AFTER_ARTLIST_WITH_DRIVE (was $BEFORE_ARTLIST_WITH_DRIVE)"
echo "  artlist with_hash=$AFTER_ARTLIST_WITH_HASH (was $BEFORE_ARTLIST_WITH_HASH)"

AFTER_STOCK_TOTAL=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='stock';" 2>/dev/null || echo 0)
AFTER_STOCK_INDEXED=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='stock' AND index_state='INDEXED';" 2>/dev/null || echo 0)
AFTER_STOCK_WITH_DRIVE=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='stock' AND drive_file_id != '';" 2>/dev/null || echo 0)
echo "  stock total=$AFTER_STOCK_TOTAL (was $BEFORE_STOCK_TOTAL)"
echo "  stock indexed=$AFTER_STOCK_INDEXED (was $BEFORE_STOCK_INDEXED)"
echo "  stock with_drive=$AFTER_STOCK_WITH_DRIVE (was $BEFORE_STOCK_WITH_DRIVE)"

check "New artlist assets created" '[ "$AFTER_ARTLIST_TOTAL" -gt "$BEFORE_ARTLIST_TOTAL" ]'
check "Artlist assets have drive_file_id" '[ "$AFTER_ARTLIST_WITH_DRIVE" -gt "$BEFORE_ARTLIST_WITH_DRIVE" ]'
check "Artlist assets have file_hash" '[ "$AFTER_ARTLIST_WITH_HASH" -gt "$BEFORE_ARTLIST_WITH_HASH" ]'
check "Artlist index_state=INDEXED" '[ "$AFTER_ARTLIST_INDEXED" -gt "$BEFORE_ARTLIST_INDEXED" ]'

# --- Latest artlist rows detail ---
echo ""
echo "--- Latest artlist assets (top 5) ---"
sqlite3 -header -column data/media/media.db.sqlite \
  "SELECT id, filename, CASE WHEN drive_file_id='' THEN '(empty)' ELSE substr(drive_file_id,1,12)||'...' END as drive_id, CASE WHEN file_hash='' THEN '(empty)' ELSE substr(file_hash,1,12)||'...' END as hash, index_state, lifecycle_state FROM media_assets WHERE source='artlist' ORDER BY created_at DESC LIMIT 5;" 2>&1

# --- Latest stock rows detail ---
echo ""
echo "--- Latest stock assets (top 5) ---"
sqlite3 -header -column data/media/media.db.sqlite \
  "SELECT id, filename, CASE WHEN drive_file_id='' THEN '(empty)' ELSE substr(drive_file_id,1,12)||'...' END as drive_id, CASE WHEN file_hash='' THEN '(empty)' ELSE substr(file_hash,1,12)||'...' END as hash, index_state, lifecycle_state FROM media_assets WHERE source='stock' ORDER BY created_at DESC LIMIT 5;" 2>&1

# --- Outbox events ---
echo ""
echo "--- Outbox events ---"
sqlite3 -header -column data/media/media.db.sqlite \
  "SELECT event_type, status, COUNT(*) as cnt FROM outbox_events GROUP BY event_type, status ORDER BY event_type, status;" 2>&1

# --- Log scan ---
echo ""
echo "--- Log scan for critical errors ---"
echo "  database is locked: $(grep -c 'database is locked' "$LOGFILE" 2>/dev/null || echo 0)"
echo "  panic: $(grep -c 'panic' "$LOGFILE" 2>/dev/null || echo 0)"
echo "  nil pointer: $(grep -c 'nil pointer\|nil dereference' "$LOGFILE" 2>/dev/null || echo 0)"
echo "  concurrent map: $(grep -c 'concurrent map' "$LOGFILE" 2>/dev/null || echo 0)"
echo "  SIGSEGV: $(grep -c 'SIGSEGV' "$LOGFILE" 2>/dev/null || echo 0)"

# --- Cleanup ---
echo ""
echo "--- Stopping server ---"
kill $SERVER_PID 2>/dev/null || true
wait $SERVER_PID 2>/dev/null || true

# --- Summary ---
echo ""
echo "========================================="
echo " §8 VERDICT: $PASS/$TOTAL passed, $FAIL/$TOTAL failed"
echo "========================================="

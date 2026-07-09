#!/usr/bin/env bash
set -uo pipefail
cd "$(dirname "$0")/.."

TOKEN=$(grep '^VELOX_ADMIN_TOKEN=' .env | cut -d= -f2 | tr -d '"' | tr -d "'")
BASE="http://127.0.0.1:8000"
LOGFILE="/tmp/pipelinegen_e2e_v2.log"
PASS=0; FAIL=0; TOTAL=0
MAX_POLL=30  # 30 polls × 5s = 150s max per job

check() {
  TOTAL=$((TOTAL+1))
  if eval "$2"; then echo "  ✅ PASS: $1"; PASS=$((PASS+1)); else echo "  ❌ FAIL: $1"; FAIL=$((FAIL+1)); fi
}

echo "========================================="
echo " §8 Artlist/Stock Pipeline E2E Test v2"
echo "========================================="

# --- Cleanup ---
fuser -k 8000/tcp 2>/dev/null || true
sleep 1

# --- SQLite baseline BEFORE ---
echo ""
echo "=== SQLite baseline (BEFORE) ==="
BEFORE_ARTLIST_TOTAL=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist';" 2>/dev/null || echo 0)
BEFORE_ARTLIST_DRIVE=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND drive_file_id != '';" 2>/dev/null || echo 0)
BEFORE_ARTLIST_HASH=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND file_hash != '';" 2>/dev/null || echo 0)
BEFORE_ARTLIST_IDX=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND index_state='INDEXED';" 2>/dev/null || echo 0)
BEFORE_STOCK_TOTAL=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='stock';" 2>/dev/null || echo 0)
BEFORE_STOCK_DRIVE=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='stock' AND drive_file_id != '';" 2>/dev/null || echo 0)
BEFORE_STOCK_IDX=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='stock' AND index_state='INDEXED';" 2>/dev/null || echo 0)
echo "  artlist: total=$BEFORE_ARTLIST_TOTAL drive=$BEFORE_ARTLIST_DRIVE hash=$BEFORE_ARTLIST_HASH indexed=$BEFORE_ARTLIST_IDX"
echo "  stock:   total=$BEFORE_STOCK_TOTAL drive=$BEFORE_STOCK_DRIVE indexed=$BEFORE_STOCK_IDX"

# --- Launch server ---
echo ""
echo "=== Launching PipelineGen server ==="
./pipelinegen --mode all </dev/null >"$LOGFILE" 2>&1 &
SERVER_PID=$!
echo "  PID=$SERVER_PID"

READY=0
for i in $(seq 1 20); do
  if curl -s --max-time 2 "$BASE/health" >/dev/null 2>&1; then READY=1; echo "  Ready after ${i}s"; break; fi
  sleep 1
done
if [ "$READY" -eq 0 ]; then echo "  ❌ Server not ready in 20s"; tail -20 "$LOGFILE"; kill $SERVER_PID 2>/dev/null; exit 1; fi

# --- Phase 1: Read-only endpoints ---
echo ""
echo "=== Phase 1: Artlist diagnostics/search/stats ==="
D_HTTP=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 -H "X-Velox-Admin-Token: $TOKEN" "$BASE/api/artlist/diagnostics?term=boxing" 2>&1)
S_HTTP=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 -H "X-Velox-Admin-Token: $TOKEN" -X POST -H 'Content-Type: application/json' -d '{"term":"boxing","limit":3}' "$BASE/api/artlist/search" 2>&1)
T_HTTP=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 -H "X-Velox-Admin-Token: $TOKEN" "$BASE/api/artlist/stats" 2>&1)
echo "  diagnostics=$D_HTTP search=$S_HTTP stats=$T_HTTP"
check "Artlist diagnostics 200" '[ "$D_HTTP" = "200" ]'
check "Artlist search 200" '[ "$S_HTTP" = "200" ]'
check "Artlist stats 200" '[ "$T_HTTP" = "200" ]'

# --- Phase 2: Launch BOTH runs in parallel ---
echo ""
echo "=== Phase 2: Launch Artlist + Stock runs in parallel ==="

# Artlist run (background)
curl -s --max-time 90 -H "X-Velox-Admin-Token: $TOKEN" -X POST -H 'Content-Type: application/json' \
  -d '{"term":"boxing","limit":3}' "$BASE/api/artlist/run" > /tmp/artlist_run_response.json 2>&1 &
ARTLIST_CURL_PID=$!

# Stock run (background)
curl -s --max-time 90 -H "X-Velox-Admin-Token: $TOKEN" -X POST -H 'Content-Type: application/json' \
  -d '{"search_queries":["boxing training"],"total_minutes":1,"chunk_duration":10,"clip_duration":10,"max_videos":1,"async":true}' \
  "$BASE/api/stock-pipeline/run" > /tmp/stock_run_response.json 2>&1 &
STOCK_CURL_PID=$!

# Wait for both to complete
echo "  Waiting for both runs to enqueue..."
wait $ARTLIST_CURL_PID 2>/dev/null || true
wait $STOCK_CURL_PID 2>/dev/null || true

echo "  Artlist response:"
cat /tmp/artlist_run_response.json 2>/dev/null | python3 -m json.tool 2>/dev/null | head -15 || echo "  (empty/error)"
echo "  Stock response:"
cat /tmp/stock_run_response.json 2>/dev/null | python3 -m json.tool 2>/dev/null | head -10 || echo "  (empty/error)"

# Extract IDs
ARTLIST_RUN_ID=$(python3 -c "import json; d=json.load(open('/tmp/artlist_run_response.json')); print(d.get('run_id',''))" 2>/dev/null || echo "")
STOCK_JOB_ID=$(python3 -c "import json; d=json.load(open('/tmp/stock_run_response.json')); print(d.get('job_id',''))" 2>/dev/null || echo "")
echo "  Artlist run_id=$ARTLIST_RUN_ID"
echo "  Stock job_id=$STOCK_JOB_ID"
check "Artlist returns run_id" '[ -n "$ARTLIST_RUN_ID" ]'
check "Stock returns job_id" '[ -n "$STOCK_JOB_ID" ]'

# --- Phase 3: Poll BOTH in parallel ---
echo ""
echo "=== Phase 3: Poll both jobs (max ${MAX_POLL}×5s = $((MAX_POLL*5))s) ==="
ARTLIST_TERMINAL=""
STOCK_TERMINAL=""
BOTH_DONE=0

for i in $(seq 1 $MAX_POLL); do
  # Poll Artlist (if not terminal)
  if [ -n "$ARTLIST_RUN_ID" ] && [ -z "$ARTLIST_TERMINAL" ]; then
    A_RESP=$(curl -s --max-time 8 -H "X-Velox-Admin-Token: $TOKEN" "$BASE/api/artlist/runs/$ARTLIST_RUN_ID" 2>&1)
    ARTLIST_TERMINAL=$(echo "$A_RESP" | python3 -c "import sys,json; s=json.load(sys.stdin).get('status','unknown'); print(s) if s in ('completed','succeeded','SUCCEEDED','FAILED','failed','not_found','dead_lettered') else print('')" 2>/dev/null || echo "")
    A_STATUS=$(echo "$A_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('status','unknown'))" 2>/dev/null || echo "unknown")
  else
    A_STATUS="${ARTLIST_TERMINAL:-done}"
  fi

  # Poll Stock (if not terminal)
  if [ -n "$STOCK_JOB_ID" ] && [ -z "$STOCK_TERMINAL" ]; then
    S_RESP=$(curl -s --max-time 8 -H "X-Velox-Admin-Token: $TOKEN" "$BASE/api/jobs/$STOCK_JOB_ID/full" 2>&1)
    STOCK_TERMINAL=$(echo "$S_RESP" | python3 -c "import sys,json; s=json.load(sys.stdin).get('status',''); print(s) if s in ('SUCCEEDED','FAILED','DEAD_LETTERED') else print('')" 2>/dev/null || echo "")
    S_STATUS=$(echo "$S_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('status','unknown'))" 2>/dev/null || echo "unknown")
  else
    S_STATUS="${STOCK_TERMINAL:-done}"
  fi

  echo "  Poll $i: artlist=$A_STATUS stock=$S_STATUS"

  if [ -n "$ARTLIST_TERMINAL" ] && [ -n "$STOCK_TERMINAL" ]; then
    BOTH_DONE=1; break
  fi
  sleep 5
done

echo "  Artlist terminal=${ARTLIST_TERMINAL:-TIMEOUT}"
echo "  Stock terminal=${STOCK_TERMINAL:-TIMEOUT}"
check "Artlist completed/succeeded" "case '${ARTLIST_TERMINAL}' in completed|succeeded|SUCCEEDED) exit 0;; *) exit 1;; esac"
check "Stock SUCCEEDED" '[ "$STOCK_TERMINAL" = "SUCCEEDED" ]'

# --- Phase 4: SQLite verification AFTER ---
echo ""
echo "=== Phase 4: SQLite verification (AFTER) ==="
AFTER_ARTLIST_TOTAL=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist';" 2>/dev/null || echo 0)
AFTER_ARTLIST_DRIVE=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND drive_file_id != '';" 2>/dev/null || echo 0)
AFTER_ARTLIST_HASH=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND file_hash != '';" 2>/dev/null || echo 0)
AFTER_ARTLIST_IDX=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND index_state='INDEXED';" 2>/dev/null || echo 0)
AFTER_STOCK_TOTAL=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='stock';" 2>/dev/null || echo 0)
AFTER_STOCK_DRIVE=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='stock' AND drive_file_id != '';" 2>/dev/null || echo 0)
AFTER_STOCK_IDX=$(sqlite3 data/media/media.db.sqlite "SELECT COUNT(*) FROM media_assets WHERE source='stock' AND index_state='INDEXED';" 2>/dev/null || echo 0)
echo "  artlist: total=$AFTER_ARTLIST_TOTAL (was $BEFORE_ARTLIST_TOTAL) drive=$AFTER_ARTLIST_DRIVE (was $BEFORE_ARTLIST_DRIVE) hash=$AFTER_ARTLIST_HASH (was $BEFORE_ARTLIST_HASH) indexed=$AFTER_ARTLIST_IDX (was $BEFORE_ARTLIST_IDX)"
echo "  stock:   total=$AFTER_STOCK_TOTAL (was $BEFORE_STOCK_TOTAL) drive=$AFTER_STOCK_DRIVE (was $BEFORE_STOCK_DRIVE) indexed=$AFTER_STOCK_IDX (was $BEFORE_STOCK_IDX)"

check "New artlist assets created" '[ "$AFTER_ARTLIST_TOTAL" -gt "$BEFORE_ARTLIST_TOTAL" ]'
check "Artlist assets have drive_file_id" '[ "$AFTER_ARTLIST_DRIVE" -gt "$BEFORE_ARTLIST_DRIVE" ]'
check "Artlist assets have file_hash" '[ "$AFTER_ARTLIST_HASH" -gt "$BEFORE_ARTLIST_HASH" ]'
check "Artlist index_state=INDEXED" '[ "$AFTER_ARTLIST_IDX" -gt "$BEFORE_ARTLIST_IDX" ]'
check "New stock assets created" '[ "$AFTER_STOCK_TOTAL" -gt "$BEFORE_STOCK_TOTAL" ]'

# --- Phase 5: Detail tables ---
echo ""
echo "=== Phase 5: Asset detail ==="
echo "  --- Latest artlist (top 5) ---"
sqlite3 -header -column data/media/media.db.sqlite \
  "SELECT id, filename, CASE WHEN drive_file_id='' THEN '-' ELSE substr(drive_file_id,1,12)||'...' END as drive, CASE WHEN file_hash='' THEN '-' ELSE substr(file_hash,1,12)||'...' END as hash, index_state, lifecycle_state FROM media_assets WHERE source='artlist' ORDER BY created_at DESC LIMIT 5;" 2>&1

echo "  --- Latest stock (top 5) ---"
sqlite3 -header -column data/media/media.db.sqlite \
  "SELECT id, filename, CASE WHEN drive_file_id='' THEN '-' ELSE substr(drive_file_id,1,12)||'...' END as drive, CASE WHEN file_hash='' THEN '-' ELSE substr(file_hash,1,12)||'...' END as hash, index_state, lifecycle_state FROM media_assets WHERE source='stock' ORDER BY created_at DESC LIMIT 5;" 2>&1

echo "  --- Outbox events ---"
sqlite3 -header -column data/media/media.db.sqlite \
  "SELECT event_type, status, COUNT(*) as cnt FROM outbox_events GROUP BY event_type, status ORDER BY event_type, status;" 2>&1

echo "  --- Jobs (artlist+stock) ---"
sqlite3 -header -column data/media/media.db.sqlite \
  "SELECT id, type, status, substr(error_message,1,60) as error FROM jobs WHERE type LIKE '%artlist%' OR type LIKE '%stock%' ORDER BY created_at DESC LIMIT 5;" 2>&1

# --- Phase 6: Log scan ---
echo ""
echo "=== Phase 6: Log scan ==="
echo "  database is locked: $(grep -c 'database is locked' "$LOGFILE" 2>/dev/null || echo 0)"
echo "  panic: $(grep -c 'panic' "$LOGFILE" 2>/dev/null || echo 0)"
echo "  nil pointer: $(grep -c 'nil pointer\|nil dereference' "$LOGFILE" 2>/dev/null || echo 0)"
echo "  concurrent map: $(grep -c 'concurrent map' "$LOGFILE" 2>/dev/null || echo 0)"
echo "  SIGSEGV: $(grep -c 'SIGSEGV' "$LOGFILE" 2>/dev/null || echo 0)"
echo "  --- Artlist errors in log ---"
grep -i 'artlist.*error\|artlist.*fail\|artlist.*warn' "$LOGFILE" 2>/dev/null | tail -5 || echo "  (none)"
echo "  --- Stock errors in log ---"
grep -i 'stock.*error\|stock.*fail\|stock.*warn' "$LOGFILE" 2>/dev/null | tail -5 || echo "  (none)"

# --- Cleanup ---
echo ""
echo "=== Stopping server ==="
kill $SERVER_PID 2>/dev/null || true
wait $SERVER_PID 2>/dev/null || true

# --- Summary ---
echo ""
echo "========================================="
echo " §8 VERDICT: $PASS/$TOTAL passed, $FAIL/$TOTAL failed"
echo "========================================="

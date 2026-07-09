#!/usr/bin/env bash
set -uo pipefail
cd "$(dirname "$0")/.."

TOKEN=$(grep '^VELOX_ADMIN_TOKEN=' .env | cut -d= -f2 | tr -d '"' | tr -d "'")
BASE="http://127.0.0.1:8000"
OUTFILE="/tmp/artlist_stock_e2e_final_output.txt"

exec > >(tee "$OUTFILE") 2>&1

echo "========================================="
echo " §8 Artlist/Stock Pipeline E2E Final"
echo " $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "========================================="

# Cleanup
fuser -k 8000/tcp 2>/dev/null || true
sleep 1

# SQLite baseline
echo ""
echo "=== SQLite BEFORE ==="
sqlite3 data/media/media.db.sqlite "SELECT 'artlist:' || COUNT(*) || ' drive:' || COUNT(CASE WHEN drive_file_id!='' THEN 1 END) || ' hash:' || COUNT(CASE WHEN file_hash!='' THEN 1 END) || ' indexed:' || COUNT(CASE WHEN index_state='INDEXED' THEN 1 END) FROM media_assets WHERE source='artlist';" 2>/dev/null
sqlite3 data/media/media.db.sqlite "SELECT 'stock:' || COUNT(*) || ' drive:' || COUNT(CASE WHEN drive_file_id!='' THEN 1 END) || ' indexed:' || COUNT(CASE WHEN index_state='INDEXED' THEN 1 END) FROM media_assets WHERE source='stock';" 2>/dev/null

# Launch server
echo ""
echo "=== Launching server ==="
LOGFILE="/tmp/pipelinegen_final.log"
./pipelinegen --mode all </dev/null >"$LOGFILE" 2>&1 &
SERVER_PID=$!
echo "PID=$SERVER_PID"

READY=0
for i in $(seq 1 15); do
  if curl -s --max-time 2 "$BASE/health" >/dev/null 2>&1; then READY=1; break; fi
  sleep 1
done
echo "Ready=$READY (after ${i}s)"
if [ "$READY" -eq 0 ]; then echo "FAIL: server not ready"; tail -20 "$LOGFILE"; exit 1; fi

# Quick diagnostics
echo ""
echo "=== Phase 1: Endpoints ==="
for ep in "GET /api/artlist/diagnostics?term=boxing" "POST /api/artlist/search" "GET /api/artlist/stats" "GET /api/artlist/cache/status"; do
  METHOD=$(echo "$ep" | cut -d' ' -f1)
  PATH_=$(echo "$ep" | cut -d' ' -f2)
  if [ "$METHOD" = "POST" ]; then
    HTTP=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 -H "X-Velox-Admin-Token: $TOKEN" -X POST -H 'Content-Type: application/json' -d '{"term":"boxing","limit":3}' "$BASE$PATH_" 2>&1)
  else
    HTTP=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 -H "X-Velox-Admin-Token: $TOKEN" "$BASE$PATH_" 2>&1)
  fi
  echo "  $METHOD $PATH_ → $HTTP"
done

# Fire Artlist run (limit=1 for speed)
echo ""
echo "=== Phase 2: Artlist run (limit=1) ==="
ARTLIST_RESP=$(curl -s --max-time 90 -H "X-Velox-Admin-Token: $TOKEN" -X POST -H 'Content-Type: application/json' \
  -d '{"term":"boxing","limit":1}' "$BASE/api/artlist/run" 2>&1)
echo "$ARTLIST_RESP" | python3 -m json.tool 2>/dev/null || echo "$ARTLIST_RESP"
ARTLIST_RUN_ID=$(echo "$ARTLIST_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('run_id',''))" 2>/dev/null || echo "")
echo "run_id=$ARTLIST_RUN_ID"

# Fire Stock run (limit=1 for speed)
echo ""
echo "=== Phase 3: Stock run (1 video) ==="
STOCK_RESP=$(curl -s --max-time 90 -H "X-Velox-Admin-Token: $TOKEN" -X POST -H 'Content-Type: application/json' \
  -d '{"search_queries":["boxing training"],"total_minutes":1,"chunk_duration":10,"clip_duration":10,"max_videos":1,"async":true}' \
  "$BASE/api/stock-pipeline/run" 2>&1)
echo "$STOCK_RESP" | python3 -m json.tool 2>/dev/null || echo "$STOCK_RESP"
STOCK_JOB_ID=$(echo "$STOCK_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('job_id',''))" 2>/dev/null || echo "")
echo "job_id=$STOCK_JOB_ID"

# Poll both (max 40 iterations × 5s = 200s)
echo ""
echo "=== Phase 4: Polling (max 200s) ==="
ARTLIST_DONE=""
STOCK_DONE=""
for i in $(seq 1 40); do
  # Artlist
  if [ -n "$ARTLIST_RUN_ID" ] && [ -z "$ARTLIST_DONE" ]; then
    A=$(curl -s --max-time 5 -H "X-Velox-Admin-Token: $TOKEN" "$BASE/api/artlist/runs/$ARTLIST_RUN_ID" 2>&1)
    AS=$(echo "$A" | python3 -c "import sys,json; print(json.load(sys.stdin).get('status','?'))" 2>/dev/null || echo "?")
    case "$AS" in completed|succeeded|FAILED|failed|not_found|dead_lettered) ARTLIST_DONE="$AS" ;; esac
  else AS="${ARTLIST_DONE:-done}"; fi
  # Stock
  if [ -n "$STOCK_JOB_ID" ] && [ -z "$STOCK_DONE" ]; then
    S=$(curl -s --max-time 5 -H "X-Velox-Admin-Token: $TOKEN" "$BASE/api/jobs/$STOCK_JOB_ID/full" 2>&1)
    SS=$(echo "$S" | python3 -c "import sys,json; print(json.load(sys.stdin).get('status','?'))" 2>/dev/null || echo "?")
    case "$SS" in SUCCEEDED|FAILED|DEAD_LETTERED) STOCK_DONE="$SS" ;; esac
  else SS="${STOCK_DONE:-done}"; fi
  echo "  $i: artlist=$AS stock=$SS"
  if [ -n "$ARTLIST_DONE" ] && [ -n "$STOCK_DONE" ]; then break; fi
  sleep 5
done

# === CRITICAL: Dump jobs table for RETRY_WAIT/FAILED error messages ===
echo ""
echo "=== Phase 5: Jobs table (error messages) ==="
sqlite3 -header -column data/media/media.db.sqlite \
  "SELECT id, type, status, attempt, substr(error_message,1,200) as error FROM jobs ORDER BY created_at DESC LIMIT 10;" 2>/dev/null

echo ""
echo "=== Phase 6: Outbox events ==="
sqlite3 -header -column data/media/media.db.sqlite \
  "SELECT id, event_type, status, attempt_count, substr(last_error,1,150) as error FROM outbox_events ORDER BY created_at DESC LIMIT 10;" 2>/dev/null

# SQLite AFTER
echo ""
echo "=== Phase 7: SQLite AFTER ==="
sqlite3 data/media/media.db.sqlite "SELECT 'artlist:' || COUNT(*) || ' drive:' || COUNT(CASE WHEN drive_file_id!='' THEN 1 END) || ' hash:' || COUNT(CASE WHEN file_hash!='' THEN 1 END) || ' indexed:' || COUNT(CASE WHEN index_state='INDEXED' THEN 1 END) FROM media_assets WHERE source='artlist';" 2>/dev/null
sqlite3 data/media/media.db.sqlite "SELECT 'stock:' || COUNT(*) || ' drive:' || COUNT(CASE WHEN drive_file_id!='' THEN 1 END) || ' indexed:' || COUNT(CASE WHEN index_state='INDEXED' THEN 1 END) FROM media_assets WHERE source='stock';" 2>/dev/null

# Log scan
echo ""
echo "=== Phase 8: Log scan ==="
echo "  database is locked: $(grep -c 'database is locked' "$LOGFILE" 2>/dev/null || echo 0)"
echo "  panic: $(grep -c 'panic' "$LOGFILE" 2>/dev/null || echo 0)"
echo "  nil pointer: $(grep -c 'nil pointer\|nil dereference' "$LOGFILE" 2>/dev/null || echo 0)"
echo "  concurrent map: $(grep -c 'concurrent map' "$LOGFILE" 2>/dev/null || echo 0)"

# Artlist/Stock error lines from log
echo ""
echo "=== Phase 9: Error lines from log ==="
grep -iE 'artlist.*(error|fail|warn)|RETRY_WAIT|retry_pending|step.*fail|worker.*error|orchestrator.*error' "$LOGFILE" 2>/dev/null | head -20
echo "---"
grep -iE 'stock.*(error|fail|warn)|stock.*retry' "$LOGFILE" 2>/dev/null | head -10

# Cleanup
echo ""
echo "=== Stopping server ==="
kill $SERVER_PID 2>/dev/null || true
wait $SERVER_PID 2>/dev/null || true

echo ""
echo "========================================="
echo " Full output saved to: $OUTFILE"
echo "========================================="

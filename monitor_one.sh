#!/usr/bin/env bash
# Usage: monitor_one.sh <job_id> <logfile>
JOB_ID="$1"; LOG="$2"
cd /home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored
TOK=$(grep -m1 '^VELOX_ADMIN_TOKEN=' .env | cut -d= -f2- | tr -d '"' | tr -d "'" | tr -d ' ')
echo "START $JOB_ID at $(date -u +%FT%TZ)" >> "$LOG"
for i in $(seq 1 120); do
  OUT=$(curl -s -m 20 -H "X-Velox-Admin-Token: $TOK" "http://127.0.0.1:8000/api/jobs/$JOB_ID/full" 2>/dev/null)
  STATUS=$(echo "$OUT" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('status','?'))" 2>/dev/null || echo "?")
  echo "$(date -u +%FT%TZ) poll=$i status=$STATUS" >> "$LOG"
  case "$STATUS" in
    SUCCEEDED|FAILED|CANCELLED) echo "$(date -u +%FT%TZ) TERMINAL status=$STATUS" >> "$LOG"; exit 0;;
  esac
  sleep 30
done
echo "$(date -u +%FT%TZ) TIMEOUT after 60min" >> "$LOG"

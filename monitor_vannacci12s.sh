#!/usr/bin/env bash
# Polls both staggered Vannacci 60-clip waves until terminal.
LOG="/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/data/vannacci12s_waves.log"
JOBS="job_1792055317217018301_43cf5f4d job_1792055317361074202_8f5505a9"
cd /home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored
TOK=$(grep -m1 '^VELOX_ADMIN_TOKEN=' .env | cut -d= -f2- | tr -d '"' | tr -d "'" | tr -d ' ')
echo "START waves at $(date -u +%FT%TZ): $JOBS" >> "$LOG"
declare -A DONE
for i in $(seq 1 160); do
  ALL_DONE=1
  for J in $JOBS; do
    if [ "${DONE[$J]:-0}" = "1" ]; then continue; fi
    OUT=$(curl -s -m 20 -H "X-Velox-Admin-Token: $TOK" "http://127.0.0.1:8000/api/jobs/$J/full" 2>/dev/null)
    STATUS=$(echo "$OUT" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('status','?'))" 2>/dev/null || echo "?")
    STAGE=$(echo "$OUT" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('current_stage','?'))" 2>/dev/null || echo "?")
    echo "$(date -u +%FT%TZ) poll=$i job=${J:4:12} status=$STATUS stage=$STAGE" >> "$LOG"
    case "$STATUS" in
      SUCCEEDED|FAILED|CANCELLED) DONE[$J]=1; echo "$(date -u +%FT%TZ) TERMINAL $J status=$STATUS stage=$STAGE" >> "$LOG";;
      *) ALL_DONE=0;;
    esac
  done
  if [ "$ALL_DONE" = "1" ]; then exit 0; fi
  sleep 30
done
echo "$(date -u +%FT%TZ) TIMEOUT after 80min" >> "$LOG"

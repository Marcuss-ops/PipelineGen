#!/usr/bin/env bash
# Polls both Corona 60-clip waves until terminal.
LOG="/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/data/corona12_waves.log"
JOBS="job_1792049316985693190_249c70d4 job_1792049317089946311_5b4c0bff"
cd /home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored
TOK=$(grep -m1 '^VELOX_ADMIN_TOKEN=' .env | cut -d= -f2- | tr -d '"' | tr -d "'" | tr -d ' ')
echo "START waves at $(date -u +%FT%TZ): $JOBS" >> "$LOG"
declare -A DONE
for i in $(seq 1 120); do
  ALL_DONE=1
  for J in $JOBS; do
    if [ "${DONE[$J]:-0}" = "1" ]; then continue; fi
    OUT=$(curl -s -m 20 -H "X-Velox-Admin-Token: $TOK" "http://127.0.0.1:8000/api/jobs/$J/full" 2>/dev/null)
    STATUS=$(echo "$OUT" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('status','?'))" 2>/dev/null || echo "?")
    echo "$(date -u +%FT%TZ) poll=$i job=${J:4:12} status=$STATUS" >> "$LOG"
    case "$STATUS" in
      SUCCEEDED|FAILED|CANCELLED) DONE[$J]=1; echo "$(date -u +%FT%TZ) TERMINAL $J status=$STATUS" >> "$LOG";;
      *) ALL_DONE=0;;
    esac
  done
  if [ "$ALL_DONE" = "1" ]; then exit 0; fi
  sleep 30
done
echo "$(date -u +%FT%TZ) TIMEOUT after 60min" >> "$LOG"

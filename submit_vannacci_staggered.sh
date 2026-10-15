#!/usr/bin/env bash
# Staggered Vannacci relaunch: wave1 -> wait until not QUEUED/staging -> wave2.
# Avoids the x10 yt-dlp thundering herd that killed stage_sources.
set -euo pipefail
cd /home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored
FOLDER="1NUdqtIQkF05Pe5OrmQMFbOFbOlXo7yE8"
TOK=$(grep -m1 '^VELOX_ADMIN_TOKEN=' .env | cut -d= -f2- | tr -d '"' | tr -d "'" | tr -d ' ')
submit() {
  local tag="$1"; shift
  local urls; urls=$(printf '"%s",' "$@" | sed 's/,$//')
  curl -s -m 30 -X POST http://127.0.0.1:8000/api/stock-pipeline/run \
    -H 'Content-Type: application/json' \
    -H "X-Velox-Admin-Token: $TOK" \
    -d "{\"direct_urls\":[$urls],\"clips_per_source\":12,\"clip_duration_seconds\":5,\"target_total_duration_seconds\":300,\"target_duration_per_source_seconds\":60,\"download_mode\":\"sections_only\",\"drive_folder_id\":\"$FOLDER\",\"folder_id\":\"$FOLDER\",\"subfolder\":\"\",\"async\":true,\"persist\":true}" \
    | tee "data/stock12_${tag}.submit.json"
  echo
}
W1=(https://www.youtube.com/watch?v=WvDLMyYZW9A https://www.youtube.com/watch?v=Sh7RIPIVhnA https://www.youtube.com/watch?v=1yZ8qsfPgiY https://www.youtube.com/watch?v=8gv34dcRtyw https://www.youtube.com/watch?v=iRceplkQe2s)
W2=(https://www.youtube.com/watch?v=ecN7BXbw_VQ https://www.youtube.com/watch?v=dlzbmV1nfGQ https://www.youtube.com/watch?v=8IJBRlAJTW4 https://www.youtube.com/watch?v=oMNPlX0RYDU https://www.youtube.com/watch?v=DPjFqKlQecQ)
echo "=== WAVE1 submit $(date -u +%FT%TZ)"; submit vannacci1s "${W1[@]}"
J1=$(python3 -c "import json; print(json.load(open('data/stock12_vannacci1s.submit.json'))['job_id'])")
echo "J1=$J1"
# Wait until wave1 leaves early staging (stage != stage_sources or terminal), max 25 min.
for i in $(seq 1 50); do
  OUT=$(curl -s -m 20 -H "X-Velox-Admin-Token: $TOK" "http://127.0.0.1:8000/api/jobs/$J1/full")
  ST=$(echo "$OUT" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('status'), d.get('current_stage'))" 2>/dev/null || echo "? ?")
  echo "$(date -u +%FT%TZ) wave1 $ST" | tee -a data/vannacci12s_waves.log
  case "$ST" in
    *FAILED*|*CANCELLED*) echo "WAVE1 terminal-bad, aborting wave2"; exit 1;;
    *SUCCEEDED*) break;;
  esac
  if [[ "$ST" == *"RUNNING"* ]] && [[ "$ST" != *"stage_sources"* && "$ST" != *"QUEUED"* && "$ST" != *"STAGED"* ]]; then echo "wave1 past staging -> launching wave2"; break; fi
  # Also launch wave2 after 12 min regardless (staging usually done)
  if [ "$i" -ge 24 ]; then echo "12min elapsed -> launching wave2 anyway"; break; fi
  sleep 30
done
echo "=== WAVE2 submit $(date -u +%FT%TZ)"; submit vannacci2s "${W2[@]}"
J2=$(python3 -c "import json; print(json.load(open('data/stock12_vannacci2s.submit.json'))['job_id'])")
echo "J1=$J1 J2=$J2" | tee data/vannacci12s_jobs.txt

#!/usr/bin/env bash
set -u

BASE="${VELOX_BASE_URL:-http://127.0.0.1:8000}"
OUT="${OUT_FILE:-$(dirname "$0")/results/r6_monitor.log}"
mkdir -p "$(dirname "$OUT")"
IDS=(
  job_1792526664806258673_53368d94
  job_1792526670692378516_e4d4e982
  job_1792526676171614193_e259662a
  job_1792526683478768902_a4632756
)

for attempt in $(seq 1 60); do
  pending=0
  for id in "${IDS[@]}"; do
    body=$(curl -fsS --max-time 30 -H "X-Velox-Admin-Token: $VELOX_ADMIN_TOKEN" "$BASE/api/jobs/$id/full" 2>/dev/null) || continue
    status=$(printf '%s' "$body" | jq -r '.job.status // .status // "UNKNOWN"')
    first=$(printf '%s' "$body" | jq -r '((.result.result.canonical_timeline.segments // [])[0] // {}) | [.id, (.fixed_media // false), (.duration_ms // 0)] | @tsv')
    remote=$(printf '%s' "$body" | jq -r 'if .result.result.remote_final_job then [.result.result.remote_final_job.status, (.result.result.remote_final_job.job_id // ""), (.result.result.remote_final_job.artifact_url // "")] | @tsv else "none" end')
    err=$(printf '%s' "$body" | jq -r '(.job.error // .error // "") | .[0:200]')
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$(date -u +%FT%TZ)" "$id" "$status" "$first" "$remote" "$err" >>"$OUT"
    case "$status" in
      SUCCEEDED|COMPLETED|FAILED|CANCELLED|DEAD_LETTER) ;;
      *) pending=$((pending + 1)) ;;
    esac
  done
  if [ "$pending" -eq 0 ]; then
    echo "all terminal" >>"$OUT"
    exit 0
  fi
  sleep 120
done
echo "monitor timeout" >>"$OUT"

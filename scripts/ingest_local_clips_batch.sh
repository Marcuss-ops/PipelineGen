#!/usr/bin/env bash
# Ingest pre-downloaded video segments through the canonical upload pipeline.
# Usage: scripts/ingest_local_clips_batch.sh <source-video> <clips.json> <drive-folder-id>
set -euo pipefail
umask 077

source_file=${1:?source video path is required}
manifest=${2:?JSON manifest path is required}
folder_id=${3:?Drive folder ID is required}
base_url=${PIPELINEGEN_BASE_URL:-http://127.0.0.1:8000}

[[ -f "$source_file" ]] || { echo "source video not found: $source_file" >&2; exit 2; }
[[ -f "$manifest" ]] || { echo "manifest not found: $manifest" >&2; exit 2; }
jq -e 'type == "array" and length > 0 and all(.[]; .name and (.start|numbers) and (.end|numbers) and (.end > .start))' "$manifest" >/dev/null || {
  echo "manifest must be a non-empty array with name, start and end" >&2
  exit 2
}

work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT
total=$(jq 'length' "$manifest")
passed=0
failed=0

for i in $(seq 0 $((total - 1))); do
  name=$(jq -r ".[$i].name" "$manifest")
  description=$(jq -r ".[$i].description // \"\"" "$manifest")
  tags=$(jq -c ".[$i].tags // [\"local\",\"youtube\"]" "$manifest")
  start=$(jq -r ".[$i].start" "$manifest")
  end=$(jq -r ".[$i].end" "$manifest")
  duration=$(awk -v s="$start" -v e="$end" 'BEGIN {printf "%.6f", e-s}')
  output="$work_dir/$(printf '%03d' "$((i + 1))")_${name}.mp4"

  ffmpeg -hide_banner -loglevel error -y -ss "$start" -i "$source_file" -t "$duration" \
    -map 0:v:0 -map 0:a? -c:v libx264 -c:a aac -movflags +faststart "$output"

  response="$work_dir/$(printf '%03d' "$((i + 1))").json"
  http=$(curl -sS -o "$response" -w '%{http_code}' -X POST "$base_url/api/media/clips/upload-video" \
    -H "Authorization: Bearer ${VELOX_ADMIN_TOKEN:?run through scripts/with-velox-auth}" \
    -H "Idempotency-Key: local:${folder_id}:${name}:${start}:${end}" \
    -F "file=@$output;type=video/mp4" \
    -F "name=$name" \
    -F "description=$description" \
    -F "tags=$tags" \
    -F 'source=local' \
    -F 'category=actors' \
    -F 'group=comedy-clips' \
    -F "folder_id=$folder_id")

  if [[ "$http" == 2* ]] && jq -e '.ok == true and (.clip_id|length > 0)' "$response" >/dev/null; then
    clip_id=$(jq -r '.clip_id' "$response")
    drive_link=$(jq -r '.drive_link // empty' "$response")
    echo "PASS $((i + 1))/$total $name clip_id=$clip_id drive=$drive_link"
    passed=$((passed + 1))
  else
    echo "FAIL $((i + 1))/$total $name http=$http" >&2
    jq -c . "$response" >&2 || true
    failed=$((failed + 1))
  fi
done

printf 'LOCAL_INGEST total=%d passed=%d failed=%d\n' "$total" "$passed" "$failed"
[[ "$failed" -eq 0 ]]

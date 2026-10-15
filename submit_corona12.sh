#!/usr/bin/env bash
# Usage: submit_corona12.sh <folder_id> <tag> <url1> <url2> <url3> <url4> <url5>
# Submits 5 videos x 12 stock clips x 5s to the given Drive folder.
set -euo pipefail
cd /home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored
FOLDER="$1"; TAG="$2"; shift 2
TOK=$(grep -m1 '^VELOX_ADMIN_TOKEN=' .env | cut -d= -f2- | tr -d '"' | tr -d "'" | tr -d ' ')
URLS=$(printf '"%s",' "$@" | sed 's/,$//')
date -u +%FT%TZ
curl -s -m 30 -X POST http://127.0.0.1:8000/api/stock-pipeline/run \
  -H 'Content-Type: application/json' \
  -H "X-Velox-Admin-Token: $TOK" \
  -d "{\"direct_urls\":[$URLS],\"clips_per_source\":12,\"clip_duration_seconds\":5,\"target_total_duration_seconds\":300,\"target_duration_per_source_seconds\":60,\"download_mode\":\"sections_only\",\"drive_folder_id\":\"$FOLDER\",\"folder_id\":\"$FOLDER\",\"subfolder\":\"\",\"async\":true,\"persist\":true}" | tee "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/data/stock12_$TAG.submit.json"
echo

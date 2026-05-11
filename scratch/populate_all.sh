#!/bin/bash
BASE_URL="http://127.0.0.1:8080/api"

echo "Populating Voiceover..."
curl -s -X POST $BASE_URL/voiceover/sync | jq .

echo "Populating Images..."
curl -s -X POST $BASE_URL/images/sync | jq .

echo "Populating YouTube Clips (source: youtube)..."
curl -s -X POST $BASE_URL/assets/youtube/reconcile -d '{"fix": true}' -H "Content-Type: application/json" | jq .

echo "Populating Artlist..."
curl -s -X POST $BASE_URL/assets/artlist/reconcile -d '{"fix": true}' -H "Content-Type: application/json" | jq .

echo "Populating Stock..."
curl -s -X POST $BASE_URL/assets/stock/reconcile -d '{"fix": true}' -H "Content-Type: application/json" | jq .

echo "Done."

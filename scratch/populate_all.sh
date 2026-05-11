#!/bin/bash
BASE_URL="http://127.0.0.1:8080/api"

echo "Populating Voiceover..."
curl -s -X POST $BASE_URL/voiceover/sync | jq .

echo "Populating Images..."
curl -s -X POST $BASE_URL/images/sync | jq .

echo "Populating Artlist..."
curl -s -X POST $BASE_URL/assets/artlist/reconcile -d '{"folder_id": "1eJDqcjjrSwhqVdJsv3wUKmt05ylfM1Fc", "fix": true}' -H "Content-Type: application/json" | jq .

echo "Populating Stock..."
curl -s -X POST $BASE_URL/assets/stock/reconcile -d '{"folder_id": "1x1dvQIEbJG_veo5cXbj1CzbxpNmGpK1l", "fix": true}' -H "Content-Type: application/json" | jq .

echo "Done."

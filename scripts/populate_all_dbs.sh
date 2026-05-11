#!/bin/bash

# PipelineGen Global Sync Script
# This script triggers synchronization for all major database modules.

API_BASE="http://127.0.0.1:8080/api"
INTERNAL_HEADER="X-Internal: true"

echo "=== Starting Global Database Population ==="

# 1. Sync Catalogs (Stock, Clips, Artlist)
echo "--- Syncing Catalogs (Stock, Clips, Artlist) ---"
curl -s -X POST "$API_BASE/artlist/sync-catalogs" \
     -H "$INTERNAL_HEADER" \
     -H "Content-Type: application/json" | jq .

# 2. Sync Images
echo -e "\n--- Syncing Images ---"
curl -s -X POST "$API_BASE/images/sync" \
     -H "Content-Type: application/json" | jq .

# 3. Sync Voiceovers
echo -e "\n--- Syncing Voiceovers ---"
curl -s -X POST "$API_BASE/voiceover/sync" \
     -H "Content-Type: application/json" | jq .

echo -e "\n=== Global Sync Completed ==="

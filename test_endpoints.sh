#!/bin/bash
# Test script to verify PipelineGen endpoints work correctly
# after SQLite connection centralization

set -e

BASE_URL="http://127.0.0.1:8080"
echo "Testing PipelineGen endpoints..."
echo "================================"

# Check if server is running
echo -n "Checking if server is up... "
if curl -s --connect-timeout 2 "$BASE_URL/api/artlist/diagnostics" > /dev/null 2>&1; then
    echo "OK"
else
    echo "FAILED - Server not responding"
    echo "Start the server with: sudo systemctl start pipelinegen"
    exit 1
fi

# Test diagnostics endpoint
echo ""
echo "1. Testing /api/artlist/diagnostics"
echo "-----------------------------------"
curl -s "$BASE_URL/api/artlist/diagnostics" | jq '.' || curl -s "$BASE_URL/api/artlist/diagnostics"

# Test Artlist runs endpoint (should return empty or list)
echo ""
echo ""
echo "2. Testing /api/artlist/runs (list)"
echo "-----------------------------------"
curl -s "$BASE_URL/api/artlist/runs" | jq '.' || curl -s "$BASE_URL/api/artlist/runs"

echo ""
echo "================================"
echo "Endpoint tests completed."

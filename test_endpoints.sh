#!/bin/bash
# Test script for PipelineGen endpoints
# Usage: ./test_endpoints.sh

BASE_URL="http://127.0.0.1:8080"
TOKEN="CHANGE_ME_IN_PRODUCTION"

echo "=========================================="
echo "PipelineGen API Endpoint Tests"
echo "=========================================="
echo ""

# Test 1: Health check
echo "1. Testing /health endpoint:"
curl -s "$BASE_URL/health" | jq . 2>/dev/null || curl -s "$BASE_URL/health"
echo -e "\n"

# Test 2: API Health check
echo "2. Testing /api/health endpoint:"
curl -s "$BASE_URL/api/health" | jq . 2>/dev/null || curl -s "$BASE_URL/api/health"
echo -e "\n"

# Test 3: Jobs list
echo "3. Testing /api/jobs endpoint:"
curl -s -H "X-Velox-Admin-Token: $TOKEN" "$BASE_URL/api/jobs" | jq '.count' 2>/dev/null || echo "Jobs endpoint test"
echo -e "\n"

# Test 4: Voiceover generate
echo "4. Testing /api/voiceover/generate endpoint:"
curl -s -H "X-Velox-Admin-Token: $TOKEN" \
  -X POST "$BASE_URL/api/voiceover/generate" \
  -H "Content-Type: application/json" \
  -d '{"text":"This is a test","voice":"it"}' | jq . 2>/dev/null || echo "Voiceover test complete"
echo -e "\n"

# Test 5: Artlist run (dry run)
echo "5. Testing /api/artlist/run endpoint (dry run):"
curl -s -H "X-Velox-Admin-Token: $TOKEN" \
  -X POST "$BASE_URL/api/artlist/run" \
  -H "Content-Type: application/json" \
  -d '{"term":"test","limit":1,"strategy":"verify","dry_run":true}' | jq . 2>/dev/null || echo "Artlist test"
echo -e "\n"

# Test 6: List workflows
echo "6. Testing /api/workflows/list endpoint:"
curl -s -H "X-Velox-Admin-Token: $TOKEN" \
  "$BASE_URL/api/workflows/list" | jq . 2>/dev/null || echo "Workflow list test"
echo -e "\n"

# Test 7: Run workflow from file
echo "7. Testing /api/workflows/run-file endpoint (test_artlist.yaml):"
curl -s -H "X-Velox-Admin-Token: $TOKEN" \
  -X POST "$BASE_URL/api/workflows/run-file" \
  -H "Content-Type: application/json" \
  -d '{"path":"test_artlist.yaml"}' | jq '.Status' 2>/dev/null || echo "Workflow run-file test"
echo -e "\n"

# Test 8: Enqueue workflow job
echo "8. Testing /api/workflows/run endpoint:"
curl -s -H "X-Velox-Admin-Token: $TOKEN" \
  -X POST "$BASE_URL/api/workflows/run" \
  -H "Content-Type: application/json" \
  -d '{"workflow":"test_artlist.yaml"}' | jq . 2>/dev/null || echo "Workflow run test"
echo -e "\n"

echo "=========================================="
echo "Test complete. Check output above."
echo "=========================================="

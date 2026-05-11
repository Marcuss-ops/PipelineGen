#!/bin/bash
# Test Artlist Pipeline Endpoints
# Usage: bash test_artlist_pipeline.sh

set -e

BASE_URL="http://127.0.0.1:8080"
API_URL="${BASE_URL}/api"

# Color output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${YELLOW}=== Artlist Pipeline Test Suite ===${NC}\n"

# 1. Health Check
echo -e "${YELLOW}1. Testing Health Endpoint...${NC}"
HEALTH=$(curl -s "${BASE_URL}/health")
if echo "$HEALTH" | grep -q '"status":"ok"'; then
    echo -e "${GREEN}✓ Health check passed${NC}"
else
    echo -e "${RED}✗ Health check failed${NC}"
    echo "$HEALTH"
fi

# 2. Check Service Status
echo -e "\n${YELLOW}2. Checking PipelineGen Service Status...${NC}"
echo "ciao" | sudo -S systemctl status pipelinegen --no-pager -l || true

# 3. Test Artlist Diagnostics
echo -e "\n${YELLOW}3. Testing Artlist Diagnostics Endpoint...${NC}"
DIAG=$(curl -s "${API_URL}/artlist/diagnostics?term=test")
if echo "$DIAG" | grep -q '"ok"'; then
    echo -e "${GREEN}✓ Diagnostics endpoint working${NC}"
    echo "$DIAG" | jq . 2>/dev/null || echo "$DIAG"
else
    echo -e "${RED}✗ Diagnostics endpoint failed${NC}"
    echo "$DIAG"
fi

# 4. Test Artlist Stats
echo -e "\n${YELLOW}4. Testing Artlist Stats Endpoint...${NC}"
STATS=$(curl -s "${API_URL}/artlist/stats")
if echo "$STATS" | grep -q '"total_clips"\|"ok"'; then
    echo -e "${GREEN}✓ Stats endpoint working${NC}"
    echo "$STATS" | jq . 2>/dev/null || echo "$STATS"
else
    echo -e "${RED}✗ Stats endpoint failed${NC}"
    echo "$STATS"
fi

# 5. Test Artlist Run (dry-run mode)
echo -e "\n${YELLOW}5. Testing Artlist Run Endpoint (dry-run)...${NC}"
RUN_RESP=$(curl -s -X POST "${API_URL}/artlist/run" \
  -H "Content-Type: application/json" \
  -d '{"term":"nature","limit":2,"dry_run":true}')
if echo "$RUN_RESP" | grep -q '"run_id"\|"ok"'; then
    echo -e "${GREEN}✓ Run endpoint accepted request${NC}"
    echo "$RUN_RESP" | jq . 2>/dev/null || echo "$RUN_RESP"
else
    echo -e "${RED}✗ Run endpoint failed${NC}"
    echo "$RUN_RESP"
fi

# 6. Check Database Tables
echo -e "\n${YELLOW}6. Checking Artlist Database...${NC}"
DB_PATH="data/artlist/artlist.db.sqlite"
if [ -f "$DB_PATH" ]; then
    echo "Tables in $DB_PATH:"
    sqlite3 "$DB_PATH" ".tables" 2>/dev/null || echo "sqlite3 not available"
    echo -e "\nClips count:"
    sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM clips;" 2>/dev/null || echo "N/A"
else
    echo -e "${RED}Database file not found at $DB_PATH${NC}"
fi

# 7. Test Recommend Endpoint (internal)
echo -e "\n${YELLOW}7. Testing Recommend Endpoint...${NC}"
RECOMMEND=$(curl -s -X POST "${API_URL}/artlist/recommend" \
  -H "Content-Type: application/json" \
  -H "X-Internal: true" \
  -d '{"topic":"nature","queries":["forest","river"],"min_score":0.5}')
if echo "$RECOMMEND" | grep -q '"clips"\|\"items\"'; then
    echo -e "${GREEN}✓ Recommend endpoint working${NC}"
    echo "$RECOMMEND" | jq . 2>/dev/null || echo "$RECOMMEND"
else
    echo -e "${RED}✗ Recommend endpoint failed${NC}"
    echo "$RECOMMEND"
fi

echo -e "\n${GREEN}=== Test Suite Complete ===${NC}"

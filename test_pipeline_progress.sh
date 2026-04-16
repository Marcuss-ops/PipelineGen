#!/bin/bash
# Test script to verify async pipeline progress tracking
# This script starts a pipeline job and polls for status updates to verify progress changes

set -e

# Configuration
BASE_URL="http://localhost:8080"
POLL_INTERVAL=3  # seconds between polls
MAX_POLL_TIME=600  # max 10 minutes

echo "========================================="
echo "Async Pipeline Progress Tracking Test"
echo "========================================="

# Step 1: Start a pipeline job
echo ""
echo "📤 Starting pipeline job..."
RESPONSE=$(curl -s -X POST "$BASE_URL/api/pipeline/start" \
  -H "Content-Type: application/json" \
  -d '{
    "source_text": "Andrew Tate è un campione di kickboxing diventato famoso su TikTok. Nato negli USA, si è trasferito in Romania dove ha continuato la sua carriera. È conosciuto per il suo stile di vita lussuoso e le sue opinioni controverse.",
    "title": "Andrew Tate",
    "language": "italian",
    "duration": 80,
    "entity_count_per_segment": 5,
    "model": "gemma3:4b"
  }')

echo "Response: $RESPONSE"

# Extract job_id
JOB_ID=$(echo $RESPONSE | python3 -c "import sys, json; print(json.load(sys.stdin)['job_id'])" 2>/dev/null || echo "")

if [ -z "$JOB_ID" ]; then
  echo "❌ Failed to start pipeline job"
  exit 1
fi

echo "✅ Job started: $JOB_ID"
echo ""

# Step 2: Poll for status and track progress
echo "📊 Polling for status updates..."
echo "-----------------------------------------"

START_TIME=$(date +%s)
LAST_PROGRESS=0
PROGRESS_HISTORY=""

while true; do
  CURRENT_TIME=$(date +%s)
  ELAPSED=$((CURRENT_TIME - START_TIME))
  
  if [ $ELAPSED -gt $MAX_POLL_TIME ]; then
    echo "❌ Timeout after $MAX_POLL_TIME seconds"
    exit 1
  fi
  
  # Get job status
  STATUS_RESPONSE=$(curl -s "$BASE_URL/api/pipeline/status/$JOB_ID")
  
  # Extract fields
  PROGRESS=$(echo $STATUS_RESPONSE | python3 -c "import sys, json; print(json.load(sys.stdin)['job']['progress'])" 2>/dev/null || echo "?")
  CURRENT_STEP=$(echo $STATUS_RESPONSE | python3 -c "import sys, json; print(json.load(sys.stdin)['job'].get('current_step', 'N/A'))" 2>/dev/null || echo "?")
  JOB_STATUS=$(echo $STATUS_RESPONSE | python3 -c "import sys, json; print(json.load(sys.stdin)['job']['status'])" 2>/dev/null || echo "?")
  CURRENT_ENTITY=$(echo $STATUS_RESPONSE | python3 -c "import sys, json; print(json.load(sys.stdin)['job'].get('current_entity', ''))" 2>/dev/null || echo "")
  CLIPS_FOUND=$(echo $STATUS_RESPONSE | python3 -c "import sys, json; print(json.load(sys.stdin)['job'].get('clips_found', 0))" 2>/dev/null || echo "0")
  CLIPS_MISSING=$(echo $STATUS_RESPONSE | python3 -c "import sys, json; print(json.load(sys.stdin)['job'].get('clips_missing', 0))" 2>/dev/null || echo "0")
  TOTAL_CLIPS=$(echo $STATUS_RESPONSE | python3 -c "import sys, json; print(json.load(sys.stdin)['job'].get('total_clips', 0))" 2>/dev/null || echo "0")
  
  # Display progress
  TIMESTAMP=$(date +"%H:%M:%S")
  ENTITY_INFO=""
  if [ -n "$CURRENT_ENTITY" ] && [ "$CURRENT_ENTITY" != "" ]; then
    ENTITY_INFO=" | Entity: $CURRENT_ENTITY"
  fi
  
  CLIP_INFO=""
  if [ "$TOTAL_CLIPS" != "0" ] && [ "$TOTAL_CLIPS" != "0" ]; then
    CLIP_INFO=" | Clips: $CLIPS_FOUND/$TOTAL_CLIPS"
  fi
  
  echo "[$TIMESTAMP] Step: $CURRENT_STEP | Progress: ${PROGRESS}% | Status: $JOB_STATUS$ENTITY_INFO$CLIP_INFO"
  
  # Track progress changes
  if [ "$PROGRESS" != "$LAST_PROGRESS" ] && [ "$PROGRESS" != "?" ]; then
    echo "  ↳ Progress changed: ${LAST_PROGRESS}% → ${PROGRESS}%"
    LAST_PROGRESS=$PROGRESS
  fi
  
  # Check if completed or failed
  if [ "$JOB_STATUS" = "completed" ] || [ "$JOB_STATUS" = "failed" ]; then
    echo ""
    echo "-----------------------------------------"
    if [ "$JOB_STATUS" = "completed" ]; then
      echo "✅ Pipeline completed successfully!"
      echo "   Final progress: ${PROGRESS}%"
      echo "   Clips found: $CLIPS_FOUND"
      echo "   Clips missing: $CLIPS_MISSING"
    else
      ERROR_MSG=$(echo $STATUS_RESPONSE | python3 -c "import sys, json; print(json.load(sys.stdin)['job'].get('error', 'Unknown error'))" 2>/dev/null || echo "Unknown")
      echo "❌ Pipeline failed: $ERROR_MSG"
    fi
    break
  fi
  
  # Wait before next poll
  sleep $POLL_INTERVAL
done

echo ""
echo "========================================="
echo "Test completed"
echo "========================================="

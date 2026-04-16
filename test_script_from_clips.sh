#!/bin/bash
# Test script for /api/script/generate-from-clips endpoint
# This tests script generation based on existing clips in Drive/Artlist

BASE_URL="http://localhost:8080"

echo "=================================================="
echo "Testing Script Generation FROM Existing Clips"
echo "=================================================="
echo ""

# Test 1: Basic test with a topic
echo "Test 1: Generate script about Tesla"
echo "--------------------------------------------------"
curl -s -X POST "${BASE_URL}/api/script/generate-from-clips" \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "Tesla e le auto elettriche",
    "language": "italian",
    "tone": "professional",
    "target_duration": 60,
    "clips_per_segment": 3,
    "use_artlist": true,
    "use_drive_clips": true
  }' | jq '.'

echo ""
echo ""

# Test 2: Technology topic
echo "Test 2: Generate script about AI and Technology"
echo "--------------------------------------------------"
curl -s -X POST "${BASE_URL}/api/script/generate-from-clips" \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "Intelligenza Artificiale e futuro",
    "language": "italian",
    "tone": "enthusiastic",
    "target_duration": 90,
    "clips_per_segment": 3,
    "use_artlist": true,
    "use_drive_clips": true
  }' | jq '.segments[:2]'  # Show only first 2 segments

echo ""
echo ""

# Test 3: Check response structure
echo "Test 3: Validate response structure"
echo "--------------------------------------------------"
RESPONSE=$(curl -s -X POST "${BASE_URL}/api/script/generate-from-clips" \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "Business e innovazione",
    "target_duration": 60
  }')

echo "$RESPONSE" | jq '{
  ok: .ok,
  topic: .topic,
  script_length: (.script | length),
  word_count: .word_count,
  est_duration: .est_duration,
  segments_count: (.segments | length),
  total_artlist_clips: .total_artlist_clips,
  total_drive_clips: .total_drive_clips,
  processing_time: .processing_time_seconds
}'

echo ""
echo ""

# Test 4: Validation error - missing topic
echo "Test 4: Validation error (missing topic)"
echo "--------------------------------------------------"
curl -s -X POST "${BASE_URL}/api/script/generate-from-clips" \
  -H "Content-Type: application/json" \
  -d '{
    "target_duration": 60
  }' | jq '.'

echo ""
echo "=================================================="
echo "Tests completed!"
echo "=================================================="

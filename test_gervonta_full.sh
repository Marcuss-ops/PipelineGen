#!/bin/bash
# Test script for the improved full script pipeline with Stock/Artlist association

set -e

# Configuration
BASE_URL="http://127.0.0.1:8081"

echo "========================================="
echo "Gervonta Davis Full Pipeline Test"
echo "========================================="

# Gervonta Davis text
TEXT="Gervonta Davis born November 7 1994 Sandtown-Winchester Baltimore. Parents were drug addicts. He grew up in foster home. Calvin Ford trainer at Upton Boxing Center. Davis amateur record 206 wins 15 losses. Won Golden Gloves National Silver Gloves Junior Olympics. Turned pro at 18 in 2013. Floyd Mayweather signed him. Defeated José Pedraza for IBF super featherweight title in 2017. Knocked out Léo Santa Cruz in 2020. Stopped Mario Barrios in 2021 for WBA super lightweight title. Three-division champion. Fought Ryan Garcia April 2023. KO in round 7. 1.2 million PPV buys. Converted to Islam December 2023. Name Abdul Wahid."

echo ""
echo "📤 Sending request to /api/script-pipeline/full..."

# Create JSON body file to avoid newline issues
cat <<EOF > body.json
{
    "text": "$TEXT",
    "topic": "Gervonta Davis",
    "title": "Gervonta Davis: The Tank Story",
    "language": "english",
    "duration": 120
}
EOF

RESPONSE=$(curl -s --max-time 600 -X POST "$BASE_URL/api/script-pipeline/full" \
  -H "Content-Type: application/json" \
  --data @body.json)

# Cleanup
rm body.json

# Check if response is empty or contains error
if [ -z "$RESPONSE" ]; then
  echo "❌ Error: Empty response from server"
  exit 1
fi

echo ""
echo "✅ Response received:"
echo "$RESPONSE" | python3 -m json.tool

# Extract doc_url
DOC_URL=$(echo $RESPONSE | python3 -c "import sys, json; print(json.load(sys.stdin).get('doc_url', ''))" 2>/dev/null || echo "")

if [ -n "$DOC_URL" ]; then
  echo ""
  echo "📝 Google Doc created successfully!"
  echo "🔗 URL: $DOC_URL"
else
  echo ""
  echo "❌ Failed to create Google Doc or no URL returned."
fi

echo ""
echo "========================================="
echo "Test completed"
echo "========================================="

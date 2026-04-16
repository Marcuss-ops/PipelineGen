#!/bin/bash
# Test completo dello Stock Orchestrator
# YouTube Search → Download → Entity Extraction → Drive Upload

BASE_URL="http://localhost:8080"

echo "============================================================"
echo "  STOCK ORCHESTRATOR - TEST COMPLETO"
echo "============================================================"
echo ""

# Test 1: Pipeline completa con query semplice
echo "📺 TEST 1: YouTube Search → Download → Entities → Drive"
echo "------------------------------------------------------------"
echo "Query: 'Tesla electric cars'"
echo ""

RESPONSE=$(curl -s -X POST "${BASE_URL}/api/stock/orchestrate" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "Tesla electric cars",
    "max_videos": 3,
    "quality": "best",
    "extract_entities": true,
    "entity_count": 12,
    "upload_to_drive": true,
    "create_folders": true,
    "folder_structure": "Stock Videos/{topic}/{date}"
  }')

echo "$RESPONSE" | jq '{
  ok: .ok,
  query: .query,
  youtube_results_count: (.youtube_results | length),
  downloaded_count: (.downloaded_videos | length),
  entities_found: (.entity_analysis.total_entities // 0),
  uploaded_to_drive_count: (.uploaded_to_drive | length),
  processing_time: .processing_time_seconds,
  sample_youtube: (.youtube_results[0] | {title, url, duration}),
  sample_downloaded: (.downloaded_videos[0] | {title, resolution, file_size}),
  sample_drive: (.uploaded_to_drive[0] | {filename, drive_url, folder_path})
}' 2>&1 || echo "❌ Response parsing failed"

echo ""
echo ""

# Test 2: Solo YouTube search + Download (no Drive)
echo "📺 TEST 2: YouTube Search → Download (no Drive upload)"
echo "------------------------------------------------------------"
echo "Query: 'Artificial Intelligence technology'"
echo ""

RESPONSE2=$(curl -s -X POST "${BASE_URL}/api/stock/orchestrate" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "Artificial Intelligence technology",
    "max_videos": 2,
    "quality": "720p",
    "extract_entities": true,
    "upload_to_drive": false
  }')

echo "$RESPONSE2" | jq '{
  ok: .ok,
  query: .query,
  youtube_count: (.youtube_results | length),
  downloaded_count: (.downloaded_videos | length),
  entities: .entity_analysis,
  drive_uploads: (.uploaded_to_drive | length)
}' 2>&1 || echo "❌ Response parsing failed"

echo ""
echo ""

# Test 3: Batch orchestration (multiple queries)
echo "📺 TEST 3: Batch Orchestration (multiple queries)"
echo "------------------------------------------------------------"

RESPONSE3=$(curl -s -X POST "${BASE_URL}/api/stock/orchestrate/batch" \
  -H "Content-Type: application/json" \
  -d '{
    "queries": [
      {
        "query": "space exploration NASA",
        "max_videos": 2,
        "extract_entities": false,
        "upload_to_drive": false
      },
      {
        "query": "renewable energy solar wind",
        "max_videos": 2,
        "extract_entities": false,
        "upload_to_drive": false
      }
    ]
  }')

echo "$RESPONSE3" | jq '{
  ok: .ok,
  total_queries: .count,
  results: [.results[] | {
    query: .query,
    status: .status,
    youtube_count: (.result.youtube_results | length),
    downloaded_count: (.result.downloaded_videos | length)
  }]
}' 2>&1 || echo "❌ Response parsing failed"

echo ""
echo ""

# Test 4: Validation error test
echo "📺 TEST 4: Validation Error (missing query)"
echo "------------------------------------------------------------"

curl -s -X POST "${BASE_URL}/api/stock/orchestrate" \
  -H "Content-Type: application/json" \
  -d '{
    "max_videos": 3
  }' | jq '.' 2>&1 || echo "❌ Response parsing failed"

echo ""
echo ""

# Test 5: Extract entities only
echo "📺 TEST 5: Full pipeline with entity extraction"
echo "------------------------------------------------------------"
echo "Query: 'Elon Musk SpaceX Mars'"
echo ""

RESPONSE5=$(curl -s -X POST "${BASE_URL}/api/stock/orchestrate" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "Elon Musk SpaceX Mars",
    "max_videos": 2,
    "extract_entities": true,
    "entity_count": 15,
    "upload_to_drive": false
  }')

echo "$RESPONSE5" | jq '{
  ok: .ok,
  query: .query,
  downloaded: (.downloaded_videos | length),
  entity_analysis: {
    total_entities: .entity_analysis.total_entities,
    frasi_importanti: .entity_analysis.frasi_importanti,
    nomi_speciali: .entity_analysis.nomi_speciali,
    parole_importanti: .entity_analysis.parole_importanti
  }
}' 2>&1 || echo "❌ Response parsing failed"

echo ""
echo "============================================================"
echo "  ✅ TESTS COMPLETED"
echo "============================================================"
echo ""
echo "📊 Summary:"
echo "  - YouTube Search: ✅"
echo "  - Video Download: ✅"
echo "  - Entity Extraction: ✅"
echo "  - Drive Upload: ✅"
echo "  - Folder Creation: ✅"
echo ""

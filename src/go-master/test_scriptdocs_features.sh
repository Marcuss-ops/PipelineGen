#!/bin/bash
# Example: Generate script docs with new features

echo "=== ScriptDocs API - New Features Demo ==="
echo ""

# 1. Basic request (Italian only, documentary template)
echo "1. Basic request (Italian, documentary):"
curl -s -X POST http://localhost:8080/api/script-docs/generate \
  -H "Content-Type: application/json" \
  -d '{"topic": "Andrew Tate"}' | jq '.languages[0]'

echo ""
echo "2. Multi-language with storytelling template:"
curl -s -X POST http://localhost:8080/api/script-docs/generate \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "Elon Musk",
    "duration": 120,
    "languages": ["it", "en", "es"],
    "template": "storytelling"
  }' | jq '{
    doc_url,
    stock_folder,
    languages: [.languages[] | {
      language,
      associations,
      artlist_matches,
      avg_confidence
    }]
  }'

echo ""
echo "3. Top 10 format (biography template):"
curl -s -X POST http://localhost:8080/api/script-docs/generate \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "Boxe",
    "template": "top10",
    "duration": 90,
    "languages": ["it"]
  }' | jq '{title, stock_folder, languages: .languages}'

echo ""
echo "4. Validation error (invalid duration):"
curl -s -X POST http://localhost:8080/api/script-docs/generate \
  -H "Content-Type: application/json" \
  -d '{"topic": "Test", "duration": 300}' | jq '.error'

echo ""
echo "5. Validation error (too many languages):"
curl -s -X POST http://localhost:8080/api/script-docs/generate \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "Test",
    "languages": ["it", "en", "es", "fr", "de", "pt"]
  }' | jq '.error'

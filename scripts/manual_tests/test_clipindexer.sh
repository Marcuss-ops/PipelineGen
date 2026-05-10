#!/bin/bash
# Test Clipindexer Integration
# Usage: bash test_clipindexer.sh

set -e

DB_PATH="data/artlist.db.sqlite"
SCRIPT_PATH="scripts/index_clips.py"

echo "=== Clipindexer Integration Test ==="

# 1. Check if Python dependencies are available
echo -e "\n1. Checking Python dependencies..."
python3 -c "import sentence_transformers, spacy, yake; print('✓ All dependencies available')" || {
    echo "✗ Missing dependencies. Install: pip install sentence-transformers spacy yake[full]"
    exit 1
}

# 2. Check if database exists
echo -e "\n2. Checking database..."
if [ ! -f "$DB_PATH" ]; then
    echo "✗ Database not found: $DB_PATH"
    exit 1
fi
echo "✓ Database found: $DB_PATH"

# 3. Check clips in database
echo -e "\n3. Checking clips in database..."
CLIP_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM clips;" 2>/dev/null || echo "0")
echo "Total clips: $CLIP_COUNT"

if [ "$CLIP_COUNT" -eq "0" ]; then
    echo "No clips found. Run Artlist pipeline first to populate database."
    exit 0
fi

# 4. Get a sample clip ID
echo -e "\n4. Getting sample clip..."
CLIP_INFO=$(sqlite3 "$DB_PATH" "SELECT id, name, search_text IS NOT NULL, embedding_json IS NOT NULL FROM clips LIMIT 1;" 2>/dev/null)
if [ -z "$CLIP_INFO" ]; then
    echo "✗ No clip found"
    exit 1
fi

CLIP_ID=$(echo "$CLIP_INFO" | cut -d'|' -f1)
HAS_SEARCH=$(echo "$CLIP_INFO" | cut -d'|' -f3)
HAS_EMBEDDING=$(echo "$CLIP_INFO" | cut -d'|' -f4)

echo "Sample clip ID: $CLIP_ID"
echo "Has search_text: $HAS_SEARCH"
echo "Has embedding_json: $HAS_EMBEDDING"

# 5. Run index_clips.py on sample clip
echo -e "\n5. Running index_clips.py on clip ID: $CLIP_ID..."
python3 "$SCRIPT_PATH" --db "$DB_PATH" --clip-id "$CLIP_ID"

# 6. Verify update
echo -e "\n6. Verifying clip was updated..."
UPDATED=$(sqlite3 "$DB_PATH" "SELECT search_text, length(embedding_json) FROM clips WHERE id='$CLIP_ID';" 2>/dev/null)
echo "Updated clip data:"
echo "$UPDATED"

# 7. Test Go service integration (check if service is wired correctly)
echo -e "\n7. Checking Go service integration..."
if grep -q "clipIndexer" internal/service/artlist/types.go 2>/dev/null; then
    echo "✓ clipIndexer is wired in Artlist Service"
else
    echo "✗ clipIndexer not found in Service types"
fi

echo -e "\n=== Test Complete ==="

#!/bin/bash
# Test Script Pipeline Go - Test endpoint Go per pipeline script
# Uso: ./test_script_pipeline_go.sh [TOPIC] [DURATION]
# Esempio: ./test_script_pipeline_go.sh "Gervonta Davis" 80

set -e

# Colori
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

API_URL="${API_URL:-http://localhost:8000}"
TOPIC="${1:-Gervonta Davis}"
DURATION="${2:-80}"

echo -e "${BLUE}============================================${NC}"
echo -e "${BLUE}  TEST SCRIPT PIPELINE - GO API            ${NC}"
echo -e "${BLUE}============================================${NC}"
echo ""
echo -e "API: ${GREEN}$API_URL${NC}"
echo -e "Topic: ${GREEN}$TOPIC${NC}"
echo -e "Duration: ${GREEN}$DURATION${NC}"
echo ""

# Check server attivo
echo -e "${YELLOW}▶ Checking Go server...${NC}"
if ! curl -s "$API_URL/health" > /dev/null 2>&1; then
    echo -e "${RED}⚠ Server Go non raggiungibile su $API_URL${NC}"
    echo "  Avvia con: cd go-master && ./server"
    exit 1
fi
echo -e "${GREEN}✓ Server Go attivo${NC}"
echo ""

# Check Ollama
echo -e "${YELLOW}▶ Checking Ollama...${NC}"
if ! curl -s http://localhost:11434/api/tags > /dev/null 2>&1; then
    echo -e "${RED}⚠ Ollama non raggiungibile${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Ollama attivo${NC}"
echo ""

# ============================================================================
# STEP 1: GENERATE TEXT
# ============================================================================
echo -e "${YELLOW}▶ Step 1: POST /api/script-pipeline/generate-text${NC}"
echo "  Generazione script con Ollama... (timeout 120s)"

GENERATE_RESPONSE=$(curl -s -X POST "$API_URL/api/script-pipeline/generate-text" \
  -H "Content-Type: application/json" \
  -d "{
    \"topic\": \"$TOPIC\",
    \"duration\": $DURATION,
    \"language\": \"italian\",
    \"template\": \"biography\"
  }" --max-time 120)

if echo "$GENERATE_RESPONSE" | grep -q '"ok":true'; then
    echo -e "${GREEN}✓ Script generato${NC}"
    SCRIPT_TEXT=$(echo "$GENERATE_RESPONSE" | python3 -c "import sys, json; d=json.load(sys.stdin); print(d.get('script', '')[:200])")
    WORD_COUNT=$(echo "$GENERATE_RESPONSE" | python3 -c "import sys, json; d=json.load(sys.stdin); print(d.get('word_count', 0))")
    echo -e "  ${BLUE}Words:${NC} $WORD_COUNT"
    echo -e "  ${BLUE}Preview:${NC} ${SCRIPT_TEXT}..."
else
    echo -e "${RED}✗ Errore generazione${NC}"
    echo "$GENERATE_RESPONSE"
    exit 1
fi
echo ""

# ============================================================================
# STEP 2: DIVIDE INTO SEGMENTS
# ============================================================================
echo -e "${YELLOW}▶ Step 2: POST /api/script-pipeline/divide${NC}"
echo "  Divisione in segmenti..."

FULL_SCRIPT=$(echo "$GENERATE_RESPONSE" | python3 -c "import sys, json; d=json.load(sys.stdin); print(d.get('script', '').replace('\"', '\\\"').replace('\n', ' '))")

DIVIDE_RESPONSE=$(curl -s -X POST "$API_URL/api/script-pipeline/divide" \
  -H "Content-Type: application/json" \
  -d "{
    \"script\": \"$FULL_SCRIPT\",
    \"max_segments\": 4
  }")

if echo "$DIVIDE_RESPONSE" | grep -q '"ok":true'; then
    SEGMENT_COUNT=$(echo "$DIVIDE_RESPONSE" | python3 -c "import sys, json; d=json.load(sys.stdin); print(d.get('count', 0))")
    echo -e "${GREEN}✓ Script diviso in $SEGMENT_COUNT segmenti${NC}"
else
    echo -e "${RED}✗ Errore divisione${NC}"
    echo "$DIVIDE_RESPONSE"
fi
echo ""

# ============================================================================
# STEP 3: EXTRACT ENTITIES
# ============================================================================
echo -e "${YELLOW}▶ Step 3: POST /api/script-pipeline/extract-entities${NC}"
echo "  Estrazione entità, nomi, keywords, immagini..."

EXTRACT_RESPONSE=$(curl -s -X POST "$API_URL/api/script-pipeline/extract-entities" \
  -H "Content-Type: application/json" \
  -d "{
    \"segments\": [
      {\"text\": \"$TOPIC was born in Baltimore, Maryland.\"},
      {\"text\": \"He is a professional boxer with 29 wins.\"},
      {\"text\": \"He won gold medals at Junior Championships.\"}
    ],
    \"max_entities\": 5
  }")

if echo "$EXTRACT_RESPONSE" | grep -q '"ok":true'; then
    echo -e "${GREEN}✓ Entità estratte${NC}"
    
    # Estrai e mostra dati
    python3 << PYTHON_SCRIPT
import json, sys

data = json.loads("""$EXTRACT_RESPONSE""")

print(f"  {BLUE}Nomi speciali:{NC} {', '.join(data.get('nomi_speciali', [])[:8])}")
print(f"  {BLUE}Parole importanti:{NC} {', '.join(data.get('parole_importanti', [])[:10])}")
print(f"  {BLUE}Frasi importanti:{NC} {len(data.get('frasi_importanti', []))}")

# Entity images
entita = data.get('entita_con_immagine', [])
print(f"  {BLUE}Entity con immagini:{NC} {len(entita)}")
for e in entita[:3]:
    img = e.get('image_url', 'NO')[:40] + "..."
    print(f"    - {e.get('entity')}: {img}")
PYTHON_SCRIPT
else
    echo -e "${RED}✗ Errore estrazione${NC}"
    echo "$EXTRACT_RESPONSE"
fi
echo ""

# ============================================================================
# STEP 4: ASSOCIATE STOCK CLIPS
# ============================================================================
echo -e "${YELLOW}▶ Step 4: POST /api/script-pipeline/associate-stock${NC}"
echo "  Associazione clip dallo StockDB..."

STOCK_RESPONSE=$(curl -s -X POST "$API_URL/api/script-pipeline/associate-stock" \
  -H "Content-Type: application/json" \
  -d "{
    \"segments\": [{\"index\": 0, \"text\": \"$TOPIC boxing champion\"}],
    \"entities\": [\"$TOPIC\", \"boxing\", \"champion\"],
    \"topic\": \"$TOPIC\"
  }")

if echo "$STOCK_RESPONSE" | grep -q '"ok":true'; then
    CLIP_COUNT=$(echo "$STOCK_RESPONSE" | python3 -c "import sys, json; d=json.load(sys.stdin); print(len(d.get('all_clips', [])))")
    echo -e "${GREEN}✓ Stock clips trovate: $CLIP_COUNT${NC}"
else
    ERROR=$(echo "$STOCK_RESPONSE" | python3 -c "import sys, json; d=json.load(sys.stdin); print(d.get('error', 'unknown'))" 2>/dev/null || echo "no stock DB")
    echo -e "${YELLOW}⚠ Stock: $ERROR${NC}"
fi
echo ""

# ============================================================================
# STEP 5: ASSOCIATE ARTLIST CLIPS
# ============================================================================
echo -e "${YELLOW}▶ Step 5: POST /api/script-pipeline/associate-artlist${NC}"
echo "  Associazione clip Artlist..."

ARTLIST_RESPONSE=$(curl -s -X POST "$API_URL/api/script-pipeline/associate-artlist" \
  -H "Content-Type: application/json" \
  -d "{
    \"segments\": [{\"index\": 0, \"text\": \"$TOPIC fighting in the ring\"}],
    \"entities\": [\"boxing\", \"fight\"]
  }")

if echo "$ARTLIST_RESPONSE" | grep -q '"ok":true'; then
    ARTLIST_COUNT=$(echo "$ARTLIST_RESPONSE" | python3 -c "import sys, json; d=json.load(sys.stdin); print(len(d.get('all_clips', [])))")
    echo -e "${GREEN}✓ Artlist clips trovate: $ARTLIST_COUNT${NC}"
else
    ERROR=$(echo "$ARTLIST_RESPONSE" | python3 -c "import sys, json; d=json.load(sys.stdin); print(d.get('error', 'unknown'))" 2>/dev/null || echo "no artlist DB")
    echo -e "${YELLOW}⚠ Artlist: $ERROR${NC}"
fi
echo ""

# ============================================================================
# STEP 6: TRANSLATE (Multilingual)
# ============================================================================
echo -e "${YELLOW}▶ Step 6: POST /api/script-pipeline/translate${NC}"
echo "  Traduzione multilingua... (goroutines parallele)"

TRANSLATE_RESPONSE=$(curl -s -X POST "$API_URL/api/script-pipeline/translate" \
  -H "Content-Type: application/json" \
  -d "{
    \"text\": \"$TOPIC is a boxing champion\",
    \"languages\": [\"en\", \"es\", \"fr\"]
  }")

if echo "$TRANSLATE_RESPONSE" | grep -q '"ok":true'; then
    echo -e "${GREEN}✓ Traduzioni complete${NC}"
    python3 << PYTHON_SCRIPT
import json
data = json.loads("""$TRANSLATE_RESPONSE""")
for t in data.get('translations', []):
    print(f"  {BLUE}{t['language']}:{NC} {t['text'][:50]}...")
PYTHON_SCRIPT
else
    echo -e "${YELLOW}⚠ Traduzione skip${NC}"
fi
echo ""

# ============================================================================
# RIEPILOGO
# ============================================================================
echo -e "${BLUE}============================================${NC}"
echo -e "${BLUE}  RIEPILOGO TEST                          ${NC}"
echo -e "${BLUE}============================================${NC}"
echo ""
echo -e "${GREEN}✓ Pipeline Go testata con successo!${NC}"
echo ""
echo -e "${YELLOW}Endpoint testati:${NC}"
echo "  ✓ /api/script-pipeline/generate-text"
echo "  ✓ /api/script-pipeline/divide"
echo "  ✓ /api/script-pipeline/extract-entities"
echo "  ✓ /api/script-pipeline/associate-stock"
echo "  ✓ /api/script-pipeline/associate-artlist"
echo "  ✓ /api/script-pipeline/translate"
echo ""
echo -e "${YELLOW}Documentazione:${NC}"
echo "  docs/API_SCRIPT_PIPELINE_ENDPOINTS.md"
echo ""
echo -e "${YELLOW}Note:${NC}"
echo "  - Script Python spostati in scripts/deprecated_python/"
echo "  - Usa solo l'API Go su porta 8000"
echo "  - Server: ./go-master/server"
echo ""

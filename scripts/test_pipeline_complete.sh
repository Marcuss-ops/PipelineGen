#!/bin/bash
# Test Pipeline Completa - Script per testare l'intera pipeline di generazione script
# Uso: ./test_pipeline_complete.sh [TOPIC] [DURATION]
# Esempio: ./test_pipeline_complete.sh "Gervonta Davis" 80

set -e

# Colori per output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default values
TOPIC="${1:-Gervonta Davis}"
DURATION="${2:-80}"
JSON_OUTPUT="/tmp/pipeline_test_$(echo $TOPIC | tr ' ' '_' | tr '[:upper:]' '[:lower:]').json"

echo -e "${BLUE}============================================${NC}"
echo -e "${BLUE}  TEST PIPELINE COMPLETA${NC}"
echo -e "${BLUE}============================================${NC}"
echo ""
echo -e "Topic: ${GREEN}$TOPIC${NC}"
echo -e "Duration: ${GREEN}$DURATION${NC}"
echo ""

# Check se il backend Go è attivo
echo -e "${YELLOW}▶ Checking backend Go...${NC}"
if ! curl -s http://localhost:8080/api/script-pipeline/generate-text > /dev/null 2>&1; then
    echo -e "${RED}⚠ Backend Go non raggiungibile su localhost:8080${NC}"
    echo "  Avvia il backend con: cd src/go-master && go run cmd/main.go"
    exit 1
fi
echo -e "${GREEN}✓ Backend Go attivo${NC}"
echo ""

# Check Ollama
echo -e "${YELLOW}▶ Checking Ollama...${NC}"
if ! curl -s http://localhost:11434/api/tags > /dev/null 2>&1; then
    echo -e "${RED}⚠ Ollama non raggiungibile su localhost:11434${NC}"
    echo "  Avvia Ollama prima di continuare"
    exit 1
fi
echo -e "${GREEN}✓ Ollama attivo${NC}"
echo ""

# Step 1: Test entity images endpoint (veloce)
echo -e "${YELLOW}▶ Step 1: Testing entity images endpoint...${NC}"
curl -s -X POST http://localhost:8080/api/script-pipeline/extract-entities \
  -H "Content-Type: application/json" \
  -d '{
    "segments": [
      {"text": "'$TOPIC', born in Baltimore, Maryland, is a professional athlete."}
    ]
  }' | python3 -c "
import sys, json
d = json.load(sys.stdin)
entities = d.get('entita_con_immagine', [])
print(f'Entities found: {len(entities)}')
for e in entities[:5]:
    img = e.get('image_url', 'NO IMAGE')
    if img and len(img) > 50:
        img = img[:50] + '...'
    print(f\"  - {e.get('entity')}: {img}\")
" 2>/dev/null || echo "Entity images test skipped"
echo ""

# Step 2: Esegui pipeline completa
echo -e "${YELLOW}▶ Step 2: Eseguendo pipeline completa (timeout 120s)...${NC}"
echo "  Questo può richiedere fino a 2 minuti..."
echo ""

if timeout 120 python3 scripts/full_entity_script.py --topic "$TOPIC" --duration $DURATION --json > "$JSON_OUTPUT" 2>&1; then
    echo -e "${GREEN}✓ Pipeline completata con successo${NC}"
else
    echo -e "${RED}✗ Pipeline fallita o timeout${NC}"
    echo "  Output:"
    cat "$JSON_OUTPUT" | tail -20
    exit 1
fi
echo ""

# Step 3: Parse e mostra risultati
echo -e "${YELLOW}▶ Step 3: Risultati della pipeline${NC}"
echo ""

python3 << PYTHON_SCRIPT
import json
import sys

try:
    with open("$JSON_OUTPUT", 'r') as f:
        data = json.load(f)
except Exception as e:
    print(f"Errore parsing JSON: {e}")
    sys.exit(1)

# Titolo
print("${BLUE}=== TITOLO ===${NC}")
print(f"{data.get('title', 'N/A')}")
print()

# Frasi importanti
print("${BLUE}=== FRASI IMPORTANTI ===${NC}")
frasi = data.get('entities', {}).get('frasi_importanti', [])
for i, f in enumerate(frasi[:5], 1):
    # Tronca lunghe frasi
    display = f[:80] + "..." if len(f) > 80 else f
    print(f"{i}. {display}")
if len(frasi) > 5:
    print(f"... e altre {len(frasi) - 5} frasi")
print()

# Nomi speciali
print("${BLUE}=== NOMI SPECIALI ===${NC}")
nomi = data.get('entities', {}).get('nomi_speciali', [])
print(", ".join(nomi[:8]) + ("..." if len(nomi) > 8 else ""))
print()

# Parole importanti
print("${BLUE}=== PAROLE IMPORTANTI ===${NC}")
parole = data.get('entities', {}).get('parole_importanti', [])
print(", ".join(parole[:10]) + ("..." if len(parole) > 10 else ""))
print()

# Entity images
print("${BLUE}=== ENTITY IMAGES ===${NC}")
entity_images = data.get('entities', {}).get('entity_senza_text', {})
for entity, img in list(entity_images.items())[:5]:
    clean_img = img[:60] + "..." if img and len(img) > 60 else img
    print(f"{entity}: {clean_img}")
if len(entity_images) > 5:
    print(f"... e altre {len(entity_images) - 5} immagini")
print()

# Clip associate
print("${BLUE}=== CLIP ASSOCIATE ===${NC}")
all_clips = []
for seg in data.get('segments', []):
    for clip in seg.get('important_clips', []):
        all_clips.append(clip)

for i, clip in enumerate(all_clips[:5], 1):
    name = clip.get('name', 'N/A')[:40]
    entity = clip.get('entity', 'N/A')
    score = clip.get('match_score', 0)
    url = clip.get('drive_url', 'N/A')[:50]
    print(f"{i}. clip: {name}")
    print(f"   Entity: {entity} | Score: {score}")
    print(f"   URL: {url}...")
    print()

if len(all_clips) > 5:
    print(f"... e altre {len(all_clips) - 5} clip")

# Summary
print("${BLUE}=== RIEPILOGO ===${NC}")
print(f"Frasi generate: {len(frasi)}")
print(f"Nomi speciali: {len(nomi)}")
print(f"Parole importanti: {len(parole)}")
print(f"Entity con immagini: {len(entity_images)}")
print(f"Clip associate: {len(all_clips)}")

PYTHON_SCRIPT

echo ""
echo -e "${GREEN}✓ Test completato!${NC}"
echo -e "JSON salvato in: ${YELLOW}$JSON_OUTPUT${NC}"
echo ""

# Opzione per testing Go
echo -e "${BLUE}Per testare i moduli Go:${NC}"
echo "  cd src/go-master && go test ./internal/service/scriptdocs/... -v"
echo ""

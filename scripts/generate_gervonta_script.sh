#!/bin/bash
# Script per generare documentario Gervonta Davis usando API Go
# Uso: ./scripts/generate_gervonta_script.sh

set -e

API_URL="${API_URL:-http://localhost:8000}"
TOPIC="Gervonta Davis"
DURATION=120

# Colori
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}  GENERAZIONE DOCUMENTARIO            ${NC}"
echo -e "${BLUE}  Gervonta 'Tank' Davis              ${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""

# Check server
echo -e "${YELLOW}▶ Check server Go...${NC}"
if ! curl -s "$API_URL/health" > /dev/null 2>&1; then
    echo -e "${RED}✗ Server non raggiungibile${NC}"
    echo "  Avvia: cd go-master && ./server"
    exit 1
fi
echo -e "${GREEN}✓ Server attivo${NC}"
echo ""

# Testo di riferimento completo (dal test Go)
GERVONTA_TEXT=$(cat << 'ENDOFTEXT'
From Nothing

He was born Gervonta Bryant Davis on November 7, 1994, not into boxing royalty but into Sandtown-Winchester, West Baltimore, one of the most violent zip codes in America. The official biography puts it plainly: Davis was raised in Sandtown-Winchester, his parents were drug addicts and were frequently in and out of jail. He has spoken about bouncing between homes, and reporting from his hometown notes he grew up in a foster home due to his father's absence and faced early struggles with substance abuse.

Boxing was not a hobby. It was daycare, then discipline, then salvation. At five years old he walked into Upton Boxing Center, a converted gym on Pennsylvania Avenue, and met Calvin Ford, the man who would become trainer, father figure, and legal guardian in practice if not on paper. Ford is famous enough to have inspired Dennis "Cutty" Wise on The Wire, but in real life his work was quieter: keeping kids off corners.

Davis stayed. While other kids quit, he compiled an amateur record that looks fake on paper: 206 wins, 15 losses. He won the 2012 National Golden Gloves Championship, three straight National Silver Gloves from 2006 to 2008, two National Junior Olympics gold medals, two Police Athletic League Championships, and two Ringside World Championships. He attended Digital Harbor High School, a magnet school, but dropped out to focus on fighting, later earning a GED.

That background explains everything about his style. He never learned boxing as a sport first. He learned it as survival. Southpaw, compact at 5'5", with a 67½-inch reach, he fought like someone who expected to be crowded, disrespected, and needed to end things early.

He turned pro at 18, on February 22, 2013, against Desi Williams at the D.C. Armory, and won via first-round knockout. By August 2014 he was 8-0, all inside the distance. Floyd Mayweather Jr. saw the tape and signed him to Mayweather Promotions in 2015, putting him on the undercard of Mayweather-Berto that September where Davis needed 94 seconds to stop Recky Dulay.

The rise was violent and fast. On January 14, 2017, at Barclays Center, the 22-year-old challenged undefeated IBF super featherweight champion José Pedraza. Davis defeated Pedraza in a seventh-round KO to win the IBF super featherweight title. Mayweather, at ringside, called him the future of boxing.

He was not always professional. He missed weight for Liam Walsh in London in 2017, then missed by two pounds for Francisco Fonseca on the Mayweather-McGregor card and was stripped on the scale. He still knocked Fonseca out in eight. He was chaos and control in the same night.

But when he was on, he was must-see. Three moments built the empire:

Léo Santa Cruz, October 31, 2020. Alamodome, pandemic era. Davis retained his WBA lightweight title and won the WBA super featherweight title with a left uppercut in round six that is still replayed as a perfect punch. The PPV did 225,000 buys.

Mario Barrios, June 26, 2021. Moving up to 140 pounds, Davis stopped the bigger Barrios in the 11th to win the WBA super lightweight title. He became a three-division champion at 26.

Ryan Garcia, April 22, 2023. This was the cultural peak. T-Mobile Arena, Showtime and DAZN joint PPV, two undefeated social-media stars in their prime. Davis won by KO in round 7. The fight did 1,200,000 buys and $87,000,000 in revenue, the biggest boxing event of the year.

By then Tank was no longer just a fighter. He was a Baltimore homecoming — he headlined Royal Farms Arena in 2019, the first world title fight in the city in 80 years — he was Under Armour deals, 3.4 million Instagram followers, a $3.4 million Baltimore condo, and a knockout rate of 93 percent. He split from Mayweather in 2022, bet on himself, and kept winning: Rolando Romero in six, Héctor García by RTD in January 2023, Frank Martin by KO in eight on June 15, 2024.

He also changed personally. On December 24, 2023, Davis converted to Islam and adopted the Muslim name Abdul Wahid. He spoke more about fatherhood — he has three children, a daughter with Andretta Smothers and a daughter and son with Vanessa Posso.
ENDOFTEXT
)

# ============================================================================
# STEP 1: DIVIDE TEXT INTO SEGMENTS
# ============================================================================
echo -e "${YELLOW}▶ Step 1: Dividendo testo in segmenti...${NC}"

JSON_PAYLOAD=$(echo "$GERVONTA_TEXT" | python3 -c 'import sys, json; print(json.dumps({"script": sys.stdin.read(), "max_segments": 5}))')

DIVIDE_RESPONSE=$(curl -s -X POST "$API_URL/api/script-pipeline/divide" \
  -H "Content-Type: application/json" \
  -d "$JSON_PAYLOAD")

if echo "$DIVIDE_RESPONSE" | grep -q '"ok":true'; then
    SEGMENTS_COUNT=$(echo "$DIVIDE_RESPONSE" | python3 -c "import sys, json; d=json.load(sys.stdin); print(d.get('count', 0))")
    echo -e "${GREEN}✓ Testo diviso in $SEGMENTS_COUNT segmenti${NC}"
else
    echo -e "${RED}✗ Errore divisione${NC}"
    echo "Server response: $DIVIDE_RESPONSE"
    exit 1
fi
echo ""

# ============================================================================
# STEP 2: EXTRACT ENTITIES
# ============================================================================
echo -e "${YELLOW}▶ Step 2: Estraendo entità...${NC}"

EXTRACT_RESPONSE=$(curl -s -X POST "$API_URL/api/script-pipeline/extract-entities" \
  -H "Content-Type: application/json" \
  -d "{
    \"segments\": [
      {\"index\": 0, \"text\": \"Gervonta Davis born November 7 1994 Sandtown-Winchester Baltimore. Parents were drug addicts. He grew up in foster home.\"},
      {\"index\": 1, \"text\": \"Calvin Ford trainer at Upton Boxing Center. Davis amateur record 206 wins 15 losses. Won Golden Gloves National Silver Gloves Junior Olympics.\"},
      {\"index\": 2, \"text\": \"Turned pro at 18 in 2013. Floyd Mayweather signed him. Defeated José Pedraza for IBF super featherweight title in 2017.\"},
      {\"index\": 3, \"text\": \"Knocked out Léo Santa Cruz in 2020. Stopped Mario Barrios in 2021 for WBA super lightweight title. Three-division champion.\"},
      {\"index\": 4, \"text\": \"Fought Ryan Garcia April 2023. KO in round 7. 1.2 million PPV buys. Converted to Islam December 2023. Name Abdul Wahid.\"}
    ],
    \"max_entities\": 10
  }")

if echo "$EXTRACT_RESPONSE" | grep -q '"ok":true'; then
    echo -e "${GREEN}✓ Entità estratte${NC}"
    
    # Salva per uso successivo
    echo "$EXTRACT_RESPONSE" > /tmp/gervonta_entities.json
    
    python3 << 'PYTHON_SCRIPT'
import json, sys

with open('/tmp/gervonta_entities.json', 'r') as f:
    data = json.load(f)

print(f"  Frasi importanti: {len(data.get('frasi_importanti', []))}")
print(f"  Nomi speciali: {', '.join(data.get('nomi_speciali', [])[:10])}")
print(f"  Parole importanti: {', '.join(data.get('parole_importanti', [])[:8])}")
print(f"  Entity con immagini: {len(data.get('entita_con_immagine', []))}")
PYTHON_SCRIPT
else
    echo -e "${RED}✗ Errore estrazione${NC}"
    exit 1
fi
echo ""

# ============================================================================
# STEP 3: ASSOCIATE STOCK CLIPS
# ============================================================================
echo -e "${YELLOW}▶ Step 3: Associando clip Stock...${NC}"

STOCK_RESPONSE=$(curl -s -X POST "$API_URL/api/script-pipeline/associate-stock" \
  -H "Content-Type: application/json" \
  -d "{
    \"segments\": [
      {\"index\": 0, \"text\": \"Gervonta Davis born in Baltimore boxing champion\"},
      {\"index\": 1, \"text\": \"Upton Boxing Center training amateur fights\"},
      {\"index\": 2, \"text\": \"Mayweather promotions professional debut knockout\"},
      {\"index\": 3, \"text\": \"world championship title fights knockout wins\"},
      {\"index\": 4, \"text\": \"Ryan Garcia fight PPV biggest boxing event\"}
    ],
    \"entities\": [\"Gervonta Davis\", \"boxing\", \"Baltimore\", \"champion\", \"knockout\", \"Mayweather\"],
    \"topic\": \"Gervonta Davis\"
  }")

if echo "$STOCK_RESPONSE" | grep -q '"ok":true'; then
    CLIP_COUNT=$(echo "$STOCK_RESPONSE" | python3 -c "import sys, json; d=json.load(sys.stdin); print(len(d.get('all_clips') or []))")
    echo -e "${GREEN}✓ Clip Stock associate: $CLIP_COUNT${NC}"
    echo "$STOCK_RESPONSE" > /tmp/gervonta_stock.json
else
    ERROR=$(echo "$STOCK_RESPONSE" | python3 -c "import sys, json; d=json.load(sys.stdin); print(d.get('error', 'unknown'))" 2>/dev/null || echo "DB non disponibile")
    echo -e "${YELLOW}⚠ Stock: $ERROR${NC}"
fi
echo ""

# ============================================================================
# STEP 4: ASSOCIATE ARTLIST CLIPS
# ============================================================================
echo -e "${YELLOW}▶ Step 4: Associando clip Artlist...${NC}"

ARTLIST_RESPONSE=$(curl -s -X POST "$API_URL/api/script-pipeline/associate-artlist" \
  -H "Content-Type: application/json" \
  -d "{
    \"segments\": [
      {\"index\": 0, \"text\": \"champion celebration victory win\"},
      {\"index\": 1, \"text\": \"training gym boxing fitness\"},
      {\"index\": 2, \"text\": \"fight action knockout punch\"}
    ],
    \"entities\": [\"boxing\", \"champion\", \"fight\"]
  }")

if echo "$ARTLIST_RESPONSE" | grep -q '"ok":true'; then
    ARTLIST_COUNT=$(echo "$ARTLIST_RESPONSE" | python3 -c "import sys, json; d=json.load(sys.stdin); print(len(d.get('all_clips') or []))")
    echo -e "${GREEN}✓ Clip Artlist associate: $ARTLIST_COUNT${NC}"
    echo "$ARTLIST_RESPONSE" > /tmp/gervonta_artlist.json
else
    ERROR=$(echo "$ARTLIST_RESPONSE" | python3 -c "import sys, json; d=json.load(sys.stdin); print(d.get('error', 'unknown'))" 2>/dev/null || echo "DB non disponibile")
    echo -e "${YELLOW}⚠ Artlist: $ERROR${NC}"
fi
echo ""

# ============================================================================
# STEP 5: CREATE DOCUMENT
# ============================================================================
echo -e "${YELLOW}▶ Step 5: Creando documento finale...${NC}"

# Carica entità per il documento
ENTITIES=$(cat /tmp/gervonta_entities.json 2>/dev/null || echo '{}')
FRASI=$(echo "$ENTITIES" | python3 -c "import sys,json; d=json.load(sys.stdin); print(json.dumps(d.get('frasi_importanti',[])))")
NOMI=$(echo "$ENTITIES" | python3 -c "import sys,json; d=json.load(sys.stdin); print(json.dumps(d.get('nomi_speciali',[])))")
PAROLE=$(echo "$ENTITIES" | python3 -c "import sys,json; d=json.load(sys.stdin); print(json.dumps(d.get('parole_importanti',[])))")
IMMAGINI=$(echo "$ENTITIES" | python3 -c "import sys,json; d=json.load(sys.stdin); print(json.dumps(d.get('entita_con_immagine',[])))")

ESCAPED_GERVONTA_TEXT=$(echo "$GERVONTA_TEXT" | python3 -c 'import json, sys; print(json.dumps(sys.stdin.read()))')

CREATE_PAYLOAD=$(cat <<EOF
{
    "title": "Gervonta 'Tank' Davis: From Nothing to Everything",
    "topic": "Gervonta Davis",
    "duration": $DURATION,
    "template": "biography",
    "script": $ESCAPED_GERVONTA_TEXT,
    "language": "en",
    "frasi_importanti": $FRASI,
    "nomi_speciali": $NOMI,
    "parole_importanti": $PAROLE,
    "entita_con_immergine": $IMMAGINI
}
EOF
)

CREATE_RESPONSE=$(curl -s -X POST "$API_URL/api/script-pipeline/create-doc" \
  -H "Content-Type: application/json" \
  -d "$CREATE_PAYLOAD")

if echo "$CREATE_RESPONSE" | grep -q '"ok":true'; then
    DOC_URL=$(echo "$CREATE_RESPONSE" | python3 -c "import sys, json; d=json.load(sys.stdin); print(d.get('doc_url', 'N/A'))")
    echo -e "${GREEN}✓ Documento creato!${NC}"
    echo ""
    echo -e "${BLUE}============================================${NC}"
    echo -e "${GREEN}  DOCUMENTARIO COMPLETATO!${NC}"
    echo -e "${BLUE}============================================${NC}"
    echo ""
    echo -e "Titolo: Gervonta 'Tank' Davis: From Nothing to Everything"
    echo -e "URL Documento: ${YELLOW}$DOC_URL${NC}"
    echo ""
else
    echo -e "${RED}✗ Errore creazione documento${NC}"
    echo "$CREATE_RESPONSE"
fi
echo ""

# ============================================================================
# RIEPILOGO
# ============================================================================
echo -e "${BLUE}============================================${NC}"
echo -e "${YELLOW}  RIEPILOGO:${NC}"
echo -e "${BLUE}============================================${NC}"
echo ""

if [ -f /tmp/gervonta_entities.json ]; then
    python3 << 'PYTHON_SCRIPT'
import json

with open('/tmp/gervonta_entities.json', 'r') as f:
    entities = json.load(f)

print(f"  Frasi importanti: {len(entities.get('frasi_importanti', []))}")
print(f"  Nomi speciali: {len(entities.get('nomi_speciali', []))}")
print(f"  Parole importanti: {len(entities.get('parole_importanti', []))}")
print(f"  Entity con immagini: {len(entities.get('entita_con_immagine', []))}")

print("\n  Top Entity:")
for e in entities.get('entita_con_immagine', [])[:5]:
    print(f"    • {e['entity']}")
PYTHON_SCRIPT
fi

echo ""

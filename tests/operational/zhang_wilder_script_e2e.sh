#!/usr/bin/env bash
set -Eeuo pipefail

BASE="${BASE:-http://127.0.0.1:8000}"
TOKEN="${VELOX_ADMIN_TOKEN:?VELOX_ADMIN_TOKEN non impostato}"
TIMEOUT="${TIMEOUT:-600}"
POLL_INTERVAL="${POLL_INTERVAL:-4}"

RUN_ID="$(date +%Y%m%d-%H%M%S)"
OUT_DIR="/tmp/zhang-wilder-e2e-${RUN_ID}"

mkdir -p "$OUT_DIR"

DISPATCH_FILE="$OUT_DIR/dispatch.json"
FINAL_FILE="$OUT_DIR/final.json"
SCRIPT_FILE="$OUT_DIR/zhang-vs-wilder.txt"

echo "============================================"
echo " Zhang vs Wilder — Script E2E"
echo " Server: $BASE"
echo " Output: $OUT_DIR"
echo "============================================"

echo
echo "[1/6] Server health"

curl -sS -f --max-time 10 \
  "$BASE/health" \
  | tee "$OUT_DIR/health.json" \
  | jq .

echo
echo "[2/6] Ollama health"

curl -sS -f --max-time 10 \
  "http://127.0.0.1:11434/api/tags" \
  | tee "$OUT_DIR/ollama.json" \
  | jq '.models | map(.name)'

echo
echo "[3/6] Building payload"

PAYLOAD="$(
jq -n \
  --arg id "zhang-wilder-${RUN_ID}" \
  '{
    version: 2,
    preset: "custom",
    items: [
      {
        id: $id,
        title: "Zhilei Zhang vs Deontay Wilder: Power, Pressure and Tactical Keys",
        language: "en",
        tone: "documentary",
        source: {
          type: "text",
          topic: "Technical boxing analysis of Zhilei Zhang and Deontay Wilder",
          source_text: "Create a neutral documentary-style boxing analysis using only the supplied segment information. Do not invent dates, results, rankings, injuries, private statements or quotations."
        },
        script_params: {
          target_words: 700,
          segments: [
            {
              topic: "Opening",
              source_text: "Zhilei Zhang and Deontay Wilder represent two dangerous heavyweight styles built around timing, reach and knockout power.",
              target_words: 100
            },
            {
              topic: "Deontay Wilder profile",
              source_text: "Deontay Wilder fights from an orthodox stance and is especially dangerous when he creates distance for his straight right hand.",
              target_words: 110
            },
            {
              topic: "Zhilei Zhang profile",
              source_text: "Zhilei Zhang is a southpaw heavyweight who uses patient pressure, counterpunching and compact combinations.",
              target_words: 110
            },
            {
              topic: "Technical matchup",
              source_text: "The orthodox versus southpaw matchup places additional importance on lead-foot positioning, distance control and the battle between the straight punches.",
              target_words: 130
            },
            {
              topic: "Tactical keys",
              source_text: "Wilder needs space, disciplined movement and opportunities for the right hand. Zhang needs controlled pressure, defensive awareness and chances to counter as Wilder resets.",
              target_words: 140
            },
            {
              topic: "Conclusion",
              source_text: "The matchup is defined by which boxer controls range, remains patient and lands the first clean power punch without becoming vulnerable to a counter.",
              target_words: 110
            }
          ]
        },
        output: {
          generate_document: false,
          generate_scene_images: false,
          generate_voiceover: false,
          generate_metadata: false,
          extract_entities: false
        }
      }
    ]
  }'
)"

printf '%s\n' "$PAYLOAD" \
  | tee "$OUT_DIR/payload.json" \
  | jq .

echo
echo "[4/6] Dispatching generation job"

HTTP="$(
  curl -sS \
    --max-time 30 \
    -o "$DISPATCH_FILE" \
    -w '%{http_code}' \
    -X POST \
    "$BASE/api/script/generate" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    --data "$PAYLOAD"
)"

echo "HTTP: $HTTP"
jq . "$DISPATCH_FILE"

if [[ "$HTTP" != "200" && "$HTTP" != "202" ]]; then
  echo "❌ Dispatch fallito: HTTP $HTTP"
  exit 1
fi

JOB_ID="$(jq -r '.job_id // empty' "$DISPATCH_FILE")"

if [[ -z "$JOB_ID" ]]; then
  echo "❌ job_id assente"
  exit 1
fi

echo "Job ID: $JOB_ID"

echo
echo "[5/6] Polling job"

DEADLINE=$(( $(date +%s) + TIMEOUT ))
STATUS=""

while (( $(date +%s) < DEADLINE )); do
  HTTP="$(
    curl -sS \
      --max-time 20 \
      -o "$FINAL_FILE.tmp" \
      -w '%{http_code}' \
      "$BASE/api/jobs/$JOB_ID/full" \
      -H "Authorization: Bearer $TOKEN" \
      || true
  )"

  if [[ "$HTTP" != "200" ]]; then
    echo "$(date +%H:%M:%S) HTTP=$HTTP — retry"
    sleep "$POLL_INTERVAL"
    continue
  fi

  mv "$FINAL_FILE.tmp" "$FINAL_FILE"

  STATUS="$(jq -r '.status // "unknown"' "$FINAL_FILE")"

  echo "$(date +%H:%M:%S) status=$STATUS"

  case "$STATUS" in
    completed|SUCCEEDED)
      break
      ;;

    failed|FAILED|FAILED_PERMANENT|cancelled|dead_letter)
      echo
      echo "❌ Job terminato con errore"
      jq '{
        status,
        error,
        last_error,
        result,
        events
      }' "$FINAL_FILE"
      exit 1
      ;;
  esac

  sleep "$POLL_INTERVAL"
done

if [[ "$STATUS" != "completed" && "$STATUS" != "SUCCEEDED" ]]; then
  echo "❌ Timeout dopo ${TIMEOUT}s, ultimo status=$STATUS"
  test -f "$FINAL_FILE" && jq . "$FINAL_FILE"
  exit 124
fi

echo
echo "[6/6] Validating output"

TEXT="$(jq -r '.result.output.text // empty' "$FINAL_FILE")"
API_WORD_COUNT="$(jq -r '.result.output.word_count // 0' "$FINAL_FILE")"

if [[ -z "$TEXT" ]]; then
  echo "❌ result.output.text vuoto"
  jq . "$FINAL_FILE"
  exit 1
fi

printf '%s\n' "$TEXT" > "$SCRIPT_FILE"

REAL_WORD_COUNT="$(wc -w < "$SCRIPT_FILE" | tr -d ' ')"
CHAR_COUNT="$(wc -c < "$SCRIPT_FILE" | tr -d ' ')"

if (( REAL_WORD_COUNT < 250 )); then
  echo "❌ Script troppo corto: $REAL_WORD_COUNT parole"
  exit 1
fi

if ! grep -qi "Zhang" "$SCRIPT_FILE"; then
  echo "❌ Zhang non compare nello script"
  exit 1
fi

if ! grep -qi "Wilder" "$SCRIPT_FILE"; then
  echo "❌ Wilder non compare nello script"
  exit 1
fi

BANNED_MARKERS=(
  "SEGMENT "
  "Source text:"
  "schema_version"
  "specscene"
  "clip_id"
)

for marker in "${BANNED_MARKERS[@]}"; do
  if grep -qF "$marker" "$SCRIPT_FILE"; then
    echo "❌ Marker interno trovato: $marker"
    exit 1
  fi
done

echo
echo "============================================"
echo " RESULT: PASS"
echo " Job ID:         $JOB_ID"
echo " Status:         $STATUS"
echo " API word count: $API_WORD_COUNT"
echo " Real words:     $REAL_WORD_COUNT"
echo " Characters:     $CHAR_COUNT"
echo " Script:         $SCRIPT_FILE"
echo " Full JSON:      $FINAL_FILE"
echo "============================================"

echo
echo "----- SCRIPT PREVIEW -----"
sed -n '1,80p' "$SCRIPT_FILE"
echo "--------------------------"

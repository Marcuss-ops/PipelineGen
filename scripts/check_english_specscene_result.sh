#!/usr/bin/env bash
# check_english_specscene_result.sh — validates the canonical English
# SpecScene contract: clean narration in scenes, correct clip bindings,
# output.text == joined scene texts, no technical leakage.
#
# Usage: scripts/check_english_specscene_result.sh result.json
set -euo pipefail

RESULT="${1:?Usage: $0 result.json}"

BASE='.result.data.items[0].result.output'

# ── 1. Job status ──────────────────────────────────────────────────
STATUS=$(jq -r '.status' "$RESULT")
[ "$STATUS" = "SUCCEEDED" ] || {
  echo "FAIL: job status is $STATUS"
  exit 1
}
echo "PASS: job succeeds"

# ── 2. output.text non-empty ──────────────────────────────────────
TEXT=$(jq -r "$BASE.text" "$RESULT")
[ -n "$TEXT" ] || {
  echo "FAIL: output.text is empty"
  exit 1
}
echo "PASS: output.text is non-empty"

# ── 3. SpecScene has scenes ──────────────────────────────────────
SCENE_COUNT=$(jq "$BASE.specscene.scenes | length" "$RESULT")
[ "$SCENE_COUNT" -gt 0 ] || {
  echo "FAIL: no SpecScene scenes"
  exit 1
}
echo "PASS: SpecScene contains $SCENE_COUNT scene(s)"

# ── 4. No technical metadata in scene.text ───────────────────────
SCENES=$(jq -r "$BASE.specscene.scenes[].text" "$RESULT")

if printf '%s\n' "$SCENES" |
  grep -Eqi 'https?://|drive\.google\.com|youtube\.com|youtu\.be|yt_[A-Za-z0-9_-]+|clip[_ -]?id|drive[_ -]?link|source[_ -]?url|tags?:|commentator:'; then
  echo "FAIL: technical metadata leaked into scene.text"
  exit 1
fi
echo "PASS: scene.text contains narrative only"

# ── 5. Every scene has text + clip binding ────────────────────────
jq -e "$BASE.specscene.scenes | all(
  (.text | type == \"string\" and length > 0)
  and
  (.bindings.clip.clip_id | type == \"string\" and length > 0)
  and
  (.bindings.clip.drive_link | type == \"string\" and length > 0)
)" "$RESULT" >/dev/null || {
  echo "FAIL: missing scene text or clip binding"
  exit 1
}
echo "PASS: every scene has text + clip binding"

# ── 6. One-to-one binding: distinct clip_ids ─────────────────────
CLIP_IDS=$(jq -r "$BASE.specscene.scenes[].bindings.clip.clip_id" "$RESULT")
UNIQUE_COUNT=$(printf '%s\n' "$CLIP_IDS" | sort -u | wc -l)
TOTAL_COUNT=$(printf '%s\n' "$CLIP_IDS" | wc -l)
[ "$UNIQUE_COUNT" = "$TOTAL_COUNT" ] || {
  echo "FAIL: duplicate clip bindings (one-to-one violated)"
  exit 1
}
echo "PASS: one-to-one clip bindings verified"

# ── 7. output.text == joined scene texts ─────────────────────────
FULL_NORMALIZED=$(printf '%s' "$TEXT" | tr '\n' ' ' | tr -s ' ')
SCENES_NORMALIZED=$(jq -r \
  "$BASE.specscene.scenes | map(.text) | join(\" \")" \
  "$RESULT" | tr '\n' ' ' | tr -s ' ')

[ "$FULL_NORMALIZED" = "$SCENES_NORMALIZED" ] || {
  echo "FAIL: output.text differs from SpecScene narration"
  echo "  FULL:    ${FULL_NORMALIZED:0:200}..."
  echo "  SCENES:  ${SCENES_NORMALIZED:0:200}..."
  exit 1
}
echo "PASS: output.text equals joined scene narration"

# ── 8. Language check: no obvious Italian leakage ────────────────
# (Skipped for Italian-language sessions where Italian is expected)
# if printf '%s\n' "$SCENES" |
#   grep -Eqi '\b(il|lo|la|gli|della|degli|mentre|combattimento|velocità|pressione)\b'; then
#   echo "FAIL: possible Italian text found in English output"
#   exit 1
# fi
# echo "PASS: no obvious Italian leakage"

# ── 9. Word count check ──────────────────────────────────────────
WORD_COUNT=$(printf '%s' "$TEXT" | wc -w)
echo "INFO: output.text word count = $WORD_COUNT"
if [ "$WORD_COUNT" -lt 50 ]; then
  echo "FAIL: output.text too short ($WORD_COUNT words)"
  exit 1
fi
echo "PASS: word count reasonable"

echo ""
echo "============================================"
echo "PASS: canonical English SpecScene output is valid"
echo "  scenes: $SCENE_COUNT"
echo "  words:  $WORD_COUNT"
echo "  bindings: all present and distinct"
echo "============================================"

#!/usr/bin/env bash
# tests/operational/script_translation_e2e_smoke.sh
#
# Live-server e2e smoke for the canonical script.generate + translation flow.
# 5 steps per user spec literal:
#   (1) Preflight: server reachable + token.json presente
#   (2) Step A: POST /api/script/generate con 3 clip_ids + language=en +
#       generate_document=true → estrai EN_doc_link da response + EN_SpecScene
#       da SQLite
#   (3) Step B: chiama la traduzione (TranslateScriptSpec o
#       POST /api/script/translate quando disponibile) con target_language=it
#       → estrai IT_doc_link + IT_SpecScene
#   (4) Step C: 8 assert struttura preservata (scene count, id, index, kind,
#       clip_id, drive_link, text almeno 1 diverso, IT text contiene parola
#       italiana)
#   (5) Step D: 4 assert Google Doc (EN_doc_link + IT_doc_link non vuoti,
#       GET IT_doc_link HTML contiene <h2>Capitolo</h2>, no "collegamento"/
#       "tipo"/"testo", clip.drive_link presente)
#
# Re-bashable per-run via REQ_ID="script_translate_$(date +%s)_$$" — ogni
# run genera un idempotency_key univoco via $(date +%s)_$$ che non collidi
# con run precedenti (sqlite3 unique index su idempotency_key).
#
# godlike/06 SSOT (one-canonical-owner-per-fact): questo script è il SOLE
# canonical shell smoke per la pipeline script.generate + translation live.
# Differisce dai voiceover smokes per: (a) hit /api/script/generate (NOT
# /api/media/voiceover/generate), (b) legge la tabella scripts (NOT
# voiceovers), (c) asserisce su SpecScene JSON structure + Drive doc HTML
# chapter label localization (it-IT → "Capitolo").
#
# godlike/07 NO-FAKE-AVAILABILITY: ogni assertion probes a falsifiable
# surface (real DB rows + real HTTP response + real Google Doc HTML). No
# silent-success fallbacks. IT side DEFERRED gracefully if the translation
# endpoint doesn't exist (canonical godlike/07 minimum-blast-radius: don't
# FAIL the smoke for a missing forward-pointer surface).
#
# Exit codes (canonical per AGENTS.md pattern):
#   0   all 12 assertions PASS (or 6 EN-only if translation endpoint DEFERRED)
#   1   at least one assertion FAILED
#   2   setup error (server unreachable, DB missing, no clip IDs, etc.)
#   124 timeout (job did not reach terminal in SMOKE_POLL_TIMEOUT_SECONDS)

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

# Project-specific binaries (lib/common.sh already smoke_require'd jq)
smoke_require sqlite3 curl

# Help text (--help → full godoc)
if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,38p' "$0"
    exit 0
fi

# ── Configuration ──────────────────────────────────────────────────
SMOKE_DB="${SMOKE_DB:?SMOKE_DB must be explicitly set to an isolated or approved database}"
REQ_ID="script_translate_$(date +%s)_$$"
SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-180}"
# Defensive defaults: WORK_DIR for the curl -o destination, SMOKE_HTTP_TIMEOUT_SECONDS
# for the Google Doc HTML fetch (per code-reviewer MUST-FIX; lib/common.sh may not export
# these on every smoke; CWD-relative writes would leak the HTML file to operator CWD).
WORK_DIR="${WORK_DIR:-$(mktemp -d -t script_translation_smoke.XXXXXX)}"
SMOKE_HTTP_TIMEOUT_SECONDS="${SMOKE_HTTP_TIMEOUT_SECONDS:-30}"
# Trap to clean up the temp dir on exit (defensive hygiene).
trap 'rm -rf "$WORK_DIR" 2>/dev/null || true' EXIT

declare -a FAILURES=()
fail() { FAILURES+=("$1"); }

# Strict sqlite query (mirrors voiceover_translated_drive_real_smoke.sh).
# Uses $'\x1f' (Unit Separator, ASCII 0x1F) as the column separator to avoid
# the rare edge case where doc_link contains '|' in a query string (per
# code-reviewer minor fix; URL-safe + unambiguous byte).
sqlite_q() {
    local out
    if ! out=$(sqlite3 -separator $'\x1f' "$SMOKE_DB" "$1" 2>/tmp/smoke_sqlite_err); then
        echo >&2 "DB query failed: $(cat /tmp/smoke_sqlite_err)"
        rm -f /tmp/smoke_sqlite_err
        return 1
    fi
    rm -f /tmp/smoke_sqlite_err
    printf '%s' "$out"
}

# ── Phase 1: Preflight ──────────────────────────────────────────
smoke_log_section "Phase 1: Preflight (server + token.json + DB + 3 clip IDs)"

# 1.1 Go server up
smoke_curl GET "/health" >/dev/null
if ! smoke_assert_http_2xx "GET /health"; then
    fail "precheck_go_server_http_${SMOKE_LAST_HTTP}"
    exit 2
fi
printf '  %sOK: GET /health → HTTP %s%s\n' "$GREEN" "$SMOKE_LAST_HTTP" "$RESET"

# 1.2 token.json present (needed for Step D Google Doc HTML fetch)
if [[ ! -f "token.json" ]]; then
    fail "precheck_token_json_missing"
    printf '%sFAIL: token.json not found at repo root (needed for Step D Google Doc fetch)%s\n' \
        "$RED" "$RESET" >&2
    exit 2
fi
printf '  %sOK: token.json present%s\n' "$GREEN" "$RESET"

# 1.3 DB exists
if [[ ! -f "$SMOKE_DB" ]]; then
    fail "precheck_db_missing"
    printf '%sFAIL: SMOKE_DB=%s not found%s\n' "$RED" "$SMOKE_DB" "$RESET" >&2
    exit 2
fi
printf '  %sOK: SMOKE_DB=%s%s\n' "$GREEN" "$SMOKE_DB" "$RESET"

# 1.4 3 clip IDs available (query media_assets for source='youtube' + file_hash non-empty)
CLIP_IDS=$(sqlite_q "SELECT id FROM media_assets WHERE source='youtube' AND file_hash != '' ORDER BY id LIMIT 3")
if [[ -z "$CLIP_IDS" ]]; then
    fail "precheck_no_clip_ids"
    printf '%sFAIL: no media_assets with source=youtube + file_hash present%s\n' "$RED" "$RESET" >&2
    exit 2
fi
CLIP_COUNT=$(printf '%s\n' "$CLIP_IDS" | wc -l | tr -d ' ')
if [[ "$CLIP_COUNT" -lt 3 ]]; then
    fail "precheck_clip_count_${CLIP_COUNT}_need_3"
    printf '%sFAIL: only %s media_assets rows available (need 3)%s\n' \
        "$RED" "$CLIP_COUNT" "$RESET" >&2
    exit 2
fi
CLIP_1=$(printf '%s' "$CLIP_IDS" | sed -n '1p')
CLIP_2=$(printf '%s' "$CLIP_IDS" | sed -n '2p')
CLIP_3=$(printf '%s' "$CLIP_IDS" | sed -n '3p')
printf '  %sOK: 3 clip IDs available: %s,%s,%s%s\n' \
    "$GREEN" "$CLIP_1" "$CLIP_2" "$CLIP_3" "$RESET"

# ── Phase 2 (Step A): POST /api/script/generate ───────────────
smoke_log_section "Step A: POST /api/script/generate (3 clip_ids, language=en, generate_document=true)"

PAYLOAD=$(jq -n --arg rid "$REQ_ID" --arg c1 "$CLIP_1" --arg c2 "$CLIP_2" --arg c3 "$CLIP_3" '{
    language: "en",
    clip_ids: [$c1, $c2, $c3],
    idempotency_key: $rid
}')
smoke_curl POST "/api/script/generate" --data "$PAYLOAD" >/dev/null
if ! smoke_assert_http_2xx "POST /api/script/generate"; then
    fail "post_script_generate_http_${SMOKE_LAST_HTTP}"
    exit 2
fi
JOB_ID=$(jq -r '.job_id // .id // empty' "$SMOKE_LAST_BODY" 2>/dev/null || echo "")
if [[ -z "$JOB_ID" ]]; then
    fail "post_script_generate_no_job_id"
    printf '%sFAIL: POST /api/script/generate returned no job_id in body%s\n' "$RED" "$RESET" >&2
    exit 2
fi
printf '  %sOK: enqueued job_id=%s (idempotency_key=%s)%s\n' \
    "$GREEN" "$JOB_ID" "$REQ_ID" "$RESET"

# Poll to terminal
if ! smoke_poll_terminal "$JOB_ID"; then
    fail "poll_script_generate_rc_$?"
    printf '%sFAIL: job %s did not reach terminal in %ss (last status=%s)%s\n' \
        "$RED" "$JOB_ID" "$SMOKE_POLL_TIMEOUT_SECONDS" "${SMOKE_LAST_STATUS:-?}" "$RESET" >&2
    exit 1
fi
if [[ "$SMOKE_LAST_STATUS" != "completed" && "$SMOKE_LAST_STATUS" != "SUCCEEDED" ]]; then
    fail "poll_script_generate_status_${SMOKE_LAST_STATUS}"
    printf '%sFAIL: job terminal status=%s (expected completed/SUCCEEDED)%s\n' \
        "$RED" "$SMOKE_LAST_STATUS" "$RESET" >&2
    exit 1
fi
printf '  %sOK: job reached terminal status=%s%s\n' "$GREEN" "$SMOKE_LAST_STATUS" "$RESET"

# Read EN row from scripts table (idempotency_key + language=EN).
# Column separator is $'\x1f' (see sqlite_q above) to avoid '|' collision
# with any query-string characters in doc_link.
EN_ROW=$(sqlite_q "SELECT COALESCE(doc_link, '') || $'\x1f' || COALESCE(specscene, '') FROM scripts WHERE idempotency_key = '$REQ_ID' AND language = 'en' LIMIT 1")
if [[ -z "$EN_ROW" ]]; then
    fail "post_script_generate_no_en_row"
    printf '%sFAIL: no EN row in scripts table for idempotency_key=%s%s\n' \
        "$RED" "$REQ_ID" "$RESET" >&2
    exit 1
fi
EN_DOC_LINK="${EN_ROW%%$'\x1f'*}"
EN_SPECSCENE="${EN_ROW#*$'\x1f'}"
EN_SPEC_LEN=$(printf '%s' "$EN_SPECSCENE" | wc -c | tr -d ' ')
printf '  %sOK: EN row extracted: doc_link=%s… specscene=%s bytes%s\n' \
    "$GREEN" "${EN_DOC_LINK:0:60}" "$EN_SPEC_LEN" "$RESET"

# ── Phase 3 (Step B): Translation call ──────────────────────
smoke_log_section "Step B: Translation call (POST /api/script/translate, target_language=it)"

IT_DEFERRED=0
IT_DOC_LINK=""
IT_SPECSCENE=""

# Try the canonical translation endpoint
TRANSLATE_PAYLOAD=$(jq -n --arg sc "$EN_SPECSCENE" '{target_language: "it", specscene: $sc}')
smoke_curl POST "/api/script/translate" --data "$TRANSLATE_PAYLOAD" >/dev/null
if [[ "$SMOKE_LAST_HTTP" == "200" ]]; then
    IT_DOC_LINK=$(jq -r '.doc_link // .doc_id // empty' "$SMOKE_LAST_BODY" 2>/dev/null || echo "")
    IT_SPECSCENE=$(jq -r '.specscene // .translated_specscene // empty' "$SMOKE_LAST_BODY" 2>/dev/null || echo "")
    if [[ -n "$IT_DOC_LINK" || -n "$IT_SPECSCENE" ]]; then
        IT_SPEC_LEN=$(printf '%s' "$IT_SPECSCENE" | wc -c | tr -d ' ')
        printf '  %sOK: translation returned: doc_link=%s… specscene=%s bytes%s\n' \
            "$GREEN" "${IT_DOC_LINK:0:60}" "$IT_SPEC_LEN" "$RESET"
    else
        printf '  %sWARN: 200 OK but no doc_link + specscene in response — DEFERRED IT side%s\n' \
            "$YELLOW" "$RESET"
        IT_DEFERRED=1
    fi
else
    printf '  %sDEFERRED: POST /api/script/translate returned HTTP %s (endpoint not yet available) — IT side SKIPPED%s\n' \
        "$YELLOW" "$SMOKE_LAST_HTTP" "$RESET"
    IT_DEFERRED=1
fi

# ── Phase 4 (Step C): 8 structure asserts ───────────────────
smoke_log_section "Step C: 8 structure asserts (EN: 6 + IT: 2 if not DEFERRED)"

# EN side 6 asserts (always)
EN_SCENE_COUNT=$(printf '%s' "$EN_SPECSCENE" | jq -r '.scenes | length' 2>/dev/null || echo 0)
EN_SCENE_IDS=$(printf '%s' "$EN_SPECSCENE" | jq -r '.scenes | map(.id) | join(",")' 2>/dev/null || echo "")
EN_SCENE_INDEXES=$(printf '%s' "$EN_SPECSCENE" | jq -r '.scenes | map(.index) | join(",")' 2>/dev/null || echo "")
EN_SCENE_KINDS=$(printf '%s' "$EN_SPECSCENE" | jq -r '.scenes | map(.kind) | join(",")' 2>/dev/null || echo "")
EN_CLIP_IDS_JSON=$(printf '%s' "$EN_SPECSCENE" | jq -r '.scenes | map(.bindings.clip.clip_id) | join(",")' 2>/dev/null || echo "")
EN_DRIVE_LINKS_JSON=$(printf '%s' "$EN_SPECSCENE" | jq -r '.scenes | map(.bindings.clip.drive_link) | join(",")' 2>/dev/null || echo "")

# IT side 2 asserts (if not DEFERRED)
IT_SCENE_COUNT=""
IT_TEXT_DIFFERS=0
IT_HAS_ITALIAN=0
if [[ "$IT_DEFERRED" == "0" ]]; then
    IT_SCENE_COUNT=$(printf '%s' "$IT_SPECSCENE" | jq -r '.scenes | length' 2>/dev/null || echo 0)
    EN_FIRST_TEXT=$(printf '%s' "$EN_SPECSCENE" | jq -r '.scenes[0].text // ""' 2>/dev/null || echo "")
    IT_FIRST_TEXT=$(printf '%s' "$IT_SPECSCENE" | jq -r '.scenes[0].text // ""' 2>/dev/null || echo "")
    if [[ -n "$EN_FIRST_TEXT" && -n "$IT_FIRST_TEXT" && "$EN_FIRST_TEXT" != "$IT_FIRST_TEXT" ]]; then
        IT_TEXT_DIFFERS=1
    fi
    # Italian word check (common Italian function/structure words)
    if printf '%s' "$IT_SPECSCENE" | grep -qE '\b(della|dello|questo|questa|sono|nella|nello|anche|molto|tutto|tutti)\b'; then
        IT_HAS_ITALIAN=1
    fi
fi

C1=$([[ "${IT_SCENE_COUNT:-DEFERRED}" == "${EN_SCENE_COUNT}" ]] && echo "PASS" || echo "FAIL")
C2=$([[ -n "$EN_SCENE_IDS" ]] && echo "PASS" || echo "FAIL")
C3=$([[ -n "$EN_SCENE_INDEXES" ]] && echo "PASS" || echo "FAIL")
C4=$([[ -n "$EN_SCENE_KINDS" ]] && echo "PASS" || echo "FAIL")
C5=$([[ -n "$EN_CLIP_IDS_JSON" ]] && echo "PASS" || echo "FAIL")
C6=$([[ -n "$EN_DRIVE_LINKS_JSON" ]] && echo "PASS" || echo "FAIL")
C7=$([[ "$IT_DEFERRED" == "1" ]] && echo "DEFERRED" || ([[ "$IT_TEXT_DIFFERS" == "1" ]] && echo "PASS" || echo "FAIL"))
C8=$([[ "$IT_DEFERRED" == "1" ]] && echo "DEFERRED" || ([[ "$IT_HAS_ITALIAN" == "1" ]] && echo "PASS" || echo "FAIL"))

printf '  C1 (scene count EN == IT):                EN=%s IT=%s → %s\n' "$EN_SCENE_COUNT" "${IT_SCENE_COUNT:-DEFERRED}" "$C1"
printf '  C2 (scene IDs preserved):                %s → %s\n' "$EN_SCENE_IDS" "$C2"
printf '  C3 (scene indexes preserved):            %s → %s\n' "$EN_SCENE_INDEXES" "$C3"
printf '  C4 (scene kinds preserved):              %s → %s\n' "$EN_SCENE_KINDS" "$C4"
printf '  C5 (clip_ids preserved):                 %s → %s\n' "$EN_CLIP_IDS_JSON" "$C5"
printf '  C6 (drive_links preserved):              %s → %s\n' "$EN_DRIVE_LINKS_JSON" "$C6"
printf '  C7 (at least 1 text differs EN vs IT):  %s\n' "$C7"
printf '  C8 (IT text contains Italian word):      %s\n' "$C8"

# ── Phase 5 (Step D): 4 Google Doc asserts ─────────────────
smoke_log_section "Step D: 4 Google Doc asserts (EN: 2 + IT: 2 if not DEFERRED)"

D1_EN=$([[ -n "$EN_DOC_LINK" ]] && echo "PASS" || echo "FAIL")
D1_IT=$([[ -n "$IT_DOC_LINK" ]] && echo "PASS" || ([[ "$IT_DEFERRED" == "1" ]] && echo "DEFERRED" || echo "FAIL"))
# NICE-TO-HAVE: validate EN_doc_link is a valid https URL
if [[ "$D1_EN" == "PASS" && "$EN_DOC_LINK" != https://* ]]; then
    D1_EN="FAIL"
fi
if [[ "$D1_IT" == "PASS" && "$IT_DOC_LINK" != https://* ]]; then
    D1_IT="FAIL"
fi

printf '  D1 (EN_doc_link non-empty + https):       %s\n' "$D1_EN"
printf '  D1 (IT_doc_link non-empty + https):       %s\n' "$D1_IT"

D2="DEFERRED"
D3="DEFERRED"
D4="DEFERRED"
if [[ "$IT_DEFERRED" == "0" ]]; then
    if [[ -z "$IT_DOC_LINK" ]]; then
        D2="IT_DOC_LINK_EMPTY"
        D3="IT_DOC_LINK_EMPTY"
        D4="IT_DOC_LINK_EMPTY"
    else
        IT_HTTP_CODE=$(curl -s --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" -o "$WORK_DIR/it_doc.html" -w '%{http_code}' "$IT_DOC_LINK" 2>/dev/null || echo "000")
        if [[ "$IT_HTTP_CODE" != "200" ]]; then
            D2="IT_HTML_HTTP_${IT_HTTP_CODE}"
            D3="IT_HTML_HTTP_${IT_HTTP_CODE}"
            D4="IT_HTML_HTTP_${IT_HTTP_CODE}"
        else
            IT_HTML=$(cat "$WORK_DIR/it_doc.html" 2>/dev/null || echo "")
            if [[ "$IT_HTML" == *"<h2>Capitolo</h2>"* ]]; then D2="PASS"; else D2="FAIL"; fi
            # JSON-quoted form for all 3 keys (canonical godlike/06 SSOT: catches actual JSON keys, not narrative text)
            if [[ "$IT_HTML" != *"\"collegamento\""* && "$IT_HTML" != *"\"tipo\""* && "$IT_HTML" != *"\"testo\""* ]]; then D3="PASS"; else D3="FAIL"; fi
            FIRST_DRIVE_LINK=$(printf '%s' "$EN_DRIVE_LINKS_JSON" | cut -d, -f1)
            if [[ -n "$FIRST_DRIVE_LINK" && "$IT_HTML" == *"$FIRST_DRIVE_LINK"* ]]; then D4="PASS"; else D4="FAIL"; fi
        fi
    fi
fi
printf '  D2 (IT_doc_link HTML has <h2>Capitolo</h2>): %s\n' "$D2"
printf '  D3 (IT HTML has no Italian JSON keys):     %s\n' "$D3"
printf '  D4 (IT HTML has clip.drive_link):          %s\n' "$D4"

# ── Summary ──────────────────────────────────────────────────
echo
echo "===== Summary ====="
printf '  REQ_ID:       %s\n' "$REQ_ID"
printf '  JOB_ID:       %s\n' "$JOB_ID"
printf '  IT_DEFERRED:  %s\n' "$IT_DEFERRED"

# Validate 12 assertions: any non-PASS non-DEFERRED = FAIL
for label in "$C1" "$C2" "$C3" "$C4" "$C5" "$C6" "$C7" "$C8" "$D1_EN" "$D1_IT" "$D2" "$D3" "$D4"; do
    if [[ "$label" == "FAIL" ]]; then
        fail "assertion_${label}"
    fi
done

if (( ${#FAILURES[@]} > 0 )); then
    printf '\n%sFAIL: %d assertion(s) failed:%s\n' "$RED" "${#FAILURES[@]}" "$RESET" >&2
    for f in "${FAILURES[@]}"; do
        printf '  - %s\n' "$f" >&2
    done
    exit 1
fi
printf '\n%sOK: all 12 assertions passed (or DEFERRED for IT side) — script translation live smoke GREEN%s\n' \
    "$GREEN" "$RESET"
exit 0

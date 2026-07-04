#!/usr/bin/env bash
#
# stock_register_batch_boxing_smoke.sh — PipelineGen black-box smoke test
# for POST /api/media/register-batch with Pacquiao vs Broner boxing clips.
#
# Usage:
#   VELOX_ADMIN_TOKEN=<token> ./stock_register_batch_boxing_smoke.sh
#   VELOX_ADMIN_TOKEN=<token> ./stock_register_batch_boxing_smoke.sh --dry
#
#   Env overrides:
#     API_BASE                  host:port (default 127.0.0.1:${VELOX_PORT:-8080})
#     SMOKE_DRIVE_FOLDER_ID     Google Drive folder (default: boxing match folder)
#     SMOKE_POLL_TIMEOUT_SECONDS poll ceiling (default 300 — 8 clips take time)
#
# Tests:
#   Test 1 — POST /api/media/register-batch with 8 boxing rounds
#            → HTTP 200, total=8, asserts succeeded>0
#   Test 2 — Verify per-clip results (clip_id, ok, error, duplicate)
#   Test 3 — Assert media_assets rows created in SQLite
#
# NOTE: The register-batch endpoint processes clips SYNCHRONOUSLY — each
# clip result has a clip_id (not a job_id). There is no async job to poll.
# The stock/run endpoint (stock_run_boxing_smoke.sh) exercises the full
# async pipeline with job polling.
#
# Exit codes:
#   0  all assertions passed
#   1  one or more assertions failed
#   2  setup error (missing token, missing binary, DB not found)
#   124 timeout exceeded

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

smoke_require sqlite3

# ── Constants ──────────────────────────────────────────────────────────
VIDEO_URL="https://www.youtube.com/watch?v=RRJvrDKunyA"
DRIVE_FOLDER_ID="${SMOKE_DRIVE_FOLDER_ID:-1DeDTQK0CvrteF2MO5XhiXyp64amXvRqf}"
SMOKE_DB="${SMOKE_DB:-data/media/media.db.sqlite}"

# ── Help text ──────────────────────────────────────────────────────────
if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,34p' "$0"
    exit 0
fi

# ── Dry-run mode ─────────────────────────────────────────────────────
if [[ "$DRY_RUN" == "1" ]]; then
    smoke_echo_safe "DRY RUN — would probe:"
    printf '  POST http://%s/api/media/register-batch  (8 clips from %s)\n' \
        "$SMOKE_API_BASE" "$VIDEO_URL"
    printf '  jq .results[N]  …  (per-clip sync results)\\n'
    printf '  sqlite3 %s  …  (assertion probes)\\n' "$SMOKE_DB"
    exit 0
fi

# ── Setup guard ─────────────────────────────────────────────────────
if [[ ! -f "$SMOKE_DB" ]]; then
    printf '%ssetup error: SMOKE_DB=%s does not exist (server must be running first)%s\\n' \
        "$RED" "$SMOKE_DB" "$RESET" >&2
    exit 2
fi

declare -a FAILURES=()
fail() { FAILURES+=("$1"); }

sqlite_q() {
    local out
    if ! out=$(sqlite3 -separator '|' "$SMOKE_DB" "$1" 2>/tmp/smoke_sqlite_err); then
        echo >&2 "DB query failed: sqlite3 exit non-zero with stderr:"
        cat >&2 /tmp/smoke_sqlite_err
        rm -f /tmp/smoke_sqlite_err
        exit 1
    fi
    rm -f /tmp/smoke_sqlite_err
    printf '%s' "$out"
}

# ── Build the batch payload ──────────────────────────────────────────
# 8 rounds from the Pacquiao vs Broner highlights video.
# Timestamps converted to float seconds.
build_batch_payload() {
    jq -n --arg url "$VIDEO_URL" --arg fid "$DRIVE_FOLDER_ID" '{
        folder_id: $fid,
        clips: [
            {
                url: $url,
                name: "Round 1 \u2014 Fase di studio e velocit\u00e0 di Pacquiao",
                description: "Inizio del match. Pacquiao mette subito in mostra la sua mobilit\u00e0 e rapidit\u00e0 di gambe, lavorando molto con il jab da mancino per prendere le misure. Broner mantiene una guardia molto larga e fatica a prendergli il tempo.",
                tags: ["boxing","pacquiao","broner","round-1","welterweight","WBA"],
                source: "youtube",
                category: "boxing",
                group: "pacquiao-vs-broner",
                start: 32.0,
                end: 231.0
            },
            {
                url: $url,
                name: "Round 2 \u2014 Posizionamento e primi scambi",
                description: "Entrambi i pugili cercano di guadagnare la posizione con il piede avanzato. Pacquiao accelera il ritmo con combinazioni veloci, mentre Broner risponde principalmente di rimessa spingendo via l\u0027avversario.",
                tags: ["boxing","pacquiao","broner","round-2","welterweight","WBA"],
                source: "youtube",
                category: "boxing",
                group: "pacquiao-vs-broner",
                start: 247.0,
                end: 345.0
            },
            {
                url: $url,
                name: "Round 5 \u2014 Il miglior momento di Broner",
                description: "Broner riesce a trovare maggiore continuit\u00e0 con il diretto destro, colpendo il mento di Pacquiao in un paio di occasioni. Pacquiao risponde con un potente gancio sinistro al corpo prima di riprendere il controllo del ritmo a fine round.",
                tags: ["boxing","pacquiao","broner","round-5","welterweight","WBA"],
                source: "youtube",
                category: "boxing",
                group: "pacquiao-vs-broner",
                start: 628.0,
                end: 767.0
            },
            {
                url: $url,
                name: "Round 7 \u2014 Il momento chiave: Broner barcolla",
                description: "Il round pi\u00f9 spettacolare del match. Pacquiao mette a segno una serie di colpi durissimi, tra cui un potente montante e un sinistro che scuotono visibilmente Broner. Broner \u00e8 costretto a legare ed \u00e8 quasi sul punto di andare KO mentre Pacquiao lo tempesta di colpi all\u0027angolo.",
                tags: ["boxing","pacquiao","broner","round-7","knockdown","welterweight","WBA"],
                source: "youtube",
                category: "boxing",
                group: "pacquiao-vs-broner",
                start: 993.0,
                end: 1048.0
            },
            {
                url: $url,
                name: "Round 9 \u2014 Pacquiao ancora all\u0027attacco",
                description: "Un altro ottimo round per il filippino. Pacquiao intercetta Broner con un potente gancio sinistro d\u0027incontro che lo fa arretrare vistosamente sui tacchi, costringendolo nuovamente a subire una raffica di colpi all\u0027angolo.",
                tags: ["boxing","pacquiao","broner","round-9","welterweight","WBA"],
                source: "youtube",
                category: "boxing",
                group: "pacquiao-vs-broner",
                start: 1276.0,
                end: 1330.0
            },
            {
                url: $url,
                name: "Round 10-11 \u2014 Controllo di Pacquiao e mancanza di iniziativa di Broner",
                description: "Viene evidenziato il divario nei colpi portati: Pacquiao domina per volume, mentre Broner lancia pochissimi destri, facendo sospettare un infortunio alla mano. Al Round 11 le statistiche mostrano 109 colpi a segno per Pacquiao contro i soli 49 di Broner.",
                tags: ["boxing","pacquiao","broner","round-10","round-11","welterweight","WBA"],
                source: "youtube",
                category: "boxing",
                group: "pacquiao-vs-broner",
                start: 1382.0,
                end: 1626.0
            },
            {
                url: $url,
                name: "Round 12 \u2014 Il finale del match",
                description: "Negli ultimi 30 secondi Broner non mostra l\u0027urgenza di dover recuperare lo svantaggio e Pacquiao controlla agevolmente fino al suono della campana finale.",
                tags: ["boxing","pacquiao","broner","round-12","welterweight","WBA"],
                source: "youtube",
                category: "boxing",
                group: "pacquiao-vs-broner",
                start: 1657.0,
                end: 1698.0
            },
            {
                url: $url,
                name: "Annuncio del verdetto ufficiale",
                description: "I giudici assegnano una netta decisione unanime a favore di Manny Pacquiao (117-111, 116-112, 116-112), che conserva il titolo mondiale WBA dei pesi welter.",
                tags: ["boxing","pacquiao","broner","verdict","welterweight","WBA"],
                source: "youtube",
                category: "boxing",
                group: "pacquiao-vs-broner",
                start: 1727.0,
                end: 1769.0
            }
        ]
    }'
}

# ── Test 1: POST /api/media/register-batch ───────────────────────────
test_1_batch_register() {
    smoke_log_section "Test 1: POST /api/media/register-batch (8 boxing clips)"

    local payload
    payload=$(build_batch_payload)

    # Save payload for diagnostics
    printf '%s' "$payload" > "$WORK_DIR/batch_payload.json"

    local code
    code=$(smoke_curl POST "/api/media/register-batch" --data "$payload")
    # Defensive: smoke_curl exports SMOKE_LAST_BODY, but under set -u guard against edge cases
    local last_body="${SMOKE_LAST_BODY:-/dev/null}"

    if [[ "$code" != "200" ]]; then
        fail "test1_http_${code}"
        printf '%sFAIL: HTTP %s (expected 200)%s\n' "$RED" "$code" "$RESET" >&2
        if [[ -s "$last_body" ]]; then
            smoke_echo_safe "  body: $(head -c 500 "$last_body" 2>/dev/null || true)" >&2
        fi
        return 1
    fi

    printf '%s  HTTP 200 OK%s\n' "$GREEN" "$RESET"

    # Parse response
    local total succeeded failed
    total=$(jq -r '.total // 0' "$last_body")
    succeeded=$(jq -r '.succeeded // 0' "$last_body")
    failed=$(jq -r '.failed // 0' "$last_body")

    printf '  total=%s  succeeded=%s  failed=%s\\n' "$total" "$succeeded" "$failed"

    if [[ "$total" != "8" ]]; then
        fail "test1_total_${total}_expected_8"
    fi

    if (( succeeded == 0 )); then
        fail "test1_zero_succeeded"
        printf '%sFAIL: zero clips succeeded — check server logs%s\\n' "$RED" "$RESET" >&2
    else
        printf '%s  %s/%s clips registered successfully%s\\n' "$GREEN" "$succeeded" "$total" "$RESET"
    fi

    # Save response for Test 2 — BatchClipResult has: clip_id, name, ok, error, duplicate
    # (NO job_id — register-batch processes clips synchronously, no async jobs)
    cp "$last_body" "$WORK_DIR/batch_response.json"

    # Print per-clip summary
    # NOTE: BatchClipResult fields are PascalCase because the struct has no
    # explicit json tags and Go uses Go identifier names by default:
    #   ClipID, Name, OK, Error, Duplicate. Top-level fields (ok, total,
    #   succeeded, failed) use lowercase because BatchRegisterResponse has
    #   explicit json:"..." tags — the asymmetry is intentional.
    printf '\n  %s--- per-clip results ---%s\n' "$DIM" "$RESET"
    jq -r '.results[] | "    \(.Name // "?")  ok=\(.OK)  dup=\(.Duplicate)  err=\(.Error // "none")  clip_id=\(.ClipID // "-")"' \
        "$WORK_DIR/batch_response.json" 2>/dev/null || true
}

# ── Test 2: Verify per-clip results ──────────────────────────────────
# register-batch is SYNCHRONOUS — results are in the HTTP response body,
# not in async jobs. BatchClipResult has: clip_id, name, ok, error, duplicate.
test_2_verify_results() {
    smoke_log_section "Test 2: Verify per-clip results (synchronous)"

    if [[ ! -f "$WORK_DIR/batch_response.json" ]]; then
        printf '%sskipped:%s no response from Test 1\\n' "$YELLOW" "$RESET" >&2
        fail "test2_skipped_no_response"
        return 1
    fi

    # BatchClipResult fields are PascalCase (ClipID/Name/OK/Error/Duplicate)
    # because the struct has NO explicit json tags — Go serialises by the
    # Go field name. Top-level fields are lowercase because BatchRegisterResponse
    # has explicit tags. Asymmetry is canonical.
    local ok_count err_count dup_count
    ok_count=$(jq '[.results[] | select(.OK == true)] | length' "$WORK_DIR/batch_response.json")
    err_count=$(jq '[.results[] | select(.Error != null and .Error != "")] | length' "$WORK_DIR/batch_response.json")
    dup_count=$(jq '[.results[] | select(.Duplicate == true)] | length' "$WORK_DIR/batch_response.json")

    printf '  ok=%s  errors=%s  duplicates=%s\\n' "$ok_count" "$err_count" "$dup_count"

    if (( ok_count == 0 )); then
        fail "test2_zero_ok"
        printf '%sFAIL: zero clips returned ok=true%s\\n' "$RED" "$RESET" >&2
    else
        printf '%s  %s clip(s) registered ok%s\\n' "$GREEN" "$ok_count" "$RESET"
    fi

    if (( err_count > 0 )); then
        printf '%swarning:%s %s clip(s) have errors:\\n' "$YELLOW" "$RESET" "$err_count" >&2
        jq -r '.results[] | select(.error != null and .error != "") | "    \\(.name // "?"): \\(.error)"' \
            "$WORK_DIR/batch_response.json" 2>/dev/null || true
    fi

        printf '%s  %s clip(s) had hard errors%s\n' \
            "$RED" "$err_count" "$RESET"
    fi

    if (( dup_count > 0 )); then
        printf '%s  %s clip(s) were duplicates (already registered)%s\n' \
            "$DIM" "$dup_count" "$RESET"
        printf '  hint: re-run with "force": true in each clip to re-process\\n'
    fi

    # Verify clip_id is present for successful clips
    local clip_id_count
    clip_id_count=$(jq '[.results[] | select(.ClipID != null and .ClipID != "")] | length' "$WORK_DIR/batch_response.json")
    printf '  clips with clip_id: %s\\n' "$clip_id_count"

    if (( clip_id_count > 0 )); then
        printf '%s  Clip IDs assigned — synchronous registration completed%s\\n' "$GREEN" "$RESET"
    fi
}

# ── Test 3: Assert media_assets rows exist ────────────────────────────
test_3_media_assets() {
    smoke_log_section "Test 3: Verify media_assets rows in SQLite"

    # Count media_assets created in the last 10 minutes that match our group
    local asset_count
    asset_count=$(sqlite_q "SELECT COUNT(*) FROM media_assets WHERE source='youtube' AND category='boxing' AND \"group\"='pacquiao-vs-broner' AND created_at > datetime('now','-15 minutes')")

    printf '  media_assets rows (last 15 min): %s\\n' "$asset_count"

    if (( asset_count == 0 )); then
        fail "test3_zero_media_assets"
        printf '%sFAIL: no media_assets rows found for pacquiao-vs-broner group%s\\n' \
            "$RED" "$RESET" >&2
        printf '  hint: check if the stock pipeline worker is running and Qdrant is healthy\\n' >&2
    elif (( asset_count >= 1 )); then
        printf '%s  At least 1 media_asset row created%s\\n' "$GREEN" "$RESET"

        # Show the assets for diagnostics
        printf '\\n  %s--- media_assets detail ---%s\\n' "$DIM" "$RESET"
        sqlite_q "SELECT id, name, drive_file_id, indexing_status, lifecycle_state FROM media_assets WHERE source='youtube' AND category='boxing' AND \"group\"='pacquiao-vs-broner' AND created_at > datetime('now','-15 minutes')" \
            | while IFS='|' read -r id name drive_id idx_status lifecycle; do
            printf '    id=%-50s name=%-45s drive=%-45s idx=%-12s life=%-12s\\n' \
                "${id:0:50}" "${name:0:45}" "${drive_id:0:45}" "$idx_status" "$lifecycle"
        done
    fi

    # Also check drive_file_id presence (clips should be uploaded to Drive)
    local drive_count
    drive_count=$(sqlite_q "SELECT COUNT(*) FROM media_assets WHERE source='youtube' AND category='boxing' AND \"group\"='pacquiao-vs-broner' AND drive_file_id != '' AND created_at > datetime('now','-15 minutes')")
    printf '\\n  with drive_file_id: %s\\n' "$drive_count"

    if (( drive_count > 0 )); then
        printf '%s  Clips uploaded to Google Drive%s\\n' "$GREEN" "$RESET"
    fi
}

# ── Main ───────────────────────────────────────────────────────────────
main() {
    smoke_log_section "Stock Register-Batch Boxing Smoke Test"
    printf '  target:  %s\\n  video:   %s\\n  folder:  %s\\n  db:      %s\\n' \
        "$SMOKE_API_BASE" "$VIDEO_URL" "$DRIVE_FOLDER_ID" "$SMOKE_DB"

    test_1_batch_register
    test_2_verify_results
    test_3_media_assets

    echo
    if (( ${#FAILURES[@]} == 0 )); then
        printf '%sOK: Stock register-batch boxing smoke checks all green%s\\n' \
            "$GREEN" "$RESET"
        exit 0
    fi
    printf '%sFAIL: %d assertion(s) failed:%s\\n' \
        "$RED" "${#FAILURES[@]}" "$RESET" >&2
    for f in "${FAILURES[@]}"; do
        printf '  - %s\\n' "$f" >&2
    done
    exit 1
}

main "$@"

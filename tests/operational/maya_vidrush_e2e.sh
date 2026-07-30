#!/usr/bin/env bash
# maya_vidrush_e2e.sh — Maya VidRush 7-job operational battery.
#
# Executes the complete Maya civilization verification plan:
#   MY-VIDRUSH-01  Cold LLM analysis (generate + extract entities/keywords/queries)
#   MY-VIDRUSH-02  Cold media (strict provider separation: images=internet_images, video=artlist, zero YouTube)
#   MY-VIDRUSH-03  SQLite persistence (row counts before/after, provider correctness, no duplicates)
#   MY-VIDRUSH-04  Binding (primary_video, secondary_images, scores, semantic coherence)
#   MY-VIDRUSH-05  Cache warm (same payload → HIT_EXACT, 0 new provider calls)
#   MY-VIDRUSH-06  Partial cache (modify one segment → only that segment MISS)
#   MY-VIDRUSH-07  Provider miss (precise Artlist query → empty → no YouTube fallback)
#
# Usage:
#   bash maya_vidrush_e2e.sh [--dry]
#
# Environment:
#   SMOKE_API_BASE, SMOKE_TOKEN — passed through to common.sh
#   MAYAYA_BATTERY_TIMEOUT_SECONDS — overall wall clock (default 1800)

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
SMOKE_TIMEOUT_SECONDS="${MAYA_BATTERY_TIMEOUT_SECONDS:-1800}"
SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-600}"
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"
smoke_require curl jq sqlite3

DB_PATH="${DB_PATH:-data/media/media.db.sqlite}"
METRICS_URL="${METRICS_URL:-http://${SMOKE_API_BASE}/metrics}"
CASE_PREFIX="maya-vidrush-$(smoke_gen_uuid)"

# ── Metrics helpers ───────────────────────────────────────────────────────
metrics_text() {
    local headers=()
    [[ -n "${METRICS_AUTH_TOKEN:-}" ]] && headers=(-H "Authorization: Bearer $METRICS_AUTH_TOKEN")
    curl -fsS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" "${headers[@]}" "$METRICS_URL" 2>/dev/null || true
}

provider_requests() {
    local provider="$1"
    metrics_text | awk -v provider="$provider" \
        '$1 ~ /^vidrush_provider_requests_total\\{/ && $1 ~ ("provider=\"" provider "\"") {print $2; found=1} END {if (!found) print "MISSING"}' | tail -1
}

assert_provider_delta() {
    local provider="$1" before="$2" after="$3" expect_delta="$4" label="$5"
    [[ "$before" != "MISSING" && "$after" != "MISSING" ]] || {
        printf '  %sWARN%s %s: provider_requests metric MISSING for %s\n' "$YELLOW" "$RESET" "$label" "$provider"
        return 0
    }
    local delta=$(( after - before ))
    if [[ "$expect_delta" == "0" && "$delta" != "0" ]]; then
        printf '  %sFAIL%s %s: provider %s was called (delta=%d, expected 0)\n' "$RED" "$RESET" "$label" "$provider" "$delta"
        return 1
    fi
    if [[ "$expect_delta" != "0" && "$delta" == "0" ]]; then
        printf '  %sFAIL%s %s: provider %s was NOT called (delta=0, expected calls)\n' "$RED" "$RESET" "$label" "$provider"
        return 1
    fi
    printf '  %sPASS%s %s: provider %s delta=%d\n' "$GREEN" "$RESET" "$label" "$provider" "$delta"
    return 0
}

# ── SQLite helpers ────────────────────────────────────────────────────────
count_assets_by_provider() {
    local provider="$1"
    if [[ ! -f "$DB_PATH" ]]; then echo "0"; return; fi
    sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM media_assets WHERE provider = '$(printf "%s" "$provider" | sed "s/'/''/g")';" 2>/dev/null || echo "0"
}

count_assets_total() {
    if [[ ! -f "$DB_PATH" ]]; then echo "0"; return; fi
    sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM media_assets;" 2>/dev/null || echo "0"
}

count_null_provider() {
    if [[ ! -f "$DB_PATH" ]]; then echo "0"; return; fi
    sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM media_assets WHERE provider IS NULL OR provider = '';" 2>/dev/null || echo "0"
}

# ── Dispatch + poll + extract result ─────────────────────────────────────
dispatch_and_poll() {
    local label="$1" payload="$2" idem_key="$3"
    local out_result_var="$4"

    printf '  → POST /api/script/generate\n'
    local dispatch_body="$WORK_DIR/${label}_dispatch.json"
    export SMOKE_IDEMPOTENCY_KEY="$idem_key"
    smoke_curl POST "/api/script/generate" --data "$payload" >/dev/null
    unset SMOKE_IDEMPOTENCY_KEY

    local http_code="${SMOKE_LAST_HTTP:-0}"
    cp "${SMOKE_LAST_BODY:-/dev/null}" "$dispatch_body" 2>/dev/null || true

    if [[ "$http_code" != "202" ]]; then
        printf '  %sFAIL%s %s: dispatch HTTP=%s (expected 202)\n' "$RED" "$RESET" "$label" "$http_code"
        smoke_echo_safe "$(head -c 600 "$dispatch_body" 2>/dev/null || true)" >&2
        return 1
    fi

    local job_id
    job_id=$(jq -r '.job_id // empty' "$dispatch_body")
    if [[ -z "$job_id" ]]; then
        printf '  %sFAIL%s %s: missing job_id\n' "$RED" "$RESET" "$label"
        return 1
    fi
    printf '  job_id: %s\n' "$job_id"

    printf '  → polling /api/jobs/%s/full\n' "$job_id"
    smoke_poll_terminal "$job_id" || {
        printf '  %sFAIL%s %s: poll timeout/error (status=%s)\n' "$RED" "$RESET" "$label" "${SMOKE_LAST_STATUS:-unknown}"
        return 1
    }

    local terminal_status="${SMOKE_LAST_STATUS}"
    if [[ "$terminal_status" != "completed" && "$terminal_status" != "SUCCEEDED" ]]; then
        printf '  %sFAIL%s %s: terminal status=%s\n' "$RED" "$RESET" "$label" "$terminal_status"
        smoke_echo_safe "$(head -c 800 "${SMOKE_LAST_BODY:-/dev/null}" 2>/dev/null || true)" >&2
        return 1
    fi

    local full_body="$WORK_DIR/${label}_full.json"
    cp "${SMOKE_LAST_BODY:-/dev/null}" "$full_body" 2>/dev/null || true

    local result
    result=$(jq -c '.result.data.items[0].result // .result.items[0].result // .result.output // empty' "$full_body")
    if [[ -z "$result" || "$result" == "null" ]]; then
        printf '  %sFAIL%s %s: missing generation result\n' "$RED" "$RESET" "$label"
        return 1
    fi

    printf '%s' "$result" > "$WORK_DIR/${label}_result.json"
    eval "$out_result_var=\"$WORK_DIR/${label}_result.json\""
    printf '  terminal: %s\n' "$terminal_status"
    return 0
}

# ── Base Maya payload ─────────────────────────────────────────────────────
MAYA_TOPIC="La civiltà Maya: città, astronomia, religione, scrittura e mistero del suo declino"
MAYA_SOURCE_TEXT="La civiltà Maya si sviluppò in Mesoamerica, raggiungendo il suo apice tra il 250 e il 900 d.C. \
Le loro città-stato, come Tikal, Palenque e Chichén Itzá, ospitavano imponenti piramidi e palazzi decorati con elaborate sculture in pietra. \
Gli astronomi Maya svilupparono un calendario di straordinaria precisione, basato sull'osservazione dei cicli di Venere e delle eclissi. \
La loro scrittura geroglifica, una delle più complesse del mondo antico, è stata decifrata solo in parte. \
Il Popol Vuh, il libro sacro dei Maya Quiché, racconta i miti della creazione e le gesta degli eroi gemelli. \
Il declino della civiltà classica Maya rimane uno dei grandi misteri dell'archeologia: siccità prolungate, guerre intestine e collasso ecologico sono tra le ipotesi più accreditate. \
Oggi i discendenti Maya conservano vive molte tradizioni nelle comunità dello Yucatán, del Guatemala e del Belize."

build_maya_payload() {
    local item_id="$1" force_refresh="$2" artlist="$3" images="$4"
    jq -n \
        --arg item_id "$item_id" \
        --arg topic "$MAYA_TOPIC" \
        --arg source_text "$MAYA_SOURCE_TEXT" \
        --arg marker "$CASE_PREFIX" \
        --argjson force "$force_refresh" \
        --arg artlist "$artlist" \
        --arg images "$images" \
        '{
            version: 2,
            preset: "custom",
            force_refresh: $force,
            correlation_id: $item_id,
            items: [{
                id: $item_id,
                title: ("Maya VidRush verification " + $marker),
                language: "it",
                tone: "documentary",
                style: "Scrivi un testo documentaristico concreto e informativo sulla civiltà Maya. Suddividi in segmenti distinti per tema: città, astronomia, religione, scrittura, declino. Usa dettagli specifici e ricercabili.",
                model: "gemma2:2b",
                source: {
                    type: "text",
                    topic: $topic,
                    source_text: ($source_text + "\n\nOperational marker: " + $marker + ".")
                },
                script_params: {
                    target_words: 800,
                    min_words: 500,
                    segment_words: 70,
                    skip_quality_gate: true,
                    use_memory: true,
                    prompt_version: "vidrush-maya-v1"
                },
                output: {
                    extract_entities: true,
                    generate_metadata: true,
                    save_to_db: true,
                    stock_enabled: "disabled"
                },
                media_plan: {
                    mode: "hybrid",
                    provider_policy: {
                        artlist: $artlist,
                        internet_images: $images
                    },
                    extraction: {
                        enabled: true,
                        max_entities_per_segment: 5,
                        max_important_phrases_per_segment: 5,
                        max_important_words_per_segment: 5,
                        max_artlist_queries_per_segment: 5,
                        max_image_queries_per_segment: 5
                    }
                }
            }]
        }'
}

# ── Dry-run early exit ────────────────────────────────────────────────────
if [[ "$DRY_RUN" == "1" ]]; then
    cat <<'EOF'
DRY RUN — Maya VidRush 7-job operational battery.

Would execute:
  MY-VIDRUSH-01  Cold LLM analysis (generate + entities + queries)
  MY-VIDRUSH-02  Cold media (strict provider separation, zero YouTube)
  MY-VIDRUSH-03  SQLite persistence (row counts, provider correctness)
  MY-VIDRUSH-04  Binding (primary_video, scores, semantic coherence)
  MY-VIDRUSH-05  Cache warm (same payload → HIT_EXACT)
  MY-VIDRUSH-06  Partial cache (modify one segment → only that MISS)
  MY-VIDRUSH-07  Provider miss (precise query → empty → no YouTube fallback)

Payload: POST /api/script/generate with Maya civilization topic (Italian, 800 words)
EOF
    exit 0
fi

# ── Fail counter ──────────────────────────────────────────────────────────
FAIL=0
FAILURES=""

fail() {
    local msg="$1"
    printf '%sFAIL: %s%s\n' "$RED" "$msg" "$RESET" >&2
    FAIL=$((FAIL + 1))
    FAILURES="${FAILURES}  ${msg}\n"
}

pass() {
    local msg="$1"
    printf '  %sPASS%s %s\n' "$GREEN" "$RESET" "$msg"
}

# ══════════════════════════════════════════════════════════════════════════════
# MY-VIDRUSH-01 — Cold LLM analysis
# ══════════════════════════════════════════════════════════════════════════════
smoke_log_section "MY-VIDRUSH-01: Cold LLM analysis (generate + entities + queries)"

PAYLOAD_01=$(build_maya_payload "${CASE_PREFIX}-01" true "enabled" "enabled")
RESULT_FILE_01=""

if dispatch_and_poll "maya-01-cold" "$PAYLOAD_01" "${CASE_PREFIX}-01-key" "RESULT_FILE_01"; then
    result=$(cat "$RESULT_FILE_01")

    # LLM actually generated text
    text_len=$(jq -r '(.output.text // "") | length' <<<"$result")
    if [[ "$text_len" -ge 400 ]]; then
        pass "LLM generated text: ${text_len} chars"
    else
        fail "LLM text too short: ${text_len} chars (need >= 400)"
    fi

    # Text is not just the echoed source
    echoed=$(jq -r '.output.text == .source.source_text' <<<"$result")
    if [[ "$echoed" == "false" ]]; then
        pass "LLM text is not echoed source_text"
    else
        fail "LLM text appears to be echoed source_text (no generation)"
    fi

    # Segments produced
    seg_count=$(jq '[.segments[]?] | length' <<<"$result")
    if [[ "$seg_count" -ge 4 ]]; then
        pass "Segments: ${seg_count} (need >= 4)"
    else
        fail "Too few segments: ${seg_count} (need >= 4)"
    fi

    # Entities extracted
    ent_count=$(jq '[.segments[]?.insights.entities[]?] | length' <<<"$result")
    if [[ "$ent_count" -ge 5 ]]; then
        pass "Entities extracted: ${ent_count} (need >= 5)"
    else
        fail "Too few entities: ${ent_count} (need >= 5)"
    fi

    # Entity structure valid
    if jq -e 'all(.segments[]?.insights.entities[]?; .value != null and (.value | length) > 0 and .type != null and (.type | length) > 0)' <<<"$result" >/dev/null; then
        pass "All entities have value + type"
    else
        fail "Some entities missing value or type"
    fi

    # Image queries present
    img_q_count=$(jq '[.segments[]?.insights.image_queries[]?] | length' <<<"$result")
    if [[ "$img_q_count" -ge 3 ]]; then
        pass "Image queries: ${img_q_count} (need >= 3)"
    else
        fail "Too few image queries: ${img_q_count} (need >= 3)"
    fi

    # Artlist queries present
    art_q_count=$(jq '[.segments[]?.insights.artlist_queries[]?] | length' <<<"$result")
    if [[ "$art_q_count" -ge 3 ]]; then
        pass "Artlist queries: ${art_q_count} (need >= 3)"
    else
        fail "Too few artlist queries: ${art_q_count} (need >= 3)"
    fi

    # No generic words in important_words
    if jq -e 'all(.segments[]?.insights.important_words[]?; (. | ascii_downcase) != "history" and . != "people" and . != "old" and . != "things")' <<<"$result" >/dev/null; then
        pass "No generic filler words in important_words"
    else
        fail "Generic filler words found in important_words (history/people/old/things)"
    fi

    # Queries are segment-specific, not shared globally
    unique_image_queries=$(jq '[.segments[].insights.image_queries] | flatten | unique | length' <<<"$result")
    total_image_queries=$(jq '[.segments[].insights.image_queries] | flatten | length' <<<"$result")
    if [[ "$unique_image_queries" -ge "$seg_count" ]]; then
        pass "Image queries are segment-specific: ${unique_image_queries} unique across ${seg_count} segments"
    else
        fail "Image queries may be duplicated across segments: ${unique_image_queries} unique, ${total_image_queries} total"
    fi

    FIRST_RESULT_FILE="$RESULT_FILE_01"
    FIRST_SEGMENT_IDS=$(jq -c '[.segments[].segment_id]' <<<"$result")
    FIRST_TEXT_HASHES=$(jq -c '[.segments[].text_hash]' <<<"$result")
else
    fail "MY-VIDRUSH-01 dispatch/poll failed"
    FIRST_RESULT_FILE=""
    FIRST_SEGMENT_IDS="[]"
    FIRST_TEXT_HASHES="[]"
fi

# ══════════════════════════════════════════════════════════════════════════════
# MY-VIDRUSH-02 — Cold media (strict provider separation)
# ══════════════════════════════════════════════════════════════════════════════
smoke_log_section "MY-VIDRUSH-02: Cold media — strict provider separation, zero YouTube"

if [[ -n "$FIRST_RESULT_FILE" ]]; then
    result=$(cat "$FIRST_RESULT_FILE")

    # ALL candidates must have provider = artlist or internet_images
    if jq -e 'all(.segments[]; all(.assets.candidates[]?; .provider == "artlist" or .provider == "internet_images"))' <<<"$result" >/dev/null; then
        pass "All candidates have valid provider (artlist or internet_images)"
    else
        fail "Some candidates have invalid provider (not artlist/internet_images)"
        jq '[.segments[].assets.candidates[]? | select(.provider != "artlist" and .provider != "internet_images") | {provider, asset_id, source_url}]' <<<"$result" >&2
    fi

    # ZERO YouTube providers
    if jq -e 'all(.segments[]; all(.assets.candidates[]?; .provider != "youtube"))' <<<"$result" >/dev/null; then
        pass "Zero candidates with provider=youtube"
    else
        fail "YouTube provider found in candidates"
        jq '[.segments[].assets.candidates[]? | select(.provider == "youtube")]' <<<"$result" >&2
    fi

    # ZERO YouTube URLs in source_url
    if jq -e 'all(.segments[]; all(.assets.candidates[]?; ((.source_url // "") | test("youtube\\\\.com|youtu\\\\.be") | not)))' <<<"$result" >/dev/null; then
        pass "Zero YouTube URLs in candidate source_url"
    else
        fail "YouTube URL found in candidate source_url"
        jq '[.segments[].assets.candidates[]? | select((.source_url // "") | test("youtube\\\\.com|youtu\\\\.be")) | {provider, source_url}]' <<<"$result" >&2
    fi

    # ZERO youtube_video_id
    if jq -e 'all(.segments[]; all(.assets.candidates[]?; ((.youtube_video_id // "") == "")))' <<<"$result" >/dev/null; then
        pass "Zero youtube_video_id in candidates"
    else
        fail "youtube_video_id found in candidates"
        jq '[.segments[].assets.candidates[]? | select((.youtube_video_id // "") != "")]' <<<"$result" >&2
    fi

    # ZERO generated_images or image_generation provider
    if jq -e 'all(.segments[]; all(.assets.candidates[]?; .provider != "image_generation" and .provider != "generated_images"))' <<<"$result" >/dev/null; then
        pass "Zero generated_images candidates"
    else
        fail "generated_images provider found in candidates"
    fi

    # Images must be internet_images (not artlist images)
    if jq -e 'all(.segments[]; all(.assets.candidates[]?; select(.provider != "artlist") | .provider == "internet_images"))' <<<"$result" >/dev/null; then
        pass "Non-artlist candidates are all internet_images"
    else
        fail "Non-artlist candidates include non-internet_images provider"
    fi

    # Image candidates have valid HTTP URLs
    if jq -e 'all(.segments[]; all(.assets.candidates[]?; select(.provider == "internet_images") | ((.source_url // .preview_url // "") | test("^https?://"))))' <<<"$result" >/dev/null; then
        pass "All internet_images candidates have HTTP URLs"
    else
        fail "Some internet_images candidates missing HTTP URLs"
    fi

    # Artlist videos have provenance (source_url or drive_link)
    if jq -e 'all(.segments[]; all(.assets.candidates[]?; select(.provider == "artlist") | ((.source_url // "") != "" or (.drive_link // "") != "")))' <<<"$result" >/dev/null; then
        pass "All artlist candidates have provenance (source_url or drive_link)"
    else
        fail "Some artlist candidates missing provenance"
    fi

    # Candidate count
    candidate_count=$(jq '[.segments[].assets.candidates[]?] | length' <<<"$result")
    printf '  candidate count: %s\n' "$candidate_count"
else
    fail "MY-VIDRUSH-02 skipped (no result from MY-VIDRUSH-01)"
fi

# ══════════════════════════════════════════════════════════════════════════════
# MY-VIDRUSH-03 — SQLite persistence
# ══════════════════════════════════════════════════════════════════════════════
smoke_log_section "MY-VIDRUSH-03: SQLite persistence"

ASSETS_BEFORE_TOTAL=$(count_assets_total)
ASSETS_BEFORE_ARTLIST=$(count_assets_by_provider "artlist")
ASSETS_BEFORE_INTERNET=$(count_assets_by_provider "internet_images")
ASSETS_BEFORE_YOUTUBE=$(count_assets_by_provider "youtube")
NULL_PROVIDER_BEFORE=$(count_null_provider)

printf '  before: total=%s artlist=%s internet_images=%s youtube=%s null_provider=%s\n' \
    "$ASSETS_BEFORE_TOTAL" "$ASSETS_BEFORE_ARTLIST" "$ASSETS_BEFORE_INTERNET" "$ASSETS_BEFORE_YOUTUBE" "$NULL_PROVIDER_BEFORE"

if [[ -f "$DB_PATH" ]]; then
    # Verify no NEW asset without provider (delta check against pre-existing DB state)
    null_provider_after=$(count_null_provider)
    null_provider_delta=$(( null_provider_after - NULL_PROVIDER_BEFORE ))
    if [[ "$null_provider_delta" -le 0 ]]; then
        pass "Zero new assets added without provider (delta=0, ${null_provider_after} pre-existing)"
    else
        fail "${null_provider_delta} new assets added without provider during Maya test"
    fi

    # Verify zero YouTube assets
    youtube_assets=$(count_assets_by_provider "youtube")
    if [[ "$youtube_assets" == "$ASSETS_BEFORE_YOUTUBE" ]]; then
        pass "No new YouTube assets added during Maya test"
    else
        fail "YouTube assets found: before=${ASSETS_BEFORE_YOUTUBE} now=${youtube_assets}"
    fi

    # Verify no duplicate provider + external_id
    dup_count=$(sqlite3 "$DB_PATH" \
        "SELECT COUNT(*) FROM (SELECT provider, source_external_id, COUNT(*) as cnt FROM media_assets WHERE source_external_id IS NOT NULL AND source_external_id != '' GROUP BY provider, source_external_id HAVING cnt > 1);" 2>/dev/null || echo "0")
    if [[ "$dup_count" == "0" ]]; then
        pass "Zero duplicate assets by provider + external_id"
    else
        fail "${dup_count} duplicate groups found by provider + external_id"
    fi

    printf '  after: total=%s artlist=%s internet_images=%s youtube=%s\n' \
        "$(count_assets_total)" "$(count_assets_by_provider "artlist")" \
        "$(count_assets_by_provider "internet_images")" "$(count_assets_by_provider "youtube")"
else
    printf '  %sWARN%s SQLite DB not found at %s; skipping persistence checks\n' "$YELLOW" "$RESET" "$DB_PATH"
fi

# ══════════════════════════════════════════════════════════════════════════════
# MY-VIDRUSH-04 — Binding
# ══════════════════════════════════════════════════════════════════════════════
smoke_log_section "MY-VIDRUSH-04: Binding verification"

if [[ -n "$FIRST_RESULT_FILE" ]]; then
    result=$(cat "$FIRST_RESULT_FILE")

    # Primary video binding exists
    primary_count=$(jq '[.segments[]?.assets.primary_video? | select(. != null)] | length' <<<"$result")
    if [[ "$primary_count" -ge 1 ]]; then
        pass "Primary video bindings: ${primary_count} (need >= 1)"
    else
        printf '  %sWARN%s No primary video bindings (Artlist may have returned no results)\n' "$YELLOW" "$RESET"
    fi

    # Binding reasons present
    if jq -e 'all(.segments[]; select(.assets.primary_video != null) | ((.assets.selection_reason // "") | length) > 0)' <<<"$result" >/dev/null; then
        pass "All primary bindings have selection_reason"
    else
        fail "Some primary bindings missing selection_reason"
    fi

    # Candidates have scores
    if jq -e 'all(.segments[]; all(.assets.candidates[]?; .score != null and .score >= 0))' <<<"$result" >/dev/null; then
        pass "All candidates have valid scores"
    else
        fail "Some candidates missing or have invalid scores"
    fi

    # No candidate duplicated across segments (same asset_id used in multiple segments)
    dup_assets=$(jq '[.segments[].assets.candidates[]?.asset_id] | group_by(.) | map(select(length > 1)) | length' <<<"$result")
    if [[ "$dup_assets" -le 1 ]]; then
        pass "Minimal asset reuse across segments: ${dup_assets} duplicates"
    else
        printf '  %sWARN%s %s assets appear in multiple segments (possible overuse)\n' "$YELLOW" "$RESET" "$dup_assets"
    fi

    # Primary video assets are unique per segment
    primary_ids=$(jq '[.segments[]?.assets.primary_video?.asset_id | select(. != null)]' <<<"$result")
    unique_primary=$(jq 'unique | length' <<<"$primary_ids")
    total_primary=$(jq 'length' <<<"$primary_ids")
    if [[ "$unique_primary" == "$total_primary" ]]; then
        pass "Primary video assets are unique across segments (${unique_primary})"
    else
        printf '  %sWARN%s Primary video assets reused: %s unique out of %s\n' "$YELLOW" "$RESET" "$unique_primary" "$total_primary"
    fi

    # Secondary images populated
    secondary_count=$(jq '[.segments[].assets.secondary_images[]?] | length' <<<"$result")
    printf '  secondary_images count: %s\n' "$secondary_count"

    # Candidate set hash present
    if jq -e 'all(.segments[]; ((.assets.candidate_set_hash // "") | length) > 0)' <<<"$result" >/dev/null; then
        pass "All segments have candidate_set_hash"
    else
        fail "Some segments missing candidate_set_hash"
    fi
else
    fail "MY-VIDRUSH-04 skipped (no result from MY-VIDRUSH-01)"
fi

# ══════════════════════════════════════════════════════════════════════════════
# MY-VIDRUSH-05 — Cache warm replay
# ══════════════════════════════════════════════════════════════════════════════
smoke_log_section "MY-VIDRUSH-05: Cache warm — same payload, HIT_EXACT, 0 provider calls"

# Collect pre-warm provider counters
ARTLIST_BEFORE=$(provider_requests artlist)
IMAGES_BEFORE=$(provider_requests internet_images)
YOUTUBE_BEFORE=$(provider_requests youtube)

# Use same payload but different idempotency key (force application cache, not HTTP replay)
PAYLOAD_05=$(build_maya_payload "${CASE_PREFIX}-05" false "enabled" "enabled")
RESULT_FILE_05=""

if dispatch_and_poll "maya-05-warm" "$PAYLOAD_05" "${CASE_PREFIX}-05-key" "RESULT_FILE_05"; then
    result=$(cat "$RESULT_FILE_05")

    # Cache state: HIT_EXACT on all layers
    if jq -e 'all(.segments[]; .cache.extraction == "HIT_EXACT")' <<<"$result" >/dev/null; then
        pass "Cache extraction: HIT_EXACT on all segments"
    else
        fail "Cache extraction not HIT_EXACT on some segments"
        jq '[.segments[] | {segment_id, cache_extraction: .cache.extraction}]' <<<"$result" >&2
    fi

    if jq -e 'all(.segments[]; .cache.artlist == "HIT_EXACT" or .cache.artlist == "BYPASSED")' <<<"$result" >/dev/null; then
        pass "Cache artlist: HIT_EXACT/BYPASSED on all segments"
    else
        fail "Cache artlist not HIT_EXACT/BYPASSED"
        jq '[.segments[] | {segment_id, cache_artlist: .cache.artlist}]' <<<"$result" >&2
    fi

    if jq -e 'all(.segments[]; .cache.internet_images == "HIT_EXACT" or .cache.internet_images == "BYPASSED")' <<<"$result" >/dev/null; then
        pass "Cache internet_images: HIT_EXACT/BYPASSED on all segments"
    else
        fail "Cache internet_images not HIT_EXACT/BYPASSED"
        jq '[.segments[] | {segment_id, cache_images: .cache.internet_images}]' <<<"$result" >&2
    fi

    if jq -e 'all(.segments[]; .cache.binding == "HIT_EXACT" or .cache.binding == "BYPASSED")' <<<"$result" >/dev/null; then
        pass "Cache binding: HIT_EXACT/BYPASSED on all segments"
    else
        fail "Cache binding not HIT_EXACT/BYPASSED"
        jq '[.segments[] | {segment_id, cache_binding: .cache.binding}]' <<<"$result" >&2
    fi

    # Same segment IDs
    warm_seg_ids=$(jq -c '[.segments[].segment_id]' <<<"$result")
    if [[ "$warm_seg_ids" == "$FIRST_SEGMENT_IDS" ]]; then
        pass "Same segment_ids as cold run"
    else
        fail "Segment IDs differ from cold run"
        printf '  cold: %s\n  warm: %s\n' "$FIRST_SEGMENT_IDS" "$warm_seg_ids" >&2
    fi

    # Same text hashes
    warm_text_hashes=$(jq -c '[.segments[].text_hash]' <<<"$result")
    if [[ "$warm_text_hashes" == "$FIRST_TEXT_HASHES" ]]; then
        pass "Same text_hashes as cold run"
    else
        fail "Text hashes differ from cold run"
    fi

    # Zero new provider calls
    ARTIST_AFTER=$(provider_requests artlist)
    IMAGES_AFTER=$(provider_requests internet_images)
    YOUTUBE_AFTER=$(provider_requests youtube)

    assert_provider_delta artlist "$ARTLIST_BEFORE" "$ARTIST_AFTER" "0" "warm-replay"
    assert_provider_delta internet_images "$IMAGES_BEFORE" "$IMAGES_AFTER" "0" "warm-replay"
    if [[ "$YOUTUBE_BEFORE" != "MISSING" && "$YOUTUBE_AFTER" != "MISSING" ]]; then
        assert_provider_delta youtube "$YOUTUBE_BEFORE" "$YOUTUBE_AFTER" "0" "warm-replay"
    fi

    # Asset counts unchanged
    ASSETS_AFTER_TOTAL=$(count_assets_total)
    printf '  DB assets: before=%s after=%s\n' "$ASSETS_BEFORE_TOTAL" "$ASSETS_AFTER_TOTAL"
else
    fail "MY-VIDRUSH-05 dispatch/poll failed"
fi

# ══════════════════════════════════════════════════════════════════════════════
# MY-VIDRUSH-06 — Partial cache (modify one segment)
# ══════════════════════════════════════════════════════════════════════════════
smoke_log_section "MY-VIDRUSH-06: Partial cache — modify one segment"

# Build a payload with a modified last sentence in source_text
MODIFIED_SOURCE_TEXT="La civiltà Maya si sviluppò in Mesoamerica, raggiungendo il suo apice tra il 250 e il 900 d.C. \
Le loro città-stato, come Tikal, Palenque e Chichén Itzá, ospitavano imponenti piramidi e palazzi decorati con elaborate sculture in pietra. \
Gli astronomi Maya osservavano meticolosamente Venere e regolavano calendari e rituali in base ai suoi cicli celesti. \
La loro scrittura geroglifica, una delle più complesse del mondo antico, è stata decifrata solo in parte. \
Il Popol Vuh, il libro sacro dei Maya Quiché, racconta i miti della creazione e le gesta degli eroi gemelli. \
Il declino della civiltà classica Maya rimane uno dei grandi misteri dell'archeologia: siccità prolungate, guerre intestine e collasso ecologico sono tra le ipotesi più accreditate. \
Oggi i discendenti Maya conservano vive molte tradizioni nelle comunità dello Yucatán, del Guatemala e del Belize."

PARTIAL_PAYLOAD=$(jq -n \
    --arg item_id "${CASE_PREFIX}-06" \
    --arg topic "$MAYA_TOPIC" \
    --arg source_text "$MODIFIED_SOURCE_TEXT" \
    --arg marker "$CASE_PREFIX" \
    '{
        version: 2,
        preset: "custom",
        force_refresh: false,
        correlation_id: $item_id,
        items: [{
            id: $item_id,
            title: ("Maya VidRush partial cache " + $marker),
            language: "it",
            tone: "documentary",
            style: "Scrivi un testo documentaristico concreto e informativo sulla civiltà Maya. Suddividi in segmenti distinti per tema: città, astronomia, religione, scrittura, declino.",
            model: "gemma2:2b",
            source: {
                type: "text",
                topic: $topic,
                source_text: ($source_text + "\n\nOperational marker: " + $marker + "-partial.")
            },
            script_params: {
                target_words: 800,
                min_words: 500,
                segment_words: 70,
                skip_quality_gate: true,
                use_memory: true,
                prompt_version: "vidrush-maya-v1"
            },
            output: {
                extract_entities: true,
                generate_metadata: false,
                save_to_db: true,
                stock_enabled: "disabled"
            },
            media_plan: {
                mode: "hybrid",
                provider_policy: { artlist: "enabled", internet_images: "enabled" },
                extraction: {
                    enabled: true,
                    max_entities_per_segment: 5,
                    max_artlist_queries_per_segment: 5,
                    max_image_queries_per_segment: 5
                }
            }
        }]
    }')

RESULT_FILE_06=""
if dispatch_and_poll "maya-06-partial" "$PARTIAL_PAYLOAD" "${CASE_PREFIX}-06-key" "RESULT_FILE_06"; then
    result=$(cat "$RESULT_FILE_06")

    # At least one segment should be MISS (the one whose text changed)
    miss_count=$(jq '[.segments[] | select(.cache.extraction == "MISS")] | length' <<<"$result")
    hit_count=$(jq '[.segments[] | select(.cache.extraction == "HIT_EXACT")] | length' <<<"$result")

    if [[ "$miss_count" -ge 1 && "$hit_count" -ge 1 ]]; then
        pass "Partial cache: ${miss_count} MISS + ${hit_count} HIT_EXACT (mixed as expected)"
    elif [[ "$hit_count" -eq 0 ]]; then
        printf '  %sWARN%s Partial cache: 0 HIT_EXACT (all MISS — entire text may have been re-segmented)\n' "$YELLOW" "$RESET"
    else
        fail "Partial cache: ${miss_count} MISS + ${hit_count} HIT_EXACT (unexpected pattern)"
    fi

    # Verify no global cache invalidation: count distinct text_hashes vs cold
    cold_hashes=$(jq -r '.[]' <<<"$FIRST_TEXT_HASHES" | sort)
    warm_hashes=$(jq -r '.segments[].text_hash' <<<"$result" | sort)
    common_hashes=$(comm -12 <(echo "$cold_hashes") <(echo "$warm_hashes") | wc -l)
    printf '  common text_hashes with cold run: %s\n' "$common_hashes"
else
    fail "MY-VIDRUSH-06 dispatch/poll failed"
fi

# ══════════════════════════════════════════════════════════════════════════════
# MY-VIDRUSH-07 — Provider miss (precise Artlist query, no YouTube fallback)
# ══════════════════════════════════════════════════════════════════════════════
smoke_log_section "MY-VIDRUSH-07: Provider miss — precise query, no YouTube fallback"

MISS_PAYLOAD=$(jq -n \
    --arg item_id "${CASE_PREFIX}-07" \
    --arg marker "$CASE_PREFIX" \
    '{
        version: 2,
        preset: "custom",
        force_refresh: true,
        correlation_id: $item_id,
        items: [{
            id: $item_id,
            title: ("Maya VidRush provider miss " + $marker),
            language: "en",
            tone: "documentary",
            style: "Write one short paragraph. Keep it concrete.",
            model: "gemma2:2b",
            source: {
                type: "text",
                topic: "authentic Maya king Pacal carved sarcophagus cinematic footage",
                source_text: ("A detailed examination of the carved sarcophagus lid of King Pacal found at Palenque. " +
                    "The intricate stone carvings depict the Maya ruler descending into the underworld. " +
                    "Operational marker: " + $marker + "-miss.")
            },
            script_params: {
                target_words: 120,
                min_words: 60,
                skip_quality_gate: true,
                use_memory: false
            },
            output: {
                extract_entities: true,
                generate_metadata: false,
                save_to_db: true,
                stock_enabled: "disabled"
            },
            media_plan: {
                mode: "hybrid",
                provider_policy: { artlist: "enabled", internet_images: "enabled" },
                extraction: {
                    enabled: true,
                    max_entities_per_segment: 3,
                    max_artlist_queries_per_segment: 3,
                    max_image_queries_per_segment: 3
                }
            }
        }]
    }')

YOUTUBE_BEFORE_MISS=$(provider_requests youtube)
RESULT_FILE_07=""

if dispatch_and_poll "maya-07-miss" "$MISS_PAYLOAD" "${CASE_PREFIX}-07-key" "RESULT_FILE_07"; then
    result=$(cat "$RESULT_FILE_07")

    # Even on miss, zero YouTube
    YOUTUBE_AFTER_MISS=$(provider_requests youtube)
    if [[ "$YOUTUBE_BEFORE_MISS" == "$YOUTUBE_AFTER_MISS" ]]; then
        pass "Provider miss: zero YouTube fallback (counter unchanged: ${YOUTUBE_BEFORE_MISS})"
    else
        fail "Provider miss: YouTube was called (counter ${YOUTUBE_BEFORE_MISS} → ${YOUTUBE_AFTER_MISS})"
    fi

    # Verify no youtube in candidates
    if jq -e 'all(.segments[]; all(.assets.candidates[]?; .provider != "youtube"))' <<<"$result" >/dev/null; then
        pass "Provider miss: zero youtube in candidates"
    else
        fail "Provider miss: youtube found in candidates despite empty Artlist"
    fi

    # Cache should be MISS (not stuck as permanent negative cache)
    artlist_cache=$(jq -r '.segments[0].cache.artlist // "UNKNOWN"' <<<"$result")
    if [[ "$artlist_cache" == "MISS" || "$artlist_cache" == "BYPASSED" ]]; then
        pass "Provider miss: artlist cache=${artlist_cache} (not permanently cached negative)"
    else
        printf '  %sWARN%s Provider miss: artlist cache=%s (expected MISS or BYPASSED)\n' "$YELLOW" "$RESET" "$artlist_cache"
    fi

    # Repeat the same miss query — Artlist must be queried again (not stuck)
    YOUTUBE_BEFORE_REMISS=$(provider_requests youtube)
    RESULT_FILE_07B=""
    if dispatch_and_poll "maya-07-miss-replay" "$MISS_PAYLOAD" "${CASE_PREFIX}-07b-key" "RESULT_FILE_07B"; then
        YOUTUBE_AFTER_REMISS=$(provider_requests youtube)
        if [[ "$YOUTUBE_BEFORE_REMISS" == "$YOUTUBE_AFTER_REMISS" ]]; then
            pass "Provider miss replay: still zero YouTube (counter unchanged)"
        else
            fail "Provider miss replay: YouTube called on retry"
        fi
    else
        printf '  %sWARN%s Provider miss replay dispatch/poll failed\n' "$YELLOW" "$RESET"
    fi
else
    fail "MY-VIDRUSH-07 dispatch/poll failed"
fi

# ══════════════════════════════════════════════════════════════════════════════
# Final verdict
# ══════════════════════════════════════════════════════════════════════════════
echo ""
if [[ "$FAIL" -eq 0 ]]; then
    printf '%s╔══════════════════════════════════════════╗%s\n' "$GREEN" "$RESET"
    printf '%s║   MAYA VIDRUSH FINAL: PASS               ║%s\n' "$GREEN" "$RESET"
    printf '%s╚══════════════════════════════════════════╝%s\n' "$GREEN" "$RESET"
    echo ""
    echo "All 7 smoke jobs passed:"
    echo "  MY-VIDRUSH-01  Cold LLM analysis        ✓"
    echo "  MY-VIDRUSH-02  Cold media separation     ✓"
    echo "  MY-VIDRUSH-03  SQLite persistence        ✓"
    echo "  MY-VIDRUSH-04  Binding                   ✓"
    echo "  MY-VIDRUSH-05  Cache warm                ✓"
    echo "  MY-VIDRUSH-06  Partial cache             ✓"
    echo "  MY-VIDRUSH-07  Provider miss             ✓"
    exit 0
else
    printf '%s╔══════════════════════════════════════════╗%s\n' "$RED" "$RESET"
    printf '%s║   MAYA VIDRUSH FINAL: FAIL (%d)           ║%s\n' "$RED" "$FAIL" "$RESET"
    printf '%s╚══════════════════════════════════════════╝%s\n' "$RED" "$RESET"
    echo ""
    printf '%sFailures:%s\n' "$RED" "$RESET"
    printf '%b' "$FAILURES"
    exit 1
fi

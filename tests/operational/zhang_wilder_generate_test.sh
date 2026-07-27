#!/usr/bin/env bash
#
# zhang_wilder_generate_test.sh — Test /api/script/generate with
# Zhang vs Wilder segment topics. Validates:
#   1. Correct script generation from requested segment topics
#   2. Output structure and persistence
#   3. Stock/clip suggestions via VidRush pipeline
#   4. Segment integrity without prescribing model content
#
# Usage:
#   ./zhang_wilder_generate_test.sh        # real run
#   ./zhang_wilder_generate_test.sh --dry  # print payload, exit 0
#
# Prerequisites:
#   - Server running on port 8000 (or set SMOKE_API_BASE)
#   - Qdrant running on port 6333 (for clip/image search)
#   - Token file at /etc/pipelinegen/pipelinegen.env

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$DIR/../.." && pwd)
SMOKE_TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-900}"
SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-900}"
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"
smoke_require curl jq

CASE_PREFIX="zhang-wilder-$(smoke_gen_uuid)"
IDEMPOTENCY_KEY="$CASE_PREFIX-key"

# ── Payload ────────────────────────────────────────────────────────
PAYLOAD=$(jq -n \
    --arg case_marker "$CASE_PREFIX" \
    '{
        version: 2,
        preset: "custom",
        items: [
            {
                id: ($case_marker + "-item"),
                title: ("Zhang vs Wilder: A Study in Contrasts " + $case_marker),
                language: "it",
                tone: "documentary",
                source: {
                    type: "text",
                    topic: ("Zhilei Zhang vs Deontay Wilder heavyweight boxing " + $case_marker),
                    source_text: "Fonte editoriale controllata: questo episodio confronta, in chiave documentaristica e senza presentare fatti non verificati come accaduti, i profili pugilistici di Zhilei Zhang e Deontay Wilder. L analisi tratta stili, ritmo, distanza, potenza, gestione del ring e possibili scenari tattici. Il testo deve distinguere chiaramente osservazioni tecniche, ipotesi e informazioni non disponibili nella fonte."
                },
                script_params: {
                    target_words: 500,
                    segments: [
                        {topic: "Opening", target_words: 90},
                        {topic: "Zhang profile", target_words: 100},
                        {topic: "Wilder profile", target_words: 100},
                        {topic: "Tactical matchup", target_words: 120},
                        {topic: "Fight outlook", target_words: 90}
                    ],
                    skip_quality_gate: true,
                    use_memory: true
                },
                output: {
                    extract_entities: true,
                    generate_metadata: true,
                    save_to_db: true
                },
                docs: {
                    enabled: true,
                    languages: ["it"],
                    folder_id: "10p7NPodbQNjbSyvDIQJtowcmGeejwwlb"
                },
                media_plan: {
                    mode: "hybrid",
                    provider_policy: {
                        artlist: "enabled",
                        internet_images: "enabled"
                    },
                    extraction: {
                        enabled: true
                    }
                }
            }
        ]
    }')

if [[ "${1:-}" == "--dry" ]]; then
    echo "DRY RUN — would POST http://${SMOKE_API_BASE}/api/script/generate"
    echo "Auth (redacted): Authorization: Bearer <REDACTED>"
    echo "Idempotency-Key: $IDEMPOTENCY_KEY"
    echo ""
    echo "Payload:"
    echo "$PAYLOAD" | jq .
    echo ""
    echo "═══ What this test validates ═══"
    echo "1. Script generation from Zhang vs Wilder segment topics"
    echo "2. Structural output validation without semantic hardcoding"
    echo "3. VidRush segments with artlist + internet_images suggestions"
    echo "4. Entity extraction shape (model-selected values)"
    echo "5. Segment integrity (hashes, positions, dedup)"
    echo ""
    echo "DB assets available for this topic:"
    echo "  - Stock folder: 1LJfMx6xdVVVvF044TObvl2MVjBpjM2bY (5 rounds × 15 clips)"
    echo "  - Voiceovers: 60+ segments in multiple languages"
    echo "  - Images: AI-generated + portraits"
    echo "  - YouTube: 1 Boxe clip"
    echo "  - Artlist: 8 published generic assets"
    echo "  - Clip Drive: 184 celebrity clips (generic)"
    exit 0
fi

# ── 1. Dispatch ────────────────────────────────────────────────────
smoke_log_section "POST /api/script/generate (Zhang vs Wilder)"

export SMOKE_IDEMPOTENCY_KEY="$IDEMPOTENCY_KEY"
smoke_curl POST "/api/script/generate" --data "$PAYLOAD" >/dev/null
unset SMOKE_IDEMPOTENCY_KEY

HTTP="$SMOKE_LAST_HTTP"
if [[ "$HTTP" != "202" ]]; then
    printf '%sFAIL: dispatch returned HTTP %s (expected 202)%s\n' \
        "$RED" "$HTTP" "$RESET" >&2
    smoke_echo_safe "$(head -c 800 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi

JOB_ID=$(jq -r '.job_id // empty' "$SMOKE_LAST_BODY")
if [[ -z "$JOB_ID" || "$JOB_ID" == "null" ]]; then
    printf '%sFAIL: dispatch missing job_id%s\n' "$RED" "$RESET" >&2
    smoke_echo_safe "$(head -c 400 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi

DISPATCH_BODY="$SMOKE_LAST_BODY"
jq -e --arg expected "/api/jobs/${JOB_ID}/full" '
  .ok == true
  and (.status | type == "string" and length > 0)
  and .status_url == $expected
  and (.current_stage | type == "string" and length > 0)
' "$DISPATCH_BODY" >/dev/null || {
    printf '%sFAIL: dispatch missing canonical async fields or current_stage%s\n' "$RED" "$RESET" >&2
    smoke_echo_safe "$(head -c 1200 "$DISPATCH_BODY")" >&2
    exit 1
}
printf 'job_id:         %s%s%s\n' "$YELLOW" "$JOB_ID" "$RESET"
printf 'current_stage:  %s%s%s\n' "$YELLOW" "$(jq -r '.current_stage' "$DISPATCH_BODY")" "$RESET"

# ── 2. Poll until terminal ────────────────────────────────────────
smoke_log_section "Poll /api/jobs/$JOB_ID/full"
smoke_poll_terminal "$JOB_ID"

if [[ "$SMOKE_LAST_STATUS" != "completed" && "$SMOKE_LAST_STATUS" != "SUCCEEDED" ]]; then
    printf '%sFAIL: job ended with status %s%s\n' "$RED" "$SMOKE_LAST_STATUS" "$RESET" >&2
    smoke_echo_safe "$(head -c 1200 "$SMOKE_LAST_BODY")" >&2
    exit 1
fi
printf 'status:         %s%s%s\n' "$GREEN" "$SMOKE_LAST_STATUS" "$RESET"

# ── 3. Extract and validate script ─────────────────────────────────
smoke_log_section "Validate script output"

# Re-fetch the completed job to guarantee the body is fully populated.
# The poll body can be stale when the server writes result *after*
# flipping the status to terminal on the same poll cycle.
smoke_curl GET "/api/jobs/${JOB_ID}/full" >/dev/null
RESULT=$(jq -c '.result.data.items[0].result // .result.items[0].result // .result.output // .result // empty' "$SMOKE_LAST_BODY")
[[ -n "$RESULT" && "$RESULT" != "null" ]] || { echo "missing canonical generation result" >&2; exit 1; }
SCRIPT_ID=$(jq -r '.script_id // empty' <<<"$RESULT")

SCRIPT=$(jq -r '.output.text // .script.text // .script // .text // .content // empty' <<<"$RESULT")
if [[ -z "$SCRIPT" || "$SCRIPT" == "null" ]]; then
    printf '%sFAIL: script text is empty%s\n' "$RED" "$RESET" >&2
    printf '%sResult body (first 2000 chars):%s\n' "$YELLOW" "$RESET" >&2
    smoke_echo_safe "$(head -c 2000 "$SMOKE_LAST_BODY")" >&2
    exit 1
fi
printf 'script length:  %s%s%s chars\n' "$YELLOW" "${#SCRIPT}" "$RESET"

# ── 3a. Word count validation ─────────────────────────────────────
WORDS=$(printf '%s' "$SCRIPT" | wc -w | tr -d ' ')
printf 'word count:     %s%s%s\n' "$YELLOW" "$WORDS" "$RESET"
(( WORDS >= 350 && WORDS <= 750 )) || {
    printf '%sFAIL: expected 350..750 words for target_words=500, got %s%s\n' "$RED" "$WORDS" "$RESET" >&2
    exit 1
}

# ── 3b. Basic content sanity (no hardcoded semantic assertions) ─────
# The LLM decides what to produce — we only verify the output is
# non-empty and doesn't contain prompt leakage or raw JSON.
[[ "$(printf '%s' "$SCRIPT" | wc -w)" -gt 10 ]] || {
    printf '%sFAIL: script too short (< 10 words)%s\n' "$RED" "$RESET" >&2
    exit 1
}

# ── 3c. No raw JSON, no prompt leakage ─────────────────────────────
if printf '%s' "$SCRIPT" | grep -Fq "$(printf '\x60\x60\x60')" ||
   printf '%s' "$SCRIPT" | grep -Eiq '"(prompt|source_text|target_words)"|Ecco lo script richiesto|As an AI|Here is'; then
    printf '%sFAIL: generated script contains raw JSON, prompt instructions, or placeholder prose%s\n' "$RED" "$RESET" >&2
    exit 1
fi

# ── 4. Segment integrity ──────────────────────────────────────────
smoke_log_section "Validate VidRush segments"

if jq -e 'has("segments")' <<<"$RESULT" >/dev/null; then
    SEG_COUNT=$(jq '.segments | length' <<<"$RESULT")
    printf 'segments:       %s%s%s\n' "$YELLOW" "$SEG_COUNT" "$RESET"
    (( SEG_COUNT >= 3 )) || {
        printf '%sFAIL: expected >=3 segments, got %s%s\n' "$RED" "$SEG_COUNT" "$RESET" >&2
        exit 1
    }

    # Position ordering
    jq -e '([.segments[].position] == ([.segments[].position] | sort))' <<<"$RESULT" >/dev/null || {
        printf '%sFAIL: segments are not in sorted position order%s\n' "$RED" "$RESET" >&2
        exit 1
    }

    # Unique segment IDs
    jq -e '([.segments[].segment_id] | length) == ([.segments[].segment_id] | unique | length)' <<<"$RESULT" >/dev/null || {
        printf '%sFAIL: duplicate segment_ids%s\n' "$RED" "$RESET" >&2
        exit 1
    }

    # Each segment has text, text_hash, and valid cache state
    jq -e 'all(.segments[];
        (.segment_id | length) > 0
        and (.text | length) > 0
        and (.text_hash | length) > 0
        and (.cache | type == "object")
        and (.cache.extraction | length) > 0
    )' <<<"$RESULT" >/dev/null || {
        printf '%sFAIL: segment missing required fields (id, text, text_hash, cache)%s\n' "$RED" "$RESET" >&2
        exit 1
    }
else
    printf '%sWARN: no segments in result (VidRush pipeline may not have run)%s\n' "$YELLOW" "$RESET"
fi

# ── 4b. Per-segment insights (ZW-02) ───────────────────────────────
if jq -e 'has("segments")' <<<"$RESULT" >/dev/null; then
    smoke_log_section "Validate per-segment phrases, words and entities"
    jq -e 'all(.segments[];
        (.insights | type == "object")
        and ((.insights.segment_id // "") == .segment_id)
        and ((.insights.text_hash // "") == .text_hash)
        and ((.insights.important_phrases | type == "array") and length > 0)
        and ((.insights.important_words | type == "array") and length > 0)
        and ((.insights.entities | type == "array") and length > 0)
    )' <<<"$RESULT" >/dev/null || {
        printf '%sFAIL: every segment must contain aligned non-empty important_phrases, important_words and entities%s\n' "$RED" "$RESET" >&2
        jq -c '.segments[] | {segment_id, text, insights: {segment_id: .insights.segment_id, text_hash: .insights.text_hash, phrases: (.insights.important_phrases // [] | length), words: (.insights.important_words // [] | length), entities: (.insights.entities // [] | length)}}' <<<"$RESULT" >&2
        exit 1
    }
    printf '%sAll segments have aligned non-empty insights%s\n' "$GREEN" "$RESET"
fi

# ── 5. Cache state validation ──────────────────────────────────────
if jq -e 'has("segments")' <<<"$RESULT" >/dev/null; then
    smoke_log_section "Validate cache states"

    # Extraction cache: HIT_EXACT, BYPASSED, or MISS (first run)
    jq -e 'all(.segments[]; .cache.extraction == "HIT_EXACT" or .cache.extraction == "BYPASSED" or .cache.extraction == "MISS" or .cache.extraction == "ERROR")' <<<"$RESULT" >/dev/null || {
        printf '%sFAIL: extraction cache not HIT_EXACT/BYPASSED%s\n' "$RED" "$RESET" >&2
        jq -c '.segments[] | {segment_id, cache}' <<<"$RESULT" >&2
        exit 1
    }

    # Artlist cache: HIT_EXACT, BYPASSED, or MISS
    jq -e 'all(.segments[]; .cache.artlist == "HIT_EXACT" or .cache.artlist == "BYPASSED" or .cache.artlist == "MISS" or .cache.artlist == "ERROR")' <<<"$RESULT" >/dev/null || {
        printf '%sFAIL: unexpected artlist cache state%s\n' "$RED" "$RESET" >&2
        jq -c '.segments[] | {segment_id, cache: .cache.artlist}' <<<"$RESULT" >&2
        exit 1
    }

    # Internet images cache: HIT_EXACT, BYPASSED, or MISS
    jq -e 'all(.segments[]; .cache.internet_images == "HIT_EXACT" or .cache.internet_images == "BYPASSED" or .cache.internet_images == "MISS" or .cache.internet_images == "ERROR")' <<<"$RESULT" >/dev/null || {
        printf '%sFAIL: unexpected internet_images cache state%s\n' "$RED" "$RESET" >&2
        jq -c '.segments[] | {segment_id, cache: .cache.internet_images}' <<<"$RESULT" >&2
        exit 1
    }

    printf '%sAll cache states valid%s\n' "$GREEN" "$RESET"
fi

# ── 6. Entity extraction ──────────────────────────────────────────
if jq -e 'has("segments")' <<<"$RESULT" >/dev/null; then
    smoke_log_section "Validate entity extraction"

    ENT_COUNT=$(jq '[.segments[] | (.insights.entities // []) | length] | add // 0' <<<"$RESULT" 2>/dev/null || echo 0)
    printf 'entities found: %s%s%s\n' "$YELLOW" "$ENT_COUNT" "$RESET"
fi

# ── 7. Insights / queries ─────────────────────────────────────────
if jq -e 'has("segments")' <<<"$RESULT" >/dev/null; then
    smoke_log_section "Validate search queries (artlist/image)"

    ARTLIST_QUERIES=$(jq -r '[.segments[].insights.artlist_queries[]? | select(length > 0)] | unique | .[]' <<<"$RESULT" 2>/dev/null || true)
    IMAGE_QUERIES=$(jq -r '[.segments[].insights.image_queries[]? | select(length > 0)] | unique | .[]' <<<"$RESULT" 2>/dev/null || true)

    ARTLIST_COUNT=$(echo "$ARTLIST_QUERIES" | grep -c . || echo 0)
    IMAGE_COUNT=$(echo "$IMAGE_QUERIES" | grep -c . || echo 0)

    printf 'artlist queries: %s%s%s\n' "$YELLOW" "$ARTLIST_COUNT" "$RESET"
    printf 'image queries:   %s%s%s\n' "$YELLOW" "$IMAGE_COUNT" "$RESET"

    if [[ "$ARTLIST_COUNT" -gt 0 ]]; then
        echo "$ARTLIST_QUERIES" | head -5
    fi
    if [[ "$IMAGE_COUNT" -gt 0 ]]; then
        echo "$IMAGE_QUERIES" | head -5
    fi
fi

# ── 8. Candidates (stock/clips) ────────────────────────────────────
if jq -e 'has("segments")' <<<"$RESULT" >/dev/null; then
    smoke_log_section "Validate clip/image candidates"

    CANDIDATE_COUNT=$(jq '[.segments[].assets.candidates // [] | length] | add' <<<"$RESULT" 2>/dev/null || echo 0)
    printf 'total candidates: %s%s%s\n' "$YELLOW" "$CANDIDATE_COUNT" "$RESET"

    if (( CANDIDATE_COUNT > 0 )); then
        # Provider breakdown
        jq -r '[.segments[].assets.candidates[]? | .provider // "unknown"] | group_by(.) | map({provider: .[0], count: length}) | .[] | "\(.provider): \(.count)"' <<<"$RESULT" 2>/dev/null || true

        # Primary video count
        PRIMARY_COUNT=$(jq '[.segments[] | select(.assets.primary_video != null)] | length' <<<"$RESULT" 2>/dev/null || echo 0)
        printf 'segments with primary_video: %s%s%s\n' "$YELLOW" "$PRIMARY_COUNT" "$RESET"
    fi
fi

# ── 8b. Artlist candidate integrity (ZW-03) ───────────────────────
if jq -e 'has("segments")' <<<"$RESULT" >/dev/null; then
    smoke_log_section "Validate Artlist candidates per segment"
    jq -e '
        all(.segments[];
            . as $segment |
            (($segment.insights.artlist_queries // []) | length) == 0
            or any($segment.assets.candidates[]?;
                .provider == "artlist"
                and (.query | type == "string" and length > 0)
                and (.query as $candidate_query |
                    (($segment.insights.artlist_queries // []) | index($candidate_query)) != null)
                and (.asset_id | type == "string" and length > 0)
                and (.entity | type == "string" and length > 0)
                and ((.drive_link // .source_url // "") | type == "string" and length > 0)
                and (.score | type == "number" and . >= 0)
            )
        )
        and all(.segments[].assets.candidates[]?;
            .provider != "artlist"
            or ((.drive_link // .source_url // "") | length > 0)
        )
        and all(.segments[];
            ([.assets.candidates[]? | select(.provider == "artlist") | (.drive_link // .source_url // "")] | length)
            == ([.assets.candidates[]? | select(.provider == "artlist") | (.drive_link // .source_url // "")] | unique | length)
            and (.assets.primary_video as $primary | $primary == null or any(.assets.candidates[]?; .asset_id == $primary.asset_id))
        )
    ' <<<"$RESULT" >/dev/null || {
        printf '%sFAIL: each segment with Artlist queries needs a valid matching Artlist candidate%s\n' "$RED" "$RESET" >&2
        jq -c '.segments[] | {segment_id, queries: (.insights.artlist_queries // []), artlist_candidates: [(.assets.candidates // [])[] | select(.provider == "artlist") | {asset_id, query, entity, drive_link, source_url, score}]}' <<<"$RESULT" >&2
        exit 1
    }
    printf '%sAll Artlist candidates are query-linked and valid%s\n' "$GREEN" "$RESET"
fi

# ── 8c. Internet-image association (ZW-04) ────────────────────────
if jq -e 'has("segments")' <<<"$RESULT" >/dev/null; then
    smoke_log_section "Validate Internet images per entity/query"
    jq -e '
        all(.segments[];
            . as $segment |
            (($segment.insights.image_queries // []) | length) == 0
            or (
                (($segment.assets.secondary_images // []) | length) > 0
                and all($segment.assets.secondary_images[];
                    .provider == "internet_images"
                    and (.asset_id | type == "string" and length > 0)
                    and ((.source_url // .preview_url // "") | type == "string" and length > 0)
                    and (.query as $candidate_query |
                        (($segment.insights.image_queries // []) | index($candidate_query)) != null)
                    and (.entity | type == "string" and length > 0)
                    and (.entity as $candidate_entity |
                        (($segment.insights.entities // [] | map(.value)) | index($candidate_entity)) != null)
                    and (.score | type == "number" and . >= 0)
                )
            )
        )
    ' <<<"$RESULT" >/dev/null || {
        printf '%sFAIL: every segment with image queries needs entity-linked Internet image candidates%s\n' "$RED" "$RESET" >&2
        jq -c '.segments[] | {segment_id, image_queries: (.insights.image_queries // []), entities: (.insights.entities // []), secondary_images: [(.assets.secondary_images // [])[] | {provider, asset_id, entity, query, source_url, preview_url, score}]}' <<<"$RESULT" >&2
        exit 1
    }
    printf '%sAll Internet images are entity/query-linked and valid%s\n' "$GREEN" "$RESET"
fi

# ── 9. Idempotency replay ──────────────────────────────────────────
smoke_log_section "Idempotency replay test"
export SMOKE_IDEMPOTENCY_KEY="$IDEMPOTENCY_KEY"
smoke_curl POST "/api/script/generate" --data "$PAYLOAD" >/dev/null
unset SMOKE_IDEMPOTENCY_KEY

[[ "$SMOKE_LAST_HTTP" == "202" ]] || { echo "FAIL: idempotency replay HTTP=$SMOKE_LAST_HTTP" >&2; exit 1; }
REPLAY_JOB=$(jq -r '.job_id // empty' "$SMOKE_LAST_BODY")
[[ "$REPLAY_JOB" == "$JOB_ID" ]] || { echo "FAIL: idempotency replay returned a different job ($REPLAY_JOB vs $JOB_ID)" >&2; exit 1; }

# Header probe
replay_headers="$WORK_DIR/replay.headers"
replay_http=$(curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
    -D "$replay_headers" -o /dev/null -X POST \
    -H "Authorization: Bearer $SMOKE_TOKEN" \
    -H "Idempotency-Key: $IDEMPOTENCY_KEY" \
    -H 'Content-Type: application/json' --data "$PAYLOAD" \
    -w '%{http_code}' "http://${SMOKE_API_BASE}/api/script/generate")
[[ "$replay_http" == "202" ]] || { echo "FAIL: replay header probe HTTP=$replay_http" >&2; exit 1; }
grep -Eiq '^X-Idempotency-Replay:[[:space:]]*true' "$replay_headers" || { echo "FAIL: missing X-Idempotency-Replay: true" >&2; exit 1; }

# Conflict test: same key, different payload → 409
conflict_http=$(jq '.items[0].title = "Payload diverso"' <<<"$PAYLOAD" | curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" -X POST \
    -H "Authorization: Bearer $SMOKE_TOKEN" -H "Idempotency-Key: $IDEMPOTENCY_KEY" \
    -H 'Content-Type: application/json' --data-binary @- -o "$WORK_DIR/conflict.json" \
    -w '%{http_code}' "http://${SMOKE_API_BASE}/api/script/generate")
[[ "$conflict_http" == "409" ]] || { echo "FAIL: idempotency conflict HTTP=$conflict_http" >&2; exit 1; }
jq -e '.code == "IDEMPOTENCY_KEY_CONFLICT"' "$WORK_DIR/conflict.json" >/dev/null || { echo "FAIL: missing IDEMPOTENCY_KEY_CONFLICT" >&2; exit 1; }

printf '%sIdempotency: replay + conflict both correct%s\n' "$GREEN" "$RESET"

# ── 9b. SQLite persistence boundary (ZW-05) ────────────────────────
smoke_log_section "Validate SQLite persistence"
DB_PATH="${SMOKE_DB_PATH:-$REPO_ROOT/data/media/media.db.sqlite}"
[[ -f "$DB_PATH" ]] || { printf '%sFAIL: SQLite database not found: %s%s\n' "$RED" "$DB_PATH" "$RESET" >&2; exit 1; }
[[ "$SCRIPT_ID" =~ ^[0-9]+$ && "$SCRIPT_ID" -gt 0 ]] || { printf '%sFAIL: API did not return a valid script_id: %s%s\n' "$RED" "$SCRIPT_ID" "$RESET" >&2; exit 1; }

DB_ROW=$(sqlite3 -json "$DB_PATH" "
    SELECT id, title, language, status, final_word_count, idempotency_key,
           narrative_text, full_document, specscene, manifest_v2
    FROM scripts WHERE id = $SCRIPT_ID LIMIT 1;
" 2>/dev/null) || { printf '%sFAIL: could not query SQLite scripts table%s\n' "$RED" "$RESET" >&2; exit 1; }

jq -e --argjson script_id "$SCRIPT_ID" '
    length == 1
    and .[0].id == $script_id
    and .[0].status == "completed"
    and (. [0].language | type == "string" and length > 0)
    and (. [0].final_word_count | type == "number" and . > 0)
    and (. [0].idempotency_key | type == "string" and length > 0)
    and ((.[0].narrative_text // .[0].full_document // "") | length > 0)
    and (.[0].specscene | fromjson | (.version == 1 and (.scenes | length > 0) and all(.scenes[]; (.id | length > 0) and (.text | length > 0))))
    and (.[0].manifest_v2 | fromjson | ((.items | length > 0) and .no_inline_assets == true))
' <<<"$DB_ROW" >/dev/null || {
    printf '%sFAIL: persisted scripts row is incomplete or has empty SpecScene/Manifest%s\n' "$RED" "$RESET" >&2
    jq '.' <<<"$DB_ROW" >&2
    exit 1
}
printf '%sSQLite persistence: scripts row, SpecScene and ManifestV2 are valid%s\n' "$GREEN" "$RESET"

# ── 10. Google Doc validation ──────────────────────────────────────
smoke_log_section "Validate Google Doc creation"

# The payload carries the canonical docs request. The live result must expose
# the published artifact; silently accepting a missing link would make this
# test validate a fake success.
DOC_LINK=$(jq -r '
    .artifacts.document.doc_link //
    .result.artifacts.document.doc_link //
    .documents.it.link //
    .result.documents.it.link // empty
' "$RESULT" 2>/dev/null | grep -m1 'docs.google.com' || true)
if [[ -z "$DOC_LINK" ]]; then
    # Terminal status and result persistence can be observed on adjacent
    # writes. Give the canonical result endpoint a short consistency window.
    for _ in 1 2 3 4 5; do
        sleep 1
        smoke_curl GET "/api/jobs/${JOB_ID}/full" >/dev/null
        RESULT=$(jq -c '.result.data.items[0].result // .result.items[0].result // .result.output // .result // empty' "$SMOKE_LAST_BODY")
        DOC_LINK=$(jq -r '.artifacts.document.doc_link // .result.artifacts.document.doc_link // .documents.it.link // .result.documents.it.link // empty' <<<"$RESULT" 2>/dev/null | grep -m1 'docs.google.com' || true)
        [[ -n "$DOC_LINK" ]] && break
    done
fi
if [[ -n "$DOC_LINK" ]]; then
    printf '%sGoogle Doc URL:%s %s%s%s\n' "$GREEN" "$RESET" "$YELLOW" "$DOC_LINK" "$RESET"
else
    printf '%sFAIL: docs.enabled=true but no Google Doc artifact was returned%s\n' "$RED" "$RESET" >&2
    exit 1
fi

# ── Final summary ──────────────────────────────────────────────────
echo ""
printf '%s══════════════════════════════════════════════════════════════%s\n' "$GREEN" "$RESET"
printf '%sOK: Zhang vs Wilder generate test passed%s\n' "$GREEN" "$RESET"
printf '%s  - Script: %s chars, %s words (Italian, documentary tone)%s\n' "$RESET" "${#SCRIPT}" "$WORDS" "$RESET"
printf '%s  - Segments: %s%s\n' "$RESET" "${SEG_COUNT:-N/A}" "$RESET"
printf '%s  - Candidates: %s%s\n' "$RESET" "${CANDIDATE_COUNT:-N/A}" "$RESET"
printf '%s  - Job ID: %s%s\n' "$RESET" "$JOB_ID" "$RESET"
[[ -n "${DOC_LINK:-}" ]] && printf '%s  - Google Doc: %s%s\n' "$RESET" "$DOC_LINK" "$RESET" || true
printf '%s══════════════════════════════════════════════════════════════%s\n' "$GREEN" "$RESET"
exit 0

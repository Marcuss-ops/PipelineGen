#!/usr/bin/env bash
#
# script_generate_search_e2e_battery.sh — black-box smoke for the
# source.search path and its Qdrant-backed retrieval contract.
#
# This battery deliberately keeps `source.search` separate from
# `source.clips`:
#   1. Gate 1: POST /api/media/search hits the semantic index directly.
#   2. Gate 2: POST /api/script/generate with a mixed batch verifies
#      search + clips + text isolation in one production envelope.
#   3. Gate 3: a no-result search request must fail closed.
#
# Exit codes:
#   0  every assertion passed
#   1  one or more assertions failed
#   2  setup error
#   124 overall / poll-loop timeout

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

smoke_require curl jq sqlite3

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,60p' "$0"
    exit 0
fi

PAYLOAD_FILE="${PAYLOAD_FILE:-$DIR/script_generate_search_e2e_battery.json}"
NEGATIVE_PAYLOAD_FILE="${NEGATIVE_PAYLOAD_FILE:-$DIR/script_generate_search_e2e_negative.json}"
DB_PATH="${DB_PATH:-data/media/media.db.sqlite}"
RUN_MIXED_BATCH="${RUN_MIXED_BATCH:-0}"
AUDIT_GOOGLE_DOC_RENDER="${AUDIT_GOOGLE_DOC_RENDER:-0}"
DOCS_FOLDER_ID="${DOCS_FOLDER_ID:-}"
ADMIN_BIN="${ADMIN_BIN:-}"

if [[ ! -f "$PAYLOAD_FILE" ]]; then
    printf '%ssetup error: payload file not found: %s%s\n' "$RED" "$PAYLOAD_FILE" "$RESET" >&2
    exit 2
fi
if [[ ! -f "$NEGATIVE_PAYLOAD_FILE" ]]; then
    printf '%ssetup error: negative payload file not found: %s%s\n' "$RED" "$NEGATIVE_PAYLOAD_FILE" "$RESET" >&2
    exit 2
fi
if [[ "$AUDIT_GOOGLE_DOC_RENDER" == "1" && "$RUN_MIXED_BATCH" == "1" ]]; then
    printf '%ssetup error: AUDIT_GOOGLE_DOC_RENDER=1 requires RUN_MIXED_BATCH=0%s\n' "$RED" "$RESET" >&2
    exit 2
fi
if [[ "$AUDIT_GOOGLE_DOC_RENDER" == "1" && ( -z "$DOCS_FOLDER_ID" || -z "$ADMIN_BIN" ) ]]; then
    printf '%ssetup error: document audit requires DOCS_FOLDER_ID and ADMIN_BIN%s\n' "$RED" "$RESET" >&2
    exit 2
fi

SEARCH_QUERY=$(jq -r '.items[] | select(.id == "search-full-timeline") | .source.query' "$PAYLOAD_FILE")
if [[ -z "$SEARCH_QUERY" || "$SEARCH_QUERY" == "null" ]]; then
    printf '%ssetup error: payload missing search-full-timeline query%s\n' "$RED" "$RESET" >&2
    exit 2
fi

EXPECTED_CLIP_IDS_FILE="$WORK_DIR/expected_clip_ids.txt"
if [[ "$RUN_MIXED_BATCH" == "1" ]]; then
    jq -r '.items[] | select(.id == "clips-control") | .source.clip_ids[]' "$PAYLOAD_FILE" > "$EXPECTED_CLIP_IDS_FILE"
    if [[ ! -s "$EXPECTED_CLIP_IDS_FILE" ]]; then
        printf '%ssetup error: payload missing clips-control clip_ids%s\n' "$RED" "$RESET" >&2
        exit 2
    fi
fi

TEXT_ITEM_ID="text-control"
SEARCH_ITEM_ID="search-full-timeline"
CLIPS_ITEM_ID="clips-control"

if [[ "$DRY_RUN" == "1" ]]; then
    printf '\nDRY RUN — success payload:\n'
    jq . "$PAYLOAD_FILE"
    printf '\nDRY RUN — negative payload:\n'
    jq . "$NEGATIVE_PAYLOAD_FILE"
    exit 0
fi

extract_batch_item() {
    local body_file="$1"
    local item_id="$2"
    jq -c --arg id "$item_id" '
        def arr:
            (.result.data.items // .result.items // .items // []);
        [arr[]
            | select((.id // .item_id // "") == $id)
            | (.result // .)
        ][0] // empty
    ' "$body_file"
}

extract_single_result() {
    local body_file="$1"
    jq -c '
        .result.data.items[0].result
        // .result.items[0]
        // .result
        // empty
    ' "$body_file"
}

item_source_type() {
    jq -r '.source.type // .provenance.source_type // empty' <<<"$1"
}

item_field_count() {
    jq -r "$2 | length" <<<"$1"
}

assert_unique_ids() {
    local label="$1"
    local ids_blob="$2"
    local total unique
    total=$(printf '%s\n' "$ids_blob" | sed '/^$/d' | wc -l | tr -d ' ')
    unique=$(printf '%s\n' "$ids_blob" | sed '/^$/d' | sort -u | wc -l | tr -d ' ')
    if [[ "$total" != "$unique" ]]; then
        printf '%sFAIL: %s contains duplicate IDs (total=%s unique=%s)%s\n' \
            "$RED" "$label" "$total" "$unique" "$RESET" >&2
        return 1
    fi
}

assert_clip_ids_exist_in_sqlite() {
    local ids_blob="$1"
    while IFS= read -r clip_id; do
        [[ -z "$clip_id" ]] && continue
        local found
        found=$(sqlite3 "$DB_PATH" "
            SELECT COUNT(*)
            FROM media_assets
            WHERE id = '$clip_id'
               OR drive_file_id = '$clip_id';
        " 2>/dev/null || echo "0")
        if [[ "$found" != "1" ]]; then
            printf '%sFAIL: clip not uniquely hydrated in SQLite: %s (count=%s)%s\n' \
                "$RED" "$clip_id" "$found" "$RESET" >&2
            return 1
        fi
    done <<<"$ids_blob"
}

smoke_log_section "Pre-flight"
printf '  API base   : %s\n' "$SMOKE_API_BASE"
printf '  DB path    : %s\n' "$DB_PATH"
printf '  Payload    : %s\n' "$PAYLOAD_FILE"
printf '  Neg payload: %s\n' "$NEGATIVE_PAYLOAD_FILE"

smoke_curl GET "/health" >/dev/null
if ! smoke_assert_http_2xx "GET /health"; then
    exit 1
fi

smoke_log_section "Gate 1: direct semantic search"
SEARCH_BODY=$(jq -n --arg query "$SEARCH_QUERY" '{
    query: $query,
    sources: ["clips"],
    mode: "hybrid",
    filters: { media_type: "video" },
    limit: 100
}')
smoke_curl POST "/api/media/search" --data "$SEARCH_BODY" >/dev/null
if ! smoke_assert_http_2xx "POST /api/media/search"; then
    exit 1
fi

SEARCH_TOTAL=$(jq '[.items[]?] | length' "$SMOKE_LAST_BODY")
if [[ "$SEARCH_TOTAL" -lt 8 ]]; then
    printf '%sFAIL: expected at least 8 direct search hits, got %s%s\n' \
        "$RED" "$SEARCH_TOTAL" "$RESET" >&2
    exit 1
fi

if jq -e '.partial == true' "$SMOKE_LAST_BODY" >/dev/null 2>&1; then
    printf '%sFAIL: direct search response is partial%s\n' "$RED" "$RESET" >&2
    exit 1
fi

if [[ "$RUN_MIXED_BATCH" == "1" ]]; then
    for clip_id in $(cat "$EXPECTED_CLIP_IDS_FILE"); do
        if ! jq -e --arg clip_id "$clip_id" '
            any(.items[]?;
                ((.asset_id // .clip_id // "") | contains($clip_id))
            )
        ' "$SMOKE_LAST_BODY" >/dev/null; then
            printf '%sFAIL: direct search missing expected clip %s%s\n' "$RED" "$clip_id" "$RESET" >&2
            exit 1
        fi
    done
fi

if jq -e '
    any(.items[]?;
        ((.asset_id // .clip_id // "") | contains("e2e_fight_beta"))
        or ((.asset_id // .clip_id // "") | contains("e2e_training"))
        or ((.asset_id // .clip_id // "") | contains("e2e_interview"))
    )
' "$SMOKE_LAST_BODY" >/dev/null; then
    printf '%sFAIL: distractor returned from direct search%s\n' "$RED" "$RESET" >&2
    exit 1
fi

smoke_log_section "Gate 2: script.generate payload"
SEARCH_ONLY_PAYLOAD=$(jq -c '
    {
        version: .version,
        preset: .preset,
        correlation_id: ("e2e-search-full-timeline-searchonly-" + $suffix),
        force_refresh: .force_refresh,
        items: [ .items[] | select(.id == "search-full-timeline") ]
    }
' --arg suffix "$(smoke_gen_uuid)" "$PAYLOAD_FILE")
if [[ "$AUDIT_GOOGLE_DOC_RENDER" == "1" ]]; then
    SEARCH_ONLY_PAYLOAD=$(jq -c --arg folder "$DOCS_FOLDER_ID" '
        .items[0].docs = {enabled: true, languages: ["it"], folder_id: $folder}
    ' <<<"$SEARCH_ONLY_PAYLOAD")
fi

if [[ "$RUN_MIXED_BATCH" == "1" ]]; then
    PAYLOAD=$(<"$PAYLOAD_FILE")
    smoke_curl POST "/api/script/generate" -H "Idempotency-Key: script-generate-mixed-$(smoke_gen_uuid)" --data "$PAYLOAD" >/dev/null
else
    smoke_curl POST "/api/script/generate" -H "Idempotency-Key: script-generate-search-$(smoke_gen_uuid)" --data "$SEARCH_ONLY_PAYLOAD" >/dev/null
fi
if [[ "$SMOKE_LAST_HTTP" != "202" && "$SMOKE_LAST_HTTP" != "200" ]]; then
    printf '%sFAIL: script.generate dispatch returned HTTP %s%s\n' "$RED" "$SMOKE_LAST_HTTP" "$RESET" >&2
    smoke_echo_safe "$(head -c 800 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi

JOB_ID=$(jq -r '.job_id // .id // empty' "$SMOKE_LAST_BODY")
if [[ -z "$JOB_ID" || "$JOB_ID" == "null" ]]; then
    printf '%sFAIL: mixed batch dispatch did not return job_id%s\n' "$RED" "$RESET" >&2
    smoke_echo_safe "$(head -c 800 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi

smoke_log_section "Poll script.generate job"
if ! smoke_poll_terminal "$JOB_ID"; then
    printf '%sFAIL: script.generate job did not reach terminal state%s\n' "$RED" "$RESET" >&2
    exit 1
fi
case "$SMOKE_LAST_STATUS" in
    completed|SUCCEEDED)
        ;;
    *)
        printf '%sFAIL: script.generate job ended with status %s%s\n' \
            "$RED" "$SMOKE_LAST_STATUS" "$RESET" >&2
        smoke_echo_safe "$(head -c 1200 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        exit 1
        ;;
esac

if [[ "$RUN_MIXED_BATCH" == "1" ]]; then
    SEARCH_ITEM=$(extract_batch_item "$SMOKE_LAST_BODY" "$SEARCH_ITEM_ID")
    CLIPS_ITEM=$(extract_batch_item "$SMOKE_LAST_BODY" "$CLIPS_ITEM_ID")
    TEXT_ITEM=$(extract_batch_item "$SMOKE_LAST_BODY" "$TEXT_ITEM_ID")

    if [[ -z "$SEARCH_ITEM" || -z "$CLIPS_ITEM" || -z "$TEXT_ITEM" ]]; then
        printf '%sFAIL: could not extract all expected batch items%s\n' "$RED" "$RESET" >&2
        smoke_echo_safe "$(head -c 1200 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        exit 1
    fi

    if [[ "$(item_source_type "$SEARCH_ITEM")" != "search" ]]; then
        printf '%sFAIL: search item did not resolve as source.type=search%s\n' "$RED" "$RESET" >&2
        exit 1
    fi
    if [[ "$(item_source_type "$CLIPS_ITEM")" != "clips" ]]; then
        printf '%sFAIL: clips control item did not resolve as source.type=clips%s\n' "$RED" "$RESET" >&2
        exit 1
    fi
    if [[ "$(item_source_type "$TEXT_ITEM")" != "text" ]]; then
        printf '%sFAIL: text control item did not resolve as source.type=text%s\n' "$RED" "$RESET" >&2
        exit 1
    fi

    SEARCH_ACCEPTED=$(jq -r '.source.accepted_clip_ids[]? // empty' <<<"$SEARCH_ITEM")
    CLIPS_ACCEPTED=$(jq -r '.source.accepted_clip_ids[]? // empty' <<<"$CLIPS_ITEM")
    SEARCH_RESULTS_IDS=$(jq -r '.source.search_results[]?.clip_id // empty' <<<"$SEARCH_ITEM")

    if [[ -z "$SEARCH_ACCEPTED" ]]; then
        printf '%sFAIL: search item produced no accepted_clip_ids%s\n' "$RED" "$RESET" >&2
        exit 1
    fi
    if [[ -z "$CLIPS_ACCEPTED" ]]; then
        printf '%sFAIL: clips control item produced no accepted_clip_ids%s\n' "$RED" "$RESET" >&2
        exit 1
    fi

    assert_unique_ids "search accepted_clip_ids" "$SEARCH_ACCEPTED"
    assert_unique_ids "clips accepted_clip_ids" "$CLIPS_ACCEPTED"
    assert_unique_ids "search search_results" "$SEARCH_RESULTS_IDS"

    if ! diff -u <(printf '%s\n' "$SEARCH_ACCEPTED") <(printf '%s\n' "$CLIPS_ACCEPTED") >/dev/null; then
        printf '%sFAIL: search accepted_clip_ids do not match clips control order%s\n' "$RED" "$RESET" >&2
        diff -u <(printf '%s\n' "$SEARCH_ACCEPTED") <(printf '%s\n' "$CLIPS_ACCEPTED") >&2 || true
        exit 1
    fi

    if ! assert_clip_ids_exist_in_sqlite "$SEARCH_ACCEPTED"; then
        exit 1
    fi

    SEARCH_RESULTS_COUNT=$(jq '.source.search_results | length' <<<"$SEARCH_ITEM")
    if [[ "$SEARCH_RESULTS_COUNT" -lt 8 ]]; then
        printf '%sFAIL: search item search_results length=%s (expected >= 8)%s\n' \
            "$RED" "$SEARCH_RESULTS_COUNT" "$RESET" >&2
        exit 1
    fi

    if ! jq -e '
        ((.source.type // .provenance.source_type) == "search")
        and ((.source.search_results | length) >= 8)
        and ((.source.accepted_clip_ids | length) >= 8)
    ' <<<"$SEARCH_ITEM" >/dev/null; then
        printf '%sFAIL: search item did not expose the expected search evidence surface%s\n' "$RED" "$RESET" >&2
        exit 1
    fi

    if ! jq -e '
        ((.source.type // .provenance.source_type) == "clips")
        and ((.source.accepted_clip_ids | length) >= 8)
    ' <<<"$CLIPS_ITEM" >/dev/null; then
        printf '%sFAIL: clips item did not expose the expected clip evidence surface%s\n' "$RED" "$RESET" >&2
        exit 1
    fi

    if ! jq -e '
        ((.source.type // .provenance.source_type) == "text")
        and ((.source | has("clip_evidence")) | not)
    ' <<<"$TEXT_ITEM" >/dev/null; then
        printf '%sFAIL: text control item leaked clip evidence%s\n' "$RED" "$RESET" >&2
        exit 1
    fi
else
    SEARCH_ITEM=$(extract_single_result "$SMOKE_LAST_BODY")
    if [[ -z "$SEARCH_ITEM" ]]; then
        printf '%sFAIL: could not extract single search result%s\n' "$RED" "$RESET" >&2
        smoke_echo_safe "$(head -c 1200 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        exit 1
    fi
    SCRIPT_JSON_PATH=$(jq -r '
        .result.data.__artifact_manifest.artifacts[]?
        | select(.kind == "script_json") | .path // empty
    ' "$SMOKE_LAST_BODY")
    if [[ -n "$SCRIPT_JSON_PATH" && -f "$SCRIPT_JSON_PATH" ]]; then
        SEARCH_ITEM=$(<"$SCRIPT_JSON_PATH")
    fi
    SOURCE_TYPE=$(item_source_type "$SEARCH_ITEM")
    if [[ -n "$SOURCE_TYPE" && "$SOURCE_TYPE" != "search" ]]; then
        printf '%sFAIL: search job resolved as unexpected source type=%s%s\n' "$RED" "$SOURCE_TYPE" "$RESET" >&2
        exit 1
    fi
    SEARCH_ACCEPTED=$(jq -r '.source.accepted_clip_ids[]? // empty' <<<"$SEARCH_ITEM")
    SEARCH_RESULTS_IDS=$(jq -r '.source.search_results[]?.clip_id // empty' <<<"$SEARCH_ITEM")
    if [[ -z "$SEARCH_ACCEPTED" ]]; then
        SEARCH_ACCEPTED=$(jq -r '
            .output.specscene.scenes[]?
            | (.bindings.clip?.clip_id, .bindings.clips[]?.clip_id)
            | select(. != null and . != "")
        ' <<<"$SEARCH_ITEM")
    fi
    if [[ -z "$SEARCH_ACCEPTED" ]]; then
        printf '%sFAIL: search job produced no accepted_clip_ids%s\n' "$RED" "$RESET" >&2
        exit 1
    fi
    assert_unique_ids "search accepted_clip_ids" "$SEARCH_ACCEPTED"
    assert_unique_ids "search search_results" "$SEARCH_RESULTS_IDS"
    if ! assert_clip_ids_exist_in_sqlite "$SEARCH_ACCEPTED"; then
        exit 1
    fi
fi

smoke_log_section "Gate 4: accepted search evidence reaches SpecScene bindings"
SEARCH_SPEC_SCENE=$(jq -c '.output.specscene // .output.spec_scene // empty' <<<"$SEARCH_ITEM")
if [[ -z "$SEARCH_SPEC_SCENE" || "$SEARCH_SPEC_SCENE" == "null" ]]; then
    printf '%sFAIL: search result has no canonical output.specscene%s\n' "$RED" "$RESET" >&2
    exit 1
fi

BOUND_IDS=$(jq -r '
    .scenes[]?
    | (.bindings.clip?.clip_id, .bindings.clips[]?.clip_id)
    | select(. != null and . != "")
' <<<"$SEARCH_SPEC_SCENE" | sort -u)
if [[ -z "$BOUND_IDS" ]]; then
    printf '%sFAIL: source.search accepted clips but SpecScene has no clip bindings%s\n' "$RED" "$RESET" >&2
    exit 1
fi
assert_unique_ids "SpecScene bound clip IDs" "$BOUND_IDS"

if ! diff -u <(printf '%s\n' "$SEARCH_ACCEPTED" | sed '/^$/d' | sort -u) \
    <(printf '%s\n' "$BOUND_IDS" | sed '/^$/d' | sort -u) >/dev/null; then
    printf '%sFAIL: accepted_clip_ids and SpecScene clip bindings differ%s\n' "$RED" "$RESET" >&2
    diff -u <(printf '%s\n' "$SEARCH_ACCEPTED" | sed '/^$/d' | sort -u) \
        <(printf '%s\n' "$BOUND_IDS" | sed '/^$/d' | sort -u) >&2 || true
    exit 1
fi

if jq -e '.quality.clip_evidence_coverage != null' <<<"$SEARCH_ITEM" >/dev/null 2>&1; then
    if ! jq -e '.quality.clip_evidence_coverage == 1' <<<"$SEARCH_ITEM" >/dev/null; then
        printf '%sFAIL: search result clip_evidence_coverage is not 1%s\n' "$RED" "$RESET" >&2
        exit 1
    fi
elif [[ "$(printf '%s\n' "$SEARCH_ACCEPTED" | sed '/^$/d' | sort -u | wc -l | tr -d ' ')" != "$(printf '%s\n' "$BOUND_IDS" | sed '/^$/d' | sort -u | wc -l | tr -d ' ')" ]]; then
    printf '%sFAIL: accepted clips and SpecScene bindings have different coverage%s\n' "$RED" "$RESET" >&2
    exit 1
fi

if ! jq -e '
    all(.scenes[]?;
        all((.bindings.clip?, .bindings.clips[]?);
            ((.clip_id // "") != "") and ((.drive_link // "") != "")
        )
    )
' <<<"$SEARCH_SPEC_SCENE" >/dev/null; then
    printf '%sFAIL: SpecScene search clip binding is missing clip_id or drive_link%s\n' "$RED" "$RESET" >&2
    exit 1
fi

printf '%sOK: Qdrant accepted clips are fully represented in SpecScene bindings%s\n' "$GREEN" "$RESET"

if [[ "$AUDIT_GOOGLE_DOC_RENDER" == "1" ]]; then
    smoke_log_section "Gate 5: actual Google Doc matches job SpecScene hash"
    DOC_ID=$(jq -r '.artifacts.document.doc_id // empty' <<<"$SEARCH_ITEM")
    DOC_HASH=$(jq -r '.artifacts.document.specscene_sha256 // empty' <<<"$SEARCH_ITEM")
    DOC_SCENES=$(jq -r '.artifacts.document.scene_count // empty' <<<"$SEARCH_ITEM")
    DOC_RENDERER=$(jq -r '.artifacts.document.renderer // empty' <<<"$SEARCH_ITEM")
    if [[ -z "$DOC_ID" || -z "$DOC_HASH" || -z "$DOC_SCENES" || -z "$DOC_RENDERER" ]]; then
        printf '%sFAIL: search job did not expose complete document artifact metadata%s\n' "$RED" "$RESET" >&2
        exit 1
    fi
    if ! "$ADMIN_BIN" audit-google-doc-render \
        --doc-id "$DOC_ID" \
        --expected-renderer "$DOC_RENDERER" \
        --expected-sha256 "$DOC_HASH" \
        --expected-scene-count "$DOC_SCENES"; then
        printf '%sFAIL: actual Google Doc content does not match the job artifact%s\n' "$RED" "$RESET" >&2
        exit 1
    fi
    printf '%sOK: actual Google Doc matches renderer, scene count, and SpecScene hash%s\n' "$GREEN" "$RESET"
fi

smoke_log_section "Gate 3: no-result search must fail closed"
NEG_PAYLOAD=$(<"$NEGATIVE_PAYLOAD_FILE")
smoke_curl POST "/api/script/generate" -H "Idempotency-Key: script-generate-negative-$(smoke_gen_uuid)" --data "$NEG_PAYLOAD" >/dev/null
if [[ "$SMOKE_LAST_HTTP" != "202" && "$SMOKE_LAST_HTTP" != "200" ]]; then
    printf '%sFAIL: negative batch dispatch returned HTTP %s%s\n' "$RED" "$SMOKE_LAST_HTTP" "$RESET" >&2
    exit 1
fi

NEG_JOB_ID=$(jq -r '.job_id // .id // empty' "$SMOKE_LAST_BODY")
if [[ -z "$NEG_JOB_ID" || "$NEG_JOB_ID" == "null" ]]; then
    printf '%sFAIL: negative batch dispatch did not return job_id%s\n' "$RED" "$RESET" >&2
    exit 1
fi

if ! smoke_poll_terminal "$NEG_JOB_ID"; then
    printf '%sFAIL: negative batch did not reach terminal state%s\n' "$RED" "$RESET" >&2
    exit 1
fi
case "$SMOKE_LAST_STATUS" in
    failed|FAILED|dead_letter)
        ;;
    *)
        printf '%sFAIL: negative batch ended with status %s (expected failure)%s\n' \
            "$RED" "$SMOKE_LAST_STATUS" "$RESET" >&2
        smoke_echo_safe "$(head -c 1200 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        exit 1
        ;;
esac

NEG_ERROR=$(jq -r '.error // .result.error // .message // empty' "$SMOKE_LAST_BODY")
if [[ -z "$NEG_ERROR" ]]; then
    printf '%sFAIL: negative batch terminal response did not include an error message%s\n' "$RED" "$RESET" >&2
    smoke_echo_safe "$(head -c 1200 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi
if ! grep -Eqi 'source resolution|no semantic search results|source resolution failed' <<<"$NEG_ERROR"; then
    printf '%sFAIL: negative batch error does not look like a fail-closed search resolution error%s\n' \
        "$RED" "$RESET" >&2
    printf 'error=%s\n' "$NEG_ERROR" >&2
    exit 1
fi

printf '\n%sOK: search Qdrant battery passed%s\n' "$GREEN" "$RESET"
exit 0

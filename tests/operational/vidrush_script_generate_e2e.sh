#!/usr/bin/env bash
# Canonical VidRush black-box verification.
#
# This test exercises one production flow: POST /api/script/generate, then
# GET /api/jobs/<id>/full. It deliberately does not call provider routes as
# a substitute for script.generate. Provider-disabled cases assert the
# returned per-segment policy and contain no provider assets.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"
smoke_require curl jq

DB_PATH="${DB_PATH:-data/media/media.db.sqlite}"
CASE_PREFIX="vidrush-e2e-$(smoke_gen_uuid)"

if [[ "$DRY_RUN" == "1" ]]; then
    cat <<'EOF'
DRY RUN — POST /api/script/generate with three semantic paragraphs.
The live run polls GET /api/jobs/<job_id>/full and validates per-segment
insights, provider policy, cache states, provenance, and stable IDs.
EOF
    exit 0
fi

PAYLOAD=$(jq -n '{
  version: 2,
  preset: "custom",
  force_refresh: false,
  items: [{
    id: "vidrush-e2e-item",
    title: "Three visual stories",
    language: "en",
    tone: "documentary",
    source: {
      type: "text",
      topic: "three visual stories",
      source_text: ("Elon Musk visits a Tesla factory in Texas. Engineers demonstrate an electric vehicle assembly line.\n\n" +
        "A storm strikes an Italian coastal city. Residents move through flooded streets while emergency crews respond.\n\n" +
        "A doctor evaluates a new hospital technology. The medical team tests the device in a modern clinical ward.")
    },
    script_params: { target_words: 260, skip_quality_gate: true, use_memory: true },
    output: { extract_entities: true, generate_metadata: false, save_to_db: true },
    media_plan: {
      mode: "search",
      providers: { artlist: true, internet_images: true },
      extraction: { enabled: true }
    }
  }]
}')

result_file=""
LAST_JOB_ID=""
run_generation() {
    local label="$1" payload="$2" key="$3"
    local response="$WORK_DIR/${label}_dispatch.json"
    smoke_curl POST "/api/script/generate" -H "Idempotency-Key: $key" --data "$payload" -o "$response" >/dev/null
    [[ "$SMOKE_LAST_HTTP" == "202" ]] || {
        smoke_echo_safe "dispatch $label HTTP=$SMOKE_LAST_HTTP $(head -c 800 "$response")" >&2
        return 1
    }
    LAST_JOB_ID=$(jq -r '.job_id // empty' "$response")
    [[ -n "$LAST_JOB_ID" ]] || { echo "missing job_id for $label" >&2; return 1; }
    smoke_poll_terminal "$LAST_JOB_ID" || return 1
    [[ "$SMOKE_LAST_STATUS" == "completed" || "$SMOKE_LAST_STATUS" == "SUCCEEDED" ]] || {
        echo "job $LAST_JOB_ID ended in $SMOKE_LAST_STATUS" >&2
        smoke_echo_safe "$(head -c 1200 "$SMOKE_LAST_BODY")" >&2
        return 1
    }
    result_file="$WORK_DIR/${label}_full.json"
    cp "$SMOKE_LAST_BODY" "$result_file"
}

extract_result() {
    jq -c '.result.data.items[0].result // .result.items[0].result // .result.output // empty' "$1"
}

assert_segments() {
    local file="$1" artlist="$2" images="$3"
    local result
    result=$(extract_result "$file")
    [[ -n "$result" && "$result" != "null" ]] || { echo "missing generation result" >&2; return 1; }
    jq -e --argjson artlist "$artlist" --argjson images "$images" '
      (.segments | length) >= 3
      and ([.segments[].segment_id] | length) == ([.segments[].segment_id] | unique | length)
      and all(.segments[];
        (.segment_id | length) > 0 and (.text | length) > 0 and (.text_hash | length) > 0
        and (.insights.segment_id == .segment_id)
        and (.insights.text_hash == .text_hash)
        and ((.insights.entities | length) <= 5)
        and ((.insights.important_phrases | length) <= 5)
        and ((.insights.important_words | length) <= 5)
        and ((.insights.artlist_queries | length) <= 5)
        and ((.insights.image_queries | length) <= 5)
        and ((.insights.entities | map(.value) | map(select(length > 0)) | length) == (.insights.entities | map(.value) | unique | length))
        and ((.insights.important_phrases | map(select(length > 0)) | length) == (.insights.important_phrases | unique | length))
        and ((.insights.important_words | map(select(length > 0)) | length) == (.insights.important_words | unique | length))
        and ((.insights.artlist_queries | map(select(length > 0)) | length) == (.insights.artlist_queries | unique | length))
        and ((.insights.image_queries | map(select(length > 0)) | length) == (.insights.image_queries | unique | length))
        and (($artlist or (.cache.artlist == "BYPASSED")) and ($images or (.cache.internet_images == "BYPASSED")))
      )
    ' <<<"$result" >/dev/null

    if [[ "$artlist" == "true" ]]; then
        jq -e 'any(.segments[]; any(.assets.candidates[]?; .provider == "artlist" and (.asset_id|length)>0 and (.query|length)>0 and (.score >= 0)))' <<<"$result" >/dev/null
    else
        jq -e 'all(.segments[]; all(.assets.candidates[]?; .provider != "artlist"))' <<<"$result" >/dev/null
    fi
    if [[ "$images" == "true" ]]; then
        jq -e 'any(.segments[]; any(.assets.candidates[]?; (.provider != "artlist") and (.asset_id|length)>0 and (.source_url|length)>0 and (.query|length)>0))' <<<"$result" >/dev/null
    else
        jq -e 'all(.segments[]; all(.assets.candidates[]?; .provider != "internet_images"))' <<<"$result" >/dev/null
    fi
}

echo "VidRush E2E: canonical POST /api/script/generate"
run_generation first "$PAYLOAD" "$CASE_PREFIX-first"
first_job="$LAST_JOB_ID"
assert_segments "$result_file" true true
first_result=$(extract_result "$result_file")
first_ids=$(jq -c '[.segments[].segment_id]' <<<"$first_result")
first_assets=$(jq -c '[.segments[].assets.candidates[]?.asset_id] | sort' <<<"$first_result")

# Same body with a new idempotency key proves the application cache path
# rather than an HTTP idempotency replay.
run_generation warm "$PAYLOAD" "$CASE_PREFIX-warm"
second_job="$LAST_JOB_ID"
assert_segments "$result_file" true true
warm_result=$(extract_result "$result_file")
jq -e --argjson ids "$first_ids" --argjson assets "$first_assets" '
  ([.segments[].segment_id] == $ids)
  and ([.segments[].assets.candidates[]?.asset_id] | sort == $assets)
  and (all(.segments[]; (.cache.extraction == "HIT_EXACT" or .cache.extraction == "MISS")))
' <<<"$warm_result" >/dev/null

# Toggle matrix: each request still goes through script.generate. Disabled
# providers must be absent from candidates and explicitly BYPASSED.
for mode in none artlist_only images_only both; do
    artlist=false; images=false
    case "$mode" in
        artlist_only) artlist=true ;;
        images_only) images=true ;;
        both) artlist=true; images=true ;;
    esac
    case_payload=$(jq --argjson artlist "$artlist" --argjson images "$images" \
      '.items[0].id = ("vidrush-e2e-" + $mode) | .items[0].media_plan.providers.artlist = $artlist | .items[0].media_plan.providers.internet_images = $images' \
      --arg mode "$mode" <<<"$PAYLOAD")
    run_generation "toggle_${mode}" "$case_payload" "$CASE_PREFIX-$mode"
    assert_segments "$result_file" "$artlist" "$images"
done

# Verify concrete SQLite rows for every returned candidate when the database
# exposes the canonical media_assets table. A missing row is a hard failure:
# provider metadata alone is not a materialized asset.
result=$(extract_result "$result_file")
candidate_ids=$(jq -r '.segments[].assets.candidates[]?.asset_id // empty' <<<"$result" | sort -u)
if [[ -n "$candidate_ids" && -f "$DB_PATH" ]]; then
    while IFS= read -r asset_id; do
        [[ -n "$asset_id" ]] || continue
        count=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM media_assets WHERE id = '$(printf "%s" "$asset_id" | sed "s/'/''/g")';" 2>/dev/null || echo 0)
        [[ "$count" == "1" ]] || { echo "candidate $asset_id has no canonical SQLite row" >&2; exit 1; }
    done <<<"$candidate_ids"
fi

echo "PASS: jobs $first_job and $second_job; segments=$(jq '.segments|length' <<<"$first_result"); toggle matrix and warm replay validated"

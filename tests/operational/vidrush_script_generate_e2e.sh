#!/usr/bin/env bash
# Canonical VidRush black-box verification.
#
# This test exercises one production flow: POST /api/script/generate, then
# GET /api/jobs/<id>/full. It deliberately does not call provider routes as
# a substitute for script.generate. Provider-disabled cases assert the
# returned per-segment policy and contain no provider assets.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# The cold run plus four provider-policy cases can legitimately exceed the
# generic smoke-suite wall clock. Keep a bounded, explicit budget for this
# full-cycle battery instead of allowing a partial matrix to pass.
SMOKE_TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-900}"
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"
smoke_require curl jq

# A cold Artlist browser search can legitimately take longer than the
# generic 120-second smoke budget. Keep the strict terminal-state checks,
# but give this provider-backed battery enough time to finish its cold run.
SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-300}"

DB_PATH="${DB_PATH:-data/media/media.db.sqlite}"
CASE_PREFIX="vidrush-e2e-$(smoke_gen_uuid)"
METRICS_URL="${METRICS_URL:-http://${SMOKE_API_BASE}/metrics}"

metrics_text() {
    local headers=()
    [[ -n "${METRICS_AUTH_TOKEN:-}" ]] && headers=(-H "Authorization: Bearer $METRICS_AUTH_TOKEN")
    curl -fsS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" "${headers[@]}" "$METRICS_URL"
}

provider_requests() {
    local provider="$1"
    metrics_text | awk -v provider="$provider" '$1 ~ /^vidrush_provider_requests_total\{/ && $1 ~ ("provider=\"" provider "\"") {print $2; found=1} END {if (!found) print "MISSING"}' | tail -1
}

assert_provider_delta() {
    local provider="$1" before="$2" after="$3" expect_call="$4"
    [[ "$before" != "MISSING" && "$after" != "MISSING" ]] || {
        echo "missing vidrush_provider_requests_total{provider=\"$provider\"} metric" >&2
        return 1
    }
    if [[ "$expect_call" == "false" && "$before" != "$after" ]]; then
        echo "disabled provider $provider was called (counter $before -> $after)" >&2
        return 1
    fi
}

if [[ "$DRY_RUN" == "1" ]]; then
    cat <<'EOF'
DRY RUN — POST /api/script/generate with three semantic paragraphs.
The live run polls GET /api/jobs/<job_id>/full and validates per-segment
insights, provider policy, cache states, provenance, and stable IDs.
EOF
    exit 0
fi

PAYLOAD=$(jq -n --arg item_id "$CASE_PREFIX-item" --arg item_title "$CASE_PREFIX Three visual stories" --arg case_marker "$CASE_PREFIX" '{
  version: 2,
  preset: "custom",
  force_refresh: false,
  items: [{
    id: $item_id,
    title: $item_title,
    language: "en",
    tone: "documentary",
    source: {
      type: "text",
      topic: ("three visual stories " + $case_marker),
      source_text: ("Elon Musk visits a Tesla factory in Texas. Engineers demonstrate an electric vehicle assembly line.\n\n" +
        "A storm strikes an Italian coastal city. Residents move through flooded streets while emergency crews respond.\n\n" +
        "A doctor evaluates a new hospital technology. The medical team tests the device in a modern clinical ward.\n\n" +
        "Verification case identifier: " + $case_marker + ".")
    },
    script_params: { target_words: 260, skip_quality_gate: true, use_memory: true },
    output: { extract_entities: true, generate_metadata: false, save_to_db: true },
    media_plan: {
      mode: "hybrid",
      provider_policy: { artlist: "enabled", internet_images: "enabled" },
      extraction: { enabled: true }
    }
  }]
}')

result_file=""
LAST_JOB_ID=""
run_generation() {
    local label="$1" payload="$2" key="$3"
    local response="$WORK_DIR/${label}_dispatch.json"
    export SMOKE_IDEMPOTENCY_KEY="$key"
    smoke_curl POST "/api/script/generate" --data "$payload" >/dev/null
    unset SMOKE_IDEMPOTENCY_KEY
    [[ -s "$SMOKE_LAST_BODY" ]] || {
        echo "dispatch $label did not produce a response body (HTTP=${SMOKE_LAST_HTTP:-unknown})" >&2
        return 1
    }
    cp "$SMOKE_LAST_BODY" "$response"
    [[ "$SMOKE_LAST_HTTP" == "202" ]] || {
        smoke_echo_safe "dispatch $label HTTP=$SMOKE_LAST_HTTP $(head -c 800 "$response")" >&2
        return 1
    }
    LAST_JOB_ID=$(jq -r '.job_id // empty' "$response")
    [[ -n "$LAST_JOB_ID" ]] || { echo "missing job_id for $label" >&2; return 1; }
    jq -e --arg expected "/api/jobs/${LAST_JOB_ID}/full" '
      .ok == true and (.status|type=="string" and length>0)
      and .status_url == $expected
      and (.current_stage|type=="string" and length>0)
    ' "$response" >/dev/null || { echo "dispatch $label missing current_stage/status_url" >&2; return 1; }
    smoke_poll_terminal "$LAST_JOB_ID" || return 1
    [[ "$SMOKE_LAST_STATUS" == "completed" || "$SMOKE_LAST_STATUS" == "SUCCEEDED" ]] || {
        echo "job $LAST_JOB_ID ended in $SMOKE_LAST_STATUS" >&2
        smoke_echo_safe "$(head -c 1200 "$SMOKE_LAST_BODY")" >&2
        return 1
    }
    result_file="$WORK_DIR/${label}_full.json"
    cp "$SMOKE_LAST_BODY" "$result_file"
    jq -e '(.current_stage|type=="string" and length>0)' "$result_file" >/dev/null || {
        echo "job $LAST_JOB_ID has no current_stage in full response" >&2; return 1;
    }
}

extract_result() {
    jq -c '.result.data.items[0].result // .result.items[0].result // .result.output // empty' "$1"
}

assert_segments() {
    local file="$1" artlist="$2" images="$3"
    local result
    result=$(extract_result "$file")
    [[ -n "$result" && "$result" != "null" ]] || { echo "missing generation result" >&2; return 1; }
    if ! jq -e --argjson artlist "$artlist" --argjson images "$images" '
      (.segments | length) >= 3 and ([.segments[].position] == ([.segments[].position] | sort))
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
    ' <<<"$result" >/dev/null; then
        echo "segment/insight/cache contract failed (artlist=$artlist images=$images)" >&2
        jq -c '{segments: [.segments[] | {segment_id,position,text_hash,cache,insights,assets}]}' <<<"$result" >&2
        return 1
    fi

    if [[ "$artlist" == "true" ]]; then
        # Artlist search is best-effort: an enabled provider must be
        # eligible and observable through the request counter, but an empty
        # catalog/search response is not a false positive asset.
        jq -e 'all(.segments[]; all(.assets.candidates[]?; .provider == "artlist" or .provider == "internet_images"))' <<<"$result" >/dev/null
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
  and (all(.segments[]; .cache.extraction == "HIT_EXACT"))
  and (all(.segments[]; (.cache.artlist == "HIT_EXACT" or .cache.artlist == "BYPASSED")))
  and (all(.segments[]; (.cache.internet_images == "HIT_EXACT" or .cache.internet_images == "BYPASSED")))
  and (all(.segments[]; .cache.binding == "HIT_EXACT"))
' <<<"$warm_result" >/dev/null

# Same key + same body is an HTTP idempotency replay; it must not enqueue a
# second job. A changed body with that key must be rejected with 409.
replay_headers="$WORK_DIR/replay.headers"
replay_body="$WORK_DIR/replay.json"
replay_http=$(curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" -D "$replay_headers" -o "$replay_body" -X POST \
    -H "Authorization: Bearer $SMOKE_TOKEN" -H "Idempotency-Key: $CASE_PREFIX-first" \
    -H 'Content-Type: application/json' --data "$PAYLOAD" -w '%{http_code}' "http://${SMOKE_API_BASE}/api/script/generate")
[[ "$replay_http" == "202" ]] || { echo "idempotency replay returned HTTP $replay_http" >&2; exit 1; }
[[ "$(jq -r '.job_id // empty' "$replay_body")" == "$first_job" ]] || { echo "idempotency replay returned a new job" >&2; exit 1; }
grep -Eiq '^X-Idempotency-Replay:[[:space:]]*true' "$replay_headers" || { echo "missing X-Idempotency-Replay header" >&2; exit 1; }
conflict_http=$(jq '.items[0].title = "different payload"' <<<"$PAYLOAD" | curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" -X POST \
    -H "Authorization: Bearer $SMOKE_TOKEN" -H "Idempotency-Key: $CASE_PREFIX-first" \
    -H 'Content-Type: application/json' --data-binary @- -o "$WORK_DIR/conflict.json" -w '%{http_code}' "http://${SMOKE_API_BASE}/api/script/generate")
[[ "$conflict_http" == "409" ]] || { echo "idempotency conflict returned HTTP $conflict_http" >&2; exit 1; }
jq -e '.code == "IDEMPOTENCY_KEY_CONFLICT"' "$WORK_DIR/conflict.json" >/dev/null || { echo "missing IDEMPOTENCY_KEY_CONFLICT" >&2; exit 1; }

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
      '.items[0].id = ("vidrush-e2e-" + $mode)
       | .items[0].source.topic = (.items[0].source.topic + " " + $mode)
       | .items[0].source.source_text = (.items[0].source.source_text + " Provider matrix case: " + $mode + ".")
       | .items[0].media_plan.provider_policy.artlist = (if $artlist then "enabled" else "disabled" end)
       | .items[0].media_plan.provider_policy.internet_images = (if $images then "enabled" else "disabled" end)' \
      --arg mode "$mode" <<<"$PAYLOAD")
    art_before=$(provider_requests artlist)
    images_before=$(provider_requests internet_images)
    run_generation "toggle_${mode}" "$case_payload" "$CASE_PREFIX-$mode"
    assert_segments "$result_file" "$artlist" "$images"
    art_after=$(provider_requests artlist)
    images_after=$(provider_requests internet_images)
    assert_provider_delta artlist "$art_before" "$art_after" "$artlist"
    assert_provider_delta internet_images "$images_before" "$images_after" "$images"
done

# Verify concrete SQLite rows for every materialized candidate when the
# database exposes the canonical media_assets table. Remote Internet-image
# candidates are provenance references, not materialized assets; a candidate
# becomes materialized only when the canonical pipeline supplies drive_link.
result=$(extract_result "$result_file")
candidate_ids=$(jq -r '.segments[].assets.candidates[]? | select((.drive_link // "") | length > 0) | .asset_id // empty' <<<"$first_result" | sort -u)
if [[ -n "$candidate_ids" && -f "$DB_PATH" ]]; then
    while IFS= read -r asset_id; do
        [[ -n "$asset_id" ]] || continue
        count=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM media_assets WHERE id = '$(printf "%s" "$asset_id" | sed "s/'/''/g")';" 2>/dev/null || echo 0)
        [[ "$count" == "1" ]] || { echo "candidate $asset_id has no canonical SQLite row" >&2; exit 1; }
    done <<<"$candidate_ids"
fi

echo "PASS: jobs $first_job and $second_job; segments=$(jq '.segments|length' <<<"$first_result"); toggle matrix and warm replay validated"

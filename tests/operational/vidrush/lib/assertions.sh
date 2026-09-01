#!/usr/bin/env bash
# tests/operational/vidrush/lib/assertions.sh — result assertions.
#
# Source-only phase library. `vidrush_assert_result` preserves the runner's
# assertion/report contract while exposing derived values through VIDRUSH_*
# variables, avoiding command substitutions that would hide assertion logs.
# Required commands: jq, curl.

VIDRUSH_ASSERT_FAIL=0
VIDRUSH_SEG_COUNT=0
VIDRUSH_ENTITY_COUNT=0
VIDRUSH_SEGMENTS_WITH_ENTITIES=0
VIDRUSH_ENTITY_VALUES_TOTAL=0
VIDRUSH_ENTITY_PHRASES_TOTAL=0
VIDRUSH_IMPORTANT_WORDS_TOTAL=0
VIDRUSH_BINDING_COUNT=0
VIDRUSH_UNRESOLVED_COUNT=0
VIDRUSH_CACHE_MODE=""
VIDRUSH_ARTIFACT_JSON='{}'
VIDRUSH_PROVIDER_REQUESTS=0

vidrush_provider_counter() {
    local metrics_url="$1" provider="$2" value
    value=$(curl -fsS --max-time 8 -H "Authorization: Bearer ${METRICS_AUTH_TOKEN:-${SMOKE_TOKEN:-}}" "$metrics_url" 2>/dev/null \
        | awk -v provider="$provider" '$1 ~ /^vidrush_provider_requests_total\\{/ && $1 ~ ("provider=\"" provider "\"") {print $2}' | tail -1) || true
    if [[ "$value" =~ ^[0-9]+$ ]]; then
        printf '%s' "$value"
    else
        printf 'MISSING'
    fi
}

vidrush_assert_segments() {
    local result="$1" scenario_file="$2"
    local min_segments exact_segments exact_entities exact_artlist_queries exact_image_queries expected_total
    local max_artlist_queries
    local expected_positions expected_unique_segment_ids expected_unique_text_hashes expected_unique_texts expected_visual_profiles
    local visual_profile_expectations

    VIDRUSH_SEG_COUNT=$(jq '[.segments[]?] | length' <<<"$result")
    VIDRUSH_ENTITY_COUNT=$(jq '[.segments[]?.insights.entities[]?] | length' <<<"$result")
    VIDRUSH_SEGMENTS_WITH_ENTITIES=$(jq '[.segments[]? | select((.insights.entities // []) | length > 0)] | length' <<<"$result")
    VIDRUSH_ENTITY_VALUES_TOTAL="$VIDRUSH_ENTITY_COUNT"
    VIDRUSH_ENTITY_PHRASES_TOTAL=$(jq '[.segments[]?.insights.important_phrases[]?] | length' <<<"$result")
    VIDRUSH_IMPORTANT_WORDS_TOTAL=$(jq '[.segments[]?.insights.important_words[]?] | length' <<<"$result")
    VIDRUSH_BINDING_COUNT=$(jq '[.segments[]? | select(.assets.primary_video != null or ([.assets.secondary_images[]? | select(.drive_link != "" and .acquisition_status == "acquired" and .verification_status == "verified" and .persistence_status == "persisted" and .index_status == "indexed")] | length) > 0)] | length' <<<"$result")
    VIDRUSH_UNRESOLVED_COUNT=$(jq '[.segments[]? | select(.assets.primary_video == null and (.assets.candidates | length) == 0)] | length' <<<"$result")

    echo "  → running assertions on $VIDRUSH_SEG_COUNT segment(s)"
    min_segments=$(jq -r '.assertions.result.min_segments // 0' "$scenario_file")
    if (( VIDRUSH_SEG_COUNT < min_segments )); then
        printf '%sFAIL%s expected at least %s segment(s), got %s\n' "$RED" "$RESET" "$min_segments" "$VIDRUSH_SEG_COUNT"
        VIDRUSH_ASSERT_FAIL=1
    fi
    exact_segments=$(jq -r '.expect.exact_segments // 0' "$scenario_file")
    if [[ "$exact_segments" -gt 0 ]] && [[ "$VIDRUSH_SEG_COUNT" -ne "$exact_segments" ]]; then
        printf '%sFAIL%s expected exactly %s segment(s), got %s\n' "$RED" "$RESET" "$exact_segments" "$VIDRUSH_SEG_COUNT"
        VIDRUSH_ASSERT_FAIL=1
    fi

    expected_unique_segment_ids=$(jq -r '.expect.expected_unique_segment_ids // 0' "$scenario_file")
    if [[ "$expected_unique_segment_ids" -gt 0 ]] && ! jq -e --argjson expected "$expected_unique_segment_ids" '([.segments[].segment_id] | unique | length) == $expected' <<<"$result" >/dev/null; then
        printf '%sFAIL%s expected %s unique segment_id value(s)\n' "$RED" "$RESET" "$expected_unique_segment_ids"
        VIDRUSH_ASSERT_FAIL=1
    fi

    expected_unique_text_hashes=$(jq -r '.expect.expected_unique_text_hashes // 0' "$scenario_file")
    if [[ "$expected_unique_text_hashes" -gt 0 ]] && ! jq -e --argjson expected "$expected_unique_text_hashes" '([.segments[].text_hash] | unique | length) == $expected' <<<"$result" >/dev/null; then
        printf '%sFAIL%s expected %s unique text_hash value(s)\n' "$RED" "$RESET" "$expected_unique_text_hashes"
        VIDRUSH_ASSERT_FAIL=1
    fi

    expected_unique_texts=$(jq -r '.expect.expected_unique_texts // 0' "$scenario_file")
    if [[ "$expected_unique_texts" -gt 0 ]] && ! jq -e --argjson expected "$expected_unique_texts" '([.segments[].text] | unique | length) == $expected' <<<"$result" >/dev/null; then
        printf '%sFAIL%s expected %s unique segment text value(s)\n' "$RED" "$RESET" "$expected_unique_texts"
        VIDRUSH_ASSERT_FAIL=1
    fi

    expected_visual_profiles=$(jq -r '.expect.require_visual_profiles // false' "$scenario_file")
    if [[ "$expected_visual_profiles" == "true" ]] && ! jq -e '
      all(.segments[];
        ((.insights.visual_profile.subject // "") | length) > 0
        and ((.insights.visual_profile.action // "") | length) > 0
        and ((.insights.visual_profile.context // "") | length) > 0
        and ((.insights.visual_profile.terms // []) | length) > 0
        and ((.insights.visual_profile.subject | ascii_downcase) | test("generic|food|cuisine|meal") | not)
      )
    ' <<<"$result" >/dev/null; then
        printf '%sFAIL%s every segment must expose a specific visual Planner profile with subject/action/context/terms\n' "$RED" "$RESET"
        VIDRUSH_ASSERT_FAIL=1
    fi

    visual_profile_expectations=$(jq -c '.expect.visual_profile_expectations // []' "$scenario_file")
    if [[ "$visual_profile_expectations" != "[]" ]] && ! jq -e --argjson expected "$visual_profile_expectations" '
      . as $in | all($expected[];
        . as $want
        | any($in.segments[];
            . as $segment
            | ($segment.segment_id == $want.segment_id)
            and (((($segment.insights.visual_profile.subject // "") | ascii_downcase) | contains(($want.subject_contains | ascii_downcase))))
            and (any(($want.action_contains_any // [])[];
              . as $term
              | (((($segment.insights.visual_profile.action // "") | ascii_downcase) | contains(($term | ascii_downcase))))
            ))
            and (any(($want.context_contains_any // [])[];
              . as $term
              | (((($segment.insights.visual_profile.context // "") | ascii_downcase) | contains(($term | ascii_downcase))))
            ))
            and (all(($want.required_terms // [])[];
              . as $term
              | any(($segment.insights.visual_profile.terms // [])[];
                  . as $visual
                  | (((($visual // "") | ascii_downcase) | contains(($term | ascii_downcase))))
                )
            ))
          )
      )
    ' <<<"$result" >/dev/null; then
        printf '%sFAIL%s visual Planner profile is missing scene-specific subject/action/context/terms\n' "$RED" "$RESET"
        VIDRUSH_ASSERT_FAIL=1
    fi

    expected_positions=$(jq -c '.expect.expected_positions // []' "$scenario_file")
    if [[ "$expected_positions" != "[]" ]] && ! jq -e --argjson expected "$expected_positions" '[.segments[].position] == $expected' <<<"$result" >/dev/null; then
        printf '%sFAIL%s expected segment positions exactly %s\n' "$RED" "$RESET" "$expected_positions"
        VIDRUSH_ASSERT_FAIL=1
    fi

    exact_entities=$(jq -r '.expect.exact_entities_per_segment // 0' "$scenario_file")
    exact_artlist_queries=$(jq -r '.expect.exact_artlist_queries_per_segment // 0' "$scenario_file")
    max_artlist_queries=$(jq -r '.expect.max_artlist_queries_per_segment // 0' "$scenario_file")
    exact_image_queries=$(jq -r '.expect.exact_image_queries_per_segment // 0' "$scenario_file")
    if [[ "$exact_entities" -gt 0 ]] && ! jq -e --argjson exact "$exact_entities" 'all(.segments[]; (.insights.entities | length) == $exact)' <<<"$result" >/dev/null; then
        printf '%sFAIL%s every segment must contain exactly %s extracted entit(y/ies)\n' "$RED" "$RESET" "$exact_entities"
        VIDRUSH_ASSERT_FAIL=1
    fi
    if [[ "$exact_artlist_queries" -gt 0 ]] && ! jq -e --argjson exact "$exact_artlist_queries" 'all(.segments[]; (.insights.artlist_queries | length) == $exact)' <<<"$result" >/dev/null; then
        printf '%sFAIL%s every segment must contain exactly %s Artlist quer(y/ies)\n' "$RED" "$RESET" "$exact_artlist_queries"
        VIDRUSH_ASSERT_FAIL=1
    fi
    if [[ "$max_artlist_queries" -gt 0 ]] && ! jq -e --argjson max "$max_artlist_queries" 'all(.segments[]; (.insights.artlist_queries | length) <= $max)' <<<"$result" >/dev/null; then
        printf '%sFAIL%s every segment must contain at most %s Artlist quer(y/ies)\n' "$RED" "$RESET" "$max_artlist_queries"
        VIDRUSH_ASSERT_FAIL=1
    fi
    if [[ "$exact_image_queries" -gt 0 ]] && ! jq -e --argjson exact "$exact_image_queries" 'all(.segments[]; (.insights.image_queries | length) == $exact)' <<<"$result" >/dev/null; then
        printf '%sFAIL%s every segment must contain exactly %s image quer(y/ies)\n' "$RED" "$RESET" "$exact_image_queries"
        VIDRUSH_ASSERT_FAIL=1
    fi
    expected_total=$(jq -r '.expect.expected_total_entities // 0' "$scenario_file")
    if [[ "$expected_total" -gt 0 ]] && ! jq -e --argjson expected "$expected_total" '[.segments[]?.insights.entities[]?] | length == $expected' <<<"$result" >/dev/null; then
        printf '%sFAIL%s expected exactly %s extracted entities overall\n' "$RED" "$RESET" "$expected_total"
        VIDRUSH_ASSERT_FAIL=1
    fi
    if ! jq -e '
      (.segments | length) >= 1
      and ([.segments[].position] == ([.segments[].position] | sort))
      and (([.segments[].segment_id] | unique | length) == ([.segments[].segment_id] | length))
      and all(.segments[];
        (.segment_id | length) > 0
        and (.text | length) > 0
        and (.text_hash | length) > 0
      )
    ' <<<"$result" >/dev/null; then
        printf '%sFAIL%s segment structural contract failed\n' "$RED" "$RESET"
        VIDRUSH_ASSERT_FAIL=1
    fi
}

vidrush_assert_entities() {
    local result="$1" scenario_file="$2" expect_entities
    expect_entities=$(jq -r '.expect.max_entities_per_segment // 0' "$scenario_file")
    if [[ "$expect_entities" != "0" ]]; then
        if ! jq -e --argjson max "$expect_entities" '
          all(.segments[]; (.insights.entities | length) <= $max)
          and all(.segments[]; all(.insights.entities[]?; .type != null and (.type | length) > 0))
        ' <<<"$result" >/dev/null; then
            printf '%sFAIL%s entity assertions failed (max=%s, entities=%s)\n' "$RED" "$RESET" "$expect_entities" "$VIDRUSH_ENTITY_COUNT"
            VIDRUSH_ASSERT_FAIL=1
        fi
    fi
}

vidrush_assert_artlist_isolation() {
    local result="$1"

    # Artlist queries are segment-owned. A duplicate query across segments
    # makes cache/provider results ambiguous and is rejected before binding.
    if ! jq -e '
      def norm: ascii_downcase | gsub("^\\s+|\\s+$"; "") | gsub("\\s+"; " ");
      [ .segments[] as $segment
        | ($segment.insights.artlist_queries // [])[]?
        | select(type == "string" and length > 0)
        | {segment: $segment.segment_id, query: (norm)}
      ]
      | group_by(.query)
      | all(.[]; ([.[].segment] | unique | length) == 1)
    ' <<<"$result" >/dev/null; then
        printf '%sFAIL%s Artlist query contamination detected across segments\n' "$RED" "$RESET"
        VIDRUSH_ASSERT_FAIL=1
    fi

    # Every Artlist candidate and winner must carry the owning segment
    # envelope and, when queries are declared, use one of that segment's
    # queries. This catches a foreign provider response even if the asset
    # itself is otherwise durable.
    if ! jq -e '
      def norm: ascii_downcase | gsub("^\\s+|\\s+$"; "") | gsub("\\s+"; " ");
      def candidate_ok($segment):
        (.segment_id // "") == ($segment.segment_id // "")
        and (.position // -1) == ($segment.position // -1)
        and (.text_hash // "") == ($segment.text_hash // "")
        and (.asset_id // "") != ""
        and (.provider // "") == "artlist"
        and (.query // "") != ""
        and (
          (($segment.insights.artlist_queries // []) | length) == 0
          or ((.query | norm) as $query |
              (($segment.insights.artlist_queries // []) | map(norm) | index($query)) != null)
        );
      all(.segments[];
        . as $segment |
        all(($segment.assets.candidates // [])[] | select(.provider == "artlist"); candidate_ok($segment))
        and all(([$segment.assets.primary_video] | map(select(. != null)))[] | select(.provider == "artlist"); candidate_ok($segment))
      )
    ' <<<"$result" >/dev/null; then
        printf '%sFAIL%s Artlist candidate/winner provenance or query ownership failed\n' "$RED" "$RESET"
        VIDRUSH_ASSERT_FAIL=1
    fi

    # The same Artlist asset may not be bound to more than one segment.
    if ! jq -e '
      [ .segments[] as $segment
        | ([($segment.assets.candidates // [])[], $segment.assets.primary_video?][])
        | select(.provider == "artlist" and (.asset_id // "") != "")
        | {asset_id: (.asset_id | ascii_downcase), segment: $segment.segment_id}
      ]
      | group_by(.asset_id)
      | all(.[]; ([.[].segment] | unique | length) == 1)
    ' <<<"$result" >/dev/null; then
        printf '%sFAIL%s Artlist asset winner/candidate is reused across segments\n' "$RED" "$RESET"
        VIDRUSH_ASSERT_FAIL=1
    fi
}

vidrush_assert_provider_disabled() {
    local scenario_file="$1" metrics_url="$2" artlist_before="$3" images_before="$4"
    local youtube_before="${5:-MISSING}" generation_before="${6:-MISSING}"
    local expect_artlist expect_images expect_youtube expect_generation
    local artlist_after images_after youtube_after generation_after

    expect_artlist=$(jq -r '.expect.provider_requests_artlist // -1' "$scenario_file")
    expect_images=$(jq -r '.expect.provider_requests_internet_images // -1' "$scenario_file")
    expect_youtube=$(jq -r '.expect.provider_requests_youtube // -1' "$scenario_file")
    expect_generation=$(jq -r '.expect.provider_requests_image_generation // -1' "$scenario_file")
    if [[ "$expect_artlist" == "0" && "$artlist_before" != "MISSING" ]]; then
        artlist_after=$(curl -fsS --max-time 8 -H "Authorization: Bearer ${METRICS_AUTH_TOKEN:-$SMOKE_TOKEN}" "$metrics_url" 2>/dev/null | awk '$1 ~ /^vidrush_provider_requests_total\{/ && $1 ~ /provider="artlist"/ {print $2}' | tail -1) || true
        if [[ "$artlist_before" != "$artlist_after" ]]; then
            printf '%sFAIL%s artlist provider was called but should be disabled (counter %s → %s)\n' "$RED" "$RESET" "$artlist_before" "$artlist_after"
            VIDRUSH_ASSERT_FAIL=1
        else
            printf '  %sPASS%s artlist provider not called (counter unchanged: %s)\n' "$GREEN" "$RESET" "$artlist_before"
        fi
    fi
    if [[ "$expect_images" == "0" && "$images_before" != "MISSING" ]]; then
        images_after=$(curl -fsS --max-time 8 -H "Authorization: Bearer ${METRICS_AUTH_TOKEN:-$SMOKE_TOKEN}" "$metrics_url" 2>/dev/null | awk '$1 ~ /^vidrush_provider_requests_total\{/ && $1 ~ /provider="internet_images"/ {print $2}' | tail -1) || true
        if [[ "$images_before" != "$images_after" ]]; then
            printf '%sFAIL%s internet_images provider was called but should be disabled (counter %s → %s)\n' "$RED" "$RESET" "$images_before" "$images_after"
            VIDRUSH_ASSERT_FAIL=1
        else
            printf '  %sPASS%s internet_images provider not called (counter unchanged: %s)\n' "$GREEN" "$RESET" "$images_before"
        fi
    fi
    if [[ "${expect_youtube:- -1}" == "0" && "$youtube_before" != "MISSING" ]]; then
        local youtube_after
        youtube_after=$(vidrush_provider_counter "$metrics_url" "youtube")
        if [[ "$youtube_before" != "$youtube_after" ]]; then
            printf '%sFAIL%s youtube provider was called but should be disabled (counter %s → %s)\n' "$RED" "$RESET" "$youtube_before" "$youtube_after"
            VIDRUSH_ASSERT_FAIL=1
        else
            printf '  %sPASS%s youtube provider not called (counter unchanged: %s)\n' "$GREEN" "$RESET" "$youtube_before"
        fi
    fi
    if [[ "${expect_generation:- -1}" == "0" && "$generation_before" != "MISSING" ]]; then
        local generation_after
        generation_after=$(vidrush_provider_counter "$metrics_url" "image_generation")
        if [[ "$generation_before" != "$generation_after" ]]; then
            printf '%sFAIL%s image_generation provider was called but should be disabled (counter %s → %s)\n' "$RED" "$RESET" "$generation_before" "$generation_after"
            VIDRUSH_ASSERT_FAIL=1
        else
            printf '  %sPASS%s image_generation provider not called (counter unchanged: %s)\n' "$GREEN" "$RESET" "$generation_before"
        fi
    fi
}

vidrush_assert_live_media() {
    local result="$1" scenario_file="$2" min_images exact_images expected_total allowed_provider

    # Live acceptance is deliberately fail-closed. Prose in a scenario is not
    # evidence: durable media must carry lifecycle proof and a Drive link.
    if [[ "$(jq -r '.expect.require_primary_per_segment // false' "$scenario_file")" == "true" ]]; then
        if ! jq -e 'all(.segments[]; .assets.primary_video != null and (.assets.primary_video.drive_link | length) > 0 and .assets.primary_video.acquisition_status == "acquired" and .assets.primary_video.verification_status == "verified" and .assets.primary_video.persistence_status == "persisted" and .assets.primary_video.index_status == "indexed" and .assets.primary_video.rights_status == "verified")' <<<"$result" >/dev/null; then
            printf '%sFAIL%s live Artlist acceptance: every segment needs a persisted, indexed, verified primary video\n' "$RED" "$RESET"
            VIDRUSH_ASSERT_FAIL=1
        fi
    fi
    if [[ "$(jq -r '.expect.min_secondary_images_per_segment // 0' "$scenario_file")" -gt 0 ]]; then
        min_images=$(jq -r '.expect.min_secondary_images_per_segment' "$scenario_file")
        if ! jq -e --argjson min "$min_images" 'all(.segments[]; ([.assets.secondary_images[]? | select((.drive_link // "") != "" and .acquisition_status == "acquired" and .verification_status == "verified" and .persistence_status == "persisted" and .index_status == "indexed")] | length) >= $min)' <<<"$result" >/dev/null; then
            printf '%sFAIL%s live image acceptance: every segment needs at least %s durable technically valid images\n' "$RED" "$RESET" "$min_images"
            VIDRUSH_ASSERT_FAIL=1
        fi
    fi

    exact_images=$(jq -r '.expect.exact_secondary_images_per_segment // 0' "$scenario_file")
    if [[ "$exact_images" -gt 0 ]]; then
        if ! jq -e --argjson exact "$exact_images" 'all(.segments[]; ([.assets.secondary_images[]? | select((.drive_link // "") != "" and .acquisition_status == "acquired" and .verification_status == "verified" and .persistence_status == "persisted" and .index_status == "indexed")] | length) == $exact)' <<<"$result" >/dev/null; then
            printf '%sFAIL%s live image acceptance: every segment needs exactly %s durable selected images\n' "$RED" "$RESET" "$exact_images"
            VIDRUSH_ASSERT_FAIL=1
        else
            printf '  %sPASS%s exactly %s durable selected image(s) per segment\n' "$GREEN" "$RESET" "$exact_images"
        fi

        expected_total=$(jq -r '.expect.expected_total_secondary_images // 0' "$scenario_file")
        if [[ "$expected_total" -gt 0 ]] && ! jq -e --argjson expected "$expected_total" '[.segments[]?.assets.secondary_images[]? | select((.drive_link // "") != "" and .acquisition_status == "acquired" and .verification_status == "verified" and .persistence_status == "persisted" and .index_status == "indexed")] | length == $expected' <<<"$result" >/dev/null; then
            printf '%sFAIL%s expected exactly %s selected images overall\n' "$RED" "$RESET" "$expected_total"
            VIDRUSH_ASSERT_FAIL=1
        fi

        allowed_provider=$(jq -r '.expect.allowed_image_provider // "internet_images"' "$scenario_file")
        if ! jq -e --arg provider "$allowed_provider" 'all([.segments[]?.assets.secondary_images[]?][]; .provider == $provider)' <<<"$result" >/dev/null; then
            printf '%sFAIL%s selected images contain a provider other than %s\n' "$RED" "$RESET" "$allowed_provider"
            VIDRUSH_ASSERT_FAIL=1
        else
            printf '  %sPASS%s selected images use only provider=%s\n' "$GREEN" "$RESET" "$allowed_provider"
        fi

        if ! jq -e 'all(.segments[]; (([.assets.secondary_images[]?.asset_id] | length) == ([.assets.secondary_images[]?.asset_id] | unique | length)) and (([.assets.secondary_images[]?.query] | length) == ([.assets.secondary_images[]?.query] | unique | length)))' <<<"$result" >/dev/null; then
            printf '%sFAIL%s selected images contain duplicate asset IDs or duplicate entity queries within a segment\n' "$RED" "$RESET"
            VIDRUSH_ASSERT_FAIL=1
        fi
    fi
}

vidrush_derive_artifact_evidence() {
    local result="$1"
    VIDRUSH_CACHE_MODE=$(jq -r '[.segments[]?.cache.extraction // "UNKNOWN"] | if all(.[]; . == "HIT_EXACT") then "warm" elif all(.[]; . == "MISS") then "cold" else "mixed" end' <<<"$result")
    VIDRUSH_ARTIFACT_JSON=$(jq -c '
      [
        .segments[]?.assets.primary_video?,
        .segments[]?.assets.secondary_images[]?,
        .segments[]?.assets.generated_images[]?
      ] | map(select(. != null))
      | if length == 0 then
          {sqlite_verified:false, qdrant_verified:false, drive_verified:false, render_verified:false}
        else
          {
            sqlite_verified: all(.[]; .persistence_status == "persisted"),
            qdrant_verified: all(.[]; .index_status == "indexed"),
            drive_verified: all(.[]; ((.drive_link // "") | length) > 0),
            render_verified: false
          }
        end' <<<"$result")
}

vidrush_count_provider_requests() {
    local metrics_url="$1" artlist_before="$2" images_before="$3"
    local artlist_after images_after
    VIDRUSH_PROVIDER_REQUESTS=0
    if [[ "$artlist_before" != "MISSING" ]]; then
        artlist_after=$(vidrush_provider_counter "$metrics_url" "artlist")
        if [[ "$artlist_before" =~ ^[0-9]+$ && "$artlist_after" =~ ^[0-9]+$ ]]; then
            VIDRUSH_PROVIDER_REQUESTS=$((VIDRUSH_PROVIDER_REQUESTS + artlist_after - artlist_before))
        fi
    fi
    if [[ "$images_before" != "MISSING" ]]; then
        images_after=$(vidrush_provider_counter "$metrics_url" "internet_images")
        if [[ "$images_before" =~ ^[0-9]+$ && "$images_after" =~ ^[0-9]+$ ]]; then
            VIDRUSH_PROVIDER_REQUESTS=$((VIDRUSH_PROVIDER_REQUESTS + images_after - images_before))
        fi
    fi
}

vidrush_assert_result() {
    local result="$1" scenario_file="$2" metrics_url="$3" artlist_before="$4" images_before="$5"
    local youtube_before="${6:-MISSING}" generation_before="${7:-MISSING}"
    VIDRUSH_ASSERT_FAIL=0
    vidrush_assert_segments "$result" "$scenario_file"
    vidrush_assert_entities "$result" "$scenario_file"
    vidrush_assert_artlist_isolation "$result"
    vidrush_assert_provider_disabled "$scenario_file" "$metrics_url" "$artlist_before" "$images_before" "$youtube_before" "$generation_before"
    vidrush_assert_live_media "$result" "$scenario_file"
    vidrush_derive_artifact_evidence "$result"
    vidrush_count_provider_requests "$metrics_url" "$artlist_before" "$images_before"
}

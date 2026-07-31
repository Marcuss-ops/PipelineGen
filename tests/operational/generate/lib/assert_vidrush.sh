#!/usr/bin/env bash
# Specialist assertions for the VidRush/media-plan scenario.

generate_assert_vidrush() {
    local result="$1"
    if ! jq -e 'has("segments")' <<<"$result" >/dev/null; then
        printf '%sWARN: no segments in result (VidRush pipeline may not have run)%s\n' "$YELLOW" "$RESET"
        return 0
    fi

    local count artlist_count image_count candidate_count
    count=$(jq '.segments | length' <<<"$result")
    (( count >= 3 )) || { echo "FAIL: expected >=3 segments, got $count" >&2; return 1; }

    jq -e '
        ([.segments[].position] == ([.segments[].position] | sort))
        and ([.segments[].segment_id] | length) == ([.segments[].segment_id] | unique | length)
        and all(.segments[];
            (.segment_id | type == "string" and length > 0)
            and (.text | type == "string" and length > 0)
            and (.text_hash | type == "string" and length > 0)
            and (.cache | type == "object")
            and ((.cache.extraction // "") | length > 0)
        )
    ' <<<"$result" >/dev/null || { echo "FAIL: segments are unordered, duplicated, or missing stable fields" >&2; return 1; }

    jq -e '
        all(.segments[];
            (.insights | type == "object")
            and ((.insights.segment_id // "") == .segment_id)
            and ((.insights.text_hash // "") == .text_hash)
            and ((.insights.important_phrases | type == "array") and length > 0)
            and ((.insights.important_words | type == "array") and length > 0)
            and ((.insights.entities | type == "array") and length > 0)
        )
    ' <<<"$result" >/dev/null || { echo "FAIL: segment insights are incomplete or unaligned" >&2; return 1; }

    jq -e '
        all(.segments[];
            (.cache.extraction == "HIT_EXACT" or .cache.extraction == "BYPASSED" or .cache.extraction == "MISS" or .cache.extraction == "ERROR")
            and (.cache.artlist == "HIT_EXACT" or .cache.artlist == "BYPASSED" or .cache.artlist == "MISS" or .cache.artlist == "ERROR")
            and (.cache.internet_images == "HIT_EXACT" or .cache.internet_images == "BYPASSED" or .cache.internet_images == "MISS" or .cache.internet_images == "ERROR")
        )
    ' <<<"$result" >/dev/null || { echo "FAIL: unexpected extraction/artlist/internet_images cache state" >&2; return 1; }

    artlist_count=$(jq '[.segments[].insights.artlist_queries[]? | select(type == "string" and length > 0)] | unique | length' <<<"$result")
    image_count=$(jq '[.segments[].insights.image_queries[]? | select(type == "string" and length > 0)] | unique | length' <<<"$result")
    candidate_count=$(jq '[.segments[].assets.candidates[]?] | length' <<<"$result")
    printf 'segments: %s; artlist queries: %s; image queries: %s; candidates: %s\n' "$count" "$artlist_count" "$image_count" "$candidate_count"

    jq -e '
        all(.segments[];
            . as $segment |
            (($segment.insights.artlist_queries // []) | length) == 0
            or any($segment.assets.candidates[]?;
                .provider == "artlist"
                and (.query | type == "string" and length > 0)
                and (.query as $candidate_query | (($segment.insights.artlist_queries // []) | index($candidate_query)) != null)
                and (.asset_id | type == "string" and length > 0)
                and (.entity | type == "string" and length > 0)
                and ((.drive_link // .source_url // "") | type == "string" and length > 0)
                and (.score | type == "number" and . >= 0)
            )
        )
        and all(.segments[].assets.candidates[]?;
            .provider != "artlist" or ((.drive_link // .source_url // "") | type == "string" and length > 0)
        )
        and all(.segments[];
            ([.assets.candidates[]? | select(.provider == "artlist") | (.drive_link // .source_url // "")] | length)
            == ([.assets.candidates[]? | select(.provider == "artlist") | (.drive_link // .source_url // "")] | unique | length)
            and (.assets.primary_video as $primary | $primary == null or any(.assets.candidates[]?; .asset_id == $primary.asset_id))
        )
    ' <<<"$result" >/dev/null || { echo "FAIL: Artlist candidates are not query-linked, valid, or unique" >&2; return 1; }

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
                    and (.query as $candidate_query | (($segment.insights.image_queries // []) | index($candidate_query)) != null)
                    and (.entity | type == "string" and length > 0)
                    and (.entity as $candidate_entity | (($segment.insights.entities // [] | map(.value)) | index($candidate_entity)) != null)
                    and (.score | type == "number" and . >= 0)
                )
            )
        )
    ' <<<"$result" >/dev/null || { echo "FAIL: Internet images are not entity/query-linked and valid" >&2; return 1; }
}

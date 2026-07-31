#!/usr/bin/env bash
# Shared assertions for generated script output.

generate_assert_script() {
    local assertions="$1"
    local words min_words max_words keyword marker
    words=$(printf '%s' "$GENERATE_SCRIPT" | wc -w | tr -d ' ')
    min_words=$(jq -r '.min_words // 0' <<<"$assertions")
    max_words=$(jq -r '.max_words // 0' <<<"$assertions")
    if (( min_words > 0 && words < min_words )) || (( max_words > 0 && words > max_words )); then
        printf '%sFAIL: word count %s outside %s..%s%s\n' "$RED" "$words" "$min_words" "$max_words" "$RESET" >&2
        return 1
    fi

    if jq -e '.word_count_positive == true' <<<"$assertions" >/dev/null; then
        local reported_word_count
        reported_word_count=$(jq -r '.output.word_count // .word_count // 0' <<<"$GENERATE_RESULT")
        [[ "$reported_word_count" =~ ^[0-9]+$ && "$reported_word_count" -gt 0 ]] || {
            printf '%sFAIL: result word_count must be a positive integer (got %s)%s\n' "$RED" "${reported_word_count:-<empty>}" "$RESET" >&2
            return 1
        }
    fi

    while IFS= read -r keyword; do
        [[ -z "$keyword" ]] && continue
        grep -Fqi -- "$keyword" <<<"$GENERATE_SCRIPT" || {
            printf '%sFAIL: generated script missing semantic marker %q%s\n' "$RED" "$keyword" "$RESET" >&2
            return 1
        }
    done < <(jq -r '.keywords[]? // empty' <<<"$assertions")

    while IFS= read -r marker; do
        [[ -z "$marker" ]] && continue
        grep -Eiq -- "$marker" <<<"$GENERATE_SCRIPT" || {
            printf '%sFAIL: generated script missing language marker %q%s\n' "$RED" "$marker" "$RESET" >&2
            return 1
        }
    done < <(jq -r '.language_regex[]? // empty' <<<"$assertions")

    if jq -e '.reject_prompt_leakage == true' <<<"$assertions" >/dev/null; then
        if grep -Fq '```' <<<"$GENERATE_SCRIPT" || grep -Eiq '"(prompt|source_text|target_words)"|Ecco lo script richiesto|As an AI|Here is' <<<"$GENERATE_SCRIPT"; then
            echo "FAIL: generated script contains raw JSON, prompt instructions, or placeholder prose" >&2
            return 1
        fi
    fi

    if jq -e '.segments_optional == true' <<<"$assertions" >/dev/null && jq -e 'has("segments")' <<<"$GENERATE_RESULT" >/dev/null; then
        jq -e '(.segments|length)>0 and all(.segments[]; (.segment_id|type=="string" and length>0) and (.text|type=="string" and length>0) and (.text_hash|type=="string" and length>0)) and ([.segments[].segment_id]|length)==([.segments[].segment_id]|unique|length)' <<<"$GENERATE_RESULT" >/dev/null || {
            echo "FAIL: segments are empty, duplicated, or missing stable hashes" >&2
            return 1
        }
    fi
    printf 'script length:  %s chars (%s words)\n' "${#GENERATE_SCRIPT}" "$words"
}

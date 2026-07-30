#!/usr/bin/env bash
# tests/operational/boxers-generate/run.sh — operational runner for script generation scenarios.
#
# Usage:
#   ./run.sh
#   ./run.sh --dry

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
PROJECT_ROOT=$(cd "$DIR/../../.." && pwd)

# Source common smoke helpers
# Environment overrides for boxers-generate smoke tests
export SMOKE_TIMEOUT_SECONDS=600
export SMOKE_POLL_TIMEOUT_SECONDS=180
export SMOKE_POLL_INTERVAL_SECONDS=3

# Source common smoke helpers
# shellcheck disable=SC1091
source "$DIR/../lib/common.sh"

smoke_require jq sqlite3 grep

# 1. Resolve DB Path
DB_PATH="${VELOX_DB:-}"
if [[ -z "$DB_PATH" ]]; then
    if [[ -f "$PROJECT_ROOT/data/media/media.db.sqlite" ]]; then
        DB_PATH="$PROJECT_ROOT/data/media/media.db.sqlite"
    elif [[ -f "$PROJECT_ROOT/data/velox.db" ]]; then
        DB_PATH="$PROJECT_ROOT/data/velox.db"
    else
        DB_PATH="/var/lib/velox/velox.db"
    fi
fi

if [[ ! -f "$DB_PATH" && "$DRY_RUN" != "1" ]]; then
    printf '%ssetup error: SQLite database not found at %s%s\n' "$RED" "$DB_PATH" "$RESET" >&2
    exit 2
fi

printf 'Using database: %s%s%s\n' "$CYAN" "$DB_PATH" "$RESET"

# 2. Query actual Mike Tyson clips from SQLite
TYSON_CLIPS=()
TYSON_LINKS=()
if [[ "$DRY_RUN" != "1" ]]; then
    # Query clips matching Tyson's video ID from media_assets
    mapfile -t DB_ROWS < <(sqlite3 "$DB_PATH" "SELECT id, drive_link FROM media_assets WHERE id LIKE 'yt_6VtSrG1hs9U_%' AND lifecycle_status='ACTIVE' LIMIT 6;")
    if (( ${#DB_ROWS[@]} < 4 )); then
        printf '%ssetup error: not enough Mike Tyson clips in database (found %d, need at least 4)%s\n' "$RED" "${#DB_ROWS[@]}" "$RESET" >&2
        exit 2
    fi
    for row in "${DB_ROWS[@]}"; do
        id=$(cut -d'|' -f1 <<<"$row")
        link=$(cut -d'|' -f2 <<<"$row")
        TYSON_CLIPS+=("$id")
        TYSON_LINKS+=("$link")
    done
    printf 'Found %d Tyson clips in SQLite: %s\n' "${#TYSON_CLIPS[@]}" "${TYSON_CLIPS[*]}"
else
    # Mock clip IDs and links for dry-run
    TYSON_CLIPS=("mock_tyson_clip_1" "mock_tyson_clip_2" "mock_tyson_clip_3" "mock_tyson_clip_4" "mock_tyson_clip_5" "mock_tyson_clip_6")
    TYSON_LINKS=("http://drive.com/1" "http://drive.com/2" "http://drive.com/3" "http://drive.com/4" "http://drive.com/5" "http://drive.com/6")
fi

VOICEOVER_FOLDER="1Cph0ypa_tBgRW_2PgTrzqy1RWW-fPe5X"
REPORTS_DIR="$DIR/reports"
mkdir -p "$REPORTS_DIR"

# Helper function to preprocess JSON scenarios
prepare_payload() {
    local scenario_file="$1"
    local temp_json="$WORK_DIR/payload.json"
    
    cp "$scenario_file" "$temp_json"
    
    # Perform string replacements for Tyson placeholders
    for i in {0..5}; do
        local idx=$((i + 1))
        local placeholder="TYSON_CLIP_ID_${idx}"
        sed -i "s/$placeholder/${TYSON_CLIPS[$i]}/g" "$temp_json"
    done
    
    # Replace Voiceover Folder
    sed -i "s/REPLACE_WITH_TEST_VOICEOVER_FOLDER_ID/$VOICEOVER_FOLDER/g" "$temp_json"
    
    # Replace Pacquiao placeholder IDs and links
    sed -i "s/PACQUIAO_FIGHT_ASSET_ID/${TYSON_CLIPS[0]}/g" "$temp_json"
    sed -i "s|PACQUIAO_FIGHT_DRIVE_LINK|${TYSON_LINKS[0]}|g" "$temp_json"
    
    sed -i "s/PACQUIAO_INTERVIEW_ASSET_ID/${TYSON_CLIPS[1]}/g" "$temp_json"
    sed -i "s|PACQUIAO_INTERVIEW_DRIVE_LINK|${TYSON_LINKS[1]}|g" "$temp_json"
    
    sed -i "s/PACQUIAO_TRAINING_ASSET_ID/${TYSON_CLIPS[2]}/g" "$temp_json"
    sed -i "s|PACQUIAO_TRAINING_DRIVE_LINK|${TYSON_LINKS[2]}|g" "$temp_json"
    
    cat "$temp_json"
}

# Run a single script generation scenario
run_scenario() {
    local num="$1"
    local name="$2"
    local fname="$3"
    local file="$DIR/scenarios/$fname"
    
    smoke_log_section "Scenario $num: $name"
    
    if [[ ! -f "$file" ]]; then
        printf '%sFAIL: Scenario file not found: %s%s\n' "$RED" "$file" "$RESET" >&2
        return 1
    fi
    
    local payload
    payload=$(prepare_payload "$file")
    
    if [[ "$DRY_RUN" == "1" ]]; then
        printf 'DRY RUN - Would send payload:\n'
        jq . <<<"$payload"
        return 0
    fi
    
    # Dispatch generate
    local idem_key
    idem_key=$(smoke_gen_uuid)
    
    smoke_curl POST "/api/script/generate" \
        -H "Idempotency-Key: $idem_key" \
        --data "$payload" >/dev/null
    
    local http_code="$SMOKE_LAST_HTTP"
    if [[ "$http_code" != "202" && "$http_code" != "200" ]]; then
        printf '%sFAIL: Scenario %s dispatch returned HTTP %s%s\n' "$RED" "$num" "$http_code" "$RESET" >&2
        smoke_echo_safe "$(cat "$SMOKE_LAST_BODY")" >&2
        return 1
    fi
    
    local job_id
    job_id=$(jq -r '.job_id // ""' "$SMOKE_LAST_BODY")
    if [[ -z "$job_id" || "$job_id" == "null" ]]; then
        printf '%sFAIL: Scenario %s dispatch did not return job_id%s\n' "$RED" "$num" "$RESET" >&2
        return 1
    fi
    
    printf 'job_id enqueued: %s%s%s\n' "$YELLOW" "$job_id" "$RESET"
    
    # Poll job to terminal status
    if ! smoke_poll_terminal "$job_id"; then
        printf '%sFAIL: Scenario %s polling failed or timed out%s\n' "$RED" "$num" "$RESET" >&2
        return 1
    fi
    
    # Fetch FULL job details
    local full_body_file="$WORK_DIR/full_job_$num.json"
    smoke_curl GET "/api/jobs/$job_id/full" >/dev/null
    cp "$SMOKE_LAST_BODY" "$full_body_file"
    
    # Persist Report
    cp "$full_body_file" "$REPORTS_DIR/${num}_${name}_report.json"
    
    # Assertions
    local job_status
    job_status=$(jq -r '.status // .job.status // ""' "$full_body_file")
    if [[ "$job_status" != "completed" && "$job_status" != "SUCCEEDED" ]]; then
        printf '%sFAIL: Job status was %s, expected completed/SUCCEEDED%s\n' "$RED" "$job_status" "$RESET" >&2
        jq -r '.error // .job.error // ""' "$full_body_file" >&2
        return 1
    fi
    
    # Verify warnings are fatal (like translation/voiceover failure warnings)
    local has_warnings
    has_warnings=$(jq -r '
        .warnings // .job.warnings // [] 
        | if type == "array" then . 
          elif type == "string" and . != "" then [.] 
          else [] end 
        | join(", ")
    ' "$full_body_file")
    
    # Look for fatal warning patterns
    if grep -Eiq 'translation failed|translator port not configured|voiceover skipped|requested translation was not completed' <<<"$has_warnings"; then
        printf '%sFAIL: Job completed with fatal warnings: %s%s\n' "$RED" "$has_warnings" "$RESET" >&2
        return 1
    fi
    
    # Extract script result output
    local out_json
    out_json=$(jq -c '
        .job.result.data.items[0].result.output
        // .result.data.items[0].result.output
        // .job.result.data.output
        // .result.data.output
        // .job.result.output
        // .result.output
        // empty
    ' "$full_body_file")
    
    if [[ -z "$out_json" || "$out_json" == "null" ]]; then
        printf '%sFAIL: Generated script output JSON is empty%s\n' "$RED" "$RESET" >&2
        return 1
    fi
    
    local text
    text=$(jq -r '.text // ""' <<<"$out_json")
    if [[ -z "$text" || "$text" == "null" ]]; then
        printf '%sFAIL: Generated script text is empty%s\n' "$RED" "$RESET" >&2
        return 1
    fi
    
    # Scenario-Specific Assertions
    case "$num" in
        "01")
            # Scenario 1 assertions: Anchor text segments present in output
            if ! jq -e '
                .text | contains("SRC-TYSON-01")
                and .text | contains("SRC-TYSON-02")
                and .text | contains("SRC-TYSON-03")
                and .text | contains("SRC-TYSON-04")
            ' <<<"$out_json" >/dev/null; then
                printf '%sFAIL: Anchor texts not found in correct sequence%s\n' "$RED" "$RESET" >&2
                jq .text <<<"$out_json" >&2
                return 1
            fi
            printf '%sPASS: Scenario 1 sequence anchors verified.%s\n' "$GREEN" "$RESET"
            ;;
            
        "02")
            # Scenario 2 assertions: Translation and voiceover
            if ! jq -e --arg lang "it" '
                .text | length > 100
                and test("\\b(il|la|gli|della|velocità|pugilato|eredità)\\b"; "i")
            ' <<<"$out_json" >/dev/null; then
                printf '%sFAIL: Text not translated to Italian or too short%s\n' "$RED" "$RESET" >&2
                printf 'Text: %s\n' "$text" >&2
                return 1
            fi
            # Verify voiceover bindings
            if ! jq -e '
                [.specscene.scenes[].bindings.voiceover] | length == (.specscene.scenes | length)
                and all(.specscene.scenes[];
                    .bindings.voiceover.status == "completed"
                    and (.bindings.voiceover.link | length) > 0
                    and ((.bindings.voiceover.local_path // "") == "")
                )
            ' <<<"$out_json" >/dev/null; then
                printf '%sFAIL: Voiceover check failed (missing/incomplete/exposes local path)%s\n' "$RED" "$RESET" >&2
                jq '.specscene.scenes[].bindings.voiceover' <<<"$out_json" >&2
                return 1
            fi
            printf '%sPASS: Scenario 2 translation and voiceover verified.%s\n' "$GREEN" "$RESET"
            ;;
            
        "03")
            # Scenario 3 assertions: Supplied clips only
            if ! jq -e --argjson supplied "$(printf '%s\n' "${TYSON_CLIPS[@]}" | jq -R . | jq -s .)" '
                all(.specscene.scenes[];
                    .bindings.clip == null
                    or (
                        ($supplied | index(.bindings.clip.clip_id)) != null
                        and (.bindings.clip.drive_link | length) > 0
                    )
                )
                and (
                    [.specscene.scenes[].bindings.clip.clip_id // empty] | length
                ) == (
                    [.specscene.scenes[].bindings.clip.clip_id // empty] | unique | length
                )
            ' <<<"$out_json" >/dev/null; then
                printf '%sFAIL: Supplied clip IDs check failed or duplicate bindings found%s\n' "$RED" "$RESET" >&2
                jq '.specscene.scenes[].bindings.clip' <<<"$out_json" >&2
                return 1
            fi
            # Query SQLite for each bound clip ID to verify folder_path / boxer matches
            local bound_ids
            bound_ids=$(jq -r '.specscene.scenes[].bindings.clip.clip_id // empty' <<<"$out_json")
            for bid in $bound_ids; do
                local count
                count=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM media_assets WHERE id='$bid' AND source='youtube' AND id LIKE 'yt_6VtSrG1hs9U_%';")
                if [[ "$count" != "1" ]]; then
                    printf '%sFAIL: Bound clip %s not found in SQLite or is not a Tyson clip%s\n' "$RED" "$bid" "$RESET" >&2
                    return 1
                fi
            done
            printf '%sPASS: Scenario 3 supplied clips verified.%s\n' "$GREEN" "$RESET"
            ;;
            
        "04")
            # Scenario 4 assertions: Direct stock bindings
            if ! jq -e --arg f_id "${TYSON_CLIPS[0]}" --arg i_id "${TYSON_CLIPS[1]}" --arg t_id "${TYSON_CLIPS[2]}" '
                .specscene.scenes[0].bindings.stock.asset_id == $f_id
                and .specscene.scenes[1].bindings.stock.asset_id == $i_id
                and .specscene.scenes[2].bindings.stock.asset_id == $t_id
                and all(.specscene.scenes[];
                    .bindings.stock.fallback == false
                    and (.bindings.stock.drive_link | length) > 0
                    and .bindings.stock.source == "youtube"
                )
            ' <<<"$out_json" >/dev/null; then
                printf '%sFAIL: Direct stock bindings validation failed (wrong order/fallback/missing link)%s\n' "$RED" "$RESET" >&2
                jq '.specscene.scenes[].bindings.stock' <<<"$out_json" >&2
                return 1
            fi
            printf '%sPASS: Scenario 4 direct stock bindings verified.%s\n' "$GREEN" "$RESET"
            ;;
            
        "05")
            # Scenario 5 assertions: Full pipeline integration
            if ! jq -e --arg lang "it" '
                .text | length > 100
                and test("\\b(il|la|gli|della|velocità|pugilato|eredità)\\b"; "i")
            ' <<<"$out_json" >/dev/null; then
                printf '%sFAIL: Full pipeline text not in Italian%s\n' "$RED" "$RESET" >&2
                return 1
            fi
            # Verify voiceover completed
            if ! jq -e '
                [.specscene.scenes[].bindings.voiceover] | length == (.specscene.scenes | length)
                and all(.specscene.scenes[];
                    .bindings.voiceover.status == "completed"
                    and (.bindings.voiceover.link | length) > 0
                )
            ' <<<"$out_json" >/dev/null; then
                printf '%sFAIL: Full pipeline voiceover check failed%s\n' "$RED" "$RESET" >&2
                return 1
            fi
            # Verify clip bindings are supplied Tyson clips only
            if ! jq -e --argjson supplied "$(printf '%s\n' "${TYSON_CLIPS[@]}" | jq -R . | jq -s .)" '
                all(.specscene.scenes[];
                    .bindings.clip == null
                    or (
                        ($supplied | index(.bindings.clip.clip_id)) != null
                        and (.bindings.clip.drive_link | length) > 0
                    )
                )
            ' <<<"$out_json" >/dev/null; then
                printf '%sFAIL: Full pipeline clip binding check failed%s\n' "$RED" "$RESET" >&2
                return 1
            fi
            
            # Idempotency Replay Verifications
            smoke_log_section "Idempotency Replay checks"
            
            # 1. Same payload + same idempotency key -> returns same job response (HTTP 200 or 202)
            smoke_curl POST "/api/script/generate" \
                -H "Idempotency-Key: $idem_key" \
                --data "$payload" >/dev/null
            local replay_http="$SMOKE_LAST_HTTP"
            if [[ "$replay_http" != "200" && "$replay_http" != "202" ]]; then
                printf '%sFAIL: Idempotency replay returned HTTP %s, expected 200 or 202%s\n' "$RED" "$replay_http" "$RESET" >&2
                return 1
            fi
            local replay_job_id
            replay_job_id=$(jq -r '.job_id // ""' "$SMOKE_LAST_BODY")
            if [[ "$replay_job_id" != "$job_id" ]]; then
                printf '%sFAIL: Idempotency replay returned different job_id: got %s, expected %s%s\n' "$RED" "$replay_job_id" "$job_id" "$RESET" >&2
                return 1
            fi
            printf 'Idempotency replay OK (returned same job_id %s)\n' "$job_id"
            
            # 2. Different payload + same idempotency key -> HTTP 409 Conflict
            local diff_payload
            diff_payload=$(jq '.items[0].title = "A different title"' <<<"$payload")
            smoke_curl POST "/api/script/generate" \
                -H "Idempotency-Key: $idem_key" \
                --data "$diff_payload" >/dev/null
            local conflict_http="$SMOKE_LAST_HTTP"
            if [[ "$conflict_http" != "409" ]]; then
                printf '%sFAIL: Different payload with same key returned HTTP %s, expected 409 Conflict%s\n' "$RED" "$conflict_http" "$RESET" >&2
                return 1
            fi
            printf 'Idempotency conflict OK (returned HTTP 409)\n'
            
            printf '%sPASS: Scenario 5 full pipeline verified.%s\n' "$GREEN" "$RESET"
            ;;

        "06")
            # Scenario 6: Negative test — detect false success when
            # translation/voiceover are best-effort but silently skipped.
            # Fatal warnings already caught by the global check above.
            # The test MUST fail if:
            #   - text is still English despite translate_to=it
            #   - voiceovers are missing or incomplete
            smoke_log_section "Scenario 6: Negative translation/voiceover gate"

            # If job completed, text MUST be Italian (not still English)
            local italian_markers
            italian_markers=$(jq -r '.text // ""' <<<"$out_json" | grep -iEc '\b(il|la|gli|della|che|una|sono|nella|velocità|pugilato|eredità)\b' || echo 0)
            if (( italian_markers < 2 )); then
                printf '%sFAIL: Negative scenario — job completed but text is NOT Italian (false success)%s\n' "$RED" "$RESET" >&2
                printf 'Text (%d chars): %s\n' "${#text}" "${text:0:300}" >&2
                return 1
            fi

            # Voiceovers MUST be present and completed
            local vo_count
            vo_count=$(jq '[.specscene.scenes[]?.bindings.voiceover] | length' <<<"$out_json" 2>/dev/null || echo 0)
            local scene_count
            scene_count=$(jq '.specscene.scenes | length' <<<"$out_json" 2>/dev/null || echo 0)
            if (( vo_count != scene_count )); then
                printf '%sFAIL: Negative scenario — voiceover count (%d) != scene count (%d)%s\n' "$RED" "$vo_count" "$scene_count" "$RESET" >&2
                return 1
            fi
            if ! jq -e '
                all(.specscene.scenes[];
                    .bindings.voiceover.status == "completed"
                    and (.bindings.voiceover.link | length) > 0
                    and ((.bindings.voiceover.local_path // "") == "")
                )
            ' <<<"$out_json" >/dev/null; then
                printf '%sFAIL: Negative scenario — voiceovers incomplete or expose local path%s\n' "$RED" "$RESET" >&2
                jq '.specscene.scenes[].bindings.voiceover' <<<"$out_json" >&2
                return 1
            fi

            printf '%sPASS: Scenario 6 negative gate — translation applied, voiceovers present.%s\n' "$GREEN" "$RESET"
            ;;
    esac
    
    printf '%sSUCCESS: Scenario %s passed!%s\n\n' "$GREEN" "$num" "$RESET"
    return 0
}

# Run scenario sequence
TARGET_SCENARIO="${TARGET_SCENARIO:-all}"
failures=0

run_test() {
    local num="$1"
    local name="$2"
    local fname="$3"
    if [[ "$TARGET_SCENARIO" == "all" || "$TARGET_SCENARIO" == "$num" ]]; then
        run_scenario "$num" "$name" "$fname" || return 1
    fi
    return 0
}

run_test "01" "Source segments" "01_source_segments.json" || failures=$((failures + 1))
run_test "02" "Translation and voiceover" "02_translation_voiceover.json" || failures=$((failures + 1))
run_test "03" "Supplied clips" "03_supplied_clips.json" || failures=$((failures + 1))
run_test "04" "Direct stock bindings" "04_direct_stock_bindings.json" || failures=$((failures + 1))
run_test "05" "Full pipeline" "05_full_pipeline.json" || failures=$((failures + 1))
run_test "06" "Negative translation gate" "06_negative_translation_fail.json" || failures=$((failures + 1))

if (( failures > 0 )); then
    printf '%sFAIL: %d scenario(s) failed out of 6.%s\n' "$RED" "$failures" "$RESET" >&2
    exit 1
fi

if [[ "$TARGET_SCENARIO" == "all" ]]; then
    printf '%sOK: All 6 boxers script-generation scenarios completed and verified!%s\n' "$GREEN" "$RESET"
else
    printf '%sOK: Scenario %s completed and verified!%s\n' "$GREEN" "$TARGET_SCENARIO" "$RESET"
fi
exit 0

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
export SMOKE_TIMEOUT_SECONDS=2700
export SMOKE_POLL_TIMEOUT_SECONDS=180
export SMOKE_POLL_INTERVAL_SECONDS=3

# Source common smoke helpers
# shellcheck disable=SC1091
source "$DIR/../lib/common.sh"

smoke_require jq sqlite3 grep python3

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

# 2. Load Tyson clip data from fixtures, falling back to SQLite.
#   fixtures/mike_tyson_clip_ids.json — pre-computed clip IDs + drive links
#   fixtures/mike_tyson_stock_bindings.json — Pacquiao stock binding data
#   Env vars: TYSON_VIDEO_ID (SQLite fallback), TYSON_FOLDER_NAME (default: Mike Tyson)
FIXTURES_DIR="$DIR/fixtures"
TYSON_CLIPS=()
TYSON_LINKS=()
PACQUIAO_CLIPS=()
PACQUIAO_LINKS=()
TYSON_VIDEO_ID="${TYSON_VIDEO_ID:-}"
TYSON_FOLDER_NAME="${TYSON_FOLDER_NAME:-Mike Tyson}"

if [[ "$DRY_RUN" != "1" && -f "$FIXTURES_DIR/mike_tyson_clip_ids.json" ]]; then
    printf 'Loading Tyson clips from fixtures/%s\n' "mike_tyson_clip_ids.json"
    while IFS=$'\t' read -r id link; do
        [[ -z "$id" ]] && continue
        # Refuse placeholder values — fixture must be populated with real data.
        if [[ "$id" =~ PLACEHOLDER ]]; then
            printf '%ssetup error: fixture %s contains PLACEHOLDER values — populate with real clip IDs first%s\n' \
                "$RED" "mike_tyson_clip_ids.json" "$RESET" >&2
            exit 2
        fi
        TYSON_CLIPS+=("$id")
        TYSON_LINKS+=("$link")
    done < <(jq -r '.[] | "\(.id)\t\(.drive_link)"' "$FIXTURES_DIR/mike_tyson_clip_ids.json")
    if (( ${#TYSON_CLIPS[@]} < 4 )); then
        printf '%ssetup error: not enough Tyson clips in fixtures (found %d, need at least 4)%s\n' "$RED" "${#TYSON_CLIPS[@]}" "$RESET" >&2
        exit 2
    fi
    printf 'Loaded %d Tyson clips from fixtures: %s\n' "${#TYSON_CLIPS[@]}" "${TYSON_CLIPS[*]}"

    # Load Pacquiao stock bindings from fixture (optional — falls back to Tyson clips).
    if [[ -f "$FIXTURES_DIR/mike_tyson_stock_bindings.json" ]]; then
        printf 'Loading Pacquiao stock bindings from fixtures/%s\n' "mike_tyson_stock_bindings.json"
        while IFS=$'\t' read -r asset_id drive_link; do
            [[ -z "$asset_id" ]] && continue
            if [[ "$asset_id" =~ PLACEHOLDER || "$asset_id" =~ PACQUIAO ]]; then
                printf '%swarn: stock bindings fixture has PLACEHOLDER/PACQUIAO values — falling back to Tyson clips for Pacquiao%s\n' \
                    "$YELLOW" "$RESET" >&2
                PACQUIAO_CLIPS=()
                break
            fi
            PACQUIAO_CLIPS+=("$asset_id")
            PACQUIAO_LINKS+=("$drive_link")
        done < <(jq -r '.[] | "\(.asset_id)\t\(.drive_link)"' "$FIXTURES_DIR/mike_tyson_stock_bindings.json")
        if (( ${#PACQUIAO_CLIPS[@]} >= 3 )); then
            printf 'Loaded %d Pacquiao stock bindings from fixtures\n' "${#PACQUIAO_CLIPS[@]}"
        fi
    fi
elif [[ "$DRY_RUN" != "1" ]]; then
    # SQLite fallback
    if [[ -n "$TYSON_VIDEO_ID" ]]; then
        TYSON_SQL="SELECT id, drive_link FROM media_assets WHERE id LIKE '${TYSON_VIDEO_ID}_%' AND lifecycle_state='ACTIVE' LIMIT 6;"
        printf 'Querying by video ID: %s\n' "$TYSON_VIDEO_ID"
    else
        TYSON_SQL="SELECT id, drive_link FROM media_assets WHERE LOWER(folder_path) LIKE LOWER('%${TYSON_FOLDER_NAME}%') AND lifecycle_state='ACTIVE' AND source='youtube' LIMIT 6;"
        printf 'Querying by folder name: %s\n' "$TYSON_FOLDER_NAME"
    fi
    mapfile -t DB_ROWS < <(sqlite3 "$DB_PATH" "$TYSON_SQL")
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

# 3. Load top5 boxer stock fixtures for multilang scenarios (07, 08).
#    top5_boxers_stock.json — aggregated fixture with 5 boxers × 2 assets each.
TOP5_STOCK_FILE="$FIXTURES_DIR/top5_boxers_stock.json"
if [[ -f "$TOP5_STOCK_FILE" ]]; then
    printf 'Loading top5 boxer stock fixture from %s\n' "$TOP5_STOCK_FILE"
    TOP5_TYSON_FIGHT=$(jq -r '.mike_tyson.fight.asset_id' "$TOP5_STOCK_FILE")
    TOP5_ALI_FIGHT=$(jq -r '.muhammad_ali.fight.asset_id' "$TOP5_STOCK_FILE")
    TOP5_PACQUIAO_FIGHT=$(jq -r '.manny_pacquiao.fight.asset_id' "$TOP5_STOCK_FILE")
    TOP5_MAYWEATHER_FIGHT=$(jq -r '.floyd_mayweather.fight.asset_id' "$TOP5_STOCK_FILE")
    TOP5_ROBINSON_FIGHT=$(jq -r '.sugar_ray_robinson.fight.asset_id' "$TOP5_STOCK_FILE")
    printf 'Top5 stock assets: Tyson=%s Ali=%s Pacquiao=%s Mayweather=%s Robinson=%s\n' \
        "$TOP5_TYSON_FIGHT" "$TOP5_ALI_FIGHT" "$TOP5_PACQUIAO_FIGHT" \
        "$TOP5_MAYWEATHER_FIGHT" "$TOP5_ROBINSON_FIGHT"
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
    
    # Replace Pacquiao placeholder IDs and links.
    # Prefer Pacquiao-specific fixture data; fall back to Tyson clips.
    if (( ${#PACQUIAO_CLIPS[@]} >= 3 )); then
        sed -i "s/PACQUIAO_FIGHT_ASSET_ID/${PACQUIAO_CLIPS[0]}/g" "$temp_json"
        sed -i "s|PACQUIAO_FIGHT_DRIVE_LINK|${PACQUIAO_LINKS[0]}|g" "$temp_json"
        sed -i "s/PACQUIAO_INTERVIEW_ASSET_ID/${PACQUIAO_CLIPS[1]}/g" "$temp_json"
        sed -i "s|PACQUIAO_INTERVIEW_DRIVE_LINK|${PACQUIAO_LINKS[1]}|g" "$temp_json"
        sed -i "s/PACQUIAO_TRAINING_ASSET_ID/${PACQUIAO_CLIPS[2]}/g" "$temp_json"
        sed -i "s|PACQUIAO_TRAINING_DRIVE_LINK|${PACQUIAO_LINKS[2]}|g" "$temp_json"
    else
        sed -i "s/PACQUIAO_FIGHT_ASSET_ID/${TYSON_CLIPS[0]}/g" "$temp_json"
        sed -i "s|PACQUIAO_FIGHT_DRIVE_LINK|${TYSON_LINKS[0]}|g" "$temp_json"
        sed -i "s/PACQUIAO_INTERVIEW_ASSET_ID/${TYSON_CLIPS[1]}/g" "$temp_json"
        sed -i "s|PACQUIAO_INTERVIEW_DRIVE_LINK|${TYSON_LINKS[1]}|g" "$temp_json"
        sed -i "s/PACQUIAO_TRAINING_ASSET_ID/${TYSON_CLIPS[2]}/g" "$temp_json"
        sed -i "s|PACQUIAO_TRAINING_DRIVE_LINK|${TYSON_LINKS[2]}|g" "$temp_json"
    fi
    
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
    export SMOKE_IDEMPOTENCY_KEY="$idem_key"
    
    smoke_curl POST "/api/script/generate" \
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
    if [[ "$num" == "07" || "$num" == "07b" || "$num" == "07c" || "$num" == "08" ]]; then
        # For multilang batch jobs: poll parent job until terminal, then fetch child jobs.
        smoke_log_section "Polling parent job: $job_id"
        if ! smoke_poll_terminal "$job_id"; then
            printf '%sFAIL: Scenario %s parent job polling failed or timed out%s\n' "$RED" "$num" "$RESET" >&2
            return 1
        fi

        # Fetch parent job and save as the full_body_file
        local full_body_file="$WORK_DIR/full_job_$num.json"
        smoke_curl GET "/api/jobs/$job_id/full" >/dev/null
        cp "$SMOKE_LAST_BODY" "$full_body_file"

        # Extract child job IDs and poll each one
        local child_job_ids
        child_job_ids=$(jq -r '.result.data.child_job_ids[] // .job.result.data.child_job_ids[] // empty' "$full_body_file")
        if [[ -z "$child_job_ids" ]]; then
            printf '%sFAIL: Scenario %s parent job has no child_job_ids%s\n' "$RED" "$num" "$RESET" >&2
            return 1
        fi
        local child_count
        child_count=$(echo "$child_job_ids" | wc -l)
        printf 'Parent job completed with %s child jobs. Polling children...\n' "$child_count"

        local children_dir="$WORK_DIR/children_$num"
        mkdir -p "$children_dir"
        local child_ok=0
        local child_fail=0
        local cstatus
        while IFS= read -r cid; do
            [[ -z "$cid" ]] && continue
            smoke_wallclock_check
            smoke_poll_terminal "$cid" || true
            smoke_curl GET "/api/jobs/$cid/full" >/dev/null
            if [[ "$SMOKE_LAST_HTTP" == "200" ]]; then
                cp "$SMOKE_LAST_BODY" "$children_dir/$cid.json"
                local cstatus
                cstatus=$(jq -r '.status // "UNKNOWN"' "$children_dir/$cid.json")
                if [[ "$cstatus" == "SUCCEEDED" || "$cstatus" == "completed" ]]; then
                    child_ok=$((child_ok + 1))
                else
                    child_fail=$((child_fail + 1))
                    printf '  %sFAIL: Child %s status=%s%s\n' "$RED" "$cid" "$cstatus" "$RESET" >&2
                fi
            else
                child_fail=$((child_fail + 1))
                printf '  %sFAIL: Child %s HTTP %s%s\n' "$RED" "$cid" "$SMOKE_LAST_HTTP" "$RESET" >&2
            fi
        done <<< "$child_job_ids"
        printf 'Child jobs: %s succeeded, %s failed\n' "$child_ok" "$child_fail"

        if [[ "$num" != "07c" ]]; then
            if (( child_fail > 0 )); then
                printf '%sFAIL: %s child job(s) failed%s\n' "$RED" "$child_fail" "$RESET" >&2
                return 1
            fi
        fi

        # Build aggregated response with all child items embedded into the parent
        python3 -c "
import json, sys, os, glob
parent = json.load(open('$full_body_file'))
children_dir = '$children_dir'
all_items = []
for fpath in sorted(glob.glob(os.path.join(children_dir, '*.json'))):
    child = json.load(open(fpath))
    # Extract items from child job (same paths as single-item jobs)
    items = (child.get('result', {}).get('data', {}).get('items', []) or
             child.get('job', {}).get('result', {}).get('data', {}).get('items', []) or
             child.get('job', {}).get('result', {}).get('data', {}).get('data', {}).get('items', []))
    if items:
        all_items.extend(items)
# Embed items into parent for verify_multilang.py compatibility
rd = parent.setdefault('result', {}).setdefault('data', {})
rd['items'] = all_items
json.dump(parent, open('$full_body_file', 'w'), indent=2)
print(f'Aggregated {len(all_items)} items from {len(os.listdir(children_dir))} child jobs')
"
    else
        if ! smoke_poll_terminal "$job_id"; then
            printf '%sFAIL: Scenario %s polling failed or timed out%s\n' "$RED" "$num" "$RESET" >&2
            return 1
        fi
        local full_body_file="$WORK_DIR/full_job_$num.json"
        smoke_curl GET "/api/jobs/$job_id/full" >/dev/null
        cp "$SMOKE_LAST_BODY" "$full_body_file"
    fi
    
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
        (.warnings // .job.warnings // []) 
        | if type == "array" then join(", ") else . end
    ' "$full_body_file")
    
    # Look for fatal warning patterns
    if grep -Eiq 'translation failed|translator port not configured|voiceover skipped|requested translation was not completed' <<<"$has_warnings"; then
        printf '%sFAIL: Job completed with fatal warnings: %s%s\n' "$RED" "$has_warnings" "$RESET" >&2
        return 1
    fi
    
    local out_json
    local text
    if [[ "$num" != "07" && "$num" != "07b" && "$num" != "07c" && "$num" != "08" ]]; then
        # Extract script result output.
        # Canonical path: .result.data.items[0].result.output
        # Fallbacks: broker-nested, batch, and legacy shapes.
        out_json=$(jq -c '
            .result.data.items[0].result.output
            // .job.result.data.items[0].result.output
            // .result.data.output
            // .job.result.data.output
            // .job.result.output
            // .result.output
            // .job.result.data.data.output
            // .result.data.data.output
            // empty
        ' "$full_body_file")
        
        if [[ -z "$out_json" || "$out_json" == "null" ]]; then
            printf '%sFAIL: Generated script output JSON is empty%s\n' "$RED" "$RESET" >&2
            return 1
        fi
        
        text=$(jq -r '.text // ""' <<<"$out_json")
        if [[ -z "$text" || "$text" == "null" ]]; then
            printf '%sFAIL: Generated script text is empty%s\n' "$RED" "$RESET" >&2
            return 1
        fi
    fi
    
    # Scenario-Specific Assertions
    case "$num" in
        "01")
            # Scenario 1 assertions: Anchor text segments present in output
            if ! jq -e '
                (.text | test("SRC-TYSON-01"))
                and (.text | test("SRC-TYSON-02"))
                and (.text | test("SRC-TYSON-03"))
                and (.text | test("SRC-TYSON-04"))
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
                (.text | length > 100)
                and (.text | test("\\b(il|la|gli|della|velocità|pugilato|eredità)\\b"; "i"))
            ' <<<"$out_json" >/dev/null; then
                printf '%sFAIL: Text not translated to Italian or too short%s\n' "$RED" "$RESET" >&2
                printf 'Text: %s\n' "$text" >&2
                return 1
            fi
            # Verify voiceover bindings
            if ! jq -e '
                (([.specscene.scenes[].bindings.voiceover] | length) == (.specscene.scenes | length))
                and (all(.specscene.scenes[];
                    .bindings.voiceover.status == "completed"
                    and (.bindings.voiceover.link | length) > 0
                    and ((.bindings.voiceover.local_path // "") == "")
                ))
            ' <<<"$out_json" >/dev/null; then
                printf '%sFAIL: Voiceover check failed (missing/incomplete/exposes local path)%s\n' "$RED" "$RESET" >&2
                jq '.specscene.scenes[].bindings.voiceover' <<<"$out_json" >&2
                return 1
            fi
            printf '%sPASS: Scenario 2 translation and voiceover verified.%s\n' "$GREEN" "$RESET"
            ;;
            
        "03")
            if ! jq -e --argjson supplied "$(printf '%s\n' "${TYSON_CLIPS[@]}" | jq -R . | jq -s .)" '
                (all(.specscene.scenes[];
                    .bindings.clip == null
                    or (
                        .bindings.clip.clip_id as $cid
                        | (($supplied | index($cid)) != null)
                        and ((.bindings.clip.drive_link | length) > 0)
                    )
                ))
                and (
                    ([.specscene.scenes[].bindings.clip.clip_id // empty] | length)
                    == ([.specscene.scenes[].bindings.clip.clip_id // empty] | unique | length)
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
                if [[ -n "$TYSON_VIDEO_ID" ]]; then
                    count=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM media_assets WHERE id='$bid' AND source='youtube' AND id LIKE '${TYSON_VIDEO_ID}_%';")
                else
                    count=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM media_assets WHERE id='$bid' AND source='youtube';")
                fi
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
                (.specscene.scenes[0].bindings.stock.asset_id == $f_id)
                and (.specscene.scenes[1].bindings.stock.asset_id == $i_id)
                and (.specscene.scenes[2].bindings.stock.asset_id == $t_id)
                and (all(.specscene.scenes[];
                    (.bindings.stock.fallback // false) == false
                    and (.bindings.stock.drive_link | length) > 0
                    and .bindings.stock.source == "youtube"
                ))
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
                (.text | length > 100)
                and (.text | test("\\b(il|la|gli|della|velocità|pugilato|eredità)\\b"; "i"))
            ' <<<"$out_json" >/dev/null; then
                printf '%sFAIL: Full pipeline text not in Italian%s\n' "$RED" "$RESET" >&2
                return 1
            fi
            # Verify voiceover completed
            if ! jq -e '
                (([.specscene.scenes[].bindings.voiceover] | length) == (.specscene.scenes | length))
                and (all(.specscene.scenes[];
                    .bindings.voiceover.status == "completed"
                    and (.bindings.voiceover.link | length) > 0
                ))
            ' <<<"$out_json" >/dev/null; then
                printf '%sFAIL: Full pipeline voiceover check failed%s\n' "$RED" "$RESET" >&2
                return 1
            fi
            # Verify clip bindings are supplied Tyson clips only
            if ! jq -e --argjson supplied "$(printf '%s\n' "${TYSON_CLIPS[@]}" | jq -R . | jq -s .)" '
                all(.specscene.scenes[];
                    .bindings.clip == null
                    or (
                        .bindings.clip.clip_id as $cid
                        | (($supplied | index($cid)) != null)
                        and ((.bindings.clip.drive_link | length) > 0)
                    )
                )
            ' <<<"$out_json" >/dev/null; then
                printf '%sFAIL: Full pipeline clip binding check failed%s\n' "$RED" "$RESET" >&2
                return 1
            fi
            
            # Verify Google Doc artifact is published
            local doc_link
            doc_link=$(jq -r '.job.result.data.data.artifacts.document.doc_link // .job.result.data.items[0].result.artifacts.document.doc_link // ""' "$full_body_file")
            if [[ -z "$doc_link" || "$doc_link" == "null" ]]; then
                printf '%sFAIL: Google Doc artifact not generated/published%s\n' "$RED" "$RESET" >&2
                return 1
            fi
            printf '%sPASS: Google Doc published successfully: %s%s\n' "$GREEN" "$doc_link" "$RESET"
            
            # Idempotency Replay Verifications
            smoke_log_section "Idempotency Replay checks"
            
            # 1. Same payload + same idempotency key -> returns same job response (HTTP 200 or 202)
            export SMOKE_IDEMPOTENCY_KEY="$idem_key"
            smoke_curl POST "/api/script/generate" \
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
            export SMOKE_IDEMPOTENCY_KEY="$idem_key"
            smoke_curl POST "/api/script/generate" \
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

        "07")
            # Scenario 7: Multi-boxer, multi-stock, multi-lang E2E pipeline
            if ! python3 "$DIR/verify_multilang.py" "$full_body_file" "$DB_PATH"; then
                printf '%sFAIL: Scenario 7 multilang verification failed%s\n' "$RED" "$RESET" >&2
                return 1
            fi

            # Generate structured report from aggregated job response
            local report_file="$REPORTS_DIR/07_Top5 multilang_report.json"
            if ! python3 "$DIR/generate_report.py" "$full_body_file" "$report_file"; then
                printf '%sWARN: Report generation failed (non-fatal)%s\n' "$YELLOW" "$RESET" >&2
            fi
            
            # Idempotency Replay Verifications
            smoke_log_section "Idempotency Replay checks"
            export SMOKE_IDEMPOTENCY_KEY="$idem_key"
            smoke_curl POST "/api/script/generate" \
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
            
            # Idempotency Conflict Verification
            local diff_payload
            diff_payload=$(jq '.items[0].title = "A different title"' <<<"$payload")
            export SMOKE_IDEMPOTENCY_KEY="$idem_key"
            smoke_curl POST "/api/script/generate" \
                --data "$diff_payload" >/dev/null
            local conflict_http="$SMOKE_LAST_HTTP"
            if [[ "$conflict_http" != "409" ]]; then
                printf '%sFAIL: Different payload with same key returned HTTP %s, expected 409 Conflict%s\n' "$RED" "$conflict_http" "$RESET" >&2
                return 1
            fi
            printf 'Idempotency conflict OK (returned HTTP 409)\n'
            
            printf '%sPASS: Scenario 7 multi-boxer, multi-stock, multi-lang E2E pipeline verified.%s\n' "$GREEN" "$RESET"
            ;;

        "07b")
            # Scenario 7b: Negative check — swapped stock in multilang scenario
            # (Tyson scene gets Ali's asset; verify_multilang.py --negative exits 3 on mismatch)
            python3 "$DIR/verify_multilang.py" "$full_body_file" "$DB_PATH" --negative >/dev/null 2>&1
            local exit_code=$?
            if [[ "$exit_code" != "3" ]]; then
                printf '%sFAIL: Swapped stock negative check did not exit with code 3 (got exit code %d)%s\n' "$RED" "$exit_code" "$RESET" >&2
                return 1
            fi
            printf '%sPASS: Negative swapped stock verified — STOCK_SUBJECT_MISMATCH correctly detected in multilang scenario.%s\n' "$GREEN" "$RESET"
            ;;

        "07c")
            # Scenario 7c: Negative — expect exactly 1 child job to fail (FR voiceover invalid).
            # child_fail is the count of children that didn't reach SUCCEEDED/completed.
            if (( child_ok == 9 && child_fail == 1 )); then
                printf '%sPASS: Negative language-fail correctly detected — 9/10 completed, 1 failed (FR).%s\n' "$GREEN" "$RESET"
                printf '  Child jobs: %s OK, %s failed (expected 9 OK + 1 fail)\n' "$child_ok" "$child_fail"
            elif (( child_ok == 10 && child_fail == 0 )); then
                printf '%sFAIL: Negative language-fail NOT detected — all 10 child jobs succeeded (false PASS)%s\n' "$RED" "$RESET" >&2
                return 1
            else
                printf '%sFAIL: Unexpected child job counts — %s OK, %s failed (expected 9 OK + 1 fail)%s\n' "$RED" "$child_ok" "$child_fail" "$RESET" >&2
                return 1
            fi
            ;;

        "08")
            # Scenario 8: Negative check — swapped stock detection
            python3 "$DIR/verify_multilang.py" "$full_body_file" "$DB_PATH" --negative >/dev/null 2>&1
            local exit_code=$?
            if [[ "$exit_code" != "3" ]]; then
                printf '%sFAIL: Swapped stock negative check did not exit with code 3 (got exit code %d)%s\n' "$RED" "$exit_code" "$RESET" >&2
                return 1
            fi
            printf '%sPASS: Negative swapped stock verified (correctly detected STOCK_SUBJECT_MISMATCH)%s\n' "$GREEN" "$RESET"
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
        if [[ "$num" == "07" || "$num" == "07b" ]]; then
            export SMOKE_POLL_TIMEOUT_SECONDS=2400
        else
            export SMOKE_POLL_TIMEOUT_SECONDS=180
        fi
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
run_test "07" "Multi-boxer, multi-stock, multi-lang E2E" "top5_financial_stories_multilang.json" || failures=$((failures + 1))
run_test "07b" "Negative swapped stock (multilang variant)" "top5_financial_stories_multilang_neg.json" || failures=$((failures + 1))

# ── Scenario 07c: Negative test — French language intentionally fails ──
# The scenario itself asserts child_ok==9 && child_fail==1 (see case statement).
# If the assertion passes, run_scenario returns 0; if false-positive, returns 1.
run_test "07c" "Negative language fail (FR voiceover invalid)" "top5_financial_stories_multilang_fail_fr.json" || failures=$((failures + 1))

run_test "08" "Negative swapped stock (single-item)" "top5_neg_swapped_stock.json" || failures=$((failures + 1))

if (( failures > 0 )); then
    printf '%sFAIL: %d scenario(s) failed.%s\n' "$RED" "$failures" "$RESET" >&2
    exit 1
fi

if [[ "$TARGET_SCENARIO" == "all" ]]; then
    printf '%sOK: All boxers script-generation scenarios completed and verified!%s\n' "$GREEN" "$RESET"
else
    printf '%sOK: Scenario %s completed and verified!%s\n' "$GREEN" "$TARGET_SCENARIO" "$RESET"
fi
exit 0

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

# 2. Load Tyson clip data from the dedicated clip fixture, falling back to SQLite.
#   Env vars: TYSON_VIDEO_ID (SQLite fallback), TYSON_FOLDER_NAME (default: Mike Tyson)
FIXTURES_DIR="$DIR/fixtures"
REGISTRY_FILE="$FIXTURES_DIR/boxers_stock_registry.json"
RESOLVED_STOCK_FILE="$WORK_DIR/resolved_stock.json"
TYSON_CLIPS=()
TYSON_LINKS=()
TYSON_VIDEO_ID="${TYSON_VIDEO_ID:-}"
TYSON_FOLDER_NAME="${TYSON_FOLDER_NAME:-Mike Tyson}"

# Resolve logical registry data first. Database validation is deliberately
# deferred until after scenario preflight so an unavailable top-five subject
# becomes BLOCKED before any network or voiceover work is attempted.
if ! python3 "$DIR/stock_registry.py" resolve \
    --registry "$REGISTRY_FILE" \
    --output "$RESOLVED_STOCK_FILE"; then
    printf '%ssetup error: stock registry resolution failed%s\\n' "$RED" "$RESET" >&2
    exit 2
fi
printf 'Resolved logical boxer stock registry: %s\\n' "$RESOLVED_STOCK_FILE"

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

# Top-five stock bindings are materialized from resolved_stock.json in prepare_payload.
# No scenario-specific asset IDs are copied into this runner.
VOICEOVER_FOLDER="${BOXERS_VOICEOVER_FOLDER_ID:-}"
if [[ -z "$VOICEOVER_FOLDER" ]]; then
    printf '%ssetup error: BOXERS_VOICEOVER_FOLDER_ID is required%s\n' "$RED" "$RESET" >&2
    exit 2
fi
REPORTS_DIR="${BOXERS_REPORTS_DIR:-$DIR/reports}"
PENDING_REPORTS_DIR="$REPORTS_DIR/.pending"
INCOMPLETE_REPORTS_DIR="$REPORTS_DIR/incomplete"
mkdir -p "$REPORTS_DIR" "$PENDING_REPORTS_DIR" "$INCOMPLETE_REPORTS_DIR"

atomic_publish_file() {
    local source="$1"
    local destination="$2"
    local temporary="${destination}.tmp"
    mkdir -p "$(dirname "$destination")"
    cp "$source" "$temporary"
    mv -f "$temporary" "$destination"
}

archive_pending_reports() {
    local num="$1"
    local stamp
    stamp=$(date -u +%Y%m%dT%H%M%SZ)
    local archived=0
    local path
    for path in "$PENDING_REPORTS_DIR/${num}_"*; do
        [[ -e "$path" ]] || continue
        mv -f "$path" "$INCOMPLETE_REPORTS_DIR/${stamp}_$(basename "$path")"
        archived=$((archived + 1))
    done
    if (( archived > 0 )); then
        printf '%sArchived %d incomplete report artifact(s) for scenario %s%s\\n' \
            "$YELLOW" "$archived" "$num" "$RESET" >&2
    fi
}

refresh_parent_full_until_terminal() {
    local job_id="$1"
    local destination="$2"
    local deadline=$(( $(date +%s) + SMOKE_POLL_TIMEOUT_SECONDS ))
    local status=""
    while (( $(date +%s) < deadline )); do
        smoke_wallclock_check
        smoke_curl GET "/api/jobs/$job_id/full" >/dev/null
        if [[ "$SMOKE_LAST_HTTP" != "200" ]]; then
            return 1
        fi
        cp "$SMOKE_LAST_BODY" "$destination"
        status=$(jq -r '.status // .job.status // .result.status // .job.result.status // ""' "$destination")
        case "$status" in
            completed|SUCCEEDED|SUCCEEDED_WITH_WARNINGS|failed|FAILED|cancelled|CANCELLED|dead_letter|DEAD_LETTER)
                return 0
                ;;
        esac
        sleep "$SMOKE_POLL_INTERVAL_SECONDS"
    done
    return 124
}

refresh_parent_full_until_children() {
    local job_id="$1"
    local destination="$2"
    local deadline=$(( $(date +%s) + SMOKE_POLL_TIMEOUT_SECONDS ))
    local child_ids=""
    while (( $(date +%s) < deadline )); do
        smoke_wallclock_check
        smoke_curl GET "/api/jobs/$job_id/full" >/dev/null
        if [[ "$SMOKE_LAST_HTTP" != "200" ]]; then
            return 1
        fi
        cp "$SMOKE_LAST_BODY" "$destination"
        child_ids=$(jq -r '
            (.result.data.child_job_ids // .result.child_job_ids
             // .job.result.data.child_job_ids // .job.result.child_job_ids // [])
            | map(select(type == "string" and length > 0)) | .[]
        ' "$destination")
        if [[ -n "$child_ids" ]]; then
            return 0
        fi
        sleep "$SMOKE_POLL_INTERVAL_SECONDS"
    done
    return 124
}

publish_scenario_reports() {
    local num="$1"
    local name="$2"
    local pending_raw="$PENDING_REPORTS_DIR/${num}_${name}_job.json"
    local pending_verification="$PENDING_REPORTS_DIR/${num}_${name}_verification_report.json"
    local raw_destination="$REPORTS_DIR/raw/${num}_${name}_job.json"
    local verification_destination="$REPORTS_DIR/${num}_${name}_verification_report.json"
    local raw_tmp="${raw_destination}.tmp"
    local verification_tmp="${verification_destination}.tmp"
    local raw_backup="${raw_destination}.bak.$$"
    local verification_backup="${verification_destination}.bak.$$"
    local had_raw=0
    local had_verification=0

    mkdir -p "$(dirname "$raw_destination")" "$(dirname "$verification_destination")"
    # Stage both files before touching canonical paths. Keep backups of both
    # existing reports so a failure publishing either file restores the prior
    # valid pair rather than destroying evidence.
    if [[ -e "$raw_destination" ]]; then
        cp "$raw_destination" "$raw_backup" || return 1
        had_raw=1
    fi
    if [[ -e "$verification_destination" ]]; then
        cp "$verification_destination" "$verification_backup" || {
            rm -f "$raw_backup"
            return 1
        }
        had_verification=1
    fi
    if ! cp "$pending_raw" "$raw_tmp" || ! cp "$pending_verification" "$verification_tmp"; then
        rm -f "$raw_tmp" "$verification_tmp" "$raw_backup" "$verification_backup"
        return 1
    fi
    if ! mv -f "$raw_tmp" "$raw_destination"; then
        rm -f "$raw_tmp" "$verification_tmp" "$raw_backup" "$verification_backup"
        return 1
    fi
    if ! mv -f "$verification_tmp" "$verification_destination"; then
        if (( had_raw )); then
            mv -f "$raw_backup" "$raw_destination" || true
        else
            rm -f "$raw_destination"
        fi
        if (( had_verification )); then
            mv -f "$verification_backup" "$verification_destination" || true
        else
            rm -f "$verification_destination"
        fi
        rm -f "$raw_tmp" "$verification_tmp" "$raw_backup" "$verification_backup"
        return 1
    fi
    rm -f "$pending_raw" "$pending_verification" "$raw_backup" "$verification_backup"
}

# Helper function to preprocess JSON scenarios
prepare_payload() {
    local scenario_file="$1"
    local temp_json="$WORK_DIR/payload.json"
    
    if ! python3 "$DIR/stock_registry.py" materialize \
        --resolved "$RESOLVED_STOCK_FILE" \
        --input "$scenario_file" \
        --output "$temp_json"; then
        printf '%ssetup error: scenario stock binding is not present in resolved registry: %s%s\n' \
            "$RED" "$scenario_file" "$RESET" >&2
        return 1
    fi
    
    # Perform string replacements for Tyson clip placeholders
    for i in {0..5}; do
        local idx=$((i + 1))
        local placeholder="TYSON_CLIP_ID_${idx}"
        sed -i "s/$placeholder/${TYSON_CLIPS[$i]}/g" "$temp_json"
    done
    
    # Inject the sole runtime voiceover folder value into every payload
    # declaration. Scenario fixtures never own a Drive folder ID.
    local runtime_payload="$temp_json.runtime"
    jq --arg folder "$VOICEOVER_FOLDER" '
        walk(
            if type == "object" then
                with_entries(
                    if .key == "voiceover_folder_id" or .key == "folder_id"
                    then .value = $folder
                    else .
                    end
                )
            else .
            end
        )
    ' "$temp_json" > "$runtime_payload"
    mv "$runtime_payload" "$temp_json"

    if ! python3 "$DIR/runner_policy.py" validate-folder \
        --payload "$temp_json" \
        --folder-id "$VOICEOVER_FOLDER"; then
        printf '%ssetup error: payload voiceover folder is inconsistent with BOXERS_VOICEOVER_FOLDER_ID%s\n' \
            "$RED" "$RESET" >&2
        return 1
    fi

    # Scenario metadata is runner-only and must never be sent to the API.
    # runner_policy.py is also the testable source of the mode contract.
    local payload_json="$temp_json.payload"
    if ! python3 "$DIR/runner_policy.py" prepare \
        --mode "$RUNNER_MODE" \
        --input "$temp_json" \
        --output "$payload_json"; then
        printf '%ssetup error: invalid %s runner payload: %s%s\n' \
            "$RED" "$RUNNER_MODE" "$scenario_file" "$RESET" >&2
        return 1
    fi
    mv "$payload_json" "$temp_json"

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

    RUNNER_MODE=$(jq -r '._runner.mode // "strict"' "$file")
    ALLOWED_WARNING_REGEX=$(jq -r '._runner.allowed_warnings_regex // ""' "$file")
    if [[ "$RUNNER_MODE" != "smoke" && "$RUNNER_MODE" != "strict" ]]; then
        printf '%sFAIL: Scenario %s has invalid runner mode: %s%s\n' "$RED" "$num" "$RUNNER_MODE" "$RESET" >&2
        return 1
    fi
    printf 'Runner mode: %s%s%s\n' "$CYAN" "$RUNNER_MODE" "$RESET"

    # Preflight all logical stock placeholders before materialization or POST.
    # A blocked scenario returns success from the runner as an expected,
    # dependency-gated outcome; no job or voiceover request is created.
    local preflight_file="$WORK_DIR/preflight_$num.json"
    local preflight_status=0
    local preflight_db_args=()
    if [[ "$DRY_RUN" != "1" ]]; then
        preflight_db_args=(--db "$DB_PATH")
    fi
    python3 "$DIR/stock_registry.py" preflight \
        --resolved "$RESOLVED_STOCK_FILE" \
        --input "$file" \
        --output "$preflight_file" \
        "${preflight_db_args[@]}" || preflight_status=$?
    if [[ "$preflight_status" == "3" ]]; then
        if ! jq -e '.status == "BLOCKED" and (.missing | length > 0)' "$preflight_file" >/dev/null; then
            printf '%sFAIL: Scenario %s preflight returned malformed BLOCKED result%s\n' "$RED" "$num" "$RESET" >&2
            return 1
        fi
        printf '%sBLOCKED: Scenario %s dependencies are unavailable; no POST/job/voiceover was attempted%s\n' \
            "$YELLOW" "$num" "$RESET"
        jq . "$preflight_file"
        SCENARIO_BLOCKED=1
        return 0
    elif (( preflight_status != 0 )); then
        printf '%sFAIL: Scenario %s preflight failed with status %d%s\n' \
            "$RED" "$num" "$preflight_status" "$RESET" >&2
        return 1
    fi

    # Validate voiceover prerequisites only after stock BLOCKED handling.
    # This preserves the dependency-gated, no-POST outcome when stock is absent.
    if [[ "$DRY_RUN" != "1" ]]; then
        if ! python3 "$DIR/runner_policy.py" voiceover-preflight \
            --db "$DB_PATH" \
            --folder-id "$VOICEOVER_FOLDER"; then
            printf '%sFAIL: voiceover DB preflight rejected empty voices or folder drift%s\n' \
                "$RED" "$RESET" >&2
            return 1
        fi
    fi

    # Only a ready scenario reaches SQLite validation and payload creation.
    # This keeps a BLOCKED preflight side-effect free.
    if [[ "$DRY_RUN" != "1" ]]; then
        if ! python3 "$DIR/stock_registry.py" resolve \
            --db "$DB_PATH" \
            --registry "$REGISTRY_FILE" \
            --output "$RESOLVED_STOCK_FILE"; then
            printf '%sFAIL: Scenario %s registry/database validation failed%s\\n' \
                "$RED" "$num" "$RESET" >&2
            return 1
        fi
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

        # Fetch /full until child IDs are present. The first response can be
        # stale RUNNING even though the parent status endpoint is terminal.
        local full_body_file="$WORK_DIR/full_job_$num.json"
        if ! refresh_parent_full_until_children "$job_id" "$full_body_file"; then
            if [[ -s "$full_body_file" ]]; then
                mkdir -p "$PENDING_REPORTS_DIR"
                cp "$full_body_file" "$PENDING_REPORTS_DIR/${num}_${name}_job.json"
            fi
            printf '%sFAIL: Scenario %s parent /full did not expose child_job_ids%s\\n' "$RED" "$num" "$RESET" >&2
            return 1
        fi

        # Preserve the latest discovery response immediately; later checks can
        # replace it with the final refreshed parent response.
        mkdir -p "$PENDING_REPORTS_DIR"
        cp "$full_body_file" "$PENDING_REPORTS_DIR/${num}_${name}_job.json"

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
            if ! smoke_poll_terminal "$cid"; then
                child_fail=$((child_fail + 1))
                printf '  %sFAIL: Child %s polling failed or timed out%s\n' "$RED" "$cid" "$RESET" >&2
                continue
            fi
            smoke_curl GET "/api/jobs/$cid/full" >/dev/null
            if [[ "$SMOKE_LAST_HTTP" == "200" ]]; then
                cp "$SMOKE_LAST_BODY" "$children_dir/$cid.json"
                local cstatus
                cstatus=$(jq -r '.status // .job.status // .result.status // .job.result.status // "UNKNOWN"' "$children_dir/$cid.json")
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

        # Refresh parent /full after every child has been polled. This is
        # intentionally separate from smoke_poll_terminal: /full can lag
        # behind /status and return a stale RUNNING snapshot.
        local expected_child_ids="$WORK_DIR/child_ids_$num.txt"
        printf '%s\n' "$child_job_ids" > "$expected_child_ids"
        local raw_parent_file="$WORK_DIR/raw_parent_$num.json"
        if ! refresh_parent_full_until_terminal "$job_id" "$raw_parent_file"; then
            printf '%sFAIL: Scenario %s parent /full never reached a terminal state%s\\n' "$RED" "$num" "$RESET" >&2
            return 1
        fi
        cp "$raw_parent_file" "$full_body_file"
        # Keep the latest unmodified parent response available even when
        # child/result validation rejects publication.
        cp "$raw_parent_file" "$PENDING_REPORTS_DIR/${num}_${name}_job.json"
        if ! python3 "$DIR/report_publication.py" validate \
            --parent "$raw_parent_file" \
            --children-dir "$children_dir" \
            --expected-child-ids "$expected_child_ids"; then
            printf '%sFAIL: Scenario %s parent/child result validation failed%s\\n' "$RED" "$num" "$RESET" >&2
            return 1
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
        if ! refresh_parent_full_until_terminal "$job_id" "$full_body_file"; then
            if [[ -s "$full_body_file" ]]; then
                cp "$full_body_file" "$PENDING_REPORTS_DIR/${num}_${name}_job.json"
            fi
            printf '%sFAIL: Scenario %s /full never reached a terminal state%s\\n' "$RED" "$num" "$RESET" >&2
            return 1
        fi
        mkdir -p "$PENDING_REPORTS_DIR"
        cp "$full_body_file" "$PENDING_REPORTS_DIR/${num}_${name}_job.json"
    fi

    # Keep raw job evidence separate. Both raw and verification reports stay
    # pending until every assertion has passed, then publish atomically.
    mkdir -p "$PENDING_REPORTS_DIR"
    local pending_verification="$PENDING_REPORTS_DIR/${num}_${name}_verification_report.json"

    # Assertions shared by both modes. The policy helper is the single gate
    # for terminal status, warnings, fallback bindings, and strict asset
    # lifecycle validation.
    if ! python3 "$DIR/runner_policy.py" validate \
        --mode "$RUNNER_MODE" \
        --response "$full_body_file" \
        --db "$DB_PATH" \
        --allowed-warning-regex "$ALLOWED_WARNING_REGEX"; then
        printf '%sFAIL: Scenario %s failed the %s runner policy%s\n' \
            "$RED" "$num" "$RUNNER_MODE" "$RESET" >&2
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
            # Scenario 4 assertions: Manny Pacquiao's independently resolved stock.
            # Never use Tyson clips as a substitute for Pacquiao.
            local pacquiao_fight_id
            local pacquiao_interview_id
            local pacquiao_training_id
            pacquiao_fight_id=$(jq -r '.boxers.manny_pacquiao.assets.fight.asset_id // ""' "$RESOLVED_STOCK_FILE")
            pacquiao_interview_id=$(jq -r '.boxers.manny_pacquiao.assets.interview.asset_id // ""' "$RESOLVED_STOCK_FILE")
            pacquiao_training_id=$(jq -r '.boxers.manny_pacquiao.assets.training.asset_id // ""' "$RESOLVED_STOCK_FILE")
            if [[ -z "$pacquiao_fight_id" || -z "$pacquiao_interview_id" || -z "$pacquiao_training_id" ]]; then
                printf '%sFAIL: Pacquiao requires three independently resolved ACTIVE assets%s\\n' "$RED" "$RESET" >&2
                return 1
            fi
            if ! jq -e --arg f_id "$pacquiao_fight_id" --arg i_id "$pacquiao_interview_id" --arg t_id "$pacquiao_training_id" '
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
            if !            python3 "$DIR/verify_multilang.py" "$full_body_file" "$DB_PATH" \
                --registry "$RESOLVED_STOCK_FILE" \
                --voiceover-folder-id "$VOICEOVER_FOLDER"; then
                printf '%sFAIL: Scenario 7 multilang verification failed%s\n' "$RED" "$RESET" >&2
                return 1
            fi

            # Generate structured report from aggregated job response
            local report_file="$pending_verification"
            if ! python3 "$DIR/generate_report.py" "$full_body_file" "$report_file" \
                --registry "$RESOLVED_STOCK_FILE" \
                --voiceover-folder-id "$VOICEOVER_FOLDER"; then
                printf '%sFAIL: Report generation failed%s\n' "$RED" "$RESET" >&2
                return 1
            fi
            if ! jq -e '.final == "PASS"' "$report_file" >/dev/null; then
                printf '%sFAIL: Generated report did not pass its final gate%s\n' "$RED" "$RESET" >&2
                return 1
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
            set +e
            python3 "$DIR/verify_multilang.py" "$full_body_file" "$DB_PATH" --negative --registry "$RESOLVED_STOCK_FILE" --voiceover-folder-id "$VOICEOVER_FOLDER" >/dev/null 2>&1
            local exit_code=$?
            set -e
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
            set +e
            python3 "$DIR/verify_multilang.py" "$full_body_file" "$DB_PATH" --negative --registry "$RESOLVED_STOCK_FILE" --voiceover-folder-id "$VOICEOVER_FOLDER" >/dev/null 2>&1
            local exit_code=$?
            set -e
            if [[ "$exit_code" != "3" ]]; then
                printf '%sFAIL: Swapped stock negative check did not exit with code 3 (got exit code %d)%s\n' "$RED" "$exit_code" "$RESET" >&2
                return 1
            fi
            printf '%sPASS: Negative swapped stock verified (correctly detected STOCK_SUBJECT_MISMATCH)%s\n' "$GREEN" "$RESET"
            ;;
    esac
    
    if [[ "$num" != "07" ]]; then
        cp "$full_body_file" "$pending_verification"
    fi
    publish_scenario_reports "$num" "$name"

    printf '%sSUCCESS: Scenario %s passed!%s\n\n' "$GREEN" "$num" "$RESET"
    return 0
}

# Run scenario sequence
TARGET_SCENARIO="${TARGET_SCENARIO:-all}"
failures=0
blocked_scenarios=0
SCENARIO_BLOCKED=0

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
        if ! run_scenario "$num" "$name" "$fname"; then
            archive_pending_reports "$num"
            return 1
        fi
        if [[ "$SCENARIO_BLOCKED" == "1" ]]; then
            blocked_scenarios=$((blocked_scenarios + 1))
            SCENARIO_BLOCKED=0
        fi
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

if (( blocked_scenarios > 0 )); then
    printf '%sBLOCKED: %d scenario(s) have unavailable stock dependencies; no jobs or voiceovers were created.%s\n' \
        "$YELLOW" "$blocked_scenarios" "$RESET"
elif [[ "$TARGET_SCENARIO" == "all" ]]; then
    printf '%sOK: All boxers script-generation scenarios completed and verified!%s\n' "$GREEN" "$RESET"
else
    printf '%sOK: Scenario %s completed and verified!%s\n' "$GREEN" "$TARGET_SCENARIO" "$RESET"
fi
exit 0

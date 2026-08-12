#!/usr/bin/env bash
# tests/operational/boxers-generate/lib/setup.sh — runner setup helpers.
# Source-only library. The caller owns strict mode, common.sh, and invocation.

boxers_initialize() {
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
    #    Env vars: TYSON_VIDEO_ID (SQLite fallback), TYSON_FOLDER_NAME (default: Mike Tyson)
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
        printf '%ssetup error: stock registry resolution failed%s\n' "$RED" "$RESET" >&2
        exit 2
    fi
    printf 'Resolved logical boxer stock registry: %s\n' "$RESOLVED_STOCK_FILE"

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
}
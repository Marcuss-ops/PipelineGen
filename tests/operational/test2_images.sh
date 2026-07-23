#!/usr/bin/env bash
# tests/operational/test2_images.sh — Test 2: real online images + cache hit + anti-collision.
#
# Verifies, against the live server and real providers:
#   1. First-run retrieval for three queries.
#   2. Cached replay returns the same asset with cache_hit=true.
#   3. Anti-collision: semantically different jaguar queries produce different assets.
#
# Exit codes:
#   0 all hard assertions passed
#   1 one or more assertions failed
#   2 setup error / missing prerequisite

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

smoke_require curl jq sqlite3 file

HOST="${VELOX_HOST:-127.0.0.1}"
PIPELINE_PORT="${VELOX_PORT:-8000}"
BASE_URL="http://${HOST}:${PIPELINE_PORT}"
# Project-root relative data dir so the script can be run from tests/operational.
DB_PATH="${VELOX_DATA_DIR:-${DIR}/../../data}/media/media.db.sqlite"

QUERIES=(
    "Eiffel Tower Paris at night"
    "jaguar animal in rainforest"
    "solar panels on factory roof"
)

PASS=0
WARN=0
FAIL=0

log_pass() { printf '[PASS]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; PASS=$((PASS + 1)); }
log_warn() { printf '[WARN]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; WARN=$((WARN + 1)); }
log_fail() { printf '[FAIL]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; FAIL=$((FAIL + 1)); }
log_info() { printf '[INFO]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; }

sql_escape() {
    local s="$1"
    printf '%s' "${s//\'/\'\'}"
}

query_row_count() {
    local query="$1"
    sqlite3 -readonly "$DB_PATH" "SELECT COUNT(*) FROM media_assets WHERE source='image' AND COALESCE(json_extract(metadata_json, '$.source_query'), '') = '$(sql_escape "$query")'" 2>/dev/null | tr -d '\n'
}

fetch_image() {
    local query="$1" label="$2"
    local out="$WORK_DIR/image_${label}.json"
    local code
    code=$(curl -sS --max-time 120 -G -o "$out" -w '%{http_code}' \
        -H "Authorization: Bearer $SMOKE_TOKEN" \
        --data-urlencode "q=${query}" \
        --data-urlencode "lang=en" \
        "$BASE_URL/api/images/retrieved/search")
    printf '%s\t%s\n' "$code" "$out"
}

verify_db_row() {
    local asset_id="$1" query="$2"
    local row file_hash local_path db_provider width height source_query source_image_url source_page_url source_name
    row=$(sqlite3 -readonly "$DB_PATH" "
        SELECT
            COALESCE(file_hash, '') AS file_hash,
            COALESCE(local_path, '') AS local_path,
            COALESCE(provider, '') AS provider,
            COALESCE(width, 0) AS width,
            COALESCE(height, 0) AS height,
            COALESCE(json_extract(metadata_json, '$.source_query'), '') AS source_query,
            COALESCE(json_extract(metadata_json, '$.source_image_url'), '') AS source_image_url,
            COALESCE(json_extract(metadata_json, '$.source_page_url'), '') AS source_page_url,
            COALESCE(json_extract(metadata_json, '$.source_name'), '') AS source_name
        FROM media_assets
        WHERE id = '$(sql_escape "$asset_id")'
    " 2>/dev/null)
    if [ -z "$row" ]; then
        log_fail "missing SQLite row for $asset_id"
        return 1
    fi
    IFS='|' read -r file_hash local_path db_provider width height source_query source_image_url source_page_url source_name <<< "$row"

    if [ "$file_hash" != "$asset_id" ]; then
        log_fail "hash mismatch for '$query' (asset_id=$asset_id hash=$file_hash)"
        return 1
    fi
    if [ "$source_query" != "$query" ]; then
        log_fail "source_query mismatch for '$query' (got '$source_query')"
        return 1
    fi
    if [ -z "$source_image_url" ] || [ -z "$source_page_url" ] || [ -z "$source_name" ]; then
        log_fail "incomplete provenance for '$query'"
        return 1
    fi
    if [ -z "$local_path" ] || [ ! -f "$local_path" ]; then
        log_fail "local file missing for '$query' ($local_path)"
        return 1
    fi
    local mime size
    mime=$(file -b --mime-type "$local_path" 2>/dev/null || true)
    size=$(stat -c%s "$local_path" 2>/dev/null || echo 0)
    if [[ ! "$mime" =~ ^image/ || "$size" -le 0 ]]; then
        log_fail "invalid image file for '$query' (mime=$mime size=$size)"
        return 1
    fi
    log_pass "DB/file checks for '$query' (asset_id=$asset_id)"
}

run_first() {
    local query="$1"
    local pre_count
    pre_count=$(query_row_count "$query")
    if [[ "$pre_count" != "0" ]]; then
        log_fail "precondition: '$query' already exists in SQLite ($pre_count row(s))"
        return 1
    fi

    local code out body
    IFS=$'\t' read -r code out < <(fetch_image "$query" "first")
    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "first-run HTTP $code for '$query'"
        smoke_echo_safe "$(head -c 800 "$out" 2>/dev/null || true)" >&2
        return 1
    fi

    if ! jq -e '(.results | length) == 1' "$out" >/dev/null 2>&1; then
        log_fail "first-run response shape invalid for '$query'"
        smoke_echo_safe "$(head -c 800 "$out" 2>/dev/null || true)" >&2
        return 1
    fi

    local asset_id cache_hit cache_source provider
    asset_id=$(jq -r '.results[0].asset_id // empty' "$out")
    cache_hit=$(jq -r '.results[0].cache_hit // false' "$out")
    cache_source=$(jq -r '.results[0].cache_source // empty' "$out")
    provider=$(jq -r '.results[0].retrieval_provider // empty' "$out")

    if [[ -z "$asset_id" || "$cache_hit" != "false" || "$cache_source" != "provider" || -z "$provider" ]]; then
        log_fail "first-run cache trace invalid for '$query' (hit=$cache_hit source=$cache_source provider=$provider)"
        return 1
    fi

    verify_db_row "$asset_id" "$query" || return 1
    log_pass "first-run '$query' asset_id=$asset_id provider=$provider"
}

run_replay() {
    local query="$1" first_asset_id="$2"
    local code out asset_id cache_hit cache_source
    IFS=$'\t' read -r code out < <(fetch_image "$query" "replay")
    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "replay HTTP $code for '$query'"
        smoke_echo_safe "$(head -c 800 "$out" 2>/dev/null || true)" >&2
        return 1
    fi
    asset_id=$(jq -r '.results[0].asset_id // empty' "$out")
    cache_hit=$(jq -r '.results[0].cache_hit // false' "$out")
    cache_source=$(jq -r '.results[0].cache_source // empty' "$out")

    if [[ "$asset_id" != "$first_asset_id" || "$cache_hit" != "true" || "$cache_source" != "database" ]]; then
        log_fail "replay cache identity mismatch for '$query' (first=$first_asset_id replay=$asset_id hit=$cache_hit source=$cache_source)"
        return 1
    fi

    local count
    count=$(query_row_count "$query")
    if [[ "$count" != "1" ]]; then
        log_fail "duplicate rows appeared for '$query' (count=$count)"
        return 1
    fi
    log_pass "replay '$query' cached (asset_id=$asset_id)"
}

run_anti_collision() {
    local q1="$1" q2="$2"
    local out1="$WORK_DIR/anti_a.json" out2="$WORK_DIR/anti_b.json"
    local code1 code2 id1 id2

    code1=$(curl -sS --max-time 60 -G -o "$out1" -w '%{http_code}' \
        -H "Authorization: Bearer $SMOKE_TOKEN" \
        --data-urlencode "q=${q1}" --data-urlencode "lang=en" \
        "$BASE_URL/api/images/retrieved/search")
    code2=$(curl -sS --max-time 60 -G -o "$out2" -w '%{http_code}' \
        -H "Authorization: Bearer $SMOKE_TOKEN" \
        --data-urlencode "q=${q2}" --data-urlencode "lang=en" \
        "$BASE_URL/api/images/retrieved/search")

    if [[ ! "$code1" =~ ^2[0-9][0-9]$ || ! "$code2" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "anti-collision HTTP failure ($code1 / $code2)"
        return 1
    fi

    id1=$(jq -r '.results[0].asset_id // empty' "$out1")
    id2=$(jq -r '.results[0].asset_id // empty' "$out2")
    if [[ -z "$id1" || -z "$id2" || "$id1" == "$id2" ]]; then
        log_fail "anti-collision failed: '$q1' -> $id1, '$q2' -> $id2"
        return 1
    fi
    log_pass "anti-collision: '$q1' -> $id1, '$q2' -> $id2"
}

cleanup_existing() {
    log_info "cleaning up any previous Test 2 assets and outbox rows"
    # Capture hashes BEFORE deleting media_assets so we can also nuke the
    # canonical reindex outbox rows that otherwise survive as terminal rows.
    local query q_escaped hash
    for query in "${QUERIES[@]}" "jaguar luxury car"; do
        q_escaped=$(sql_escape "$query")
        while IFS= read -r hash; do
            [[ -z "$hash" ]] && continue
            sqlite3 "$DB_PATH" "DELETE FROM outbox_events WHERE event_key LIKE 'reconcile:reindex:${hash}%';" 2>/dev/null || true
        done < <(sqlite3 -readonly "$DB_PATH" "SELECT file_hash FROM media_assets WHERE source='image' AND COALESCE(json_extract(metadata_json, '$.source_query'), '') = '${q_escaped}';" 2>/dev/null)
        sqlite3 "$DB_PATH" "DELETE FROM media_assets WHERE source='image' AND COALESCE(json_extract(metadata_json, '$.source_query'), '') = '${q_escaped}';" 2>/dev/null || true
    done
}

require_server() {
    local code
    code=$(curl -sS --max-time 5 -o /dev/null -w '%{http_code}' "$BASE_URL/health" 2>/dev/null || echo "000")
    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "server not reachable at $BASE_URL/health (HTTP $code)"
        exit 2
    fi
    log_info "server reachable at $BASE_URL/health"
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — would probe real image searches for:"
        for q in "${QUERIES[@]}"; do printf '  - %s\n' "$q"; done
        exit 0
    fi

    require_server
    cleanup_existing

    smoke_log_section "Images first-run"
    declare -A FIRST_IDS
    for query in "${QUERIES[@]}"; do
        run_first "$query" || exit 1
        # Re-fetch the just-saved asset id from the DB to be sure.
        FIRST_IDS["$query"]=$(sqlite3 -readonly "$DB_PATH" "SELECT id FROM media_assets WHERE source='image' AND COALESCE(json_extract(metadata_json, '$.source_query'), '') = '$(sql_escape "$query")'" 2>/dev/null | tr -d '\n')
    done

    smoke_log_section "Images replay"
    for query in "${QUERIES[@]}"; do
        run_replay "$query" "${FIRST_IDS[$query]}" || exit 1
    done

    smoke_log_section "Images anti-collision"
    run_anti_collision "jaguar animal in rainforest" "jaguar luxury car" || exit 1

    printf '\n============================================\n'
    printf '  Test 2 — Images E2E Battery\n'
    printf '  PASS=%d  WARN=%d  FAIL=%d\n' "$PASS" "$WARN" "$FAIL"
    printf '============================================\n'
    if [[ "$FAIL" -gt 0 ]]; then
        printf 'VERDICT: FAIL\n'
        exit 1
    fi
    printf 'VERDICT: PASS\n'
    exit 0
}

main "$@"

#!/usr/bin/env bash
# vidrush_media_e2e.sh — live operational battery for PipelineGen media.
#
# Verifies, against the real server and dependencies:
#   1. startup correctness
#   2. image first-run + cached replay + anti-collision
#   3. Artlist live route
#   4. Artlist full pipeline
#   5. Artlist cache replay
#
# Exit codes:
#   0  all hard assertions passed
#   1  one or more hard assertions failed
#   2  setup error / missing prerequisite

set -euo pipefail

SMOKE_TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-3600}"
SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-1800}"
SMOKE_POLL_INTERVAL_SECONDS="${SMOKE_POLL_INTERVAL_SECONDS:-5}"
SMOKE_HTTP_TIMEOUT_SECONDS="${SMOKE_HTTP_TIMEOUT_SECONDS:-300}"
export SMOKE_TIMEOUT_SECONDS SMOKE_POLL_TIMEOUT_SECONDS SMOKE_POLL_INTERVAL_SECONDS SMOKE_HTTP_TIMEOUT_SECONDS

DIR=$(cd "$(dirname "$0")" && pwd)
# Source the canonical Artlist DoD lib umbrella (July 2026 refactor).
# vidrush_media_e2e.sh touches the Artlist surface in:
#   * Step 1: server/scraper/DB readiness probes (Gate 0 ancestors)
#   * Step 2: /api/artlist/run dispatch + polling
#   * Step 3: per-clip API/parity assertions (a subset of Gate 5/6/7/8)
# All of those touchpoints now share the SAME canonical helpers as
# artlist_e2e.sh / run_all.sh / 05_pipeline_fresh.sh / restart_verification.sh
# via the umbrella `_artlist_common.sh`. The umbrella's
# `artlist_dod_assert_helpers_loaded` guard fires fail-closed at import
# if any expected helper is missing — operator can bypass with
# `ARTLIST_DOD_LIB_SKIP_ASSERT=1 bash vidrush_media_e2e.sh` only for
# emergency debugging of the umbrella itself.
# shellcheck disable=SC1091
source "$DIR/lib/_artlist_common.sh" || exit 1

log_info "Vidrush E2E: Artlist DoD umbrella loaded (version=${ARTLIST_DOD_LIB_VERSION})"

if [[ "${HELP_REQUESTED:-0}" == "1" ]]; then
    cat <<'EOF'
vidrush_media_e2e.sh — live media operational battery

Live checks:
  - /health and /ready
  - /api/images/diagnostics
  - /api/artlist/job-consumer
  - /api/artlist/diagnostics
  - real image retrieval first-run + replay + anti-collision
  - /api/artlist/search/live
  - real Artlist pipeline run + cached replay

Important:
  - This is a live test. It talks to the real server, scraper,
    SQLite, FFmpeg, Drive, and Qdrant.
  - The image and Artlist first-run checks require the exact target
    queries to be absent from the DB before the run starts.

Dry run:
  SMOKE_DRY_RUN=1 bash tests/operational/vidrush_media_e2e.sh
EOF
    exit 0
fi

smoke_require curl sqlite3 file ffmpeg

HOST="${VELOX_HOST:-127.0.0.1}"
[ -n "${PIPELINE_PORT:-}" ] || PIPELINE_PORT="${VELOX_PORT:-8000}"
BASE_URL="http://${HOST}:${PIPELINE_PORT}"
DB_PATH="${VELOX_DATA_DIR:-./data}/media/media.db.sqlite"
SCRAPER_URL="${VELOX_ARTLIST_SCRAPER_SERVER_URL:-http://127.0.0.1:9123}"
QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"
QDRANT_API_KEY="${QDRANT_API_KEY:-${VELOX_QDRANT_API_KEY:-}}"
COLLECTION="${QDRANT_COLLECTION:-media_assets_current}"
ARTLIST_ROOT_FOLDER="${VELOX_DRIVE_ARTLIST_ROOT:-${ROOT_FOLDER_ID:-}}"

IMAGE_QUERIES=(
    "Eiffel Tower Paris at night"
    "jaguar animal in rainforest"
    "solar panels on factory roof"
)
ANTI_COLLISION_A="jaguar animal in rainforest"
ANTI_COLLISION_B="jaguar luxury car"
ARTLIST_TERM="business team working in modern office"

# NOTE (July 2026 refactor): The previous inline log_pass/log_warn/log_fail/
# log_info family + PASS/WARN/FAIL counter initialization has been DELETED.
# These definitions are now sourced from `lib/_artlist_common.sh` (which
# transitively sources `lib/artlist_runtime.sh` — the canonical owner of
# the log_* family per godlike/06 SSOT). This eliminates the prior
# cross-script duplication: 8+ inline definitions existed in
# vidrush_media_e2e.sh, artlist_live_env.sh, test3_artlist.sh,
# artlist_drive_failure_smoke.sh, artlist_preflight_smoke.sh,
# test2_images.sh, artlist_scraper_failure_smoke.sh, etc. A refactor
# that changes log_* semantics now propagates from a single edit in
# lib/artlist_runtime.sh. Counters PASS/WARN/FAIL live in the same
# file and are initialized at lib import time.

sql_escape() {
    local s="$1"
    printf '%s' "${s//\'/\'\'}"
}

db_scalar() {
    sqlite3 -readonly "$DB_PATH" "$1" 2>/dev/null | tr -d '\n'
}

db_json() {
    sqlite3 -readonly -json "$DB_PATH" "$1" 2>/dev/null || true
}

now_ms() {
    if command -v gdate >/dev/null 2>&1; then
        gdate +%s%3N
        return 0
    fi
    if date +%s%3N >/dev/null 2>&1; then
        date +%s%3N
        return 0
    fi
    python3 - <<'PY'
import time
print(int(time.time() * 1000))
PY
}

api_get_json() {
    local path="$1"
    local out="$2"
    shift 2
    local code
    code=$(curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
        -G \
        -o "$out" \
        -w '%{http_code}' \
        -H "Authorization: Bearer $SMOKE_TOKEN" \
        "$@" \
        "$BASE_URL$path")
    SMOKE_LAST_HTTP="$code"
    SMOKE_LAST_BODY="$out"
    printf '%s' "$code"
}

api_post_json() {
    local path="$1"
    local out="$2"
    local payload="$3"
    local code
    code=$(curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
        -X POST \
        -o "$out" \
        -w '%{http_code}' \
        -H "Authorization: Bearer $SMOKE_TOKEN" \
        -H 'Content-Type: application/json' \
        -d "$payload" \
        "$BASE_URL$path")
    SMOKE_LAST_HTTP="$code"
    SMOKE_LAST_BODY="$out"
    printf '%s' "$code"
}

assert_2xx() {
    local label="$1"
    if ! smoke_assert_http_2xx "$label"; then
        return 1
    fi
}

query_row_count() {
    local query="$1"
    local escaped
    escaped=$(sql_escape "$query")
    db_scalar "SELECT COUNT(*) FROM media_assets WHERE source='image' AND COALESCE(json_extract(metadata_json, '$.source_query'), '') = '${escaped}'"
}

artlist_row_count() {
    local term="$1"
    local escaped
    escaped=$(sql_escape "$term")
    db_scalar "SELECT COUNT(*) FROM artlist_runs WHERE term='${escaped}'"
}

image_fetch() {
    local query="$1"
    local phase="$2"
    local out="$WORK_DIR/image_${phase}.json"
    local code
    code=$(api_get_json "/api/images/retrieved/search" "$out" \
        --data-urlencode "q=${query}" \
        --data-urlencode "lang=en")
    printf '%s\t%s\n' "$code" "$out"
}

image_first_and_replay() {
    local query="$1"
    local pre_count
    pre_count=$(query_row_count "$query")
    if [[ "$pre_count" != "0" ]]; then
        log_fail "image precondition failed: '$query' already exists in SQLite ($pre_count row(s))"
        return 1
    fi

    local first_code first_body replay_code replay_body
    local first_out replay_out first_start first_end replay_start replay_end
    local first_asset_id replay_asset_id first_hash replay_hash first_cache_hit replay_cache_hit
    local first_cache_source replay_cache_source first_provider replay_provider
    local first_row first_local first_mime first_size first_width first_height first_source_query first_source_image_url first_source_page_url first_source_name
    local replay_row

    local attempt=0
    local first_code first_body
    first_start=$(now_ms)
    while [[ "$attempt" -lt 3 ]]; do
        IFS=$'\t' read -r first_code first_body < <(image_fetch "$query" "first")
        if [[ "$first_code" =~ ^2[0-9][0-9]$ ]]; then
            break
        fi
        attempt=$((attempt + 1))
        if [[ "$attempt" -lt 3 ]]; then
            log_warn "image[first] HTTP $first_code for '$query' (attempt $attempt); retrying after 15s"
            sleep 15
        fi
    done
    first_end=$(now_ms)
    first_out="$first_body"
    if [[ ! "$first_code" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "image[first] HTTP $first_code for '$query' after 3 attempts"
        smoke_echo_safe "$(head -c 500 "$first_out" 2>/dev/null || true)" >&2
        return 1
    fi
    if ! jq -e '
        (.count == 1)
        and ((.results | length) == 1)
        and (.results[0].origin == "retrieved")
        and ((.results[0].provider | length) > 0)
        and ((.results[0].preview_url | length) > 0)
    ' "$first_out" >/dev/null 2>&1; then
        log_fail "image[first] response shape invalid for '$query'"
        smoke_echo_safe "$(head -c 500 "$first_out" 2>/dev/null || true)" >&2
        return 1
    fi

    first_asset_id=$(jq -r '.results[0].asset_id // empty' "$first_out")
    first_cache_hit=$(jq -r '.results[0].cache_hit // false' "$first_out")
    first_cache_source=$(jq -r '.results[0].cache_source // empty' "$first_out")
    first_provider=$(jq -r '.results[0].retrieval_provider // empty' "$first_out")
    if [[ -z "$first_asset_id" || "$first_cache_hit" != "false" || "$first_cache_source" != "provider" || -z "$first_provider" ]]; then
        log_fail "image[first] cache trace invalid for '$query' (asset_id=$first_asset_id hit=$first_cache_hit source=$first_cache_source provider=$first_provider)"
        return 1
    fi

    first_row=$(db_json "
        SELECT
            id,
            COALESCE(file_hash, '') AS file_hash,
            COALESCE(local_path, '') AS local_path,
            COALESCE(origin, '') AS origin,
            COALESCE(provider, '') AS provider,
            CASE WHEN COALESCE(width, 0) > 0 THEN width ELSE COALESCE(json_extract(metadata_json, '$.width'), 0) END AS width,
            CASE WHEN COALESCE(height, 0) > 0 THEN height ELSE COALESCE(json_extract(metadata_json, '$.height'), 0) END AS height,
            COALESCE(json_extract(metadata_json, '$.source_query'), '') AS source_query,
            COALESCE(json_extract(metadata_json, '$.resolved_query'), '') AS resolved_query,
            COALESCE(json_extract(metadata_json, '$.source_image_url'), '') AS source_image_url,
            COALESCE(json_extract(metadata_json, '$.source_page_url'), '') AS source_page_url,
            COALESCE(json_extract(metadata_json, '$.source_name'), '') AS source_name
        FROM media_assets
        WHERE id = '$(sql_escape "$first_asset_id")'
    ")
    if [[ -z "$first_row" || "$first_row" == "[]" ]]; then
        log_fail "image[first] missing SQLite row for '$query' asset_id=$first_asset_id"
        return 1
    fi
    first_hash=$(jq -r '.[0].file_hash // empty' <<<"$first_row")
    first_local=$(jq -r '.[0].local_path // empty' <<<"$first_row")
    first_width=$(jq -r '.[0].width // 0' <<<"$first_row")
    first_height=$(jq -r '.[0].height // 0' <<<"$first_row")
    first_source_query=$(jq -r '.[0].source_query // empty' <<<"$first_row")
    first_source_image_url=$(jq -r '.[0].source_image_url // empty' <<<"$first_row")
    first_source_page_url=$(jq -r '.[0].source_page_url // empty' <<<"$first_row")
    first_source_name=$(jq -r '.[0].source_name // empty' <<<"$first_row")
    if [[ "$first_hash" != "$first_asset_id" || "$first_source_query" != "$query" ]]; then
        log_fail "image[first] hash/query mismatch for '$query' (asset_id=$first_asset_id hash=$first_hash source_query=$first_source_query)"
        return 1
    fi
    if [[ -z "$first_local" || ! -f "$first_local" ]]; then
        log_fail "image[first] local file missing for '$query' ($first_local)"
        return 1
    fi
    first_mime=$(file -b --mime-type "$first_local" 2>/dev/null || true)
    first_size=$(stat -c%s "$first_local" 2>/dev/null || echo 0)
    if [[ ! "$first_mime" =~ ^image/ || "$first_size" -le 0 ]]; then
        log_fail "image[first] invalid image file for '$query' (mime=$first_mime size=$first_size path=$first_local)"
        return 1
    fi
    if [[ "$first_width" -le 0 || "$first_height" -le 0 || -z "$first_source_image_url" || -z "$first_source_page_url" || -z "$first_source_name" ]]; then
        log_fail "image[first] incomplete provenance/dimensions for '$query'"
        return 1
    fi

    if [[ "$(query_row_count "$query")" != "1" ]]; then
        log_fail "image[first] row count not stable for '$query'"
        return 1
    fi

    local first_ms replay_ms
    first_ms=$((first_end - first_start))
    log_pass "image[first] '$query' asset_id=$first_asset_id hash=$first_hash provider=$first_provider (${first_ms}ms)"

    replay_start=$(now_ms)
    IFS=$'\t' read -r replay_code replay_body < <(image_fetch "$query" "replay")
    replay_end=$(now_ms)
    replay_out="$replay_body"
    if [[ ! "$replay_code" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "image[replay] HTTP $replay_code for '$query'"
        smoke_echo_safe "$(head -c 500 "$replay_out" 2>/dev/null || true)" >&2
        return 1
    fi
    replay_asset_id=$(jq -r '.results[0].asset_id // empty' "$replay_out")
    replay_hash="$replay_asset_id"
    replay_cache_hit=$(jq -r '.results[0].cache_hit // false' "$replay_out")
    replay_cache_source=$(jq -r '.results[0].cache_source // empty' "$replay_out")
    replay_provider=$(jq -r '.results[0].retrieval_provider // empty' "$replay_out")
    if [[ "$replay_asset_id" != "$first_asset_id" || "$replay_hash" != "$first_hash" || "$replay_cache_hit" != "true" || "$replay_cache_source" != "database" ]]; then
        log_fail "image[replay] cache identity mismatch for '$query' (first=$first_asset_id replay=$replay_asset_id hit=$replay_cache_hit source=$replay_cache_source provider=$replay_provider)"
        return 1
    fi
    replay_ms=$((replay_end - replay_start))
    if [[ "$replay_ms" -ge "$first_ms" ]]; then
        log_fail "image[replay] not faster for '$query' (first=${first_ms}ms replay=${replay_ms}ms)"
        return 1
    fi
    if [[ "$(query_row_count "$query")" != "1" ]]; then
        log_fail "image[replay] duplicate row(s) appeared for '$query'"
        return 1
    fi
    replay_row=$(db_json "
        SELECT COALESCE(file_hash, '') AS file_hash,
               COALESCE(local_path, '') AS local_path
        FROM media_assets
        WHERE id = '$(sql_escape "$replay_asset_id")'
    ")
    if [[ "$(jq -r '.[0].file_hash // empty' <<<"$replay_row")" != "$first_hash" ]]; then
        log_fail "image[replay] DB hash changed for '$query'"
        return 1
    fi
    log_pass "image[replay] '$query' asset_id=$replay_asset_id hash=$replay_hash (${replay_ms}ms < ${first_ms}ms)"
}

image_anti_collision() {
    local q1="$1"
    local q2="$2"
    local out1="$WORK_DIR/image_anti_a.json"
    local out2="$WORK_DIR/image_anti_b.json"
    local code1 code2 id1 id2 hash1 hash2

    code1=$(api_get_json "/api/images/retrieved/search" "$out1" \
        --data-urlencode "q=${q1}" \
        --data-urlencode "lang=en")
    code2=$(api_get_json "/api/images/retrieved/search" "$out2" \
        --data-urlencode "q=${q2}" \
        --data-urlencode "lang=en")
    if [[ ! "$code1" =~ ^2[0-9][0-9]$ || ! "$code2" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "image anti-collision HTTP failure (q1=$code1 q2=$code2)"
        return 1
    fi
    id1=$(jq -r '.results[0].asset_id // empty' "$out1")
    id2=$(jq -r '.results[0].asset_id // empty' "$out2")
    hash1="$id1"
    hash2="$id2"
    if [[ -z "$id1" || -z "$id2" || "$id1" == "$id2" || "$hash1" == "$hash2" ]]; then
        log_fail "image anti-collision failed: '$q1' -> $id1, '$q2' -> $id2"
        return 1
    fi
    log_pass "image anti-collision: '$q1' -> $id1 and '$q2' -> $id2"
}

assert_artlist_diagnostics_green() {
    local body="$1"
    local probes=(scraper ffmpeg_binary)
    local probe
    for probe in "${probes[@]}"; do
        if ! jq -e --arg p "$probe" '.[$p].ok == true' "$body" >/dev/null 2>&1; then
            log_fail "artlist diagnostics probe $probe is not ok"
            smoke_echo_safe "$(head -c 1000 "$body" 2>/dev/null || true)" >&2
            return 1
        fi
    done
    log_pass "artlist diagnostics: all active dependency probes ok"
}

check_artlist_live() {
    local body="$1"
    if ! jq -e '
        (.live_enforced == true)
        and (.cache_strategy == "bypassed")
        and ((.clips | length) > 0)
    ' "$body" >/dev/null 2>&1; then
        log_fail "artlist live response contract failed"
        smoke_echo_safe "$(head -c 1000 "$body" 2>/dev/null || true)" >&2
        return 1
    fi
    if ! jq -e '
        .clips[]
        | select(
            ((.ID // .ExternalID // "") | length) > 0
            and ((.Title // "") | length) > 0
            and ((.PageURL // "") | startswith("https://artlist.io/"))
            and (((.Provider // "") == "artlist") or ((.SourceName // "") == "artlist"))
            and (.RawMetadata != null)
        )
    ' "$body" >/dev/null 2>&1; then
        log_fail "artlist live clips missing artlist metadata or page URLs"
        smoke_echo_safe "$(head -c 1000 "$body" 2>/dev/null || true)" >&2
        return 1
    fi
    log_pass "artlist live: forced live and returned artlist clips"
}

artlist_run_payload() {
    local limit="$1"
    if [[ -n "$ARTLIST_ROOT_FOLDER" ]]; then
        jq -nc \
            --arg term "$ARTLIST_TERM" \
            --argjson limit "$limit" \
            --arg rid "$ARTLIST_ROOT_FOLDER" \
            '{
                term: $term,
                limit: $limit,
                strategy: "replace",
                clip_duration: 7,
                width: 1920,
                height: 1080,
                fps: 30,
                concurrency: 1,
                dry_run: false,
                root_folder_id: $rid
            }'
    else
        jq -nc \
            --arg term "$ARTLIST_TERM" \
            --argjson limit "$limit" \
            '{
                term: $term,
                limit: $limit,
                strategy: "replace",
                clip_duration: 7,
                width: 1920,
                height: 1080,
                fps: 30,
                concurrency: 1,
                dry_run: false
            }'
    fi
}

artlist_run() {
    local label="$1"
    local limit="$2"
    local out="$WORK_DIR/artlist_${label}.json"
    local code jid
    code=$(api_post_json "/api/artlist/run" "$out" "$(artlist_run_payload "$limit")")
    jid=$(jq -r '.run_id // empty' "$out")
    printf '%s\t%s\t%s\n' "$code" "$jid" "$out"
}

poll_artlist() {
    local jid="$1"
    if ! smoke_poll_terminal "$jid"; then
        log_fail "artlist job $jid did not reach terminal"
        return 1
    fi
    if [[ "$SMOKE_LAST_STATUS" != "SUCCEEDED" && "$SMOKE_LAST_STATUS" != "completed" ]]; then
        log_fail "artlist job $jid terminal status=$SMOKE_LAST_STATUS"
        return 1
    fi
    log_pass "artlist job $jid terminal status=$SMOKE_LAST_STATUS"
}

artlist_expected_rows() {
    local body="$1"
    local limit="$2"
    if ! jq -e --argjson limit "$limit" '
        (.status == "SUCCEEDED" or .status == "completed")
        and ((.result.items | length) == $limit)
        and ([.result.items[]? | select((.status // "") | startswith("blocked_"))] | length == 0)
    ' "$body" >/dev/null 2>&1; then
        log_fail "artlist result shape/status invalid"
        smoke_echo_safe "$(head -c 1200 "$body" 2>/dev/null || true)" >&2
        return 1
    fi
    log_pass "artlist job returned $(jq -r '.result.items | length' "$body") completed item(s)"
}

verify_artlist_item() {
    local clip_id="$1"
    local row local_path mime_type mime_size outbox_status
    row=$(db_json "
        SELECT
            id,
            COALESCE(source, '') AS source,
            COALESCE(media_type, '') AS media_type,
            COALESCE(lifecycle_state, '') AS lifecycle_state,
            COALESCE(index_state, '') AS index_state,
            COALESCE(duration_ms, 0) AS duration_ms,
            COALESCE(width, 0) AS width,
            COALESCE(height, 0) AS height,
            COALESCE(drive_file_id, '') AS drive_file_id,
            COALESCE(drive_link, '') AS drive_link,
            COALESCE(download_link, '') AS download_link,
            COALESCE(file_hash, '') AS file_hash,
            COALESCE(source_version, '') AS source_version,
            COALESCE(source_provider, '') AS source_provider,
            COALESCE(source_url, '') AS source_url,
            COALESCE(title, '') AS title,
            COALESCE(json_extract(metadata_json, '$.metadata_origin'), '') AS metadata_origin,
            COALESCE(json_extract(metadata_json, '$.provider_tags'), '[]') AS provider_tags,
            COALESCE(json_extract(metadata_json, '$.provider_categories'), '[]') AS provider_categories,
            COALESCE(json_extract(metadata_json, '$.discovered_by_queries'), '[]') AS discovered_by_queries
        FROM media_assets
        WHERE id = '$(sql_escape "$clip_id")'
    ")
    if [[ -z "$row" || "$row" == "[]" ]]; then
        log_fail "artlist item $clip_id missing media_assets row"
        return 1
    fi
    if ! jq -e '
        .[0].source == "artlist"
        and .[0].media_type == "video"
        and .[0].lifecycle_state == "PUBLISHED"
        and .[0].index_state == "INDEXED"
        and ((.[0].duration_ms | tonumber) >= 6500)
        and ((.[0].duration_ms | tonumber) <= 8500)
        and ((.[0].width | tonumber) == 1920)
        and ((.[0].height | tonumber) == 1080)
        and ((.[0].drive_file_id | length) > 0)
        and ((.[0].drive_link | length) > 0)
        and ((.[0].download_link | length) > 0)
        and ((.[0].file_hash | length) > 0)
        and ((.[0].source_version | length) > 0)
        and ((.[0].source_provider | length) > 0)
        and ((.[0].metadata_origin) == "artlist")
        and ((.[0].provider_tags | length) > 0)
        and ((.[0].provider_categories | length) > 0)
        and ((.[0].discovered_by_queries | length) > 0)
    ' <<<"$row" >/dev/null 2>&1; then
        log_fail "artlist item $clip_id failed database shape checks"
        smoke_echo_safe "$(head -c 1200 <<<"$row" 2>/dev/null || true)" >&2
        return 1
    fi
    local_path=$(db_scalar "SELECT COALESCE(local_path, '') FROM media_assets WHERE id='$(sql_escape "$clip_id")'")
    if [[ -z "$local_path" || ! -f "$local_path" ]]; then
        log_fail "artlist item $clip_id local MP4 missing: path=$local_path"
        return 1
    fi
    mime_type=$(file -b --mime-type "$local_path" 2>/dev/null || true)
    mime_size=$(stat -c%s "$local_path" 2>/dev/null || echo 0)
    if [[ "$mime_type" != "video/mp4" ]]; then
        log_fail "artlist item $clip_id mime_type=$mime_type, want video/mp4"
        return 1
    fi
    if [[ "$mime_size" -le 0 ]]; then
        log_fail "artlist item $clip_id empty local file"
        return 1
    fi
    outbox_status=$(db_scalar "SELECT COALESCE(status, '') FROM outbox_events WHERE event_type='asset.index.requested' AND aggregate_id='$(sql_escape "$clip_id")' ORDER BY id DESC LIMIT 1")
    if [[ "$outbox_status" != "completed" && "$outbox_status" != "superseded" ]]; then
        log_fail "artlist item $clip_id outbox status=$outbox_status"
        return 1
    fi
    log_pass "artlist item $clip_id db/file checks ok ($local_path)"
}

verify_drive() {
    local clip_id="$1"
    local drive_file_id out code
    drive_file_id=$(db_scalar "SELECT COALESCE(drive_file_id, '') FROM media_assets WHERE id='$(sql_escape "$clip_id")'")
    if [[ -z "$drive_file_id" ]]; then
        log_fail "artlist item $clip_id missing drive_file_id"
        return 1
    fi
    out="$WORK_DIR/drive_${clip_id}.json"
    code=$(api_post_json "/api/drive/resolve-by-id" "$out" "$(jq -nc --arg id "$drive_file_id" '{ids: [$id]}')")
    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "Drive resolve-by-id HTTP $code for $clip_id"
        smoke_echo_safe "$(head -c 800 "$out" 2>/dev/null || true)" >&2
        return 1
    fi
    if ! jq -e '.ok == true and (.resolved_count // 0) >= 1 and (.resolved[0].trashed == false) and ((.resolved[0].size // 0) > 0)' "$out" >/dev/null 2>&1; then
        log_fail "Drive resolve-by-id contract failed for $clip_id"
        smoke_echo_safe "$(head -c 800 "$out" 2>/dev/null || true)" >&2
        return 1
    fi
    log_pass "Drive resolve-by-id ok for $clip_id"
}

verify_qdrant() {
    local clip_id="$1"
    local out="$WORK_DIR/qdrant_${clip_id}.json"
    local headers=()
    if [[ -n "$QDRANT_API_KEY" ]]; then
        headers+=(-H "api-key: $QDRANT_API_KEY")
    fi
    local code
    code=$(curl -sS --connect-timeout 5 --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
        -X POST \
        -o "$out" \
        -w '%{http_code}' \
        "${headers[@]}" \
        -H 'Content-Type: application/json' \
        -d "$(jq -nc --arg id "$clip_id" '{
            filter: { must: [ { key: "asset_id", match: { value: $id } } ] },
            limit: 5,
            with_payload: true,
            with_vector: false
        }')" \
        "$QDRANT_URL/collections/$COLLECTION/points/scroll")
    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "Qdrant scroll HTTP $code for $clip_id"
        smoke_echo_safe "$(head -c 800 "$out" 2>/dev/null || true)" >&2
        return 1
    fi
    if ! jq -e '.result.points | length >= 1 and .result.points[0].payload.source == "artlist" and .result.points[0].payload.media_type == "video" and .result.points[0].payload.lifecycle_state == "PUBLISHED"' "$out" >/dev/null 2>&1; then
        log_fail "Qdrant payload contract failed for $clip_id"
        smoke_echo_safe "$(head -c 800 "$out" 2>/dev/null || true)" >&2
        return 1
    fi
    log_pass "Qdrant point ok for $clip_id"
}

verify_media_search() {
    local clip_id="$1"
    local out="$WORK_DIR/media_search_${clip_id}.json"
    local code
    code=$(api_post_json "/api/media/search" "$out" "$(jq -nc --arg q "$ARTLIST_TERM" '{
        query: $q,
        sources: ["artlist"],
        mode: "hybrid",
        limit: 10
    }')")
    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        log_warn "/api/media/search HTTP $code"
        return 0
    fi
    if jq -e --arg id "$clip_id" '.results[]? | select((.asset_id // .id // "") == $id)' "$out" >/dev/null 2>&1; then
        log_pass "/api/media/search returned $clip_id"
    else
        log_warn "/api/media/search did not return $clip_id"
    fi
}

cleanup_existing() {
    smoke_log_section "Cleanup"
    log_info "Cleaning up previous image test assets and outbox rows..."
    local q_escaped hash query
    for query in "${IMAGE_QUERIES[@]}" "$ANTI_COLLISION_A" "$ANTI_COLLISION_B"; do
        q_escaped=$(sql_escape "$query")
        while IFS= read -r hash; do
            [[ -z "$hash" ]] && continue
            sqlite3 "$DB_PATH" "DELETE FROM outbox_events WHERE event_key LIKE 'reconcile:reindex:${hash}%';" 2>/dev/null || true
        done < <(sqlite3 -readonly "$DB_PATH" "SELECT file_hash FROM media_assets WHERE source='image' AND COALESCE(json_extract(metadata_json, '$.source_query'), '') = '${q_escaped}';" 2>/dev/null)
        sqlite3 "$DB_PATH" "DELETE FROM media_assets WHERE source='image' AND COALESCE(json_extract(metadata_json, '$.source_query'), '') = '${q_escaped}';" 2>/dev/null || true
    done
    log_info "Cleaning up previous Artlist test runs..."
    sqlite3 "$DB_PATH" "DELETE FROM artlist_runs WHERE term='$(sql_escape "$ARTLIST_TERM")';" 2>/dev/null || true
    log_info "Cleanup completed"
}

assert_image_route_available() {
    local query="$1"
    local out="$WORK_DIR/route_${RANDOM}.json"
    local code
    code=$(api_get_json "/api/images/retrieved/search" "$out" \
        --data-urlencode "q=${query}" \
        --data-urlencode "lang=en")
    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "/api/images/retrieved/search HTTP $code for '$query'"
        smoke_echo_safe "$(head -c 500 "$out" 2>/dev/null || true)" >&2
        return 1
    fi
    if ! jq -e '.results | length == 1' "$out" >/dev/null 2>&1; then
        log_fail "/api/images/retrieved/search response malformed for '$query'"
        smoke_echo_safe "$(head -c 500 "$out" 2>/dev/null || true)" >&2
        return 1
    fi
    log_pass "/api/images/retrieved/search reachable for '$query'"
}

startup_checks() {
    smoke_log_section "Startup"

    smoke_curl GET "/health" >/dev/null
    assert_2xx "GET /health" || return 1
    log_pass "server /health reachable"

    smoke_curl GET "/ready" >/dev/null
    assert_2xx "GET /ready" || return 1
    if ! jq -e '.status == "ready"' "$SMOKE_LAST_BODY" >/dev/null 2>&1; then
        log_fail "/ready is not ready"
        smoke_echo_safe "$(head -c 500 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        return 1
    fi
    log_pass "server /ready ready"

    smoke_curl GET "/api/images/diagnostics" >/dev/null
    assert_2xx "GET /api/images/diagnostics" || return 1
    if ! jq -e '.ok == true' "$SMOKE_LAST_BODY" >/dev/null 2>&1; then
        log_fail "/api/images/diagnostics ok=false"
        smoke_echo_safe "$(head -c 500 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        return 1
    fi
    log_pass "image diagnostics reachable"

    smoke_curl GET "/api/artlist/job-consumer" >/dev/null
    assert_2xx "GET /api/artlist/job-consumer" || return 1
    if ! jq -e '.active == true and .consumer_type == "media.artlist"' "$SMOKE_LAST_BODY" >/dev/null 2>&1; then
        log_fail "/api/artlist/job-consumer not active"
        smoke_echo_safe "$(head -c 500 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        return 1
    fi
    log_pass "artlist job-consumer active"

    smoke_curl GET "/api/artlist/diagnostics?term=$(printf '%s' "$ARTLIST_TERM" | jq -sRr @uri)" >/dev/null
    assert_2xx "GET /api/artlist/diagnostics" || return 1
    assert_artlist_diagnostics_green "$SMOKE_LAST_BODY" || return 1

    if ! command -v ffmpeg >/dev/null 2>&1; then
        log_fail "ffmpeg missing on PATH"
        return 1
    fi
    log_pass "ffmpeg on PATH"

    if [[ ! -f "$DB_PATH" ]]; then
        log_fail "SQLite DB missing: $DB_PATH"
        return 1
    fi
    if ! db_scalar "SELECT 1" >/dev/null; then
        log_fail "SQLite DB not readable: $DB_PATH"
        return 1
    fi
    log_pass "SQLite readable"

    local scraper_health qdrant_cols
    scraper_health=$(curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" "$SCRAPER_URL/health" 2>/dev/null || true)
    if ! jq -e '.ok == true' <<<"$scraper_health" >/dev/null 2>&1; then
        log_fail "scraper /health not ok at $SCRAPER_URL/health"
        smoke_echo_safe "$(head -c 500 <<<"$scraper_health" 2>/dev/null || true)" >&2
        return 1
    fi
    log_pass "scraper /health reachable"

    qdrant_cols=$(curl -sS --max-time 5 "$QDRANT_URL/collections" 2>/dev/null || true)
    if ! jq -e '.result.collections | length >= 0' <<<"$qdrant_cols" >/dev/null 2>&1; then
        log_fail "Qdrant /collections not reachable"
        smoke_echo_safe "$(head -c 500 <<<"$qdrant_cols" 2>/dev/null || true)" >&2
        return 1
    fi
    log_pass "Qdrant reachable"

    if [[ -n "$ARTLIST_ROOT_FOLDER" ]]; then
        log_pass "Artlist Drive root configured"
    else
        log_warn "Artlist Drive root not configured in env; diagnostics may still pass if wired elsewhere"
    fi
}

verify_live_artlist() {
    smoke_log_section "Artlist Live"
    local out="$WORK_DIR/artlist_live.json"
    local code
    code=$(api_get_json "/api/artlist/search/live" "$out" \
        --data-urlencode "term=${ARTLIST_TERM}" \
        --data-urlencode "limit=5")
    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "GET /api/artlist/search/live HTTP $code"
        smoke_echo_safe "$(head -c 800 "$out" 2>/dev/null || true)" >&2
        return 1
    fi
    check_artlist_live "$out" || return 1
}

verify_artlist_pipeline() {
    smoke_log_section "Artlist Pipeline"
    local before_count
    before_count=$(artlist_row_count "$ARTLIST_TERM")
    if [[ "$before_count" != "0" ]]; then
        log_fail "artlist precondition failed: '$ARTLIST_TERM' already exists in artlist_runs ($before_count row(s))"
        return 1
    fi

    local run1_code run1_jid run1_out run2_code run2_jid run2_out
    local run1_body run2_body
    local run1_ids run2_ids
    local run1_rows run2_rows

    IFS=$'\t' read -r run1_code run1_jid run1_out < <(artlist_run first 3)
    if [[ ! "$run1_code" =~ ^2[0-9][0-9]$ || -z "$run1_jid" ]]; then
        log_fail "artlist first run enqueue failed (HTTP $run1_code jid=$run1_jid)"
        smoke_echo_safe "$(head -c 800 "$run1_out" 2>/dev/null || true)" >&2
        return 1
    fi
    poll_artlist "$run1_jid" || return 1
    run1_body="$SMOKE_LAST_BODY"
    artlist_expected_rows "$run1_body" 3 || return 1

    run1_ids=$(jq -r '.result.items[]?.clip_id // empty' "$run1_body")
    run1_rows=$(jq -r '.result.items[] | [.clip_id, .drive_file_id, .file_hash] | @tsv' "$run1_body" | sort)
    local clip_id
    for clip_id in $run1_ids; do
        verify_artlist_item "$clip_id" || return 1
        verify_drive "$clip_id" || return 1
        verify_qdrant "$clip_id" || return 1
        verify_media_search "$clip_id" || true
    done

    IFS=$'\t' read -r run2_code run2_jid run2_out < <(artlist_run replay 3)
    if [[ ! "$run2_code" =~ ^2[0-9][0-9]$ || -z "$run2_jid" ]]; then
        log_fail "artlist replay enqueue failed (HTTP $run2_code jid=$run2_jid)"
        smoke_echo_safe "$(head -c 800 "$run2_out" 2>/dev/null || true)" >&2
        return 1
    fi
    poll_artlist "$run2_jid" || return 1
    run2_body="$SMOKE_LAST_BODY"
    artlist_expected_rows "$run2_body" 3 || return 1

    run2_ids=$(jq -r '.result.items[]?.clip_id // empty' "$run2_body")
    run2_rows=$(jq -r '.result.items[] | [.clip_id, .drive_file_id, .file_hash] | @tsv' "$run2_body" | sort)
    for clip_id in $run2_ids; do
        verify_artlist_item "$clip_id" || return 1
        verify_drive "$clip_id" || return 1
        verify_qdrant "$clip_id" || return 1
        verify_media_search "$clip_id" || true
    done

    if [[ "$(printf '%s\n' "$run1_ids" | sort)" != "$(printf '%s\n' "$run2_ids" | sort)" ]]; then
        log_fail "artlist replay returned different clip IDs"
        printf 'run1:\n%s\nrun2:\n%s\n' "$(printf '%s\n' "$run1_ids" | sort)" "$(printf '%s\n' "$run2_ids" | sort)" >&2
        return 1
    fi
    if [[ "$run1_rows" != "$run2_rows" ]]; then
        log_fail "artlist replay returned different clip/file/hash tuples"
        printf 'run1:\n%s\nrun2:\n%s\n' "$run1_rows" "$run2_rows" >&2
        return 1
    fi
    log_pass "artlist replay returned same clip IDs, drive IDs, and hashes"
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — live media battery would probe:"
        printf '  GET  %s/health\n' "$BASE_URL"
        printf '  GET  %s/ready\n' "$BASE_URL"
        printf '  GET  %s/api/images/diagnostics\n' "$BASE_URL"
        printf '  GET  %s/api/artlist/job-consumer\n' "$BASE_URL"
        printf '  GET  %s/api/artlist/diagnostics?term=%s\n' "$BASE_URL" "$ARTLIST_TERM"
        printf '  GET  %s/api/images/retrieved/search?q=<query>&lang=en\n' "$BASE_URL"
        printf '  GET  %s/api/artlist/search/live?term=<term>&limit=5\n' "$BASE_URL"
        printf '  POST %s/api/artlist/run\n' "$BASE_URL"
        exit 0
    fi

    startup_checks || exit 1
    cleanup_existing || exit 1

    smoke_log_section "Images"
    for query in "${IMAGE_QUERIES[@]}"; do
        image_first_and_replay "$query" || exit 1
    done
    image_anti_collision "$ANTI_COLLISION_A" "$ANTI_COLLISION_B" || exit 1
    assert_image_route_available "${IMAGE_QUERIES[0]}" || exit 1

    verify_live_artlist || exit 1
    verify_artlist_pipeline || exit 1

    printf '\n============================================\n'
    printf '  VidRush Media E2E Battery\n'
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

#!/usr/bin/env bash
# tests/operational/pipeline_live_e2e.sh — live 10-step pipeline gate.
#
# The live half of the pipeline E2E gate; the hermetic half is
# internal/platform/httpserver/server_pipeline_e2e_test.go
# (`make verify-pipeline-e2e`). This script is the ONLY place that asserts
# reality: a real Drive file under the requested folder, a real MP4 with a
# decodable video stream, and an asset retrievable from the canonical catalog.
#
# Coverage (10 steps, 10/10 PASS required):
#   1. YouTube keyword discovery          GET  /api/clips/search
#   2. YouTube metadata for the video     GET  /api/clips/info?url=
#   3. YouTube clip processing            POST /api/clips/process
#   4. YouTube Drive hierarchy            job full result: drive_* populated
#   5. YouTube clip indexed + searchable  POST /api/media/search
#   6. Stock direct-URL run               POST /api/stock-pipeline/run
#   7. Stock search-and-run               POST /api/stock-pipeline/search-and-run
#   8. Stock Drive artifact               job full result: drive_* populated
#   9. Stock indexed + downloadable       POST /api/media/search + /download
#  10. Idempotent replay                  POST /api/clips/process with the same key
#
# Contract facts this battery depends on (verified against the handlers):
#   - POST /api/clips/process returns an ACK only ({ok,message}). It does NOT
#     return a job_id, so step 3 locates the job through
#     GET /api/jobs?type=youtube_clip.extract and matches the segment name.
#     Set PIPELINE_E2E_JOB_ID to skip the lookup.
#   - Destination fields MUST be nested under `destination`; the legacy
#     top-level shape (group/folder_id/folder_path/subfolder_name/
#     create_subfolder) is rejected with 400 by ExtractRequest.UnmarshalJSON.
#   - /api/stock-pipeline/run takes `search_queries` (legacy shape).
#     /api/stock-pipeline/search-and-run takes `queries:[{q,limit}]`.
#     Sending `search_queries` to search-and-run is silently ignored by the
#     binding and then fails the source-presence gate with 400.
#   - The explicit Stock duration contract is all-or-nothing:
#     target_total_duration_seconds, target_duration_per_source_seconds,
#     clips_per_source, clip_duration_seconds (per_source must equal
#     clips_per_source x clip_duration_seconds, total a multiple of
#     per_source) and download_mode="sections_only".
#   - The clip download route is POST /api/media/clips/:source/clips/:id/download
#     (the clips capability mounts under the /api/media/clips wire prefix, NOT
#     directly under /api/media; the bare /api/media/:source/... shape 404s).
#
# Usage:
#   export DRIVE_ROOT_FOLDER_ID=<drive folder id>
#   export STOCK_DIRECT_URL=https://.../test-video.mp4
#   export VIDEO_URL=https://www.youtube.com/watch?v=...   # optional
#   bash tests/operational/pipeline_live_e2e.sh
#   bash tests/operational/pipeline_live_e2e.sh --dry
#
# Exit codes follow tests/operational/lib/common.sh:
#   0 every step passed | 1 at least one step failed | 2 setup error | 124 timeout.

set -Eeuo pipefail

DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"
smoke_require curl jq

if [[ "${HELP_REQUESTED:-0}" == "1" ]]; then
    sed -n '1,50p' "${BASH_SOURCE[0]}"
    exit 0
fi

# ── Tunables ────────────────────────────────────────────────────────────
RESULTS_DIR="${PIPELINE_E2E_RESULTS_DIR:-$DIR/results/pipeline-live}"
DRIVE_ROOT_FOLDER_ID="${DRIVE_ROOT_FOLDER_ID:-}"
STOCK_DIRECT_URL="${STOCK_DIRECT_URL:-}"
VIDEO_URL="${VIDEO_URL:-}"
YT_QUERY="${PIPELINE_E2E_YT_QUERY:-Muhammad Ali boxing}"
STOCK_QUERY="${PIPELINE_E2E_STOCK_QUERY:-city skyline technology b-roll}"
RUN_TAG="${PIPELINE_E2E_RUN_TAG:-pipeline-e2e-$(date -u +%Y%m%dT%H%M%SZ)-${RANDOM}}"
PIPELINE_E2E_POLL_TIMEOUT_SECONDS="${PIPELINE_E2E_POLL_TIMEOUT_SECONDS:-600}"
PIPELINE_E2E_JOB_ID="${PIPELINE_E2E_JOB_ID:-}"
SMOKE_POLL_TIMEOUT_SECONDS="$PIPELINE_E2E_POLL_TIMEOUT_SECONDS"

DRIVE_FOLDER_GROUP="$RUN_TAG-youtube"
STOCK_FOLDER_NAME="$RUN_TAG-stock"

TOTAL=0
PASSED=0
FAILED_STEPS=()
DISCOVERED_VIDEO_URL=""
TARGET_VIDEO_URL="${VIDEO_URL:-}"
TARGET_VIDEO_ID=""
YT_JOB_ID=""
STOCK_JOB_ID=""
YT_ASSET_ID=""
STOCK_ASSET_ID=""
YT_SEARCH_COUNT_BEFORE=0

if [[ "$DRY_RUN" == "1" ]]; then
    printf '%splanning only (--dry): no HTTP request will be sent%s\n' "$DIM" "$RESET"
    printf '  run tag      : %s\n' "$RUN_TAG"
    printf '  api base     : http://%s\n' "$SMOKE_API_BASE"
    printf '  results dir  : %s\n' "$RESULTS_DIR"
    printf '  yt query     : %s\n' "$YT_QUERY"
    printf '  drive root   : %s\n' "${DRIVE_ROOT_FOLDER_ID:-<unset>}"
    printf '  direct url   : %s\n' "${STOCK_DIRECT_URL:-<unset>}"
    printf '  10 steps     : search, info, process, drive, indexed, run,\n'
    printf '                 search-and-run, drive, download, replay\n'
    exit 0
fi

# ── Setup guards ────────────────────────────────────────────────────────
smoke_require ffprobe
if [[ -z "$DRIVE_ROOT_FOLDER_ID" ]]; then
    printf '%ssetup error: DRIVE_ROOT_FOLDER_ID is required (steps 3/4/6/7/8 need a real destination)%s\n' \
        "$RED" "$RESET" >&2
    exit 2
fi
if [[ -z "$STOCK_DIRECT_URL" ]]; then
    printf '%ssetup error: STOCK_DIRECT_URL is required (step 6 needs a direct source URL)%s\n' \
        "$RED" "$RESET" >&2
    exit 2
fi

mkdir -p "$RESULTS_DIR"
chmod 700 "$RESULTS_DIR"

# ── Helpers ─────────────────────────────────────────────────────────────

# results_file KIND — canonical artifact path for this run.
results_file() { printf '%s/%s-%s.json' "$RESULTS_DIR" "$1" "$RUN_TAG"; }

# capture KIND — persist the last response body as a retained artifact.
# Artifacts are token-redacted by construction (lib/common.sh never stores
# Authorization headers in the body) and stored 0600.
capture() {
    local out
    out=$(results_file "$1")
    cp "${SMOKE_LAST_BODY:-/dev/null}" "$out" 2>/dev/null || true
    chmod 600 "$out" 2>/dev/null || true
}

# urlencode STRING — percent-encode a query-string value.
urlencode() { jq -rn --arg v "$1" '$v|@uri'; }

# pipeline_fail MESSAGE — print a diagnosed failure. Always returns 0 so a
# caller can write `pipeline_fail "..."; return 1` under `set -e`.
pipeline_fail() {
    printf '%sFAIL: %s%s\n' "$RED" "$1" "$RESET" >&2
    if [[ -s "${SMOKE_LAST_BODY:-}" ]]; then
        smoke_echo_safe "$(head -c 600 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    fi
    return 0
}

# pipeline_poll_full JOB_ID — poll /api/jobs/{id}/full until terminal.
# Returns 0 on a terminal state (inspect SMOKE_LAST_STATUS) and 124 on timeout.
pipeline_poll_full() {
    local job_id="$1"
    local deadline=$(( $(date +%s) + PIPELINE_E2E_POLL_TIMEOUT_SECONDS ))
    SMOKE_LAST_STATUS=""
    while (( $(date +%s) < deadline )); do
        smoke_wallclock_check
        smoke_curl GET "/api/jobs/${job_id}/full" >/dev/null
        if smoke_rate_limit_backoff; then
            continue
        fi
        if [[ "$SMOKE_LAST_HTTP" != "200" ]]; then
            return 1
        fi
        local status
        status=$(jq -r '.job.status // .status // "?"' "$SMOKE_LAST_BODY" | tr '[:lower:]' '[:upper:]')
        SMOKE_LAST_STATUS="$status"
        case "$status" in
            SUCCEEDED|INDEX_PENDING|COMPLETED|FAILED|CANCELLED|DEAD_LETTER)
                return 0
                ;;
        esac
        sleep "$SMOKE_POLL_INTERVAL_SECONDS"
    done
    return 124
}

# pipeline_assert_job_succeeded JOB_ID LABEL — poll, retain artifacts, assert
# the job reached a success terminal state.
pipeline_assert_job_succeeded() {
    local job_id="$1" label="$2"
    if ! pipeline_poll_full "$job_id"; then
        pipeline_fail "$label: polling /api/jobs/$job_id/full failed"
        return 1
    fi
    local status="$SMOKE_LAST_STATUS"
    capture "full-${label}"
    smoke_curl GET "/api/jobs/${job_id}" >/dev/null || true
    capture "status-${label}"
    case "$status" in
        SUCCEEDED|INDEX_PENDING|COMPLETED)
            printf '  job %s: %s\n' "$job_id" "$status"
            return 0
            ;;
        *)
            printf '%sFAIL: %s: job %s reached %s%s\n' "$RED" "$label" "$job_id" "$status" "$RESET" >&2
            return 1
            ;;
    esac
}

# pipeline_youtube_jobs TAG — print the youtube_clip.extract jobs whose payload
# carries a segment named *TAG* (JSON array on stdout).
pipeline_youtube_jobs() {
    local tag="$1"
    smoke_curl GET "/api/jobs?type=youtube_clip.extract&limit=50" >/dev/null
    if [[ "$SMOKE_LAST_HTTP" != "200" ]]; then
        printf '[]'
        return 0
    fi
    jq -c --arg tag "$tag" '
        [ .jobs[]?
          | select(any(((.payload.segments // [])[]?); ((.name // "") | contains($tag))))
        ]' "$SMOKE_LAST_BODY"
}

# pipeline_run_step NAME FN — run one step, never aborting the battery.
pipeline_run_step() {
    local name="$1"; shift
    TOTAL=$((TOTAL + 1))
    printf '\n%s===== STEP %d/10 — %s =====%s\n' "$CYAN" "$TOTAL" "$name" "$RESET"
    if "$@"; then
        PASSED=$((PASSED + 1))
        printf '%sPASS%s  %s\n' "$GREEN" "$RESET" "$name"
    else
        FAILED_STEPS+=("$name")
        printf '%sFAIL%s  %s\n' "$RED" "$RESET" "$name"
    fi
    return 0
}

# ── Step 1 — YouTube keyword discovery ─────────────────────────────────
step_1_youtube_keyword_search() {
    smoke_curl GET "/api/clips/search?q=$(urlencode "$YT_QUERY")&limit=10&sort=views" >/dev/null
    capture yt-search
    smoke_assert_http_2xx "GET /api/clips/search" || return 1

    local count
    count=$(jq -r '(.results // []) | length' "$SMOKE_LAST_BODY")
    if [[ "$count" -lt 1 ]]; then
        pipeline_fail "keyword search returned zero results"
        return 1
    fi
    if ! jq -e '(.ok == true) and ((.results[0].video_id // "") != "")' "$SMOKE_LAST_BODY" >/dev/null; then
        pipeline_fail "no usable video identity in the first result"
        return 1
    fi
    DISCOVERED_VIDEO_URL="https://www.youtube.com/watch?v=$(jq -r '.results[0].video_id' "$SMOKE_LAST_BODY")"
    printf '  discovered: %s\n' "$DISCOVERED_VIDEO_URL"
}

# ── Step 2 — YouTube metadata ──────────────────────────────────────────
step_2_youtube_info() {
    TARGET_VIDEO_URL="${VIDEO_URL:-${DISCOVERED_VIDEO_URL:-}}"
    if [[ -z "$TARGET_VIDEO_URL" ]]; then
        pipeline_fail "no video URL available (step 1 produced none and VIDEO_URL is unset)"
        return 1
    fi
    smoke_curl GET "/api/clips/info?url=$(urlencode "$TARGET_VIDEO_URL")" >/dev/null
    capture yt-info
    smoke_assert_http_2xx "GET /api/clips/info" || return 1
    if ! jq -e '((.id // "") != "")
                and ((.title // "") != "")
                and ((.duration // 0) > 0)' "$SMOKE_LAST_BODY" >/dev/null; then
        pipeline_fail "metadata is missing video identity / title / duration"
        return 1
    fi
    TARGET_VIDEO_ID=$(jq -r '.id' "$SMOKE_LAST_BODY")
    printf '  resolved : %s (%ss)\n' "$(jq -r '.title' "$SMOKE_LAST_BODY")" "$(jq -r '.duration' "$SMOKE_LAST_BODY")"
}

# ── Step 3 — YouTube processing ────────────────────────────────────────
step_3_youtube_process() {
    if [[ -z "$TARGET_VIDEO_URL" ]]; then
        pipeline_fail "no video URL available; cannot submit clips/process"
        return 1
    fi
    local payload
    payload=$(results_file payload-yt)
    jq -n \
        --arg url "$TARGET_VIDEO_URL" \
        --arg tag "$DRIVE_FOLDER_GROUP" \
        --arg folder "$DRIVE_ROOT_FOLDER_ID" \
        '{
          url: $url,
          segments: [{start: "00:00:05", end: "00:00:12", name: $tag, category: "e2e"}],
          strategy: "verify",
          destination: {folder_id: $folder, group: "e2e", create_subfolder: true}
        }' > "$payload"
    chmod 600 "$payload"

    export SMOKE_IDEMPOTENCY_KEY="$RUN_TAG-youtube-process"
    smoke_curl POST "/api/clips/process" -d @"$payload" >/dev/null
    unset SMOKE_IDEMPOTENCY_KEY
    capture submit-yt
    smoke_assert_http_2xx "POST /api/clips/process" || return 1
    if ! jq -e '.ok == true' "$SMOKE_LAST_BODY" >/dev/null; then
        pipeline_fail "clips/process did not acknowledge the job"
        return 1
    fi

    if [[ -n "$PIPELINE_E2E_JOB_ID" ]]; then
        YT_JOB_ID="$PIPELINE_E2E_JOB_ID"
    else
        local deadline=$(( $(date +%s) + 120 ))
        while (( $(date +%s) < deadline )); do
            YT_JOB_ID=$(pipeline_youtube_jobs "$DRIVE_FOLDER_GROUP" | jq -r '(.[0].id // empty)')
            [[ -n "$YT_JOB_ID" ]] && break
            sleep "$SMOKE_POLL_INTERVAL_SECONDS"
        done
    fi
    if [[ -z "$YT_JOB_ID" ]]; then
        pipeline_fail "the enqueued youtube_clip.extract job was not found via /api/jobs"
        return 1
    fi
    printf '  job id   : %s\n' "$YT_JOB_ID"
    pipeline_assert_job_succeeded "$YT_JOB_ID" yt || return 1
}

# ── Step 4 — YouTube Drive hierarchy ───────────────────────────────────
step_4_youtube_drive_artifact() {
    local full
    full=$(results_file full-yt)
    if [[ ! -s "$full" ]]; then
        pipeline_fail "no retained job result at $full"
        return 1
    fi
    local artifacts
    artifacts=$(jq -c --arg vid "$TARGET_VIDEO_ID" '
        [ .. | objects | select(has("drive_file_id")) | select((.drive_file_id // "") != "") ]
        | {count: length,
           file_ids: ([.[].drive_file_id] | unique),
           links: [.[].drive_link // ""],
           folder_ids: ([.[].drive_folder_id // ""] | map(select(. != "")) | unique),
           folder_paths: ([.[].drive_folder_path // ""] | map(select(. != "")) | unique),
           per_video: ([.[].drive_folder_path // ""] | map(select($vid != "" and contains($vid))) | length)}' \
        "$full")
    printf '  artifacts: %s\n' "$artifacts"
    if ! jq -e '.count >= 1' <<<"$artifacts" >/dev/null; then
        pipeline_fail "no clip in the job result carries a drive_file_id"
        return 1
    fi
    if ! jq -e '(.links | map(select(. != "")) | length) >= 1' <<<"$artifacts" >/dev/null; then
        pipeline_fail "no clip in the job result carries a drive_link"
        return 1
    fi
    if ! jq -e '(.folder_ids | length) >= 1 or (.folder_paths | length) >= 1' <<<"$artifacts" >/dev/null; then
        pipeline_fail "no Drive folder identity in the job result"
        return 1
    fi
    if ! jq -e '(.file_ids | length) == .count' <<<"$artifacts" >/dev/null; then
        pipeline_fail "duplicate drive_file_id across clips"
        return 1
    fi
    if ! jq -e '(.folder_ids | length) <= 1' <<<"$artifacts" >/dev/null; then
        pipeline_fail "more than one Drive folder was created for the same run"
        return 1
    fi
    if ! jq -e '.per_video >= 1' <<<"$artifacts" >/dev/null; then
        pipeline_fail "the Drive folder path does not carry the per-video subfolder ($TARGET_VIDEO_ID)"
        return 1
    fi
}

# ── Step 5 — YouTube clip indexed ──────────────────────────────────────
step_5_youtube_indexed() {
    smoke_curl POST "/api/media/search" -d "$(jq -n \
        --arg q "$DRIVE_FOLDER_GROUP" \
        '{query: $q, sources: ["youtube"], mode: "hybrid", universe: "catalog",
          filters: {media_type: "video"}, limit: 20}')" >/dev/null
    capture search-yt
    smoke_assert_http_2xx "POST /api/media/search (youtube)" || return 1

    YT_SEARCH_COUNT_BEFORE=$(jq -r '[.items[]? | select(.source == "youtube")] | length' "$SMOKE_LAST_BODY")
    if [[ "$YT_SEARCH_COUNT_BEFORE" -lt 1 ]]; then
        pipeline_fail "the processed clip is not retrievable from the canonical catalog"
        return 1
    fi
    YT_ASSET_ID=$(jq -r '[.items[]? | select(.source == "youtube")][0].asset_id // ""' "$SMOKE_LAST_BODY")
    if [[ -z "$YT_ASSET_ID" ]]; then
        pipeline_fail "the canonical asset_id is empty"
        return 1
    fi
    printf '  asset id : %s (%s hit(s))\n' "$YT_ASSET_ID" "$YT_SEARCH_COUNT_BEFORE"
}

# ── Step 6 — Stock direct-URL run ──────────────────────────────────────
step_6_stock_run() {
    smoke_curl POST "/api/stock-pipeline/run" -d "$(jq -n \
        --arg url "$STOCK_DIRECT_URL" \
        --arg folder "$DRIVE_ROOT_FOLDER_ID" \
        --arg name "$STOCK_FOLDER_NAME" \
        '{
          direct_urls: [$url],
          target_total_duration_seconds: 5,
          target_duration_per_source_seconds: 5,
          clips_per_source: 1,
          clip_duration_seconds: 5,
          download_mode: "sections_only",
          drive_folder_id: $folder,
          folder_name: $name,
          subfolder: "direct",
          async: true,
          persist: true
        }')" >/dev/null
    capture submit-stock-run
    if [[ "$SMOKE_LAST_HTTP" != "202" ]]; then
        pipeline_fail "POST /api/stock-pipeline/run returned HTTP $SMOKE_LAST_HTTP (expected 202)"
        return 1
    fi
    if ! jq -e '(.status == "QUEUED") and ((.job_id // "") != "") and (.deduplicated == false)' \
        "$SMOKE_LAST_BODY" >/dev/null; then
        pipeline_fail "stock run did not return QUEUED + job_id + deduplicated=false"
        return 1
    fi
    STOCK_JOB_ID=$(jq -r '.job_id' "$SMOKE_LAST_BODY")
    printf '  job id   : %s\n' "$STOCK_JOB_ID"
    pipeline_assert_job_succeeded "$STOCK_JOB_ID" stock-run || return 1
}

# ── Step 7 — Stock search-and-run ──────────────────────────────────────
step_7_stock_search_and_run() {
    smoke_curl POST "/api/stock-pipeline/search-and-run" -d "$(jq -n \
        --arg q "$STOCK_QUERY" \
        --arg folder "$DRIVE_ROOT_FOLDER_ID" \
        --arg name "$STOCK_FOLDER_NAME-search" \
        '{
          queries: [{q: $q, limit: 2}],
          target_total_duration_seconds: 10,
          target_duration_per_source_seconds: 5,
          clips_per_source: 1,
          clip_duration_seconds: 5,
          download_mode: "sections_only",
          drive_folder_id: $folder,
          folder_name: $name,
          async: true,
          persist: true
        }')" >/dev/null
    capture submit-stock-search
    if [[ "$SMOKE_LAST_HTTP" != "202" ]]; then
        pipeline_fail "POST /api/stock-pipeline/search-and-run returned HTTP $SMOKE_LAST_HTTP (expected 202)"
        return 1
    fi
    if ! jq -e '(.status == "QUEUED") and ((.job_id // "") != "")' "$SMOKE_LAST_BODY" >/dev/null; then
        pipeline_fail "search-and-run did not return QUEUED + job_id"
        return 1
    fi
    STOCK_JOB_ID=$(jq -r '.job_id' "$SMOKE_LAST_BODY")
    printf '  job id   : %s\n' "$STOCK_JOB_ID"
    pipeline_assert_job_succeeded "$STOCK_JOB_ID" stock-search || return 1
}

# ── Step 8 — Stock Drive artifact ──────────────────────────────────────
step_8_stock_drive_artifact() {
    local full
    full=$(results_file full-stock-search)
    if [[ ! -s "$full" ]]; then
        full=$(results_file full-stock-run)
    fi
    if [[ ! -s "$full" ]]; then
        pipeline_fail "no retained stock job result to inspect"
        return 1
    fi
    local artifacts
    artifacts=$(jq -c '
        [ .. | objects | select(has("drive_file_id")) | select((.drive_file_id // "") != "") ]
        | {count: length,
           file_ids: ([.[].drive_file_id] | unique),
           links: [.[].drive_link // ""],
           sizes: [ (.[].size_bytes // .[].size // 0) ],
           folder_ids: ([.[].drive_folder_id // ""] | map(select(. != "")) | unique)}' \
        "$full")
    printf '  artifacts: %s\n' "$artifacts"
    if ! jq -e '.count >= 1' <<<"$artifacts" >/dev/null; then
        pipeline_fail "no stock chunk carries a drive_file_id"
        return 1
    fi
    if ! jq -e '(.links | map(select(. != "")) | length) >= 1' <<<"$artifacts" >/dev/null; then
        pipeline_fail "no stock chunk carries a drive_link"
        return 1
    fi
    if ! jq -e '(.folder_ids | length) >= 1' <<<"$artifacts" >/dev/null; then
        pipeline_fail "no Drive folder identity in the stock job result"
        return 1
    fi
    if ! jq -e '([.sizes[] | select(. > 0)] | length) >= 1' <<<"$artifacts" >/dev/null; then
        pipeline_fail "no stock artifact reports a non-zero size"
        return 1
    fi
}

# ── Step 9 — Stock indexed + downloadable ──────────────────────────────
step_9_stock_indexed_download() {
    smoke_curl POST "/api/media/search" -d "$(jq -n \
        --arg q "$RUN_TAG" \
        '{query: $q, sources: ["stock"], mode: "hybrid", universe: "catalog",
          filters: {source: "stock", media_type: "video"}, limit: 20}')" >/dev/null
    capture search-stock
    smoke_assert_http_2xx "POST /api/media/search (stock)" || return 1

    STOCK_ASSET_ID=$(jq -r '[.items[]? | select(.source == "stock")][0].asset_id // ""' "$SMOKE_LAST_BODY")
    if [[ -z "$STOCK_ASSET_ID" ]]; then
        pipeline_fail "no stock asset is retrievable from the canonical catalog"
        return 1
    fi
    printf '  asset id : %s\n' "$STOCK_ASSET_ID"

    local out="$RESULTS_DIR/stock-download-$RUN_TAG.mp4"
    local saved_timeout="$SMOKE_HTTP_TIMEOUT_SECONDS"
    SMOKE_HTTP_TIMEOUT_SECONDS=120
    export SMOKE_IDEMPOTENCY_KEY="$RUN_TAG-stock-download"
    smoke_curl POST "/api/media/clips/stock/clips/${STOCK_ASSET_ID}/download" >/dev/null
    unset SMOKE_IDEMPOTENCY_KEY
    SMOKE_HTTP_TIMEOUT_SECONDS="$saved_timeout"
    if [[ "$SMOKE_LAST_HTTP" != "200" ]]; then
        pipeline_fail "clip download returned HTTP $SMOKE_LAST_HTTP"
        return 1
    fi
    cp "$SMOKE_LAST_BODY" "$out"
    chmod 600 "$out" 2>/dev/null || true
    local size
    size=$(stat -c%s "$out" 2>/dev/null || stat -f%z "$out" 2>/dev/null || echo 0)
    if (( size < 100000 )); then
        pipeline_fail "downloaded clip is ${size}B (< 100000B)"
        return 1
    fi
    if ! smoke_ffprobe_check "$out" 1; then
        pipeline_fail "downloaded clip has no decodable video stream"
        return 1
    fi
    printf '  download : %s (%s bytes, decodable video stream)\n' "$out" "$size"
}

# ── Step 10 — Idempotent replay ────────────────────────────────────────
step_10_idempotent_replay() {
    local payload
    payload=$(results_file payload-yt)
    if [[ ! -s "$payload" ]]; then
        pipeline_fail "step 3 payload artifact is missing; cannot replay"
        return 1
    fi
    export SMOKE_IDEMPOTENCY_KEY="$RUN_TAG-youtube-process"
    smoke_curl POST "/api/clips/process" -d @"$payload" >/dev/null
    unset SMOKE_IDEMPOTENCY_KEY
    capture submit-yt-replay
    smoke_assert_http_2xx "POST /api/clips/process (replay)" || return 1

    local replay_header=""
    if [[ -f "$WORK_DIR/last.headers" ]]; then
        replay_header=$(grep -i '^X-Idempotency-Replay:' "$WORK_DIR/last.headers" 2>/dev/null |
            tr -d '\r' | awk '{print tolower($2)}' || true)
    fi
    if [[ "$replay_header" == "true" ]]; then
        printf '  replay   : served from the idempotency cache (X-Idempotency-Replay: true)\n'
    else
        printf '%sWARN: X-Idempotency-Replay absent; verifying the effect instead (no duplicate identity)%s\n' \
            "$YELLOW" "$RESET"
    fi

    # The canonical identity count must not grow after the replay.
    smoke_curl POST "/api/media/search" -d "$(jq -n \
        --arg q "$DRIVE_FOLDER_GROUP" \
        '{query: $q, sources: ["youtube"], mode: "hybrid", universe: "catalog", limit: 20}')" >/dev/null
    capture search-yt-after-replay
    smoke_assert_http_2xx "POST /api/media/search (after replay)" || return 1
    local after
    after=$(jq -r '[.items[]? | select(.source == "youtube")] | length' "$SMOKE_LAST_BODY")
    if (( after > YT_SEARCH_COUNT_BEFORE )); then
        pipeline_fail "the replay created an additional canonical identity ($YT_SEARCH_COUNT_BEFORE -> $after)"
        return 1
    fi

    # And exactly one extract job must exist for this run tag.
    local jobs_found
    jobs_found=$(pipeline_youtube_jobs "$DRIVE_FOLDER_GROUP" | jq -r 'length')
    if (( jobs_found > 1 )); then
        pipeline_fail "the replay enqueued a second job for the same run tag ($jobs_found)"
        return 1
    fi
    printf '  identities: %s before, %s after, %s job(s) for the tag\n' \
        "$YT_SEARCH_COUNT_BEFORE" "$after" "$jobs_found"
}

# ── Battery ────────────────────────────────────────────────────────────
printf '\n%spipeline live E2E — run %s%s\n' "$CYAN" "$RUN_TAG" "$RESET"
printf 'api base: http://%s\n' "$SMOKE_API_BASE"

pipeline_run_step "1  YouTube keyword discovery"     step_1_youtube_keyword_search
pipeline_run_step "2  YouTube metadata"              step_2_youtube_info
pipeline_run_step "3  YouTube clip processing"       step_3_youtube_process
pipeline_run_step "4  YouTube Drive hierarchy"       step_4_youtube_drive_artifact
pipeline_run_step "5  YouTube clip indexed"          step_5_youtube_indexed
pipeline_run_step "6  Stock direct-URL run"          step_6_stock_run
pipeline_run_step "7  Stock search-and-run"          step_7_stock_search_and_run
pipeline_run_step "8  Stock Drive artifact"          step_8_stock_drive_artifact
pipeline_run_step "9  Stock indexed + downloadable"  step_9_stock_indexed_download
pipeline_run_step "10 Idempotent replay"             step_10_idempotent_replay

printf '\n%s===== pipeline live E2E summary =====%s\n' "$CYAN" "$RESET"
printf 'run tag : %s\n' "$RUN_TAG"
printf 'results : %s\n' "$RESULTS_DIR"
if (( ${#FAILED_STEPS[@]} > 0 )); then
    printf '%sfailed  : %s%s\n' "$RED" "${FAILED_STEPS[*]}" "$RESET"
fi
if (( PASSED == TOTAL )); then
    printf '%s%s/%s PASS%s\n' "$GREEN" "$PASSED" "$TOTAL" "$RESET"
    exit 0
fi
printf '%s%s/%s PASS — the gate requires 10/10%s\n' "$RED" "$PASSED" "$TOTAL" "$RESET"
exit 1

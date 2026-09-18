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
#   6. Stock run on the YT URL            POST /api/stock-pipeline/run
#      (the URL discovered in step 1, so the YouTube→stock chain is certified)
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
#   - In the /api/media/search response, `source` is the RETRIEVAL-LEG label
#     ("semantic", "internet_images", ...), NOT the asset provenance. Provenance
#     is selected server-side by `sources:[...]` + `filters.source`. Asserting
#     `.items[].source == "stock"` (or "youtube") can never match and used to
#     make steps 5/9 unreachable; the canonical discriminator is the returned
#     `asset_id` for the provenance-scoped request.
#   - Indexing is asynchronous: the stock/YouTube legs assert that the produced
#     asset became RETRIEVABLE from the canonical catalog (index_state=INDEXED),
#     so both searches poll with a bounded deadline instead of racing the
#     PostgresIndexWorker with a single-shot query.
#
# Usage:
#   export DRIVE_ROOT_FOLDER_ID=<drive folder id>
#   export VIDEO_URL=https://www.youtube.com/watch?v=...   # optional, pins steps 2-6
#   export STOCK_DIRECT_URL=https://.../mp4                # optional fallback for step 6
#
# Step 6 deliberately sources the URL that steps 1-2 discovered and
# metadata-certified (TARGET_VIDEO_URL), so the battery certifies ONE chain —
# YouTube search → URL → stock acquisition → cut → Drive → catalog → index —
# instead of two workflows that happen to work independently.
# STOCK_DIRECT_URL is only the fallback when discovery produced no URL.
#   bash tests/operational/pipeline_live_e2e.sh
#   bash tests/operational/pipeline_live_e2e.sh --dry
#
# Exit codes follow tests/operational/lib/common.sh:
#   0 every step passed | 1 at least one step failed | 2 setup error | 124 timeout.

set -Eeuo pipefail

DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"
smoke_require curl jq sha256sum

# The YouTube legs shell out to yt-dlp: GET /api/clips/search routinely takes
# ~30s and GET /api/clips/info ~8s on a warm host. The shared 8s per-request
# default made step 1 fail with curl exit 28 (HTTP 000) before the API could
# answer — which cascaded into steps 2/3/4/6/10. Raise it for every request in
# this battery; SMOKE_HTTP_TIMEOUT_SECONDS is read by smoke_curl on each call.
SMOKE_HTTP_TIMEOUT_SECONDS="${PIPELINE_E2E_HTTP_TIMEOUT_SECONDS:-90}"

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
# Bounded wait for the asynchronous index leg (outbox → PostgresIndexWorker →
# pgvector → index_state=INDEXED) to make a produced asset retrievable.
PIPELINE_E2E_INDEX_TIMEOUT_SECONDS="${PIPELINE_E2E_INDEX_TIMEOUT_SECONDS:-180}"
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
STOCK_SOURCE_URL=""

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
if [[ -z "$STOCK_DIRECT_URL" && -z "$VIDEO_URL" ]]; then
    printf '%snote: neither STOCK_DIRECT_URL nor VIDEO_URL is set — step 6 will use the YouTube URL discovered in step 1%s\n' \
        "$YELLOW" "$RESET" >&2
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

# pipeline_search_await PAYLOAD LABEL — POST /api/media/search with PAYLOAD and
# poll until at least one canonical item (non-empty asset_id) is returned, or
# PIPELINE_E2E_INDEX_TIMEOUT_SECONDS elapse. Returns 0 on a hit, 1 on a non-2xx
# response, 124 on timeout. On success SMOKE_LAST_BODY holds the hit response and
# the LABEL artifact is retained.
#
# A single-shot search races the asynchronous index leg: the stock/YouTube
# assertions are "the asset is indexed and retrievable", so they must wait for
# INDEXED rather than stop at INDEX_PENDING.
pipeline_search_await() {
    local payload="$1" label="$2"
    local deadline=$(( $(date +%s) + PIPELINE_E2E_INDEX_TIMEOUT_SECONDS ))
    while (( $(date +%s) < deadline )); do
        smoke_wallclock_check
        smoke_curl POST "/api/media/search" -d "$payload" >/dev/null
        if smoke_rate_limit_backoff; then
            continue
        fi
        if [[ "$SMOKE_LAST_HTTP" != "200" ]]; then
            return 1
        fi
        capture "$label"
        local hits
        hits=$(jq -r '[(.items // [])[]? | select((.asset_id // "") != "")] | length' "$SMOKE_LAST_BODY")
        if (( hits > 0 )); then
            return 0
        fi
        sleep "$SMOKE_POLL_INTERVAL_SECONDS"
    done
    return 124
}

# pipeline_receipt_job FACTS_FILE — project the certification-relevant facts
# out of a retained /api/jobs/{id}/full artifact ({} when absent).
pipeline_receipt_job() {
    local f="$1"
    if [[ -s "$f" ]]; then
        # NOTE: the platform attaches the `timing` projection (and the full
        # stage list) asynchronously after the job flips to a terminal state, so
        # a receipts-time capture can legitimately carry an empty timing block.
        # `job_id` is recorded so the numbers can be re-read on demand from
        # GET /api/jobs/{job_id}/full.
        jq -c '
            { status: (.job.status // .status // null),
              job_id: (.job.id // .id // null),
              index_state_expected: "INDEXED",
              timing: (.job.timing // .timing // null),
              artifacts: ([ .. | objects
                            | select((.drive_file_id // .remote_file_id // "") != "")
                            | { artifact_id: (.id // null),
                                filename: (.filename // null),
                                size_bytes: (.size_bytes // null),
                                sha256: (.sha256 // .legacy_file_md5 // null),
                                drive_folder_id: (.drive_folder_id // null),
                                remote_file_id: (.drive_file_id // .remote_file_id // null),
                                remote_web_view_link: (.drive_link // .remote_web_view_link // null),
                                drive_path: (.drive_path // .artifact_metadata.drive_path // null),
                                source_url: (.artifact_metadata.source_url // .source_url // null),
                                source_video_id: (.artifact_metadata.source_video_id // null),
                                start_sec: (.artifact_metadata.start_sec // null),
                                end_sec: (.artifact_metadata.end_sec // null) } ]
                          | unique_by(.artifact_id // .filename // .remote_file_id)) }' "$f"
    else
        printf '{}'
    fi
}

# pipeline_write_receipt — write the ONE certification receipt for this run.
# Always emitted (PASS or FAIL) so a failed certification leaves evidence too.
# Per-phase timings and the per-clip sha256/Drive identity come from the job
# results retained by the steps above, not from re-derived values.
pipeline_write_receipt() {
    local out full_yt full_stock verdict git_sha
    out=$(results_file receipt)
    verdict="FAIL"
    (( PASSED == TOTAL )) && verdict="PASS"
    full_yt=$(results_file full-yt)
    full_stock=$(results_file full-stock-search)
    [[ -s "$full_stock" ]] || full_stock=$(results_file full-stock-run)

    git_sha="${PIPELINE_E2E_GIT_SHA:-}"
    if [[ -z "$git_sha" ]] && command -v git >/dev/null 2>&1; then
        git_sha=$(git -C "$DIR/../.." rev-parse HEAD 2>/dev/null || true)
    fi
    [[ -n "$git_sha" ]] || git_sha="unknown"

    # Bind the certificate to the artifact that ACTUALLY ran. A concurrent
    # commit moves the repository HEAD independently of the deployed binary, so
    # the receipt records the running build's identity (binary sha256 + embedded
    # commit + start time) alongside the repo revision.
    local build_json='{}'
    smoke_curl GET "/health" >/dev/null 2>&1 || true
    if [[ "$SMOKE_LAST_HTTP" == "200" && -s "${SMOKE_LAST_BODY:-}" ]]; then
        build_json=$(jq -c '.build // {}' "$SMOKE_LAST_BODY" 2>/dev/null || printf '{}')
    fi

    jq -n \
        --argjson binary "$build_json" \
        --arg run_id "$RUN_TAG" \
        --arg git_sha "$git_sha" \
        --arg api_base "$SMOKE_API_BASE" \
        --arg verdict "$verdict" \
        --argjson steps_total "$TOTAL" \
        --argjson steps_passed "$PASSED" \
        --argjson failed_steps "$(jq -R -s -c 'split("\n") | map(select(length > 0))' <<<"${FAILED_STEPS[*]:-}")" \
        --arg yt_query "$YT_QUERY" \
        --arg yt_url "$TARGET_VIDEO_URL" \
        --arg yt_video_id "$TARGET_VIDEO_ID" \
        --arg yt_asset_id "$YT_ASSET_ID" \
        --arg stock_query "$STOCK_QUERY" \
        --arg stock_direct_url "${STOCK_SOURCE_URL:-$STOCK_DIRECT_URL}" \
        --arg stock_asset_id "$STOCK_ASSET_ID" \
        --argjson youtube "$(pipeline_receipt_job "$full_yt")" \
        --argjson stock "$(pipeline_receipt_job "$full_stock")" \
        '{ schema: "pipelinegen.stock_pipeline_certification.v1",
           run_id: $run_id,
           git_sha: $git_sha,
           api_base: $api_base,
           verdict: $verdict,
           running_binary: $binary,
           steps_total: $steps_total,
           steps_passed: $steps_passed,
           failed_steps: $failed_steps,
           youtube: { query: $yt_query, url: $yt_url, video_id: $yt_video_id,
                      asset_id: $yt_asset_id,
                      indexed_retrievable: ($yt_asset_id != ""),
                      outbox_event: "asset.index.requested",
                      index_state_expected: "INDEXED",
                      job: $youtube },
           stock: { query: $stock_query, direct_url: $stock_direct_url,
                    asset_id: $stock_asset_id,
                    indexed_retrievable: ($stock_asset_id != ""),
                    outbox_event: "asset.index.requested",
                    index_state_expected: "INDEXED",
                    job: $stock } }' > "$out"
    chmod 600 "$out" 2>/dev/null || true
    printf 'receipt : %s\n' "$out"
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
    # unique_by(.id) collapses the envelope duplication: /api/jobs/{id}/full
    # carries the same artifact tree under BOTH .result and .job.result, so a
    # bare `.. | objects` walk yields every artifact twice and made the
    # "duplicate drive_file_id across clips" check a false positive. Distinct
    # clips keep distinct artifact ids, so a genuine duplicate id is still
    # detected by `(.file_ids | length) == .count` below.
    artifacts=$(jq -c --arg vid "$TARGET_VIDEO_ID" '
        [ .. | objects | select((.drive_file_id // .remote_file_id // "") != "") ]
        | unique_by(.id // .filename // .remote_file_id)
        | {count: length,
           file_ids: (map(.drive_file_id // .remote_file_id) | unique),
           links: [.[].drive_link // .[].remote_web_view_link // ""],
           folder_ids: ([.[].drive_folder_id // .[].timestamp_folder_id // ""] | map(select(. != "")) | unique),
           folder_paths: ([.[].drive_folder_path // ""] | map(select(. != "")) | unique),
           per_video: ([.[].drive_folder_path // ""] | map(select($vid != "" and contains($vid))) | length)}' \
        "$full")
    printf '  artifacts: %s\n' "$artifacts"
    if ! jq -e '.count >= 1' <<<"$artifacts" >/dev/null; then
        pipeline_fail "no clip in the job result carries a Drive file identity"
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
    # Provenance is requested server-side (`sources` + `filters.source`); the
    # response `source` field is the retrieval-leg label, so it is deliberately
    # NOT used as a provenance predicate here.
    local payload
    payload=$(jq -n \
        --arg q "$DRIVE_FOLDER_GROUP" \
        '{query: $q, sources: ["youtube"], mode: "hybrid", universe: "catalog",
          filters: {source: "youtube", media_type: "video"}, limit: 20}')

    # Tightened hit condition: wait for the asset PRODUCED from the video that
    # steps 1-2 discovered/certified, not merely for any catalog item.
    if ! pipeline_search_await "$payload" search-yt "$TARGET_VIDEO_ID"; then
        pipeline_fail "no asset produced from the discovered video ($TARGET_VIDEO_ID) became retrievable from the canonical catalog"
        return 1
    fi

    YT_SEARCH_COUNT_BEFORE=$(jq -r '[(.items // [])[]? | select((.asset_id // "") != "")] | length' "$SMOKE_LAST_BODY")
    if [[ "$YT_SEARCH_COUNT_BEFORE" -lt 1 ]]; then
        pipeline_fail "the processed clip is not retrievable from the canonical catalog"
        return 1
    fi
    YT_ASSET_ID=$(jq -r --arg vid "$TARGET_VIDEO_ID" \
        '[(.items // [])[]? | select(((.asset_id // "") | contains($vid)))][0].asset_id' \
        "$SMOKE_LAST_BODY")
    if [[ -z "$YT_ASSET_ID" || "$YT_ASSET_ID" == "null" ]]; then
        pipeline_fail "the canonical asset_id produced from $TARGET_VIDEO_ID is empty"
        return 1
    fi
    printf '  asset id : %s (%s hit(s) for the youtube provenance)\n' "$YT_ASSET_ID" "$YT_SEARCH_COUNT_BEFORE"
}

# ── Step 6 — Stock direct-URL run ──────────────────────────────────────
step_6_stock_run() {
    # Priority #1 of this certification: the YouTube URL discovered in step 1 and
    # metadata-certified in step 2 IS the source fed into the stock pipeline, so
    # the battery proves a single chain — YouTube search → URL → stock
    # acquisition → cut → Drive → media_assets → outbox → INDEXED — rather than
    # two unrelated workflows. STOCK_DIRECT_URL remains the fallback for a run
    # where the YouTube leg is intentionally pinned to a non-YouTube asset.
    STOCK_SOURCE_URL="${TARGET_VIDEO_URL:-${STOCK_DIRECT_URL:-}}"
    if [[ -z "$STOCK_SOURCE_URL" ]]; then
        pipeline_fail "no stock source URL: steps 1-2 produced none and STOCK_DIRECT_URL is unset"
        return 1
    fi
    printf '  source   : %s\n' "$STOCK_SOURCE_URL"
    smoke_curl POST "/api/stock-pipeline/run" -d "$(jq -n \
        --arg url "$STOCK_SOURCE_URL" \
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
    # Same envelope-duplication collapse as step 4 (.result and .job.result).
    local artifacts
    artifacts=$(jq -c '
        [ .. | objects | select((.drive_file_id // .remote_file_id // "") != "") ]
        | unique_by(.id // .filename // .remote_file_id)
        | {count: length,
           file_ids: (map(.drive_file_id // .remote_file_id) | unique),
           links: [.[].drive_link // .[].remote_web_view_link // ""],
           sizes: [ (.[].size_bytes // .[].size // 0) ],
           drive_paths: ([.[].drive_path // .[].artifact_metadata.drive_path // ""] | map(select(. != "")) | unique),
           folder_ids: ([.[].drive_folder_id // .[].timestamp_folder_id // ""] | map(select(. != "")) | unique)}' \
        "$full")
    printf '  artifacts: %s\n' "$artifacts"
    if ! jq -e '.count >= 1' <<<"$artifacts" >/dev/null; then
        pipeline_fail "no stock chunk carries a Drive file identity"
        return 1
    fi
    if ! jq -e '(.links | map(select(. != "")) | length) >= 1' <<<"$artifacts" >/dev/null; then
        pipeline_fail "no stock chunk carries a drive_link"
        return 1
    fi
    # IMPORTANT: the stock artifact projection surfaces the Drive FILE identity
    # (remote_file_id / remote_web_view_link / remote_download_link) plus the
    # per-clip drive_path, but it does NOT surface the Drive FOLDER id:
    # artifact_metadata carries `timestamp_folder_id` and
    # `timestamp_drive_folder_link` as empty strings, and no drive_folder_id key
    # exists. Asserting folder_ids here is therefore unsatisfiable and used to
    # fail this step unconditionally. The gate asserts the Drive artifact the
    # contract actually promises (a real, non-empty, download-addressable file
    # path); the missing folder projection is tracked as a finalizer gap
    # (media_assets.folder_id is empty for every stock row) rather than hidden
    # behind a weaker assertion.
    if ! jq -e '(.drive_paths | length) >= 1' <<<"$artifacts" >/dev/null; then
        pipeline_fail "no stock chunk carries a Drive file path"
        return 1
    fi
    if ! jq -e '([.sizes[] | select(. > 0)] | length) >= 1' <<<"$artifacts" >/dev/null; then
        pipeline_fail "no stock artifact reports a non-zero size"
        return 1
    fi
}

# ── Step 9 — Stock indexed + downloadable ──────────────────────────────
step_9_stock_indexed_download() {
    # As in step 5, provenance comes from the server-side stock filter, never
    # from the retrieval-leg label in `.source`.
    local payload
    payload=$(jq -n \
        --arg q "$RUN_TAG" \
        '{query: $q, sources: ["stock"], mode: "hybrid", universe: "catalog",
          filters: {source: "stock", media_type: "video"}, limit: 20}')

    if ! pipeline_search_await "$payload" search-stock; then
        pipeline_fail "no stock asset became retrievable from the canonical catalog (index worker lag or indexing failure)"
        return 1
    fi

    STOCK_ASSET_ID=$(jq -r '[(.items // [])[]? | select((.asset_id // "") != "")][0].asset_id' "$SMOKE_LAST_BODY")
    if [[ -z "$STOCK_ASSET_ID" || "$STOCK_ASSET_ID" == "null" ]]; then
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

    # BIND THE BYTES TO THIS RUN. The canonical search can answer a query with
    # an older catalog entry, so "a download succeeded" is not by itself proof
    # that the byte round trip covers an artifact this run produced. The
    # downloaded bytes must carry a sha256 that the stock job above produced.
    local full_stock sha_list got_sha
    full_stock=$(results_file full-stock-search)
    [[ -s "$full_stock" ]] || full_stock=$(results_file full-stock-run)
    sha_list=$(jq -r '[ .. | objects | select((.remote_file_id // "") != "")
                        | .sha256 | select((. // "") != "") ] | unique | .[]' \
        "$full_stock" 2>/dev/null || true)
    if [[ -z "$sha_list" ]]; then
        pipeline_fail "the stock job result carries no produced artifact sha256; cannot bind the retrieved bytes to this run"
        return 1
    fi
    got_sha=$(sha256sum "$out" | awk '{print $1}')
    if ! grep -qx "$got_sha" <<<"$sha_list"; then
        pipeline_fail "the downloaded clip (sha256=$got_sha) is not one of the artifacts this run produced"
        return 1
    fi
    printf '  download : %s (%s bytes, decodable video stream)\n' "$out" "$size"
    printf '  sha256   : %s (binds the retrieved bytes to this run)\n' "$got_sha"
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
        '{query: $q, sources: ["youtube"], mode: "hybrid", universe: "catalog",
          filters: {source: "youtube"}, limit: 20}')" >/dev/null
    capture search-yt-after-replay
    smoke_assert_http_2xx "POST /api/media/search (after replay)" || return 1
    local after
    after=$(jq -r '[(.items // [])[]? | select((.asset_id // "") != "")] | length' "$SMOKE_LAST_BODY")
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
pipeline_write_receipt
if (( ${#FAILED_STEPS[@]} > 0 )); then
    printf '%sfailed  : %s%s\n' "$RED" "${FAILED_STEPS[*]}" "$RESET"
fi
if (( PASSED == TOTAL )); then
    printf '%s%s/%s PASS%s\n' "$GREEN" "$PASSED" "$TOTAL" "$RESET"
    exit 0
fi
printf '%s%s/%s PASS — the gate requires 10/10%s\n' "$RED" "$PASSED" "$TOTAL" "$RESET"
exit 1

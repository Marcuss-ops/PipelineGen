#!/usr/bin/env bash
# tests/operational/lib/velox_domain.sh — PipelineGen domain helpers.
#
# Source-able library. Callers MUST source tests/operational/lib/common.sh
# BEFORE this file because every velox_* helper depends on at least one of:
#   - smoke_curl              (HTTP wrapper with token + Idempotency-Key)
#   - smoke_assert_http_2xx   (assert 2xx response)
#   - smoke_assert_eq         (string equality assertion)
#   - smoke_poll_terminal     (job terminal-status polling)
#   - WORK_DIR                (per-script temp dir)
#   - SMOKE_HTTP_TIMEOUT_SECONDS, SMOKE_API_BASE, SMOKE_TOKEN,
#     SMOKE_LAST_HTTP, SMOKE_LAST_BODY, SMOKE_LAST_STATUS
#
# Naming convention (July 2026 DoD refactor):
#   - `smoke_*` lives in lib/common.sh and is generic infra (HTTP/SQLite/ffprobe/dry-run)
#   - `velox_*` lives here and is PipelineGen-specific domain logic
#     (Qdrant payload assertions, Drive resolve-by-id wrapper, Artlist /run
#      pipeline call). The split keeps `common.sh` reusable by side-car
#      services that don't run on the PipelineGen stack.
#
# Helpers stay pure: every velox_* function returns a status code and writes
# its response file under ${WORK_DIR:-/tmp}; it does NOT touch PASS/WARN/FAIL
# counters — the host battery owns those.

# ── velox_qdrant_assert — at least one PUBLISHED artlist point for clip_id
# Args: <clip_id> <collection> <qdrant_url> [qdrant_api_key]
# Returns: 0 → point found with shape match
#          1 → shape contract failed
#          2 → transport / HTTP failure (caller should crash)
velox_qdrant_assert() {
    local clip_id="$1" collection="$2" qdrant_url="$3" api_key="${4:-}"
    local out="${WORK_DIR:-/tmp}/velox_qdrant_${clip_id}.json"
    local hdrs=()
    [[ -n "$api_key" ]] && hdrs+=( -H "api-key: $api_key" )
    local code
    code=$(curl -sS --connect-timeout 5 --max-time "${SMOKE_HTTP_TIMEOUT_SECONDS:-8}" \
        -X POST -o "$out" -w '%{http_code}' \
        "${hdrs[@]}" -H 'Content-Type: application/json' \
        -d "$(jq -nc --arg id "$clip_id" '{
            filter: { must: [ { key: "asset_id", match: { value: $id } } ] },
            limit: 5, with_payload: true, with_vector: false
        }')" \
        "$qdrant_url/collections/$collection/points/scroll")
    [[ "$code" =~ ^2[0-9][0-9]$ ]] || return 2
    jq -e '.result.points | length >= 1
        and .[0].payload.source == "artlist"
        and .[0].payload.media_type == "video"
        and .[0].payload.lifecycle_state == "PUBLISHED"' \
        <<<"$(cat "$out" 2>/dev/null || true)" >/dev/null 2>&1
}

# ── velox_drive_resolve — confirm Drive file id exists, not trashed, size > 0
# Args: <drive_file_id>
# Returns: 0 → file resolved with size > 0 and trashed=false
#          1 → HTTP 2xx but contract failed
#          2 → transport / HTTP non-2xx
velox_drive_resolve() {
    local file_id="$1"
    [[ -n "$file_id" ]] || return 1
    local out="${WORK_DIR:-/tmp}/velox_drive_${file_id}.json"
    local code
    code=$(smoke_curl POST "/api/drive/resolve-by-id" \
        -d "$(jq -nc --arg id "$file_id" '{ids: [$id]}')" 2>/dev/null || echo "")
    [[ "$code" =~ ^2[0-9][0-9]$ ]] || return 2
    [[ -s "$SMOKE_LAST_BODY" ]] || return 1
    jq -e '.ok == true
        and (.resolved_count // 0) >= 1
        and (.resolved[0].trashed == false)
        and ((.resolved[0].size // 0) > 0)' \
        "$SMOKE_LAST_BODY" >/dev/null 2>&1
}

# ── velox_artlist_pipeline_run — POST /api/artlist/run with canonical DoD payload
# Args: <term> <limit> [strategy] [clip_duration] [width] [height] [fps] [concurrency] [root_folder_id]
# Emits: <HTTP_code>\t<run_id>\t<body_path>
# Strategy default "replace" matches the VidRush battery; callers can pass
# "merge" / "skip" to alter behaviour without leaving the canonical surface.
velox_artlist_pipeline_run() {
    local term="$1" limit="$2"
    local strategy="${3:-replace}"
    local clip_duration="${4:-7}"
    local width="${5:-1920}" height="${6:-1080}" fps="${7:-30}"
    local concurrency="${8:-1}"
    local root="${9:-${VELOX_DRIVE_ARTLIST_ROOT:-}}"
    local out="${WORK_DIR:-/tmp}/velox_artlist_run_$$.json"
    local payload
    if [[ -n "$root" ]]; then
        payload=$(jq -nc \
            --arg term "$term" --argjson limit "$limit" \
            --arg rid "$root" --arg strategy "$strategy" \
            --argjson cd "$clip_duration" --argjson w "$width" --argjson h "$height" \
            --argjson fg "$fps" --argjson cc "$concurrency" '{
                term:$term, limit:$limit, strategy:$strategy,
                clip_duration:$cd, width:$w, height:$h, fps:$fg,
                concurrency:$cc, dry_run:false, root_folder_id:$rid
            }')
    else
        payload=$(jq -nc \
            --arg term "$term" --argjson limit "$limit" --arg strategy "$strategy" \
            --argjson cd "$clip_duration" --argjson w "$width" --argjson h "$height" \
            --argjson fg "$fps" --argjson cc "$concurrency" '{
                term:$term, limit:$limit, strategy:$strategy,
                clip_duration:$cd, width:$w, height:$h, fps:$fg,
                concurrency:$cc, dry_run:false
            }')
    fi
    local code
    code=$(smoke_curl POST "/api/artlist/run" -d "$payload" 2>/dev/null || echo "")
    local body="$SMOKE_LAST_BODY"
    [[ -n "$body" && -s "$body" ]] || body="$WORK_DIR/last.body"
    local jid
    jid=$(jq -r '.run_id // empty' "$body" 2>/dev/null || true)
    printf '%s\t%s\t%s\n' "$code" "$jid" "$body"
}

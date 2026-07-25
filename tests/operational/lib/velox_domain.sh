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

# ── velox_qdrant_assert — at least one PUBLISHED point for clip_id
# Args:
#   clip_id           asset_id to look up (positional 1, mandatory)
#   collection        qdrant collection name (positional 2, mandatory)
#   qdrant_url        base URL (positional 3, mandatory)
#   expected_source   payload.source filter (positional 4, mandatory for
#                     cross-source reuse: 'artlist', 'google-flow', ...)
#   expected_media_type
#                     payload.media_type filter (positional 5, mandatory:
#                     'video', 'image', 'audio')
#   expected_lifecycle
#                     payload.lifecycle_state (positional 6,
#                     default 'PUBLISHED')
#   api_key           optional QDRANT_API_KEY header (positional 7)
# Returns: 0 → point found with shape match + asset_id round-trip OK
#          1 → shape contract failed (including asset_id round-trip drift:
#                e.g. must: [{key: asset_id}] filter silently bypassed and
#                a different point came back)
#          2 → transport / HTTP failure
#
# Hardening note (July 2026): the original implementation checked
# payload.source / media_type / lifecycle_state only. If Qdrant's
# must-filter were ever silently bypassed (reindex mid-flight, index
# drift, schema mismatch on the asset_id field), a wrong point with
# `source=artlist media_type=video lifecycle_state=PUBLISHED` would still
# match the SHAPE contract. The point's payload.asset_id is now also
# asserted to round-trip back to the queried clip_id — DoD Gate 8
# "asset_id corretto" — so a stray-point return fails closed with rc=1.
# Lib-level fix migrates the guarantee into every caller (Gate 8 today;
# any future gate that reuses velox_qdrant_assert inherits the round-
# trip automatically per AGENTS.md single-focus rule).
velox_qdrant_assert() {
    local clip_id="$1" collection="$2" qdrant_url="$3"
    local expected_source="$4" expected_media="$5"
    local expected_lifecycle="${6:-PUBLISHED}" api_key="${7:-}"
    [[ -n "$clip_id" && -n "$collection" && -n "$qdrant_url" \
        && -n "$expected_source" && -n "$expected_media" ]] || return 1
    local out="${WORK_DIR:-/tmp}/velox_qdrant_${clip_id}.json"
    local hdrs=()
    [[ -n "$api_key" ]] && hdrs+=( -H "api-key: $api_key" )
    local code
    code=$(curl -sS --connect-timeout 5 --max-time "${SMOKE_HTTP_TIMEOUT_SECONDS:-8}" \
        -X POST -o "$out" -w '%{http_code}' \
        "${hdrs[@]}" -H 'Content-Type: application/json' \
        -d "$(jq -nc --arg id "$clip_id" '{
            filter: { must: [ { key: "asset_id", match: { value: $id } } ] },
            limit: 1, with_payload: true, with_vector: false
        }')" \
        "$qdrant_url/collections/$collection/points/scroll")
    [[ "$code" =~ ^2[0-9][0-9]$ ]] || return 2
    # Round-trip asset_id first so a filter bypass bug surfaces BEFORE
    # the canonical SHAPE checks. Order matters: rc=1 with the round-trip
    # assertion failing produces a clear "asset_id drift" log line in
    # Gate 8's rc=1 SHAPE branch (vs. an opaque source/media/lc mismatch
    # that would mean Qdrant returned a completely different point).
    jq -e --arg id "$clip_id" \
        --arg src "$expected_source" \
        --arg media "$expected_media" \
        --arg lc "$expected_lifecycle" '
        .result.points[0].payload.asset_id == $id
        and .result.points[0].payload.source == $src
        and .result.points[0].payload.media_type == $media
        and .result.points[0].payload.lifecycle_state == $lc' \
        "$out" >/dev/null 2>&1
}

# ── velox_drive_resolve — confirm Drive file id exists, not trashed, size > 0
# Args: <drive_file_id>
# Returns: 0 → file resolved with size > 0 and trashed=false
#          1 → HTTP 2xx but contract failed
#          2 → transport / HTTP non-2xx
# The body is written to a deterministic file under ${WORK_DIR:-/tmp} using
# curl directly (no smoke_curl subshell) so callers inspecting the response
# from outside the function can guarantee $WORK_DIR/velox_drive_${id}.json is
# populated. SMOKE_LAST_BODY side-effects from smoke_curl do NOT survive a
# $(...) subshell wrapping and were the source of Bug 3 in the first wave
# review.
velox_drive_resolve() {
    local file_id="$1"
    [[ -n "$file_id" ]] || return 1
    local out="${WORK_DIR:-/tmp}/velox_drive_${file_id}.json"
    local code
    code=$(curl -sS --max-time "${SMOKE_HTTP_TIMEOUT_SECONDS:-8}" -w '%{http_code}' \
        -X POST -o "$out" \
        -H "Authorization: Bearer ${SMOKE_TOKEN:-}" \
        -H 'Content-Type: application/json' \
        -d "$(jq -nc --arg id "$file_id" '{ids: [$id]}')" \
        "http://${SMOKE_API_BASE}/api/drive/resolve-by-id")
    [[ "$code" =~ ^2[0-9][0-9]$ ]] || return 2
    [[ -s "$out" ]] || return 1
    jq -e '.ok == true
        and (.resolved_count // 0) >= 1
        and (.resolved[0].trashed == false)
        and ((.resolved[0].size // 0) > 0)' \
        "$out" >/dev/null 2>&1
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

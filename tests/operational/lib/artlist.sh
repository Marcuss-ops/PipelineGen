#!/usr/bin/env bash
# tests/operational/lib/artlist.sh — Artlist-battery application-side helpers.
#
# Source-able library. Every arlist/*.sh sub-script does:
#   DIR=$(cd "$(dirname "$0")" && pwd)
#   # shellcheck disable=SC1091
#   source "$DIR/../lib/common.sh"
#   # shellcheck disable=SC1091
#   source "$DIR/../lib/artlist.sh"
#   # shellcheck disable=SC1091
#   source "$DIR/../lib/artlist_runtime.sh"
#
# Contract (July 2026, post-verify-* split):
#   - Exposes both:
#     (1) Artlist API helpers (search_live / detail / download /
#         enqueue_run / poll_run) — same name space as the scraper-side
#         verbs; these are STUBS as of this commit (arg-count guards + dry
#         mode + fail-closed `[STUB]` sentinels).
#     (2) Pipeline-side shared helpers (artlist_drive_resolve /
#         artlist_qdrant_assert / artlist_replay_run) — REAL
#         implementations moved from lib/velox_domain.sh per the DoD
#         "estrazione futura" consolidation (all artlist-shared helpers
#         live in ONE library). The 3 corresponding velox_* names in
#         velox_domain.sh are kept as thin delegators for backward
#         compatibility with the 53 in-tree call sites.
#   - Inherits BASE_URL / SCRAPER_URL / SMOKE_TOKEN / smoke_curl /
#     SMOKE_LAST_HTTP / SMOKE_LAST_BODY / DRY_RUN from common.sh
#     (sourced first by every arlist sub-script).
#
# Stub semantics for the API-side helpers (per AGENTS.md "fail closed"
# rule):
#   - Each helper validates its required args; missing args -> exit 2
#     with a [FATAL] stderr line.
#   - Under SMOKE_DRY_RUN=1: silently returns success (no [STUB] noise)
#     so dev dry-runs of `make smoke-dry` aren't spammed by stub markers.
#   - Under non-dry-run: emits a [STUB] stderr line + return 1.
#     Callers see rc=1 + an auditable log marker; never a silent no-op.
#
# Source-order contract (godlike/06 SSOT):
#   Must be sourced AFTER common.sh (for SMOKE_TOKEN / WORK_DIR) and
#   BEFORE velox_domain.sh (which delegates to these names). The
#   umbrella _artlist_common.sh enforces this ordering globally.

set -euo pipefail

# ── ANSI color defaults (defensive under `set -u` in dry-source) ─────────
# Sub-scripts source common.sh first which exports $RED / $YELLOW /
# $RESET. Stub libs may also be sourced in isolation (e.g. by a future
# regression net); defaulting here keeps the printf lines below from
# tripping set -u on $YELLOW-/- $RESET with no common.sh ancestor.
: "${YELLOW:=\033[33m}"
: "${RESET:=\033[0m}"
: "${RED:=\033[31m}"

# ── Internal arg-count guard (fail-closed per AGENTS.md) ──────────────────
# Usage: artlist_required_args EXPECTED_COUNT "$@"
# Iterates "$@" (without EXPECTED_COUNT arg) and counts non-empty entries.
# If got < need: emits [FATAL] to stderr + exit 2 (canonical setup-error).
artlist_required_args() {
    local need=$1; shift
    local got=0
    local _arg
    for _arg in "$@"; do
        [[ -n "$_arg" ]] && got=$((got + 1))
    done
    if (( got < need )); then
        printf '%s[FATAL]%s artlist lib: %d required arg(s), got %d\n' \
            "$RED" "$RESET" "$need" "$got" >&2
        exit 2
    fi
}

# ════════════════════════════════════════════════════════════════════════
# Artlist API helpers (scraper-side verbs) — REAL IMPLS (July 2026 DoD)
# ════════════════════════════════════════════════════════════════════════
# These handle the scraper-side verbs (search / detail / download / enqueue /
# poll) per the canonical, flag-based API surface.  Implementation strategy:
# artlist_* are SSOT-canonical names for the user's 5 named helpers per the
# DoD lib/ reorg directive.  Where velox_artlist_* already owns the proven
# impl (search_live / detail / download), artlist_* is a one-line delegator
# forwarding $@ to the velox_* impl — single-direction dependency; no
# recursion risk.  Where user-named helpers have NO velox_* sibling
# (enqueue_run / poll_run), the implementation lives directly in this file.
#
# Source-order contract (godlike/06 SSOT): this file MUST be sourced AFTER
# lib/common.sh (for smoke_curl/polling helpers WORK_DIR / SMOKE_TOKEN) and
# BEFORE lib/velox_domain.sh (which may re-define deltas over these names).
# The umbrella _artlist_common.sh enforces this order globally.

# ── artlist_search_live — GET /api/artlist/search/live contract probe ──
# artlist_search_live --term <query> [--limit <n>] [--timeout-seconds <n>] [--save-body <path>]
# Returns: 0 → contract pass (provider=artlist, ≥1 clip, per-clip shape tuple OK)
#          1 → contract violation
#          2 → transport failure (--max-time overrun, 401/403, 5xx, …)
# Synthesizes typed transport sentinels (SEARCH_TIMEOUT / AUTH_REQUIRED /
# SCRAPER_UNAVAILABLE) into the body when transport fails — mirrors the
# velox_artlist_search_live behaviour exactly so callers can use either
# prefix interchangeably.
artlist_search_live() { velox_artlist_search_live "$@"; }

# ── artlist_detail — POST $scraper/detail contract probe ───────────────
# artlist_detail --phase <happy|miss> --clip-page-url <url> \
#                 --scraper-url <url> [--save-body <path>]
# Returns: 0 → contract pass; 1 → contract violation; 2 → transport failure.
# Happy phase validates ok=true + page_url startswith artlist.io + primary
# URL is m3u8/MP4/manifest/playlist + stream_urls[] non-empty.  Miss phase
# validates ok=false + error=="STREAM_NOT_FOUND" + stream_urls[] EMPTY.
artlist_detail() { velox_artlist_detail "$@"; }

# ── artlist_download — POST $scraper/download contract probe ───────────
# artlist_download --clip-page-url <url> --scraper-url <url> --output-dir <dir> \
#                  [--save-body <path>]
# Returns: 0 → contract pass (ok=true + clip_id non-empty + local_path non-empty)
#          1 → contract violation
#          2 → transport / HTTP failure
# File-existence, MIME, and the DoD ffprobe contract are ownered by the
# gate layer (04_download.sh::gate_direct_download) — keeps the lib focused
# on JSON-response contract + path harvest, not local-file probing.
artlist_download() { velox_artlist_download "$@"; }

# ── artlist_enqueue_run — POST /api/artlist/run (returns run_id) ─────────
# artlist_enqueue_run TERM LIMIT [ROOT_FOLDER_ID]
# Args: TERM search term, LIMIT clip count, [ROOT_FOLDER_ID] optional Drive root.
# Default behaviour matches the DoD Gate 5 spec: strategy=replace, dry_run=false.
# Echoes run_id on stdout; writes raw body to WORK_DIR/last.body for forensic.
# Returns: 0 → enqueued, run_id echoed
#          1 → contract violation (HTTP 2xx but no run_id in response)
#          2 → transport failure
# Implementation re-uses the canonical artlist_replay_run helper (this file)
# which already owns the canonical /api/artlist/run envelope + Idempotency-Key
# + token headers per smoke_curl.  The three-arg positional signature
# (TERM, LIMIT, [ROOT_FOLDER_ID]) is the documented DoD Gate-5 minimum; richer
# payloads route through artlist_replay_run directly.
artlist_enqueue_run() {
    local term="$1" limit="$2" root="${3:-}"
    artlist_required_args 2 "$@"
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        : > "${WORK_DIR:-/tmp}/last.body"
        printf '%s\n' "stub-run-id-dry"
        return 0
    fi
    local tabbed
    tabbed=$(artlist_replay_run "$term" "$limit" replace 7 1920 1080 30 1 "$root" 2>/dev/null || true)
    local code jid body
    code=$(printf '%s' "$tabbed" | cut -f1)
    jid=$(printf '%s' "$tabbed" | cut -f2)
    body=$(printf '%s' "$tabbed" | cut -f3)
    if ! [[ "$code" =~ ^2[0-9][0-9]$ ]]; then
        printf '%s[FATAL]%s artlist_enqueue_run: HTTP=%s for term=%q\n' \
            "$RED" "$RESET" "$code" "$term" >&2
        return 2
    fi
    [[ -n "$jid" ]] || {
        printf '%s[FATAL]%s artlist_enqueue_run: empty run_id for term=%q\n' \
            "$RED" "$RESET" "$term" >&2
        return 1
    }
    printf '%s\n' "$jid"
}

# ── artlist_poll_run — poll RUN_ID until terminal ───────────────────────
# artlist_poll_run RUN_ID
# Echo: 0 → terminal (completed/SUCCEEDED/failed/FAILED/cancelled/dead_letter)
#       124 → wall-clock / poll timeout exceeded
#       1   → transport failure non-recoverable within the timeout window.
# Pattern mirrors smoke_poll_terminal (lib/common.sh) but targets the
# /api/artlist/run/{id} surface instead of /api/jobs/{id}/full and uses
# the artlist-terminal-status vocabulary verbatim.  Consumer contract:
# callers can use either smoke_poll_terminal (job-surface) OR artlist_poll_run
# (artlist-run surface); this implementation does NOT import from common.sh
# for the poll loop body to keep this file self-contained when sourced in
# isolation (e.g. by a future regression net).
artlist_poll_run() {
    artlist_required_args 1 "$@"
    local run_id="$1"
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        return 0
    fi
    local deadline=$(( $(date +%s) + ${SMOKE_POLL_TIMEOUT_SECONDS:-120} ))
    while (( $(date +%s) < deadline )); do
        if declare -F smoke_curl >/dev/null 2>&1; then
            smoke_curl GET "/api/artlist/run/${run_id}" >/dev/null
            if [[ "${SMOKE_LAST_HTTP:-}" == "200" ]]; then
                local status
                status=$(jq -r '.status // "?"' "${SMOKE_LAST_BODY:-/dev/null}" 2>/dev/null || echo "?")
                case "$status" in
                    completed|SUCCEEDED|failed|FAILED|cancelled|dead_letter) return 0 ;;
                esac
            fi
        else
            # Defensive fallback: curl directly when smoke_curl not sourced.
            local out="${WORK_DIR:-/tmp}/artlist_poll_${run_id}.json"
            local code
            code=$(curl -sS --max-time "${SMOKE_HTTP_TIMEOUT_SECONDS:-8}" \
                -o "$out" -w '%{http_code}' \
                -H "Authorization: Bearer ${SMOKE_TOKEN:-}" \
                "http://${SMOKE_API_BASE}/api/artlist/run/${run_id}" 2>/dev/null || echo 000)
            if [[ "$code" == "200" ]]; then
                local status
                status=$(jq -r '.status // "?"' "$out" 2>/dev/null || echo "?")
                case "$status" in
                    completed|SUCCEEDED|failed|FAILED|cancelled|dead_letter) return 0 ;;
                esac
            fi
        fi
        sleep "${SMOKE_POLL_INTERVAL_SECONDS:-2}"
    done
    return 124
}

# ════════════════════════════════════════════════════════════════════════
# Pipeline-side shared helpers (moved verbatim from lib/velox_domain.sh)
# ════════════════════════════════════════════════════════════════════════
# These were renamed + moved here per the user's DoD "estrazione futura"
# consolidation. The 3 corresponding velox_* names in velox_domain.sh are
# now thin delegators forwarding to these names (backward compat). All
# operational call sites should be migrated to the artlist_* names; the
# velox_* layer is preserved only as a Marshal-and-forward shim during
# the deprecation window (per AGENTS.md Refactoring Awareness).

# ── artlist_drive_resolve — confirm Drive file id exists, not trashed, size > 0
# Args: <drive_file_id>
# Returns: 0 → file resolved with size > 0 and trashed=false
#          1 → HTTP 2xx but contract failed
#          2 → transport / HTTP non-2xx
# The body is written to a deterministic file under ${WORK_DIR:-/tmp} so
# callers inspecting the response from outside the function can guarantee
# $WORK_DIR/artlist_drive_${id}.json is populated.
artlist_drive_resolve() {
    local file_id="$1"
    [[ -n "$file_id" ]] || return 1
    local out="${WORK_DIR:-/tmp}/artlist_drive_${file_id}.json"
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

# ── artlist_qdrant_assert — at least one PUBLISHED point for clip_id
# Args: clip_id collection qdrant_url expected_source expected_media_type
#       [expected_lifecycle] [api_key]
# Returns: 0 → point found with shape match + asset_id round-trip OK
#          1 → shape contract failed (including asset_id round-trip drift)
#          2 → transport / HTTP failure
artlist_qdrant_assert() {
    local clip_id="$1" collection="$2" qdrant_url="$3"
    local expected_source="$4" expected_media="$5"
    local expected_lifecycle="${6:-PUBLISHED}" api_key="${7:-}"
    [[ -n "$clip_id" && -n "$collection" && -n "$qdrant_url" \
        && -n "$expected_source" && -n "$expected_media" ]] || return 1
    local out="${WORK_DIR:-/tmp}/artlist_qdrant_${clip_id}.json"
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
    # assertion failing produces a clear "asset_id drift" log line.
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

# ── artlist_replay_run — POST /api/artlist/run wrapper for Gate 9 cache replay
# Args: <term> <limit> [strategy] [clip_duration] [width] [height] [fps] [concurrency] [root_folder_id]
# Emits: <HTTP_code>\t<run_id>\t<body_path>
# Defaults match the DoD Gate 9 contract: dry_run:false + strategy:replace
# are intrinsic (the canonical replay semantics — replay the run with the
# same parameters to confirm cache_hit=true on a fresh GetRunTagResponse).
# For a non-replay use case, callers should use the underlying smoke_curl
# POST /api/artlist/run surface directly; artlist_replay_run exists to
# make the replay semantics explicit.
artlist_replay_run() {
    local term="$1" limit="$2"
    local strategy="${3:-replace}"
    local clip_duration="${4:-7}"
    local width="${5:-1920}" height="${6:-1080}" fps="${7:-30}"
    local concurrency="${8:-1}"
    local root="${9:-${VELOX_DRIVE_ARTLIST_ROOT:-}}"
    local out="${WORK_DIR:-/tmp}/artlist_replay_$$.json"
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

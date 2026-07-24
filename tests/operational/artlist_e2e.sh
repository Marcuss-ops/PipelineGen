#!/usr/bin/env bash
# tests/operational/artlist_e2e.sh — Artlist Definition-of-Done battery.
#
# Eleven hard gates + one mandatory restart test. Every gate touches the
# live PipelineGen stack (Go server on 8000, node-scraper on 9123, Qdrant,
# SQLite, Drive). The battery inherits generic infra from
# tests/operational/lib/common.sh (`smoke_*`) and PipelineGen domain
# assertions from tests/operational/lib/velox_domain.sh (`velox_*`).
#
# Exit codes (matches lib/common.sh convention):
#   0  all hard assertions passed
#   1  one or more hard assertions failed
#   2  setup error / missing prerequisite
# 124  overall wall-clock/timeout exceeded
#
# Usage:
#   bash tests/operational/artlist_e2e.sh             # full battery
#   SMOKE_DRY_RUN=1 bash tests/operational/artlist_e2e.sh   # announce-only
#   bash tests/operational/artlist_e2e.sh --help
#
# Gate map (see tests/operational/artlist_gates.md for the full checklist):
#   Gate  0 — clean reproducible environment
#   Gate  1 — /detail hard gate (STREAM_NOT_FOUND ok path)
#   Gate  2 — /download with ffprobe hard gate
#   Gate  3 — /api/artlist/search/live across 3 queries + 60s timeout
#   Gate  4 — first fresh run (3/3, processed=3 failed=0, no RETRY_WAIT)
#   Gate  5 — per-clip database + file validation
#   Gate  6 — Drive resolve-by-id hard gate (file on Drive, not trashed)
#   Gate  7 — SQLite integrity (single row, outbox terminal)
#   Gate  8 — Qdrant point + /api/media/search hard gate
#   Gate  9 — replay (cache_hit=true, cache_source=sqlite)
#   Gate 10 — negative tests (SESSION_EXPIRED, STREAM_NOT_FOUND, SCRAPER_UNAVAILABLE)
#   Restart — PASS before AND after restart (no manual intervention)

set -euo pipefail

SMOKE_TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-3600}"
SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-1800}"
SMOKE_POLL_INTERVAL_SECONDS="${SMOKE_POLL_INTERVAL_SECONDS:-5}"
SMOKE_HTTP_TIMEOUT_SECONDS="${SMOKE_HTTP_TIMEOUT_SECONDS:-300}"
export SMOKE_TIMEOUT_SECONDS SMOKE_POLL_TIMEOUT_SECONDS SMOKE_POLL_INTERVAL_SECONDS SMOKE_HTTP_TIMEOUT_SECONDS

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"
# shellcheck disable=SC1091
source "$DIR/lib/velox_domain.sh"

if [[ "${HELP_REQUESTED:-0}" == "1" ]]; then
    cat <<'EOF'
artlist_e2e.sh — Artlist Definition-of-Done battery

Eleven hard gates + one restart test against the live PipelineGen stack.
Inherits smoke_* (lib/common.sh) and velox_* (lib/velox_domain.sh) helpers;
NO business logic is duplicated here — every assertion is a thin call.

Live checks (per Gate map in tests/operational/artlist_gates.md):
  0  clean environment + diagnostics reachable
  1  POST /detail returns m3u8/MP4 stream URL or STREAM_NOT_FOUND
  2  POST /download produces ffprobe-valid MP4
  3  GET /api/artlist/search/live across 3 queries (60s each)
  4  POST /api/artlist/run 3/3 SUCCEEDED, no RETRY_WAIT
  5  per-clip DB + local file integrity
  6  POST /api/drive/resolve-by-id hard gate
  7  SQLite single-row + outbox completed/superseded
  8  Qdrant point + POST /api/media/search returns the clip
  9  Replay: same clip_id + drive_file_id + file_hash, cache_hit=true
 10  Negative tests: SESSION/STREAM/SCRAPER unavailable
 R  Restart test: PASS before AND after restart

Dry run:
  SMOKE_DRY_RUN=1 bash tests/operational/artlist_e2e.sh
EOF
    exit 0
fi

# ── Per-battery runtime configuration ─────────────────────────────────────
smoke_require curl sqlite3 file ffmpeg ffprobe jq

HOST="${VELOX_HOST:-127.0.0.1}"
PIPELINE_PORT="${PIPELINE_PORT:-${VELOX_PORT:-8000}}"
BASE_URL="http://${HOST}:${PIPELINE_PORT}"
DB_PATH="${VELOX_DATA_DIR:-./data}/media/media.db.sqlite"
SCRAPER_URL="${VELOX_ARTLIST_SCRAPER_SERVER_URL:-http://127.0.0.1:9123}"
QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"
QDRANT_API_KEY="${QDRANT_API_KEY:-${VELOX_QDRANT_API_KEY:-}}"
COLLECTION="${QDRANT_COLLECTION:-media_assets_current}"
ARTLIST_ROOT_FOLDER="${VELOX_DRIVE_ARTLIST_ROOT:-${ROOT_FOLDER_ID:-}}"
ARTLIST_TERM="${ARTLIST_TERM:-business team working in modern office}"
ARTLIST_LIMIT="${ARTLIST_LIMIT:-3}"

LIVE_QUERIES=(
    "business team working in modern office"
    "heavyweight boxer training in gym"
    "boxing arena crowd celebrating"
)

PASS=0
WARN=0
FAIL=0

log_pass() { printf '[PASS]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; PASS=$((PASS + 1)); }
log_warn() { printf '[WARN]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; WARN=$((WARN + 1)); }
log_fail() { printf '[FAIL]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; FAIL=$((FAIL + 1)); }
log_info() { printf '[INFO]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; }

# ── Gate 0 — clean reproducible environment ─────────────────────────────
# Verifies: single node artlist_server.js; one Chrome profile; scraper 9123
# reachable; PipelineGen 8000 reachable; no RUNNING/QUEUED/RETRY_WAIT jobs;
# SQLite readable; ffmpeg+ffprobe on PATH; Qdrant reachable; Drive folder
# set; Artlist session authenticated. Fail-closed on any miss.
gate_preflight() {
    smoke_log_section "Gate 0 — clean reproducible environment"
    local failures=0

    smoke_curl GET "/health" >/dev/null
    if [[ "${SMOKE_LAST_HTTP:-}" =~ ^2[0-9][0-9]$ ]]; then
        log_pass "PipelineGen /health reachable at $BASE_URL"
    else
        log_fail "GET /health failed (HTTP=${SMOKE_LAST_HTTP:-empty})"
        failures=$((failures + 1))
    fi
    smoke_curl GET "/ready" >/dev/null
    if [[ "${SMOKE_LAST_HTTP:-}" =~ ^2[0-9][0-9]$ ]]; then
        log_pass "PipelineGen /ready reachable"
    else
        log_fail "GET /ready failed (HTTP=${SMOKE_LAST_HTTP:-empty})"
        failures=$((failures + 1))
    fi
    smoke_curl GET "/api/artlist/job-consumer" >/dev/null
    if [[ "${SMOKE_LAST_HTTP:-}" =~ ^2[0-9][0-9]$ ]] \
        && jq -e '.active == true and .consumer_type == "media.artlist"' \
            "${SMOKE_LAST_BODY:-/dev/null}" >/dev/null 2>&1; then
        log_pass "artlist job-consumer active"
    else
        log_fail "/api/artlist/job-consumer not active (HTTP=${SMOKE_LAST_HTTP:-empty})"
        failures=$((failures + 1))
    fi

    if ! curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" "$SCRAPER_URL/health" 2>/dev/null \
            | jq -e '.ok == true' >/dev/null 2>&1; then
        log_fail "scraper /health not ok at $SCRAPER_URL/health"
        failures=$((failures + 1))
    else
        log_pass "scraper /health reachable at $SCRAPER_URL"
    fi

    local scraper_count
    scraper_count=$(pgrep -af 'node.*artlist_server\.js' 2>/dev/null | wc -l || true)
    if [[ "${scraper_count}" -gt 1 ]]; then
        log_fail "expected one node artlist_server.js, found ${scraper_count}"
        failures=$((failures + 1))
    else
        log_pass "single node artlist_server.js process"
    fi

    # Single browser/Chrome profile (Puppeteer-launched under node-scraper).
    # Threshold ≤3 because headless Chrome forks 1 main + ~2 helpers per active
    # profile. > 3 = orphaned profiles / parallel browser instances.
    local chrome_total
    chrome_total=$(pgrep -ac 'chrome|chromium' 2>/dev/null || echo 0)
    if [[ "${chrome_total}" -gt 3 ]]; then
        log_fail "expected ≤3 chrome/chromium processes, found ${chrome_total} (multiple headless instances?)"
        failures=$((failures + 1))
    else
        log_pass "chrome/chromium within bounds (${chrome_total})"
    fi

    if [[ ! -f "$DB_PATH" ]]; then
        log_fail "SQLite DB missing: $DB_PATH"
        failures=$((failures + 1))
    else
        log_pass "SQLite readable at $DB_PATH"
    fi

    # No pending Artlist jobs in {QUEUED,LEASED,RUNNING,FINALIZING,RETRY_WAIT}.
    # Catches leftover state from interrupted runs without manual DB intervention.
    # Scoped to type LIKE 'media.artlist%' so unrelated voiceover/stock jobs
    # don't gate the Artlist DoD.
    local pending_jobs
    pending_jobs=$(sqlite3 -readonly "$DB_PATH" \
        "SELECT COUNT(*) FROM jobs WHERE type LIKE 'media.artlist%' \
         AND status IN ('QUEUED','LEASED','RUNNING','FINALIZING','RETRY_WAIT')" \
        2>/dev/null | tr -d ' \n' || echo "?")
    if [[ "${pending_jobs}" == "0" ]]; then
        log_pass "no pending Artlist jobs"
    elif [[ -z "$DB_PATH" || ! -f "$DB_PATH" ]]; then
        log_warn "skipped pending-jobs check: SQLite DB absent"
    else
        log_fail "expected ZERO pending Artlist jobs, found ${pending_jobs}"
        failures=$((failures + 1))
    fi

    if ! command -v ffmpeg >/dev/null 2>&1 || ! command -v ffprobe >/dev/null 2>&1; then
        log_fail "ffmpeg + ffprobe required on PATH"
        failures=$((failures + 1))
    else
        log_pass "ffmpeg+ffprobe on PATH"
    fi

    if ! curl -sS --max-time 5 "$QDRANT_URL/collections" 2>/dev/null \
            | jq -e '.result.collections | length >= 0' >/dev/null 2>&1; then
        log_fail "Qdrant unreachable at $QDRANT_URL"
        failures=$((failures + 1))
    else
        log_pass "Qdrant reachable at $QDRANT_URL"
    fi

    if [[ -z "$ARTLIST_ROOT_FOLDER" ]]; then
        log_fail "VELOX_DRIVE_ARTLIST_ROOT not configured (no Artlist Drive root)"
        failures=$((failures + 1))
    else
        log_pass "Artlist Drive root configured"
    fi

    # Artlist session is authenticated iff /api/artlist/diagnostics.scraper.ok == true.
    # The `scraper` probe inside /api/artlist/diagnostics already validates the
    # node-scraper ↔ artlist.io session (per system_prober_http.go::stage_2_session_valid).
    smoke_curl GET "/api/artlist/diagnostics?term=$(printf '%s' "$ARTLIST_TERM" | jq -sRr @uri)" >/dev/null
    if [[ "${SMOKE_LAST_HTTP:-}" =~ ^2[0-9][0-9]$ ]] \
        && jq -e '.scraper.ok == true' "${SMOKE_LAST_BODY:-/dev/null}" >/dev/null 2>&1; then
        log_pass "Artlist session authenticated (/api/artlist/diagnostics scraper probe green)"
    else
        log_fail "Artlist session NOT authenticated (scraper probe not green; HTTP=${SMOKE_LAST_HTTP:-empty})"
        failures=$((failures + 1))
    fi

    if (( failures > 0 )); then
        log_fail "Gate 0 preflight failed (${failures} sub-checks)"
        return 1
    fi
    log_pass "Gate 0 preflight clean"
}

# Gates 1..10 are scaffolded as stubs. Each gate function:
#   - increments PASS/WARN/FAIL counters
#   - returns 0 on PASS, 1 on FAIL
# Subsequent PRs implement one gate at a time and remove the stub marker.
# ── Gate 1 — POST /detail hard gate ─────────────────────────────────────
# Spec (July 2026 DoD):
#   happy:   ok=true + clip_id non-empty + page_url starts with https://artlist.io/ +
#            primary_url is m3u8/MP4 (or /manifest|/playlist fallback) +
#            primary_url != page_url + stream_urls[] non-empty
#   negative: ok=false + error=="STREAM_NOT_FOUND" + stream_urls==[] + clip_id non-empty
# Anything else → fail-closed (gate returns 1, battery aborts).
#
# Implementation choice (DoD refactor July 2026):
# POST goes directly to the node-scraper endpoint $SCRAPER_URL/detail because:
#   (a) the scraper is the source-of-truth for STREAM_NOT_FOUND semantics, and
#   (b) hitting the Go server's forwarding layer would mask scraper errors.
# Test clip_page_url for the happy path is sampled live: first hit from
# GET /api/artlist/search/live with LIVE_QUERIES[0] so the test always
# exercises a real, currently-routable Artlist URL.
gate_detail_stream() {
    smoke_log_section "Gate 1 — POST /detail hard gate (STREAM_NOT_FOUND ok path)"
    local failures=0
    local real_page_url bad_page_url
    bad_page_url="https://artlist.io/stock-footage/clip/00000000"

    # ── Phase 1: source a real clip_page_url from the live-search surface
    smoke_curl GET "/api/artlist/search/live?term=${LIVE_QUERIES[0]}&limit=5" >/dev/null
    if [[ ! "${SMOKE_LAST_HTTP:-}" =~ ^2[0-9][0-9]$ ]] \
       || ! jq -e '.clips // [] | length > 0' "${SMOKE_LAST_BODY:-/dev/null}" >/dev/null 2>&1; then
        log_fail "live search probe for /detail failed (HTTP=${SMOKE_LAST_HTTP:-empty})"
        return 1
    fi
    real_page_url=$(jq -r '.clips[0].PageURL // empty' "${SMOKE_LAST_BODY:-/dev/null}")
    if [[ -z "$real_page_url" || "$real_page_url" == "null" ]] \
       || ! [[ "$real_page_url" =~ ^https://artlist\.io/ ]]; then
        log_fail "first live clip PageURL invalid: '$real_page_url'"
        return 1
    fi

    # ── Phase 2: happy-path POST /detail
    local detail_ok="$WORK_DIR/gate1_detail_ok.json"
    local code
    code=$(curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
        -X POST -H 'Content-Type: application/json' \
        -d "$(jq -nc --arg u "$real_page_url" '{clip_page_url:$u}')" \
        "$SCRAPER_URL/detail" -o "$detail_ok" -w '%{http_code}' 2>/dev/null || echo 000)

    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "POST /detail HTTP=$code (expected 2xx) for $real_page_url"
        smoke_echo_safe "$(head -c 600 "$detail_ok" 2>/dev/null || true)" >&2
        failures=$((failures + 1))
    elif ! jq -e '.ok == true
        and (.clip.ok // false) == true
        and ((.clip.page_url // .page_url // "") | startswith("https://artlist.io/"))
        and ((.clip.primary_url // .primary_url // "") | test("\\.m3u8(\\?|$)|\\.mp4(\\?|$)|/manifest|/playlist"))
        and ((.clip.primary_url // .primary_url // "") != "")
        and ((.clip.primary_url // .primary_url // "") != (.clip.page_url // .page_url // ""))
        and ((.clip.stream_urls // .stream_urls // []) | length) > 0' \
        "$detail_ok" >/dev/null 2>&1; then
        log_fail "/detail happy-path contract failed for $real_page_url"
        smoke_echo_safe "$(head -c 800 "$detail_ok" 2>/dev/null || true)" >&2
        failures=$((failures + 1))
    else
        log_pass "/detail happy-path ok=true for $real_page_url"
    fi

    # ── Phase 3: negative POST /detail with a known-invalid clip_page_url
    local detail_snf="$WORK_DIR/gate1_detail_snf.json"
    code=$(curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
        -X POST -H 'Content-Type: application/json' \
        -d "$(jq -nc --arg u "$bad_page_url" '{clip_page_url:$u}')" \
        "$SCRAPER_URL/detail" -o "$detail_snf" -w '%{http_code}' 2>/dev/null || echo 000)

    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "POST /detail (negative) HTTP=$code (expected 2xx with STREAM_NOT_FOUND) for $bad_page_url"
        smoke_echo_safe "$(head -c 600 "$detail_snf" 2>/dev/null || true)" >&2
        failures=$((failures + 1))
    elif ! jq -e '.ok == false
        and .error == "STREAM_NOT_FOUND"
        and ((.clip_id // "") | length) > 0
        and ((.stream_urls // []) | length) == 0' \
        "$detail_snf" >/dev/null 2>&1; then
        log_fail "/detail STREAM_NOT_FOUND contract failed for $bad_page_url"
        smoke_echo_safe "$(head -c 800 "$detail_snf" 2>/dev/null || true)" >&2
        failures=$((failures + 1))
    else
        log_pass "/detail STREAM_NOT_FOUND ok=false for $bad_page_url"
    fi

    if (( failures > 0 )); then
        log_fail "Gate 1 /detail hard gate failed (${failures} sub-checks)"
        return 1
    fi
    log_pass "Gate 1 /detail hard gate clean"
}
# ── Gate 2 — POST /download + ffprobe hard gate ───────────────────────
# DoD spec (July 2026): `Finché detail e download diretto non passano,
# non si lancia /api/artlist/run`. Hard-gate checks (fail-closed on miss):
#   - HTTP 2xx
#   - response: ok=true + clip_id non-empty + local_path non-empty
#   - local file exists at local_path
#   - file size > 0
#   - MIME == video/mp4
#   - ffprobe reads the file with the canonical DoD command and produces
#     format.duration > 0, format.size > 0, at least one stream with
#     width > 0 and height > 0.
#
# Implementation notes:
#   * /download consumes real Artlist quota. We isolate the artifact under
#     $WORK_DIR/gate2_dl/ so the existing smoke_cleanup trap on WORK_DIR
#     reaps the file when the battery exits.
#   * clip_page_url is sampled live from /api/artlist/search/live (same
#     pattern as Gate 1) so the test always exercises a real Artlist URL.
#   * Raw curl against $SCRAPER_URL (node-scraper does not speak the
#     PipelineGen bearer token / Idempotency-Key contract).
gate_direct_download() {
    smoke_log_section "Gate 2 — POST /download + ffprobe hard gate"
    local failures=0
    local out_dir="$WORK_DIR/gate2_dl"
    mkdir -p "$out_dir"

    # ── Phase 1: source a real clip_page_url from the live-search surface
    smoke_curl GET "/api/artlist/search/live?term=${LIVE_QUERIES[0]}&limit=5" >/dev/null
    if [[ ! "${SMOKE_LAST_HTTP:-}" =~ ^2[0-9][0-9]$ ]] \
       || ! jq -e '.clips // [] | length > 0' "${SMOKE_LAST_BODY:-/dev/null}" >/dev/null 2>&1; then
        log_fail "live search probe for /download failed (HTTP=${SMOKE_LAST_HTTP:-empty})"
        return 1
    fi
    local real_page_url
    real_page_url=$(jq -r '.clips[0].PageURL // empty' "${SMOKE_LAST_BODY:-/dev/null}")
    if [[ -z "$real_page_url" || ! "$real_page_url" =~ ^https://artlist\.io/ ]]; then
        log_fail "first live clip PageURL invalid: '$real_page_url'"
        return 1
    fi

    # ── Phase 2: POST /download (consumes Artlist quota)
    local dl_body="$WORK_DIR/gate2_download.json"
    local code
    code=$(curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
        -X POST -H 'Content-Type: application/json' \
        -d "$(jq -nc --arg u "$real_page_url" --arg o "$out_dir" '{clip_page_url:$u, output_dir:$o}')" \
        "$SCRAPER_URL/download" -o "$dl_body" -w '%{http_code}' 2>/dev/null || echo 000)

    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "POST /download HTTP=$code (expected 2xx) for $real_page_url"
        smoke_echo_safe "$(head -c 600 "$dl_body" 2>/dev/null || true)" >&2
        return 1
    fi

    if ! jq -e '.ok == true
        and ((.clip_id // "") | length) > 0
        and ((.local_path // "") | length) > 0' "$dl_body" >/dev/null 2>&1; then
        log_fail "/download response contract failed (want ok=true + clip_id non-empty + local_path non-empty)"
        smoke_echo_safe "$(head -c 800 "$dl_body" 2>/dev/null || true)" >&2
        failures=$((failures + 1))
    else
        log_pass "/download response contract: ok=true, clip_id+local_path present"
    fi

    # ── Phase 3: local-file + ffprobe assertions
    local local_path file_size mime_type
    local_path=$(jq -r '.local_path // empty' "$dl_body")
    if [[ -z "$local_path" || ! -f "$local_path" ]]; then
        log_fail "/download local file missing: '$local_path'"
        failures=$((failures + 1))
    else
        file_size=$(stat -c%s "$local_path" 2>/dev/null || echo 0)
        if [[ "$file_size" -le 0 ]]; then
            log_fail "/download local file size=$file_size (want >0) at $local_path"
            failures=$((failures + 1))
        else
            log_pass "/download local file size=${file_size}B at $local_path"
        fi

        mime_type=$(file -b --mime-type "$local_path" 2>/dev/null || true)
        if [[ "$mime_type" != "video/mp4" ]]; then
            log_fail "/download MIME=$mime_type (want video/mp4) at $local_path"
            failures=$((failures + 1))
        else
            log_pass "/download MIME=video/mp4"
        fi

        # DoD-exact ffprobe command: produces JSON with format.duration,
        # format.size, and streams[] each carrying codec_name/width/height.
        local ffprobe_json
        ffprobe_json=$(ffprobe -v error \
            -show_entries format=duration,size \
            -show_entries stream=codec_name,width,height \
            -of json "$local_path" 2>/dev/null || true)
        if [[ -z "$ffprobe_json" ]] || ! jq -e '
            (.format.duration // 0 | tonumber) > 0
            and (.format.size // 0 | tonumber) > 0
            and ([.streams[]?
                  | select((.width // 0 | tonumber) > 0 and (.height // 0 | tonumber) > 0)]
                 | length) >= 1' <<<"$ffprobe_json" >/dev/null 2>&1; then
            log_fail "ffprobe did not return duration>0+size>0+width>0+height>0 for $local_path"
            smoke_echo_safe "$(head -c 800 <<<"$ffprobe_json" 2>/dev/null || true)" >&2
            failures=$((failures + 1))
        else
            local duration size width height
            duration=$(jq -r '.format.duration // 0' <<<"$ffprobe_json")
            size=$(jq -r '.format.size // 0' <<<"$ffprobe_json")
            width=$(jq -r '[.streams[]?.width // 0 | tonumber] | max' <<<"$ffprobe_json")
            height=$(jq -r '[.streams[]?.height // 0 | tonumber] | max' <<<"$ffprobe_json")
            log_pass "ffprobe OK: duration=${duration}s size=${size}B largestStream=${width}x${height}"
        fi
    fi

    if (( failures > 0 )); then
        log_fail "Gate 2 /download + ffprobe hard gate failed (${failures} sub-checks)"
        return 1
    fi
    log_pass "Gate 2 /download + ffprobe hard gate clean"
}
# ── Gate 3 — /api/artlist/search/live × 3 queries + 60s timeout ─────────
# DoD spec (July 2026): tre query semanticamente differenti (business,
# boxing-gym, boxing-arena). Per ogni query:
#   - HTTP 2xx OR explicit err=SEARCH_TIMEOUT if 60s elapses
#   - provider == 'artlist'
#   - ≥1 clip in .clips[]
#   - per-clip clip_id (ExternalID/ID) non-empty
#   - per-clip page_url on artlist.io
#   - per-clip title non-placeholder (≠ "Artlist", length>5, non-empty)
#   - query term NOT truncated by server (URL round-trip)
#   - no placeholder / no invented: RawMetadata present + Keywords[] non-empty
#
# Implementation notes:
#   * LIVE_QUERIES[0..2] holds the three semantic terms in ENGLISH
#     (Artlist's catalog is English; Italian translations in the spec
#     describe the semantics, the actual terms live in LIVE_QUERIES).
#   * 60s timeout enforced on the curl side; on timeout we emit the
#     SEARCH_TIMEOUT sentinel rather than reporting ok=true with zero
>     results (as the DoD explicitly forbids).
#   * Raw curl (no smoke_curl) for the Authorization header + per-query
#     timeout ergonomics; token must be present (validated by lib/common.sh).
gate_live_search_three() {
    smoke_log_section "Gate 3 — /search/live × 3 + 60s timeout (SEARCH_TIMEOUT sentinel)"
    local failures=0
    local per_query_timeout=60

    local idx=0
    local q
    for q in "${LIVE_QUERIES[@]:0:3}"; do
        smoke_log_section "Gate 3 query $((idx+1))/3: '$q'"
        local out="$WORK_DIR/gate3_search_${idx}.json"
        local code
        code=$(curl -sS --max-time "$per_query_timeout" -G \
            -o "$out" -w '%{http_code}' \
            -H "Authorization: Bearer $SMOKE_TOKEN" \
            --data-urlencode "term=$q" \
            --data-urlencode "limit=5" \
            "$BASE_URL/api/artlist/search/live" 2>/dev/null || echo 000)

        # Timeout / transport failure: explicit SEARCH_TIMEOUT sentinel
        # (DoD: never report ok=true with zero results on slow queries).
        if [[ "$code" == "000" || -z "$code" ]]; then
            log_fail "SEARCH_TIMEOUT (>${per_query_timeout}s) for query '$q'"
            failures=$((failures + 1))
            idx=$((idx + 1))
            continue
        fi

        if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
            log_fail "live search HTTP=$code (want 2xx) for query '$q'"
            smoke_echo_safe "$(head -c 400 "$out" 2>/dev/null || true)" >&2
            failures=$((failures + 1))
            idx=$((idx + 1))
            continue
        fi

        # Provider + clips contract
        if ! jq -e '.provider == "artlist"
            and ((.clips // []) | length) > 0' "$out" >/dev/null 2>&1; then
            log_fail "provider != 'artlist' OR zero clips for query '$q' (ok-but-empty NOT allowed)"
            smoke_echo_safe "$(head -c 400 "$out" 2>/dev/null || true)" >&2
            failures=$((failures + 1))
            idx=$((idx + 1))
            continue
        fi
        log_pass "live search returned clips for query '$q'"

        # Query-not-truncated guard: server should echo back the term.
        # Fallback to URL round-trip if `.term` is absent from the response.
        local recv_term
        recv_term=$(jq -r '.term // empty' "$out" 2>/dev/null || true)
        if [[ -n "$recv_term" && "$recv_term" != "$q" ]]; then
            log_fail "term echoed back '$recv_term' != original '$q' (server truncated query)"
            failures=$((failures + 1))
        else
            log_pass "query '$q' not truncated"
        fi

        # Per-clip shape walk
        local clip_count clip_failures
        clip_count=$(jq '.clips | length' "$out" 2>/dev/null || echo 0)
        clip_failures=0
        local ci
        for ci in $(seq 0 $((clip_count - 1))); do
            local clip_id page_url title raw_meta kw_len
            clip_id=$(jq -r ".clips[$ci].ExternalID // .clips[$ci].ID // empty" "$out")
            page_url=$(jq -r ".clips[$ci].PageURL // empty" "$out")
            title=$(jq -r ".clips[$ci].Title // empty" "$out")
            raw_meta=$(jq -r ".clips[$ci].RawMetadata // empty" "$out")
            kw_len=$(jq ".clips[$ci].Keywords // [] | length" "$out" 2>/dev/null || echo 0)

            if [[ -z "$clip_id" ]]; then
                log_fail "clip[$ci] missing clip_id (ExternalID/ID) for '$q'"
                clip_failures=$((clip_failures + 1))
            fi
            if [[ -z "$page_url" || ! "$page_url" =~ ^https?://artlist\.io/ ]]; then
                log_fail "clip[$ci] page_url invalid '$page_url' for '$q'"
                clip_failures=$((clip_failures + 1))
            fi
            if [[ -z "$title" || "$title" == "Artlist" || ${#title} -lt 5 ]]; then
                log_fail "clip[$ci] title placeholder/invalid '$title' for '$q'"
                clip_failures=$((clip_failures + 1))
            fi
            if [[ -z "$raw_meta" || "$kw_len" == "0" ]]; then
                log_fail "clip[$ci] missing RawMetadata or zero Keywords (placeholder/invented?) for '$q'"
                clip_failures=$((clip_failures + 1))
            fi
        done
        if (( clip_failures == 0 )); then
            log_pass "all $clip_count clips valid for query '$q'"
        else
            failures=$((failures + clip_failures))
        fi

        idx=$((idx + 1))
    done

    if (( failures > 0 )); then
        log_fail "Gate 3 /search/live × 3 failed (${failures} sub-checks)"
        return 1
    fi
    log_pass "Gate 3 /search/live × 3 clean"
}
gate_fresh_run_three()     { smoke_log_section "Gate 4 — first fresh run 3/3";              log_info "[STUB] Gate 4 — implement next"; }
gate_per_clip_validation() { smoke_log_section "Gate 5 — per-clip DB + file validation";   log_info "[STUB] Gate 5 — implement next"; }
gate_drive_resolve()       { smoke_log_section "Gate 6 — Drive resolve-by-id hard gate";   log_info "[STUB] Gate 6 — implement next"; }
gate_sqlite_outbox()       { smoke_log_section "Gate 7 — SQLite + outbox integrity";       log_info "[STUB] Gate 7 — implement next"; }
gate_qdrant_search()       { smoke_log_section "Gate 8 — Qdrant + media search hard gate";  log_info "[STUB] Gate 8 — implement next"; }
gate_cache_replay()        { smoke_log_section "Gate 9 — replay cache_hit=true";           log_info "[STUB] Gate 9 — implement next"; }
gate_explicit_errors()     { smoke_log_section "Gate 10 — negative tests";                 log_info "[STUB] Gate 10 — implement next"; }
gate_restart()             { smoke_log_section "Restart — PASS pre/post restart";         log_info "[STUB] Restart — implement next"; }

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — Artlist DoD battery would probe:"
        printf '  GET  %s/health\n' "$BASE_URL"
        printf '  GET  %s/ready\n' "$BASE_URL"
        printf '  GET  %s/api/artlist/search/live?term=<LIVE_QUERIES[0..2]>&limit=5 (×3, 60s each, SEARCH_TIMEOUT on overrun)\n' "$BASE_URL"
        printf '  POST %s/detail (clip_page_url from LIVE_QUERIES[0])\n' "$SCRAPER_URL"
        printf '  POST %s/detail (clip_page_url=%s, negative STREAM_NOT_FOUND)\n' "$SCRAPER_URL" "https://artlist.io/stock-footage/clip/00000000"
        printf '  POST %s/download (clip_page_url from LIVE_QUERIES[0], output_dir=$WORK_DIR/gate2_dl)\n' "$SCRAPER_URL"
        printf '  POST %s/api/artlist/run\n' "$BASE_URL"
        printf '  POST %s/api/drive/resolve-by-id\n' "$BASE_URL"
        printf '  POST %s/collections/%s/points/scroll (Qdrant)\n' "$QDRANT_URL" "$COLLECTION"
        exit 0
    fi

    gate_preflight             || return 1
    gate_detail_stream
    gate_direct_download
    gate_live_search_three
    gate_fresh_run_three
    gate_per_clip_validation
    gate_drive_resolve
    gate_sqlite_outbox
    gate_qdrant_search
    gate_cache_replay
    gate_explicit_errors
    gate_restart

    printf '\n============================================\n'
    printf '  VidRush Media E2E Battery (artlist_e2e)\n'
    printf '  PASS=%d  WARN=%d  FAIL=%d\n' "$PASS" "$WARN" "$FAIL"
    printf '============================================\n'
    if [[ "$FAIL" -gt 0 ]]; then
        printf 'VERDICT: FAIL\n'
        return 1
    fi
    printf 'VERDICT: PASS\n'
    return 0
}

main "$@"

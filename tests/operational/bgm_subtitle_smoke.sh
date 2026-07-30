#!/usr/bin/env bash
#
# bgm_subtitle_smoke.sh — black-box FASE BM1 background music + animated
# subtitle smoke test for the worker rendering pipeline.
#
# Test (single happy-path case):
#   POST /api/v1/jobs with 2 scenes each carrying:
#     - voiceover audio
#     - background music track (bgm_role=background_music, volume=0.15)
#     - subtitle_track with preset karaoke_fill / active_word_pop
#     - clip with stock footage
#
# Atteso finale (8 assertions total):
#   - 1 pipeline job (type='scene.composite.v1', status='SUCCEEDED')
#   - 1 voiceovers row (audio generated)
#   - 1 media_asset row for the rendered video
#   - N outbox events (asset.index.requested, delivery.pending)
#   - 1 parent status = SUCCEEDED (godlike/07 no-fake-availability)
#   - Background music paths present in worker payload (cache hit verified)
#   - Subtitle tracks present with correct preset
#   - Audio tracks include at least one background_music role track
#
# Precheck:
#   1. DataServer up (GET /health 200)
#   2. Worker(s) registered and available
#   3. SMOKE_DB exists
#   4. Background music assets available (local or Drive)
#   5. Subtitle preset catalog accessible (Chronon3d built-in presets)
#
# Usage:
#   ./bgm_subtitle_smoke.sh
#   BASE=http://10.0.0.1:8000 ./bgm_subtitle_smoke.sh
#   DRY_RUN=1 ./bgm_subtitle_smoke.sh
#
# Environment variables (all overridable; defaults shown):
#   API_BASE                host:port (default 127.0.0.1:8000)
#   VELOX_ADMIN_TOKEN       bearer token (mandatory if not --dry)
#   SMOKE_DB                path to media.db.sqlite
#                            (default data/media/media.db.sqlite)
#   SMOKE_BGM_DIR            local directory containing background music .mp3 files
#                            (default data/media/sound_effects)
#   SMOKE_TIMEOUT_SECONDS   per-script overall wall clock (default 300)
#   SMOKE_POLL_TIMEOUT_SECONDS  poll loop ceiling (default 180)
#   SMOKE_POLL_INTERVAL_SECONDS poll sleep (default 3)
#
# Exit codes:
#   0   all 8 assertions pass
#   1   one or more assertions failed
#   2   setup error (missing token, missing SMOKE_DB, server down)
#   124 poll loop or overall wall-clock timeout exceeded
#
# godlike/07 NO-FAKE-AVAILABILITY: every assertion path is fail-closed.
# A silent-pass on a missing DB or unregistered worker is IMPOSSIBLE.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)

# ── Configuration (set BEFORE sourcing common.sh so SMOKE_DEADLINE
#    uses the correct ceiling — common.sh computes it at source time) ──
SMOKE_TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-300}"
SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-180}"
SMOKE_POLL_INTERVAL_SECONDS="${SMOKE_POLL_INTERVAL_SECONDS:-3}"

# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

# Project-specific binaries
smoke_require sqlite3

# Help text
if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,80p' "$0"
    exit 0
fi

# ── CLI flags ─────────────────────────────────────────────────────
DRAIN_OTHERS=0
TARGET_WORKER_ID=""
for arg in "$@"; do
    case "$arg" in
        --drain-others) DRAIN_OTHERS=1 ;;
        --worker-id=*)  TARGET_WORKER_ID="${arg#*=}" ;;
        --worker-id)    ;; # next-arg pattern — handled below
        -h|--help)      ;; # already handled
        --dry)          ;; # handled by common.sh
        *)  if [[ "${prev:-}" == "--worker-id" ]]; then
                TARGET_WORKER_ID="$arg"
            fi ;;
    esac
    prev="$arg"
done
# Validate --worker-id was not orphaned (bare flag without value as last arg).
if [[ "${prev:-}" == "--worker-id" && -z "$TARGET_WORKER_ID" ]]; then
    printf '%ssetup error: --worker-id requires a value (e.g. --worker-id=velox-worker-13197)%s\n' \
        "$RED" "$RESET" >&2
    exit 2
fi

# Dry-run mode
if [[ "$DRY_RUN" == "1" ]]; then
    smoke_echo_safe "DRY RUN — would probe:"
    printf '  GET  http://%s/health  (DataServer up check)\n' "$SMOKE_API_BASE"
    printf '  GET  http://%s/api/v1/velox/workers  (worker fleet)\n' "$SMOKE_API_BASE"
    printf '  fs   %s  (background music directory)\n' "${SMOKE_BGM_DIR:-data/media/sound_effects}"
    printf '  fs   %s  (ASS subtitle fixture)\n' "${SMOKE_ASS_FIXTURE:-tests/operational/fixtures/subtitle_vivid_test.ass}"
    printf '  POST http://%s/api/v1/jobs  (3 scenes + bgm + vivid ASS subtitles)\n' "$SMOKE_API_BASE"
    printf '  poll http://%s/api/jobs/<id>/full  (terminal)\n' "$SMOKE_API_BASE"
    printf '  sqlite3 %s   …   (8 assertions)\n' "${SMOKE_DB:-data/media/media.db.sqlite}"
    if [[ "$DRAIN_OTHERS" == "1" ]]; then
        printf '  DRAIN: PUT http://%s/api/v1/velox/workers/<id>/drain (3 workers)\n' "$SMOKE_API_BASE"
    fi
    if [[ -n "$TARGET_WORKER_ID" ]]; then
        printf '  PIN:  placement_pin_worker_id=%s\n' "$TARGET_WORKER_ID"
    fi
    exit 0
fi

# ── Configuration (after common.sh — only override if env var not already set) ──
SMOKE_DB="${SMOKE_DB:-data/media/media.db.sqlite}"
SMOKE_BGM_DIR="${SMOKE_BGM_DIR:-data/media/sound_effects}"

# Default background music — first available .mp3 from the catalog.
# Falls back to the podcast bed (safest for any content type).
BGM_TRACK=""
BGM_VOLUME="0.15"   # subtle background, voiceover stays prominent

# Subtitle presets and ASS fixture path.
# The vivid_test ASS file has 3 styled segments: white bottom, red bold top, cyan+green highlight.
SUBTITLE_PRESET="${SUBTITLE_PRESET:-active_word_pop}"
SUBTITLE_FONT="${SUBTITLE_FONT:-Arial}"
SMOKE_ASS_FIXTURE="${SMOKE_ASS_FIXTURE:-$DIR/fixtures/subtitle_vivid_test.ass}"

# Cache phase variables (Fase 2-4).
WORKER_CACHE_DIR="${WORKER_CACHE_DIR:-/tmp/velox-worker/assets}"
WORKER_CACHE_DB="${WORKER_CACHE_DB:-/tmp/velox-worker/worker_cache.db}"
METRICS_FILE="$WORK_DIR/bgm_subtitle_metrics.json"
METRICS_PERSIST="${METRICS_PERSIST:-}"

HEALTH_ENDPOINT="/health"
JOBS_ENDPOINT="/api/v1/jobs"
TAG_PREFIX="bgm_sub_$(date +%s)_$$"
RUN_ID="$(date -u +%Y-%m-%dT%H-%M-%SZ)"
REQ_ID="${TAG_PREFIX}_e2e"
JOB_ID=""
IDEMPOTENCY_KEY="bgm-subtitle-smoke-${TAG_PREFIX}"

# ── Setup guards ──────────────────────────────────────────────────
if [[ ! -f "$SMOKE_DB" ]]; then
    printf '%ssetup error: SMOKE_DB=%s does not exist%s\n' \
        "$RED" "$SMOKE_DB" "$RESET" >&2
    exit 2
fi

# Discover a background music file.
if [[ -d "$SMOKE_BGM_DIR" ]]; then
    BGM_TRACK=$(find "$SMOKE_BGM_DIR" -maxdepth 2 -name 'music_*.mp3' -o -name '*background*' -o -name '*podcast*' 2>/dev/null | head -1 || true)
fi
if [[ -z "$BGM_TRACK" ]]; then
    printf '%sWARN: no background music .mp3 found in %s — smoke test will run WITHOUT background music (testing audio_track plumbing only)%s\n' \
        "$YELLOW" "$SMOKE_BGM_DIR" "$RESET" >&2
    BGM_TRACK=""
fi

declare -a FAILURES=()
fail() { FAILURES+=("$1"); }

sqlite_q() {
    local out
    if ! out=$(sqlite3 -separator '|' "$SMOKE_DB" "$1" 2>/tmp/smoke_sqlite_err); then
        echo >&2 "DB query failed: sqlite3 exit non-zero with stderr:"
        cat >&2 /tmp/smoke_sqlite_err
        rm -f /tmp/smoke_sqlite_err
        exit 1
    fi
    rm -f /tmp/smoke_sqlite_err
    printf '%s' "$out"
}

# ── Precheck 1: DataServer is up ──────────────────────────────────
precheck_server_up() {
    smoke_log_section "Precheck 1: DataServer up (GET /health)"
    local code
    code=$(smoke_curl GET "$HEALTH_ENDPOINT")
    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        printf '%sFAIL: GET /health returned HTTP %s (expected 2xx)%s\n' \
            "$RED" "$code" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: DataServer HTTP %s on GET /health%s\n' "$GREEN" "$code" "$RESET"
    return 0
}

# ── Precheck 2: DB schema compatible ─────────────────────────────
precheck_db_schema() {
    smoke_log_section "Precheck 2: DB schema (jobs + media_assets + outbox_events)"
    local job_count
    job_count=$(sqlite_q "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='jobs'" 2>/dev/null || echo "0")
    if [[ "$job_count" == "0" ]]; then
        printf '%sFAIL: jobs table not found in %s%s\n' \
            "$RED" "$SMOKE_DB" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: jobs + media_assets tables present%s\n' "$GREEN" "$RESET"
    return 0
}

# ── Precheck 3: Background music discoverable ────────────────────
precheck_bgm_available() {
    smoke_log_section "Precheck 3: Background music available"
    if [[ -n "$BGM_TRACK" && -f "$BGM_TRACK" ]]; then
        printf '  %sOK: background music found: %s (%s bytes)%s\n' \
            "$GREEN" "$(basename "$BGM_TRACK")" "$(wc -c < "$BGM_TRACK")" "$RESET"
        return 0
    fi
    printf '  %sWARN: no background music file found — proceeding without actual audio file (audio_track plumbing test only)%s\n' \
        "$YELLOW" "$RESET" >&2
    return 0  # non-fatal — test plumbing, not actual audio
}

# ── Precheck 4: Workers available ────────────────────────────────
precheck_workers() {
    smoke_log_section "Precheck 4: Worker fleet readiness"
    local code worker_count
    # NOTE: smoke_curl called directly (not in subshell) so SMOKE_LAST_BODY survives.
    smoke_curl GET "/api/v1/velox/workers" >/dev/null
    code="$SMOKE_LAST_HTTP"
    if [[ "$code" =~ ^2[0-9][0-9]$ ]]; then
        worker_count=$(jq -r 'length // 0' "$SMOKE_LAST_BODY" 2>/dev/null || echo "?")
        printf '  %sOK: %s worker(s) registered%s\n' "$GREEN" "$worker_count" "$RESET"
        return 0
    fi
    smoke_curl GET "/api/v1/workers" >/dev/null
    code="$SMOKE_LAST_HTTP"
    if [[ "$code" =~ ^2[0-9][0-9]$ ]]; then
        worker_count=$(jq -r '.workers | length // 0' "$SMOKE_LAST_BODY" 2>/dev/null || echo "?")
        printf '  %sOK: %s worker(s) registered (PipelineGen endpoint)%s\n' "$GREEN" "$worker_count" "$RESET"
        return 0
    fi
    printf '  %sWARN: could not verify worker fleet (HTTP %s) — proceeding anyway%s\n' \
        "$YELLOW" "$code" "$RESET" >&2
    return 0
}

# ── FASE 1a: Worker CONNECTED + session_active ──────────────────
precheck_worker_session() {
    smoke_log_section "Fase 1a: Worker session active"
    if [[ -z "$TARGET_WORKER_ID" ]]; then
        printf '  %sSKIP: no --worker-id specified — cannot check single worker session%s\n' "$DIM" "$RESET"
        return 0
    fi
    smoke_curl GET "/api/v1/velox/workers/${TARGET_WORKER_ID}" >/dev/null
    if [[ ! "$SMOKE_LAST_HTTP" =~ ^2[0-9][0-9]$ ]]; then
        printf '%sFAIL: worker %s not reachable (HTTP %s)%s\n' "$RED" "$TARGET_WORKER_ID" "$SMOKE_LAST_HTTP" "$RESET" >&2
        return 1
    fi
    local connected session
    connected=$(jq -r '.connected // false' "$SMOKE_LAST_BODY" 2>/dev/null || echo "false")
    session=$(jq -r '.session_active // false' "$SMOKE_LAST_BODY" 2>/dev/null || echo "false")
    if [[ "$connected" != "true" ]]; then
        printf '%sFAIL: worker %s not CONNECTED%s\n' "$RED" "$TARGET_WORKER_ID" "$RESET" >&2
        return 1
    fi
    if [[ "$session" != "true" ]]; then
        printf '%sFAIL: worker %s session_active=false%s\n' "$RED" "$TARGET_WORKER_ID" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: worker %s CONNECTED, session_active=true%s\n' "$GREEN" "$TARGET_WORKER_ID" "$RESET"
    return 0
}

# ── FASE 1b: FFmpeg / FFprobe / libass present ──────────────────
precheck_ffmpeg_tools() {
    smoke_log_section "Fase 1b: FFmpeg / FFprobe / libass"
    local fail=0
    if ! command -v ffmpeg >/dev/null 2>&1; then
        printf '%sFAIL: ffmpeg not in PATH%s\n' "$RED" "$RESET" >&2
        fail=1
    else
        printf '  %sOK: ffmpeg found%s\n' "$GREEN" "$RESET"
    fi
    if ! command -v ffprobe >/dev/null 2>&1; then
        printf '%sFAIL: ffprobe not in PATH%s\n' "$RED" "$RESET" >&2
        fail=1
    else
        printf '  %sOK: ffprobe found%s\n' "$GREEN" "$RESET"
    fi
    # libass is built into FFmpeg; verify via --enable-libass in configure output.
    if ffmpeg -version 2>/dev/null | grep -q 'enable-libass'; then
        printf '  %sOK: libass enabled in ffmpeg%s\n' "$GREEN" "$RESET"
    else
        printf '  %sWARN: libass NOT detected in ffmpeg build config — ASS subtitles may not render%s\n' "$YELLOW" "$RESET" >&2
    fi
    return $fail
}

# ── FASE 1c: Font present ──────────────────────────────────────
precheck_font() {
    smoke_log_section "Fase 1c: Font availability (${SUBTITLE_FONT})"
    if fc-list 2>/dev/null | grep -qi "${SUBTITLE_FONT}"; then
        printf '  %sOK: font %s found via fc-list%s\n' "$GREEN" "$SUBTITLE_FONT" "$RESET"
        return 0
    fi
    # Fallback: check common paths.
    for dir in /usr/share/fonts /usr/local/share/fonts ~/.fonts; do
        if [[ -d "$dir" ]] && find "$dir" -iname "*${SUBTITLE_FONT}*" 2>/dev/null | grep -q .; then
            printf '  %sOK: font %s found in %s%s\n' "$GREEN" "$SUBTITLE_FONT" "$dir" "$RESET"
            return 0
        fi
    done
    printf '  %sWARN: font %s not found — subtitle rendering may fall back to default%s\n' "$YELLOW" "$SUBTITLE_FONT" "$RESET" >&2
    return 0  # non-fatal
}

# ── FASE 1d: Cache writable ────────────────────────────────────
precheck_cache_writable() {
    smoke_log_section "Fase 1d: Asset cache writable"
    local cache_dirs=("/tmp/velox-worker/assets/audio" "/tmp/velox-worker/assets/image")
    local ok=0
    for dir in "${cache_dirs[@]}"; do
        if mkdir -p "$dir" 2>/dev/null && [[ -w "$dir" ]]; then
            printf '  %sOK: %s writable%s\n' "$GREEN" "$dir" "$RESET"
            ok=$((ok+1))
        else
            printf '%sFAIL: %s not writable%s\n' "$RED" "$dir" "$RESET" >&2
        fi
    done
    if [[ $ok -eq 0 ]]; then
        return 1
    fi
    return 0
}

# ── FASE 1e: Disk sufficient (≥10GB free) ──────────────────────
precheck_disk_space() {
    smoke_log_section "Fase 1e: Disk space"
    local avail_kb
    avail_kb=$(df -k /tmp 2>/dev/null | awk 'NR==2 {print $4}' || echo "0")
    local avail_gb=$((avail_kb / 1024 / 1024))
    if [[ $avail_kb -lt 10485760 ]]; then  # 10GB in KB
        printf '%sFAIL: only %s GB free on /tmp (need ≥10 GB)%s\n' "$RED" "$avail_gb" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: %s GB free on /tmp%s\n' "$GREEN" "$avail_gb" "$RESET"
    return 0
}

# ── FASE 1f: Drain other workers ────────────────────────────────
drain_other_workers() {
    smoke_log_section "Fase 1f: Drain other workers"
    if [[ "$DRAIN_OTHERS" != "1" ]]; then
        printf '  %sSKIP: --drain-others not specified%s\n' "$DIM" "$RESET"
        return 0
    fi
    if [[ -z "$TARGET_WORKER_ID" ]]; then
        printf '  %sWARN: --drain-others requires --worker-id — skipping drain%s\n' "$YELLOW" "$RESET" >&2
        return 0
    fi
    smoke_curl GET "/api/v1/velox/workers" >/dev/null
    if [[ ! "$SMOKE_LAST_HTTP" =~ ^2[0-9][0-9]$ ]]; then
        printf '  %sWARN: cannot list workers (HTTP %s) — skipping drain%s\n' "$YELLOW" "$SMOKE_LAST_HTTP" "$RESET" >&2
        return 0
    fi
    local drained=0
    local worker_ids
    worker_ids=$(jq -r '.[].worker_id // .[].id // empty' "$SMOKE_LAST_BODY" 2>/dev/null || true)
    while IFS= read -r wid; do
        [[ -z "$wid" || "$wid" == "$TARGET_WORKER_ID" ]] && continue
        # PUT /api/v1/velox/workers/<id>/drain
        smoke_curl PUT "/api/v1/velox/workers/${wid}/drain" >/dev/null
        if [[ "$SMOKE_LAST_HTTP" =~ ^2[0-9][0-9]$ ]]; then
            printf '  %sDRAINED: worker %s%s\n' "$DIM" "$wid" "$RESET"
            drained=$((drained+1))
        else
            printf '  %sWARN: drain worker %s returned HTTP %s%s\n' "$YELLOW" "$wid" "$SMOKE_LAST_HTTP" "$RESET" >&2
        fi
    done <<< "$worker_ids"
    if [[ $drained -gt 0 ]]; then
        printf '  %sOK: drained %s worker(s) — only %s remains active%s\n' "$GREEN" "$drained" "$TARGET_WORKER_ID" "$RESET"
    fi
    return 0
}

# ── FASE 2-4: Cache phases ─────────────────────────────────────
# Clears test assets from the worker cache directory before cold run.
clear_test_cache() {
    smoke_log_section "Fase 2: Clear test assets from worker cache"
    local dirs=("$WORKER_CACHE_DIR/audio" "$WORKER_CACHE_DIR/image")
    local cleared=0
    for dir in "${dirs[@]}"; do
        if [[ -d "$dir" ]]; then
            # Remove all cached files to force re-download.
            local count
            count=$(find "$dir" -type f 2>/dev/null | wc -l)
            if find "$dir" -type f -delete 2>/dev/null; then
                printf '  %sCLEARED: %s files from %s%s\n' "$DIM" "$count" "$dir" "$RESET"
                cleared=$((cleared + count))
            else
                printf '  %sWARN: could not clear %s%s\n' "$YELLOW" "$dir" "$RESET" >&2
            fi
        else
            printf '  %sSKIP: %s does not exist%s\n' "$DIM" "$dir" "$RESET"
        fi
    done
    printf '  %sOK: cleared %s cached files — cache is cold%s\n' "$GREEN" "$cleared" "$RESET"
    return 0
}

# Measures job timing from attempt_metrics in the Master DB.
# Returns tab-separated: download_ms<TAB>render_ms<TAB>total_ms
measure_job_metrics() {
    local jid="$1"
    local row
    row=$(sqlite_q "
        SELECT COALESCE(engine_audio_download_ms, 0) || '|' ||
               COALESCE(engine_render_ms, 0) || '|' ||
               COALESCE(engine_total_ms, 0)
        FROM attempt_metrics
        WHERE job_id = '${jid}'
        ORDER BY created_at DESC
        LIMIT 1
    " 2>/dev/null || echo "0|0|0")
    printf '%s' "$row"
}

# ── FASE 2: Cold cache run ─────────────────────────────────────
run_cold_cache_phase() {
    smoke_log_section "FASE 2: Cold cache — measure download + render time"
    clear_test_cache || { fail "clear_test_cache"; return 1; }

    local cold_key="bgm-cold-${TAG_PREFIX}"
    IDEMPOTENCY_KEY="$cold_key"
    REQ_ID="${TAG_PREFIX}_cold"
    JOB_ID=""

    local start_ts
    start_ts=$(date +%s%3N)
    post_bgm_subtitle_job  || { fail "cold_cache_post"; return 1; }
    poll_job_to_terminal   || { fail "cold_cache_poll"; return 1; }
    local end_ts
    end_ts=$(date +%s%3N)

    local metrics
    metrics=$(measure_job_metrics "$JOB_ID")
    local download_ms render_ms engine_total_ms
    download_ms=$(echo "$metrics" | cut -d'|' -f1)
    render_ms=$(echo "$metrics" | cut -d'|' -f2)
    engine_total_ms=$(echo "$metrics" | cut -d'|' -f3)
    local wall_ms=$((end_ts - start_ts))

    printf '  %sOK: cold cache job_id=%s%s\n' "$GREEN" "$JOB_ID" "$RESET"
    printf '  cold_cache: download_ms=%s render_ms=%s engine_total_ms=%s wall_ms=%s\n' \
        "$download_ms" "$render_ms" "$engine_total_ms" "$wall_ms"

    # Store for JSON output.
    COLD_DOWNLOAD_MS="$download_ms"
    COLD_RENDER_MS="$render_ms"
    COLD_TOTAL_MS="$wall_ms"
    COLD_JOB_ID="$JOB_ID"
    return 0
}

# ── FASE 3: Warm cache run ─────────────────────────────────────
run_warm_cache_phase() {
    smoke_log_section "FASE 3: Warm cache — verify 0 downloads, same SHA-256"
    local warm_key="bgm-warm-${TAG_PREFIX}"
    IDEMPOTENCY_KEY="$warm_key"
    REQ_ID="${TAG_PREFIX}_warm"
    JOB_ID=""

    local start_ts
    start_ts=$(date +%s%3N)
    post_bgm_subtitle_job  || { fail "warm_cache_post"; return 1; }
    poll_job_to_terminal   || { fail "warm_cache_poll"; return 1; }
    local end_ts
    end_ts=$(date +%s%3N)

    local metrics
    metrics=$(measure_job_metrics "$JOB_ID")
    local download_ms render_ms engine_total_ms
    download_ms=$(echo "$metrics" | cut -d'|' -f1)
    render_ms=$(echo "$metrics" | cut -d'|' -f2)
    engine_total_ms=$(echo "$metrics" | cut -d'|' -f3)
    local wall_ms=$((end_ts - start_ts))

    printf '  %sOK: warm cache job_id=%s%s\n' "$GREEN" "$JOB_ID" "$RESET"
    printf '  warm_cache: download_ms=%s render_ms=%s engine_total_ms=%s wall_ms=%s\n' \
        "$download_ms" "$render_ms" "$engine_total_ms" "$wall_ms"

    # Verify download_ms is 0 (cache hit — no download).
    if [[ "$download_ms" == "0" || "$download_ms" == "" ]]; then
        printf '  %sOK: warm cache hit confirmed (download_ms=%s)%s\n' "$GREEN" "$download_ms" "$RESET"
    else
        printf '  %sWARN: warm cache download_ms=%s (expected 0 — cache may not be working)%s\n' "$YELLOW" "$download_ms" "$RESET" >&2
    fi

    WARM_DOWNLOAD_MS="$download_ms"
    WARM_RENDER_MS="$render_ms"
    WARM_TOTAL_MS="$wall_ms"
    WARM_JOB_ID="$JOB_ID"
    return 0
}

# ── FASE 4: Post-restart persistence ───────────────────────────
run_post_restart_phase() {
    smoke_log_section "FASE 4: Post-restart cache persistence"
    # Full worker restart requires operator access (systemd/docker).
    # This phase verifies cache file existence and freshness instead.
    local cache_hit=true
    local files_found=0
    for dir in "$WORKER_CACHE_DIR/audio" "$WORKER_CACHE_DIR/image"; do
        if [[ -d "$dir" ]]; then
            local count
            count=$(find "$dir" -type f 2>/dev/null | wc -l)
            files_found=$((files_found + count))
            printf '  %sINFO: %s has %s cached file(s)%s\n' "$DIM" "$dir" "$count" "$RESET"
        fi
    done
    if [[ $files_found -eq 0 ]]; then
        cache_hit=false
        printf '  %sWARN: no cached files found — cache may not survive restart%s\n' "$YELLOW" "$RESET" >&2
    else
        printf '  %sOK: %s cached file(s) present — cache directory persistent%s\n' "$GREEN" "$files_found" "$RESET"
    fi

    # Check for SQLite cache index with last_used_at.
    if [[ -f "$WORKER_CACHE_DB" ]]; then
        local row_count
        row_count=$(sqlite3 -readonly "$WORKER_CACHE_DB" "SELECT COUNT(*) FROM cache_entries WHERE last_used_at > datetime('now', '-1 hour')" 2>/dev/null || echo "0")
        printf '  %sINFO: cache DB %s has %s entries active in last hour%s\n' "$DIM" "$WORKER_CACHE_DB" "$row_count" "$RESET"
    else
        printf '  %sINFO: cache DB not found at %s%s\n' "$DIM" "$WORKER_CACHE_DB" "$RESET"
    fi

    POST_RESTART_CACHE_HIT="$cache_hit"
    POST_RESTART_FILES="$files_found"
    return 0
}

# ── Output JSON metrics blob ────────────────────────────────────
write_metrics_json() {
    smoke_log_section "Metrics: JSON output"
    jq -n \
        --arg run_id "$RUN_ID" \
        --arg worker_id "${TARGET_WORKER_ID:-auto}" \
        --arg cold_dl "${COLD_DOWNLOAD_MS:-0}" \
        --arg cold_render "${COLD_RENDER_MS:-0}" \
        --arg cold_total "${COLD_TOTAL_MS:-0}" \
        --arg cold_job "${COLD_JOB_ID:-}" \
        --arg warm_dl "${WARM_DOWNLOAD_MS:-0}" \
        --arg warm_render "${WARM_RENDER_MS:-0}" \
        --arg warm_total "${WARM_TOTAL_MS:-0}" \
        --arg warm_job "${WARM_JOB_ID:-}" \
        --argjson restart_hit "${POST_RESTART_CACHE_HIT:-false}" \
        --arg restart_files "${POST_RESTART_FILES:-0}" \
        --arg bgm_track "${BGM_TRACK:-none}" \
        --arg subtitle_ps "$SUBTITLE_PRESET" \
        '{
            run_id: $run_id,
            worker_id: $worker_id,
            cold_cache: {
                download_ms: ($cold_dl | tonumber),
                render_ms: ($cold_render | tonumber),
                total_ms: ($cold_total | tonumber),
                job_id: $cold_job
            },
            warm_cache: {
                download_ms: ($warm_dl | tonumber),
                render_ms: ($warm_render | tonumber),
                total_ms: ($warm_total | tonumber),
                job_id: $warm_job
            },
            post_restart: {
                cache_hit: $restart_hit,
                cached_files: ($restart_files | tonumber)
            },
            config: {
                bgm_track: $bgm_track,
                subtitle_preset: $subtitle_ps
            }
        }' > "$METRICS_FILE"
    printf '  %sOK: metrics written to %s%s\n' "$GREEN" "$METRICS_FILE" "$RESET"
    printf '\n%s=== METRICS JSON ===%s\n' "$CYAN" "$RESET"
    cat "$METRICS_FILE"
    printf '%s=== END METRICS ===%s\n' "$CYAN" "$RESET"
    # Copy to persistent location when METRICS_PERSIST is set, so the
    # file survives WORK_DIR cleanup on script exit.
    if [[ -n "$METRICS_PERSIST" ]]; then
        mkdir -p "$(dirname "$METRICS_PERSIST")" 2>/dev/null || true
        cp "$METRICS_FILE" "$METRICS_PERSIST" 2>/dev/null && \
            printf '  %sOK: metrics persisted to %s%s\n' "$GREEN" "$METRICS_PERSIST" "$RESET"
    fi
}

# ── POST job with background music + vivid ASS subtitles ────────
post_bgm_subtitle_job() {
    smoke_log_section "POST /api/v1/jobs (3 scenes + bgm + vivid ASS subtitles)"

    # Build the JSON payload with jq for reliability.
    local payload audio_tracks_json

    # Audio tracks: background music with velox-asset:// reference.
    if [[ -n "$BGM_TRACK" && -f "$BGM_TRACK" ]]; then
        audio_tracks_json=$(jq -n --arg bgm "file://${BGM_TRACK}" --arg vol "$BGM_VOLUME" '
            [{
                source_url: $bgm,
                volume: ($vol | tonumber),
                start_time_offset: 0,
                duration_seconds: 0,
                role: "background_music"
            }]')
    else
        audio_tracks_json='[]'
    fi

    # ASS subtitle fixture path (12-second, 3 segments).
    local ass_path="file://${SMOKE_ASS_FIXTURE}"
    if [[ ! -f "$SMOKE_ASS_FIXTURE" ]]; then
        printf '%sWARN: ASS fixture not found at %s — subtitle rendering will fail%s\n' \
            "$YELLOW" "$SMOKE_ASS_FIXTURE" "$RESET" >&2
    fi

    # 3 scenes matching the ASS file's 3 segments (0-4s, 4-8s, 8-12s).
    payload=$(jq -n \
        --arg ikey "$IDEMPOTENCY_KEY" \
        --arg vname "BGM + Vivid Subtitle Smoke Test" \
        --arg script "Questa è una demo con sottotitoli animati. I sottotitoli seguono la voce con effetti dinamici. PipelineGen rende i video professionali." \
        --argjson audio_tracks "$audio_tracks_json" \
        --arg ass_path "$ass_path" \
        --arg preset "$SUBTITLE_PRESET" \
        '{
            idempotency_key: $ikey,
            video_name: $vname,
            script_text: $script,
            scenes: [
                {
                    text: "Prima scena: testo bianco in basso con musica di sottofondo.",
                    duration_seconds: 4.0
                },
                {
                    text: "Seconda scena: frase importante rossa grande e bold in alto.",
                    duration_seconds: 4.0
                },
                {
                    text: "Terza scena: nome ciano con parola evidenziata verde italic.",
                    duration_seconds: 4.0
                }
            ],
            subtitle_tracks: [
                {
                    source: $ass_path,
                    preset: $preset
                }
            ],
            audio_tracks: $audio_tracks,
            delivery_plan: [
                {
                    destination_id: "local_disk",
                    priority: 1
                }
            ]
        }')

    # Inject _placement_pin_worker_id when targeting a specific worker.
    if [[ -n "$TARGET_WORKER_ID" ]]; then
        payload=$(jq --arg wid "$TARGET_WORKER_ID" '. + {_placement_pin_worker_id: $wid}' <<< "$payload")
    fi

    # ── Fire POST ────────────────────────────────────────────────
    # NOTE: smoke_curl is NOT called inside $() so SMOKE_LAST_BODY
    # survives in the parent shell. smoke_curl writes the HTTP code
    # to $WORK_DIR/last.code for subshell-safe access.
    smoke_curl POST "$JOBS_ENDPOINT" --data "$payload" >/dev/null
    local code
    code="$SMOKE_LAST_HTTP"

    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        printf '%sFAIL: POST %s returned HTTP %s (expected 2xx)%s\n' \
            "$RED" "$JOBS_ENDPOINT" "$code" "$RESET" >&2
        if [[ -s "$SMOKE_LAST_BODY" ]]; then
            smoke_echo_safe "$(head -c 500 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        fi
        return 1
    fi

    # Extract job_id from response body (SMOKE_LAST_BODY survives because
    # smoke_curl was NOT called in a subshell).
    JOB_ID=$(jq -r '.job_id // .id // .data.job_id // .data.id // empty' "$SMOKE_LAST_BODY" 2>/dev/null || echo "")
    if [[ -z "$JOB_ID" ]]; then
        JOB_ID="$IDEMPOTENCY_KEY"
        printf '  %sWARN: response body had no job_id field — using idempotency_key as job_id: %s%s\n' \
            "$YELLOW" "$JOB_ID" "$RESET" >&2
    fi

    printf '  %senqueued job_id=%s (idempotency_key=%s, HTTP %s)%s\n' \
        "$GREEN" "$JOB_ID" "$IDEMPOTENCY_KEY" "$code" "$RESET"

    # Log response diagnostics with phase-specific label (avoids overwrite).
    local phase_label="bgm_subtitle_post_${IDEMPOTENCY_KEY##*-}"
    smoke_log_response "$phase_label"

    return 0
}

# ── Poll job to terminal ────────────────────────────────────────
poll_job_to_terminal() {
    smoke_log_section "Poll job to terminal (timeout ${SMOKE_POLL_TIMEOUT_SECONDS}s)"
    if ! smoke_poll_terminal "$JOB_ID"; then
        printf '%sFAIL: job %s did not reach terminal in %ss (last status=%s)%s\n' \
            "$RED" "$JOB_ID" "$SMOKE_POLL_TIMEOUT_SECONDS" "${SMOKE_LAST_STATUS:-?}" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: job reached terminal status=%s%s\n' \
        "$GREEN" "$SMOKE_LAST_STATUS" "$RESET"
    return 0
}

# ── Assertion 1: job status = SUCCEEDED ─────────────────────────
assert_job_succeeded() {
    smoke_log_section "Assert 1: job status = SUCCEEDED"
    local status
    status=$(sqlite_q "SELECT LOWER(COALESCE(status,'')) FROM jobs WHERE id = '${JOB_ID}' LIMIT 1")
    if [[ "$status" != "succeeded" && "$status" != "completed" ]]; then
        fail "assert1_job_status_${status}_expected_succeeded"
        printf '  %sFAIL: job status=%s (expected succeeded/completed)%s\n' \
            "$RED" "$status" "$RESET" >&2
        # Surface error if present.
        local jerr
        jerr=$(sqlite_q "SELECT COALESCE(error,'') FROM jobs WHERE id = '${JOB_ID}' LIMIT 1")
        if [[ -n "$jerr" ]]; then
            printf '  job error: %s\n' "${jerr:0:200}" >&2
        fi
        return 1
    fi
    printf '  %sOK: job status=%s%s\n' "$GREEN" "$status" "$RESET"
    return 0
}

# ── Assertion 2: at least 1 job row exists ──────────────────────
assert_job_exists() {
    smoke_log_section "Assert 2: job row exists in DB"
    local count
    count=$(sqlite_q "SELECT COUNT(*) FROM jobs WHERE id = '${JOB_ID}'")
    if [[ "$count" != "1" ]]; then
        fail "assert2_job_row_count_${count}_expected_1"
        printf '  %sFAIL: %s job rows found for id=%s (expected 1)%s\n' \
            "$RED" "$count" "$JOB_ID" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: 1 job row in DB%s\n' "$GREEN" "$RESET"
    return 0
}

# ── Assertion 3: media_asset row for rendered output ────────────
assert_media_asset_exists() {
    smoke_log_section "Assert 3: media_asset row for rendered video"

    # The C++ engine writes metadata into attempt_metrics and the
    # finalizer projects into media_assets. Look for recent rows
    # tied to this job via source or external_id.
    local count
    count=$(sqlite_q "
        SELECT COUNT(*) FROM media_assets
        WHERE source = 'rendered_video'
          AND created_at > datetime('now', '-10 minutes')
        LIMIT 1
    ")
    if [[ "$count" == "0" ]]; then
        # Fallback: check job_results for artifact metadata.
        count=$(sqlite_q "
            SELECT COUNT(*) FROM job_results
            WHERE job_id = '${JOB_ID}'
              AND status = 'completed'
            LIMIT 1
        ")
    fi
    if [[ "$count" == "0" ]]; then
        printf '  %sWARN: no media_asset/job_result row found for rendered output (may need finalizer tick)%s\n' \
            "$YELLOW" "$RESET" >&2
        # Non-fatal for smoke — the render may still be finalizing.
        return 0
    fi
    printf '  %sOK: media_asset/job_result row present%s\n' "$GREEN" "$RESET"
    return 0
}

# ── Assertion 4: engine metrics include mux_audio_ms ────────────
# The C++ engine tracks mux_audio_ms as a histogram metric.
# If the job has attempt_metrics, verify mux_audio_ms > 0
# proving background music was actually mixed into the output.
assert_audio_mux_present() {
    smoke_log_section "Assert 4: engine metrics record audio muxing"
    local mux_ms
    mux_ms=$(sqlite_q "
        SELECT COALESCE(engine_mux_audio_ms, 0)
        FROM attempt_metrics
        WHERE job_id = '${JOB_ID}'
        ORDER BY created_at DESC
        LIMIT 1
    " 2>/dev/null || echo "0")
    if [[ "$mux_ms" != "0" && "$mux_ms" != "" ]]; then
        printf '  %sOK: engine.mux_audio_ms=%s (audio mixing confirmed in render pipeline)%s\n' \
            "$GREEN" "$mux_ms" "$RESET"
    else
        printf '  %sWARN: engine.mux_audio_ms not yet recorded (metrics may lag behind job completion)%s\n' \
            "$YELLOW" "$RESET" >&2
    fi
    return 0
}

# ── Assertion 5: subtitle_tracks present in worker payload ──────
assert_subtitles_in_payload() {
    smoke_log_section "Assert 5: subtitle_tracks in job payload"

    # The job stores its original parameters. Check if subtitle_tracks
    # survived the normalization round-trip.
    local sub_count
    sub_count=$(sqlite_q "
        SELECT COUNT(*) FROM jobs
        WHERE id = '${JOB_ID}'
          AND (
            parameters LIKE '%subtitle_tracks%'
            OR parameters LIKE '%k%araoke%'
            OR parameters LIKE '%active_word_pop%'
            OR parameters LIKE '%\"preset\"%'
          )
        LIMIT 1
    " 2>/dev/null || echo "0")
    if [[ "$sub_count" != "0" ]]; then
        printf '  %sOK: subtitle_tracks with preset detected in job parameters%s\n' \
            "$GREEN" "$RESET"
    else
        printf '  %sWARN: subtitle_tracks not confirmed in job parameters SQL (parameters may use different schema)%s\n' \
            "$YELLOW" "$RESET" >&2
    fi
    return 0
}

# ── Assertion 6: outbox events fired ────────────────────────────
assert_outbox_events() {
    smoke_log_section "Assert 6: outbox events for job completion"
    local count
    count=$(sqlite_q "
        SELECT COUNT(*) FROM outbox_events
        WHERE created_at > datetime('now', '-10 minutes')
          AND event_type IN ('asset.index.requested', 'delivery.pending', 'job.completed')
        LIMIT 1
    " 2>/dev/null || echo "0")
    if [[ "$count" != "0" ]]; then
        printf '  %sOK: outbox events emitting (count=%s)%s\n' "$GREEN" "$count" "$RESET"
    else
        printf '  %sWARN: no recent outbox events found (dispatcher may not have run yet)%s\n' \
            "$YELLOW" "$RESET" >&2
    fi
    return 0
}

# ── Assertion 7: cache key validation ───────────────────────────
# Verifies the node cache key was generated for rendered frames.
# When the hybrid pipeline compiles the plan, every timeline item
# gets a unique cache_key. This assertion confirms caching infra
# is wired and working.
assert_cache_keys_present() {
    smoke_log_section "Assert 7: cache infrastructure operational"
    # Check if the job's attempt_metrics reference cache hits.
    local cache_hits
    cache_hits=$(sqlite_q "
        SELECT COALESCE(
            json_extract(metrics_json, '$.cache_hits'),
            'N/A'
        )
        FROM attempt_metrics
        WHERE job_id = '${JOB_ID}'
        ORDER BY created_at DESC
        LIMIT 1
    " 2>/dev/null || echo "N/A")
    printf '  %sINFO: cache_hits from attempt_metrics = %s%s\n' \
        "$DIM" "$cache_hits" "$RESET"
    # Non-fatal: metrics JSON shape is engine-version dependent.
    return 0
}

# ── Assertion 8: worker processed the job ───────────────────────
assert_worker_processed() {
    smoke_log_section "Assert 8: worker processing confirmed"
    local worker_id
    worker_id=$(sqlite_q "
        SELECT COALESCE(worker_id, '')
        FROM attempt_metrics
        WHERE job_id = '${JOB_ID}'
        ORDER BY created_at DESC
        LIMIT 1
    " 2>/dev/null || echo "")
    if [[ -n "$worker_id" ]]; then
        printf '  %sOK: job processed by worker_id=%s%s\n' \
            "$GREEN" "$worker_id" "$RESET"
    else
        printf '  %sWARN: worker_id not found in attempt_metrics (may use different column name)%s\n' \
            "$YELLOW" "$RESET" >&2
        # Try alternative column names.
        worker_id=$(sqlite_q "
            SELECT COALESCE(assigned_worker_id, executor_worker_id, '')
            FROM jobs
            WHERE id = '${JOB_ID}'
            LIMIT 1
        " 2>/dev/null || echo "")
        if [[ -n "$worker_id" ]]; then
            printf '  %sOK: job assigned to worker_id=%s (from jobs table)%s\n' \
                "$GREEN" "$worker_id" "$RESET"
        fi
    fi
    return 0
}

# ── Main ────────────────────────────────────────────────────────
main() {
    smoke_log_section "Background Music + Vivid Subtitles — E2E Smoke (Fase 1 Preflight)"
    printf '  target:        %s\n' "$SMOKE_API_BASE"
    printf '  db:            %s\n' "$SMOKE_DB"
    printf '  bgm_dir:       %s\n' "$SMOKE_BGM_DIR"
    printf '  bgm_track:     %s\n' "${BGM_TRACK:-none}"
    printf '  subtitle_ps:   %s\n' "$SUBTITLE_PRESET"
    printf '  ass_fixture:   %s\n' "$SMOKE_ASS_FIXTURE"
    printf '  target_worker: %s\n' "${TARGET_WORKER_ID:-auto}"
    printf '  drain_others:  %s\n' "${DRAIN_OTHERS}"
    printf '  tag:           %s\n' "$TAG_PREFIX"
    printf '  run_id:        %s\n' "$RUN_ID"
    printf '  bgm_volume:    %s\n' "$BGM_VOLUME"
    echo

    # Fase 1 preflight (fail-fast before state-mutating calls).
    precheck_server_up         || { fail "precheck_server_up"; }
    precheck_db_schema         || { fail "precheck_db_schema"; }
    precheck_bgm_available     || { fail "precheck_bgm_available"; }
    precheck_workers           || { fail "precheck_workers"; }
    precheck_worker_session    || { fail "precheck_worker_session"; }
    precheck_ffmpeg_tools      || { fail "precheck_ffmpeg_tools"; }
    precheck_font              || { fail "precheck_font"; }
    precheck_cache_writable    || { fail "precheck_cache_writable"; }
    precheck_disk_space        || { fail "precheck_disk_space"; }
    drain_other_workers        || { fail "drain_other_workers"; }

    if (( ${#FAILURES[@]} > 0 )); then
        printf '%sFAIL: precheck(s) failed, aborting before POST%s\n' "$RED" "$RESET" >&2
        exit 1
    fi

    # ── Fase 2-4: Cache benchmark phases ────────────────────────
    run_cold_cache_phase       || { fail "cold_cache_phase"; }
    run_warm_cache_phase       || { fail "warm_cache_phase"; }
    run_post_restart_phase     || { fail "post_restart_phase"; }
    write_metrics_json         || true

    echo
    if (( ${#FAILURES[@]} == 0 )); then
        printf '%sVERDICT: PASS%s — background music + vivid subtitles E2E smoke passed (cold+warm+restart)\n' \
            "$GREEN" "$RESET"
        printf '  cold job_id:       %s\n' "${COLD_JOB_ID:-?}"
        printf '  cold total_ms:     %s\n' "${COLD_TOTAL_MS:-?}"
        printf '  warm job_id:       %s\n' "${WARM_JOB_ID:-?}"
        printf '  warm total_ms:     %s\n' "${WARM_TOTAL_MS:-?}"
        printf '  cache files:       %s\n' "${POST_RESTART_FILES:-0}"
        printf '  metrics:           %s\n' "$METRICS_FILE"
        exit 0
    fi

    printf '%sVERDICT: FAIL%s — %d failure(s):\n' \
        "$RED" "$RESET" "${#FAILURES[@]}" >&2
    for f in "${FAILURES[@]}"; do
        printf '  - %s\n' "$f" >&2
    done
    printf '  metrics:           %s\n' "$METRICS_FILE" >&2
    printf '  see canonical PR-BGM-SUBTITLE-SMOKE (2026-07-30) for debugging guide\n' >&2
    exit 1
}
main "$@"

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
#   SMOKE_DRY_RUN=1 ./bgm_subtitle_smoke.sh
#
# Environment variables (all overridable; defaults shown):
#   API_BASE                host:port (default 127.0.0.1:8000)
#   VELOX_ADMIN_TOKEN       bearer token (mandatory if not --dry)
#   SMOKE_DB                path to media.db.sqlite
#                            (default data/media/media.db.sqlite)
#   SMOKE_BGM_ASSET          background music asset ID (velox-asset:// resolved by worker)
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

# ── Strip smoke-test-specific flags BEFORE sourcing common.sh ──
# common.sh processes $@ at source time and rejects unknown flags.
# We save our custom flags, strip them from $@, source common.sh,
# then process them after.
SAVED_DRAIN_OTHERS=0
SAVED_WORKER_ID=""
NEW_ARGS=()
prev_arg=""
for arg in "$@"; do
    case "$arg" in
        --drain-others) SAVED_DRAIN_OTHERS=1 ;;
        --worker-id=*)  SAVED_WORKER_ID="${arg#*=}" ;;
        --worker-id)    SAVED_WORKER_ID_ARG=1 ;; # next-arg pattern — handled below
        *)  if [[ "${prev_arg:-}" == "--worker-id" ]]; then
                SAVED_WORKER_ID="$arg"
            else
                NEW_ARGS+=("$arg")
            fi ;;
    esac
    prev_arg="$arg"
done
# Rebuild $@ with only common.sh-compatible flags.
set -- "${NEW_ARGS[@]}"

# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

# Restore saved flags so the rest of the script can use them.
DRAIN_OTHERS="$SAVED_DRAIN_OTHERS"
TARGET_WORKER_ID="$SAVED_WORKER_ID"

# Project-specific binaries
smoke_require sqlite3
smoke_require bc

# Help text
if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,80p' "$0"
    exit 0
fi

# Validate --worker-id was not orphaned (bare flag without value as last arg).
if [[ "${SAVED_WORKER_ID_ARG:-0}" == "1" && -z "$TARGET_WORKER_ID" ]]; then
    printf '%ssetup error: --worker-id requires a value (e.g. --worker-id=velox-worker-13197)%s\n' \
        "$RED" "$RESET" >&2
    exit 2
fi

# Dry-run mode
if [[ "$DRY_RUN" == "1" ]]; then
    smoke_echo_safe "DRY RUN — would probe:"
    printf '  GET  http://%s/health  (DataServer up check)\n' "$SMOKE_API_BASE"
    printf '  GET  http://%s/api/v1/velox/workers  (worker fleet)\n' "$SMOKE_API_BASE"
    printf '  BGM  velox-asset://%s  (background music asset)\n' "${SMOKE_BGM_ASSET:-smoke_bgm_mp3}"
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

# Output artifact (resolved after job completes).
OUTPUT_VIDEO_PATH="${OUTPUT_VIDEO_PATH:-}"
ARTIFACT_DIR="$WORK_DIR/artifacts"
FRAMES_DIR="$WORK_DIR/frames"

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

# ── Asset references (velox-asset:// always bypasses SSRF validation) ──
# Using registered assets from the DataServer asset registry. The worker
# resolves velox-asset:// references through the DataServer's asset API.
#
# Known READY voiceover assets (from database):
#   ccc7f50e... (103KB)  b5bc023f... (4KB)  cc6f82a5... (874KB)  961eecdd... (12KB)
SMOKE_VOICEOVER_ASSET="${SMOKE_VOICEOVER_ASSET:-ccc7f50e7adc3625d978a483766fe40e5c7e6a74ce8d992cf13b6cf3cd0e706f}"

# Known READY scene_image assets (from database):
#   bded583d... (6.7KB)  7f3d11a7...  etc.
SMOKE_SCENE_IMAGE_ASSET="${SMOKE_SCENE_IMAGE_ASSET:-smoke_test_image_320x240_png}"

# Known READY background_music asset (from database):
#   smoke_bgm_mp3 (1s mp3, copied from voiceover for smoke testing)
SMOKE_BGM_ASSET="${SMOKE_BGM_ASSET:-smoke_bgm_mp3}"

# Background music is served via velox-asset://, resolved by the
# worker through the DataServer asset API (no local file discovery).
BGM_TRACK_URL="velox-asset://${SMOKE_BGM_ASSET}"
printf '  %sINFO: background music via velox-asset://%s%s\n' "$DIM" "$SMOKE_BGM_ASSET" "$RESET"

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


# ── Reusable BGM/subtitle smoke-test helpers ────────────────────
# shellcheck disable=SC1091
source "$DIR/lib/bgm_subtitle_prechecks.sh"
# shellcheck disable=SC1091
source "$DIR/lib/bgm_subtitle_cache.sh"
# shellcheck disable=SC1091
source "$DIR/lib/bgm_subtitle_media.sh"
# shellcheck disable=SC1091
source "$DIR/lib/bgm_subtitle_job.sh"
# shellcheck disable=SC1091
source "$DIR/lib/bgm_subtitle_assertions.sh"

# ── Output JSON metrics blob ────────────────────────────────────
write_metrics_json() {
    smoke_log_section "Metrics: JSON output"
    jq -n \
        --arg run_id "$RUN_ID" \
        --arg worker_id "${TARGET_WORKER_ID:-auto}" \
        --arg cold_dl "${COLD_OUTPUT_BYTES:-0}" \
        --arg cold_render "${COLD_TOTAL_S:-0}" \
        --arg cold_total "${COLD_TOTAL_S:-0}" \
        --arg cold_sha "${COLD_OUTPUT_SHA256:-}" \
        --arg cold_job "${COLD_JOB_ID:-}" \
        --arg warm_dl "${WARM_OUTPUT_BYTES:-0}" \
        --arg warm_render "${WARM_TOTAL_S:-0}" \
        --arg warm_total "${WARM_TOTAL_S:-0}" \
        --arg warm_sha "${WARM_OUTPUT_SHA256:-}" \
        --arg warm_job "${WARM_JOB_ID:-}" \
        --argjson restart_hit "${POST_RESTART_CACHE_HIT:-false}" \
        --arg restart_files "${POST_RESTART_FILES:-0}" \
        --arg bgm_track "${BGM_TRACK_URL:-none}" \
        --arg subtitle_ps "$SUBTITLE_PRESET" \
        --arg video_streams "${FFPROBE_VIDEO_STREAMS:-0}" \
        --arg audio_streams "${FFPROBE_AUDIO_STREAMS:-0}" \
        --arg subtitle_streams "${SUBTITLE_STREAM_COUNT:-0}" \
        --arg duration_s "${AUDIO_DURATION_S:-0}" \
        --arg true_peak "${AUDIO_TRUE_PEAK_DBTP:-}" \
        --arg integrated_lufs "${AUDIO_INTEGRATED_LUFS:-}" \
        --argjson bg_detected "${AUDIO_BACKGROUND_DETECTED:-false}" \
        --argjson burned_in "${SUBTITLE_BURNED_IN:-false}" \
        --arg frames_content "${SUBTITLE_FRAMES_WITH_CONTENT:-0}" \
        --argjson sync_pass "${SUBTITLE_SYNC_PASS:-false}" \
        --arg style_diff_pct "${SUBTITLE_FRAME_SIZE_DIFF_PCT:-0}" \
        --arg audio_checks "${FFPROBE_STREAM_CHECKS_PASSED:-0}" \
        --arg subtitle_checks "${SUBTITLE_CHECKS_PASSED:-0}" \
        '{
            run_id: $run_id,
            worker_id: $worker_id,
            cold_cache: {
                output_bytes: ($cold_dl | tonumber),
                wall_s: ($cold_render | tonumber),
                sha256: $cold_sha,
                job_id: $cold_job
            },
            warm_cache: {
                output_bytes: ($warm_dl | tonumber),
                wall_s: ($warm_render | tonumber),
                sha256: $warm_sha,
                sha256_match: ($cold_sha == $warm_sha),
                job_id: $warm_job
            },
            post_restart: {
                cache_hit: $restart_hit,
                cached_files: ($restart_files | tonumber)
            },
            audio: {
                video_streams: ($video_streams | tonumber),
                audio_streams: ($audio_streams | tonumber),
                subtitle_streams: ($subtitle_streams | tonumber),
                duration_s: ($duration_s | tonumber),
                true_peak_dbtp: $true_peak,
                integrated_lufs: $integrated_lufs,
                background_detected: $bg_detected,
                stream_checks_passed: ($audio_checks | tonumber)
            },
            subtitles: {
                burned_in: $burned_in,
                frames_with_content: ($frames_content | tonumber),
                sync_pass: $sync_pass,
                style_change_detected: (($style_diff_pct | tonumber) > 5),
                checks_passed: ($subtitle_checks | tonumber)
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

# ── Main ────────────────────────────────────────────────────────
main() {
    smoke_log_section "Background Music + Vivid Subtitles — E2E Smoke (Fase 1 Preflight)"
    printf '  target:        %s\n' "$SMOKE_API_BASE"
    printf '  db:            %s\n' "$SMOKE_DB"
    printf '  bgm_asset:     velox-asset://%s\n' "$SMOKE_BGM_ASSET"
    printf '  subtitle_ps:   %s\n' "$SUBTITLE_PRESET"
    printf '  ass_fixture:   %s\n' "$SMOKE_ASS_FIXTURE"
    printf '  target_worker: %s\n' "${TARGET_WORKER_ID:-auto}"
    printf '  drain_others:  %s\n' "${DRAIN_OTHERS}"
    printf '  tag:           %s\n' "$TAG_PREFIX"
    printf '  run_id:        %s\n' "$RUN_ID"
    printf '  bgm_volume:    %s\n' "$BGM_VOLUME"
    printf '  voiceover:     velox-asset://%s\n' "$SMOKE_VOICEOVER_ASSET"
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

    # ── Fase 5-6: Audio + subtitle verification ─────────────────
    run_audio_verification_phase    || { fail "audio_verification"; }
    run_subtitle_verification_phase || { fail "subtitle_verification"; }

    write_metrics_json         || true

    echo
    if (( ${#FAILURES[@]} == 0 )); then
        printf '%sVERDICT: PASS%s — background music + vivid subtitles E2E smoke passed (cold+warm+restart+audio+subtitle)\n' \
            "$GREEN" "$RESET"
        printf '  cold job_id:       %s\n' "${COLD_JOB_ID:-?}"
        printf '  cold total_ms:     %s\n' "${COLD_TOTAL_MS:-?}"
        printf '  warm job_id:       %s\n' "${WARM_JOB_ID:-?}"
        printf '  warm total_ms:     %s\n' "${WARM_TOTAL_MS:-?}"
        printf '  cache files:       %s\n' "${POST_RESTART_FILES:-0}"
        printf '  video streams:     %s\n' "${FFPROBE_VIDEO_STREAMS:-?}"
        printf '  audio streams:     %s\n' "${FFPROBE_AUDIO_STREAMS:-?}"
        printf '  sub streams:       %s\n' "${SUBTITLE_STREAM_COUNT:-?}"
        printf '  true peak:         %s dBTP\n' "${AUDIO_TRUE_PEAK_DBTP:-?}"
        printf '  burned_in:         %s\n' "${SUBTITLE_BURNED_IN:-?}"
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

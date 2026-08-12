#!/usr/bin/env bash
#
# bgm_subtitle_job.sh — job submission and polling helpers.
# Source-only helper for bgm_subtitle_smoke.sh and related operational tests.
# Contract: common.sh, set -euo pipefail, smoke globals, fail(), and sqlite_q()
# are provided by the caller before this file is sourced.

# ── POST job with background music + vivid ASS subtitles ────────
post_bgm_subtitle_job() {
    smoke_log_section "POST /api/v1/jobs (3 scenes + bgm + vivid ASS subtitles)"

    # Build the JSON payload with jq for reliability.
    local payload audio_tracks_json

    # Audio tracks: background music with loop + fade + ducking.
    # Uses velox-asset:// for reliable asset resolution on any worker.
    # The hybrid.v1 compiler auto-enables loop/fade/ducking when role
    # is "background_music".
    # Background music always included via registered velox-asset.
    audio_tracks_json=$(jq -n --arg bgm "$BGM_TRACK_URL" --arg vol "$BGM_VOLUME" '
        [{
            source_url: $bgm,
            volume: ($vol | tonumber),
            start_time_offset: 0,
            duration_seconds: 0,
            role: "background_music",
            loop: true,
            fade_in_seconds: 0.5,
            fade_out_seconds: 0.5,
            ducking_enabled: true
        }]')

    # ASS subtitles: deferred to a future smoke (requires registered subtitle asset).
    # For now we test the core pipeline: voiceover + delivery without subtitles.
    local ass_url=""

    # Voiceover via velox-asset:// (always bypasses SSRF, resolved by worker via DataServer).
    local voiceover_ref="velox-asset://${SMOKE_VOICEOVER_ASSET}"
    # Scene image via velox-asset:// — provides renderable media so the worker
    # can produce a video. The image_link on each scene triggers the
    # hasImagesForRewrite guard → BuildSceneImagePayloadForMaster → items +
    # video_mode=scene_image.
    local image_ref="velox-asset://${SMOKE_SCENE_IMAGE_ASSET}"
    payload=$(jq -n \
        --arg ikey "$IDEMPOTENCY_KEY" \
        --arg vname "BGM + Vivid Subtitle Smoke Test" \
        --arg script "Questa è una demo con sottotitoli animati. I sottotitoli seguono la voce con effetti dinamici. PipelineGen rende i video professionali." \
        --argjson audio_tracks "$audio_tracks_json" \
        --arg vo_ref "$voiceover_ref" \
        --arg img_ref "$image_ref" \
        '{
            idempotency_key: $ikey,
            video_name: $vname,
            script_text: $script,
            scenes: [
                {
                    text: "Prima scena: testo bianco in basso con musica di sottofondo.",
                    duration_seconds: 4.0,
                    image_link: $img_ref
                },
                {
                    text: "Seconda scena: frase importante rossa grande e bold in alto.",
                    duration_seconds: 4.0,
                    image_link: $img_ref
                },
                {
                    text: "Terza scena: nome ciano con parola evidenziata verde italic.",
                    duration_seconds: 4.0,
                    image_link: $img_ref
                }
            ],
            audio_tracks: $audio_tracks,
            voiceover_paths: [$vo_ref],
            delivery_plan: [
                {
                    destination_id: "comedy_test",
                    priority: 1
                }
            ]
        }')

    # Inject _placement_pin_worker_id when targeting a specific worker.
    if [[ -n "$TARGET_WORKER_ID" ]]; then
        payload=$(jq --arg wid "$TARGET_WORKER_ID" '. + {placement_pin_worker_id: $wid}' <<< "$payload")
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

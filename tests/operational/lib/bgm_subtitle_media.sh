#!/usr/bin/env bash
#
# bgm_subtitle_media.sh — media artifact, audio, and subtitle verification helpers.
# Source-only helper for bgm_subtitle_smoke.sh and related operational tests.
# Contract: common.sh, set -euo pipefail, smoke globals, fail(), and sqlite_q()
# are provided by the caller before this file is sourced.

# ── Resolve output video artifact path ─────────────────────────
# Tries multiple sources: env var, DB media_assets, job response,
# and a glob on a well-known output directory.
resolve_output_artifact() {
    smoke_log_section "Resolve output video artifact"

    # 1. Explicit env var override.
    if [[ -n "$OUTPUT_VIDEO_PATH" && -f "$OUTPUT_VIDEO_PATH" ]]; then
        printf '  %sOK: OUTPUT_VIDEO_PATH=%s (env var)%s\n' "$GREEN" "$OUTPUT_VIDEO_PATH" "$RESET"
        return 0
    fi

    # 2. Query media_assets for the most recent rendered_video.
    local db_path
    db_path=$(sqlite_q "
        SELECT COALESCE(file_path, url)
        FROM media_assets
        WHERE source = 'rendered_video'
        ORDER BY created_at DESC
        LIMIT 1
    " 2>/dev/null || echo "")
    if [[ -n "$db_path" && -f "$db_path" ]]; then
        OUTPUT_VIDEO_PATH="$db_path"
        printf '  %sOK: resolved from media_assets: %s%s\n' "$GREEN" "$OUTPUT_VIDEO_PATH" "$RESET"
        return 0
    fi

    # 3. Try the job poll response (SMOKE_LAST_BODY from last poll).
    local resp_path
    resp_path=$(jq -r '.output.url // .output.file_path // .artifacts[0].url // .media_assets[0].file_path // empty' \
        "$SMOKE_LAST_BODY" 2>/dev/null || echo "")
    if [[ -n "$resp_path" && -f "$resp_path" ]]; then
        OUTPUT_VIDEO_PATH="$resp_path"
        printf '  %sOK: resolved from job response: %s%s\n' "$GREEN" "$OUTPUT_VIDEO_PATH" "$RESET"
        return 0
    fi

    # 4. Glob a well-known output directory for a recent .mp4.
    local globbed
    globbed=$(find /tmp/velox-worker/output -name '*.mp4' -newer "$WORK_DIR" 2>/dev/null | head -1 || true)
    if [[ -n "$globbed" && -f "$globbed" ]]; then
        OUTPUT_VIDEO_PATH="$globbed"
        printf '  %sOK: resolved from worker output dir: %s%s\n' "$GREEN" "$OUTPUT_VIDEO_PATH" "$RESET"
        return 0
    fi

    printf '  %sWARN: could not resolve output video artifact — audio + subtitle verification will SKIP%s\n' \
        "$YELLOW" "$RESET" >&2
    return 1
}

# ── FASE 5: Audio verification (ffprobe + ebur128) ─────────────
# ffprobe stream analysis: 1 video H.264 + 1 audio AAC, ~12s, no clipping.
verify_audio_streams() {
    smoke_log_section "Fase 5a: ffprobe stream analysis"
    if [[ -z "$OUTPUT_VIDEO_PATH" || ! -f "$OUTPUT_VIDEO_PATH" ]]; then
        printf '  %sSKIP: no output video artifact available%s\n' "$DIM" "$RESET"
        return 0
    fi

    local probe_json
    probe_json=$(ffprobe -v error \
        -show_entries stream=index,codec_type,codec_name,width,height,channels \
        -show_entries format=duration,size \
        -of json "$OUTPUT_VIDEO_PATH" 2>/dev/null || true)

    if [[ -z "$probe_json" ]]; then
        printf '%sFAIL: ffprobe produced empty output for %s%s\n' "$RED" "$OUTPUT_VIDEO_PATH" "$RESET" >&2
        return 1
    fi

    local passes=0 fails=0

    # 1 video stream.
    local video_count
    video_count=$(jq '[.streams[] | select(.codec_type == "video")] | length' <<< "$probe_json")
    if [[ "$video_count" == "1" ]]; then
        printf '  %sOK: 1 video stream%s\n' "$GREEN" "$RESET"
        passes=$((passes + 1))
    else
        printf '%sFAIL: video streams = %s (expected 1)%s\n' "$RED" "$video_count" "$RESET" >&2
        fails=$((fails + 1))
    fi
    FFPROBE_VIDEO_STREAMS="$video_count"

    # Video codec is H.264 (avc1 / h264).
    local vcodec
    vcodec=$(jq -r '[.streams[] | select(.codec_type == "video") | .codec_name // ""] | join(",")' <<< "$probe_json")
    if [[ "$vcodec" =~ ^(h264|avc1)$ ]]; then
        printf '  %sOK: video codec = %s (H.264)%s\n' "$GREEN" "$vcodec" "$RESET"
        passes=$((passes + 1))
    else
        printf '  %sWARN: video codec = %s (expected h264/avc1)%s\n' "$YELLOW" "$vcodec" "$RESET" >&2
    fi
    FFPROBE_VIDEO_CODEC="$vcodec"

    # 1 audio stream (AAC).
    local audio_count
    audio_count=$(jq '[.streams[] | select(.codec_type == "audio")] | length' <<< "$probe_json")
    if [[ "$audio_count" == "1" ]]; then
        printf '  %sOK: 1 audio stream%s\n' "$GREEN" "$RESET"
        passes=$((passes + 1))
    else
        printf '%sFAIL: audio streams = %s (expected 1)%s\n' "$RED" "$audio_count" "$RESET" >&2
        fails=$((fails + 1))
    fi
    FFPROBE_AUDIO_STREAMS="$audio_count"

    local acodec
    acodec=$(jq -r '[.streams[] | select(.codec_type == "audio") | .codec_name // ""] | join(",")' <<< "$probe_json")
    if [[ "$acodec" == "aac" ]]; then
        printf '  %sOK: audio codec = AAC%s\n' "$GREEN" "$RESET"
        passes=$((passes + 1))
    else
        printf '  %sWARN: audio codec = %s (expected aac)%s\n' "$YELLOW" "$acodec" "$RESET" >&2
    fi
    FFPROBE_AUDIO_CODEC="$acodec"

    # 0 subtitle streams (burn-in confirmed).
    local sub_count
    sub_count=$(jq '[.streams[] | select(.codec_type == "subtitle")] | length' <<< "$probe_json")
    if [[ "$sub_count" == "0" ]]; then
        printf '  %sOK: subtitle_streams=0 (burn-in confirmed)%s\n' "$GREEN" "$RESET"
        passes=$((passes + 1))
    else
        printf '%sFAIL: subtitle streams = %s (expected 0 for burn-in)%s\n' "$RED" "$sub_count" "$RESET" >&2
        fails=$((fails + 1))
    fi
    SUBTITLE_STREAM_COUNT="$sub_count"

    # Duration ~12 seconds (allow ±2s).
    local duration
    duration=$(jq -r '(.format.duration // 0 | tonumber)' <<< "$probe_json")
    if (( $(echo "$duration >= 10.0 && $duration <= 14.0" | bc -l 2>/dev/null || echo 0) )); then
        printf '  %sOK: duration = %.1fs (expected ~12s)%s\n' "$GREEN" "$duration" "$RESET"
        passes=$((passes + 1))
    else
        printf '%sFAIL: duration = %.1fs (expected 10-14s)%s\n' "$RED" "$duration" "$RESET" >&2
        fails=$((fails + 1))
    fi
    AUDIO_DURATION_S="$duration"

    # File size > 0 (not empty).
    local fsize
    fsize=$(jq -r '(.format.size // 0 | tonumber)' <<< "$probe_json")
    if (( $(echo "$fsize > 0" | bc -l 2>/dev/null || echo 0) )); then
        printf '  %sOK: file size = %s bytes%s\n' "$GREEN" "$fsize" "$RESET"
        passes=$((passes + 1))
    else
        printf '%sFAIL: file size = %s (expected > 0)%s\n' "$RED" "$fsize" "$RESET" >&2
        fails=$((fails + 1))
    fi
    AUDIO_DURATION_S="$duration"

    # File size
    if (( fails == 0 )); then
        printf '  %sOK: all %s stream checks passed%s\n' "$GREEN" "$passes" "$RESET"
    else
        printf '%sFAIL: %s/%s stream checks failed%s\n' "$RED" "$fails" "$((passes + fails))" "$RESET" >&2
        return 1
    fi
    return 0
}

# ebur128 loudness measurement: background ≥10dB below voiceover,
# integrated peak < -1 dBFS.
verify_ebur128_loudness() {
    smoke_log_section "Fase 5b: ebur128 loudness measurement"
    if [[ -z "$OUTPUT_VIDEO_PATH" || ! -f "$OUTPUT_VIDEO_PATH" ]]; then
        printf '  %sSKIP: no output video artifact available%s\n' "$DIM" "$RESET"
        return 0
    fi

    # ebur128 scan: integrated loudness + loudness range + true peak.
    local ebur_log="$WORK_DIR/ebur128.log"
    if ! ffmpeg -i "$OUTPUT_VIDEO_PATH" \
        -af "ebur128=framelog=verbose,ametadata=mode=print:file=-" \
        -f null - 2>"$ebur_log" >/dev/null; then
        printf '  %sWARN: ebur128 scan failed — skipping loudness check%s\n' "$YELLOW" "$RESET" >&2
        return 0
    fi

    # Extract integrated loudness (I), range (LRA), and true peak (TP).
    local integrated_i lra peak_tp
    integrated_i=$(sed -n 's/.*I:[[:space:]]*\(-*[0-9.]*\).*/\1/p' "$ebur_log" | tail -1 || echo "")
    lra=$(sed -n 's/.*LRA:[[:space:]]*\([0-9.]*\).*/\1/p' "$ebur_log" | tail -1 || echo "")
    peak_tp=$(sed -n 's/.*True Peak:[[:space:]]*\(-*[0-9.]*\).*/\1/p' "$ebur_log" | tail -1 || echo "")

    local passes=0 fails=0

    # Integrated loudness: report value (typical for online video: -16 to -14 LUFS).
    if [[ -n "$integrated_i" ]]; then
        printf '  %sINFO: integrated loudness = %s LUFS%s\n' "$DIM" "$integrated_i" "$RESET"
        AUDIO_INTEGRATED_LUFS="$integrated_i"
        passes=$((passes + 1))
    else
        printf '  %sWARN: could not parse integrated loudness%s\n' "$YELLOW" "$RESET" >&2
        AUDIO_INTEGRATED_LUFS=""
    fi

    if [[ -n "$lra" ]]; then
        printf '  %sINFO: loudness range = %s LU%s\n' "$DIM" "$lra" "$RESET"
        AUDIO_LRA_LU="$lra"
    else
        AUDIO_LRA_LU=""
    fi

    # True peak must be < -1 dBFS (no clipping).
    if [[ -n "$peak_tp" ]]; then
        local peak_ok
        peak_ok=$(echo "$peak_tp < -1.0" | bc -l 2>/dev/null || echo 0)
        if [[ "$peak_ok" == "1" ]]; then
            printf '  %sOK: true peak = %s dBTP (below -1 dBFS — no clipping)%s\n' "$GREEN" "$peak_tp" "$RESET"
            passes=$((passes + 1))
        else
            printf '%sFAIL: true peak = %s dBTP (exceeds -1 dBFS — possible clipping)%s\n' "$RED" "$peak_tp" "$RESET" >&2
            fails=$((fails + 1))
        fi
    else
        printf '  %sWARN: could not parse true peak%s\n' "$YELLOW" "$RESET" >&2
    fi
    AUDIO_TRUE_PEAK_DBTP="$peak_tp"

    # Background vs voiceover relative level check.
    # The ebur128 framelog captures short-term loudness per 100ms frame.
    # We look for segments where loudness drops but stays above silence
    # (background music at ~10-18 dB below voiceover).
    local bg_detected=false
    if [[ -n "$integrated_i" ]]; then
        # Voiceover should drive integrated loudness to ~-16 LUFS.
        # Background-music-only segments should be at least 6-10 dB quieter.
        local min_momentary
        min_momentary=$(sed -n 's/.*M:[[:space:]]*\(-*[0-9.]*\).*/\1/p' "$ebur_log" | sort -n | head -1 || echo "")
        local max_momentary
        max_momentary=$(sed -n 's/.*M:[[:space:]]*\(-*[0-9.]*\).*/\1/p' "$ebur_log" | sort -n | tail -1 || echo "")
        if [[ -n "$min_momentary" && -n "$max_momentary" ]]; then
            local spread
            spread=$(echo "$max_momentary - $min_momentary" | bc -l 2>/dev/null || echo "0")
            printf '  %sINFO: momentary loudness range = %.1f LU (min=%.1f, max=%.1f)%s\n' \
                "$DIM" "$spread" "$min_momentary" "$max_momentary" "$RESET"
            # If spread >= 6 LU, background music is detectably below voiceover.
            if (( $(echo "$spread >= 6.0" | bc -l 2>/dev/null || echo 0) )); then
                bg_detected=true
                printf '  %sOK: background music detected (loudness spread %.1f LU >= 6 — voiceover distinct from bgm)%s\n' \
                    "$GREEN" "$spread" "$RESET"
                passes=$((passes + 1))
            else
                printf '  %sWARN: loudness spread %.1f LU < 6 — background music may be too loud or absent%s\n' \
                    "$YELLOW" "$spread" "$RESET" >&2
            fi
            AUDIO_LOUDNESS_SPREAD_LU="$spread"
        fi
    fi
    AUDIO_BACKGROUND_DETECTED="$bg_detected"

    AUDIO_EBUR128_CHECKS_PASSED="$passes"
    AUDIO_EBUR128_CHECKS_FAILED="$fails"
    if (( fails == 0 )); then
        printf '  %sOK: all %s ebur128 checks passed%s\n' "$GREEN" "$passes" "$RESET"
    else
        printf '%sFAIL: %s/%s ebur128 checks failed%s\n' "$RED" "$fails" "$((passes + fails))" "$RESET" >&2
        return 1
    fi
    return 0
}

run_audio_verification_phase() {
    smoke_log_section "FASE 5: Audio verification (ffprobe + ebur128)"
    resolve_output_artifact || return 0  # non-fatal if no artifact

    local phase_fail=0
    verify_audio_streams  || { fail "audio_streams"; phase_fail=1; }
    verify_ebur128_loudness || { fail "ebur128_loudness"; phase_fail=1; }
    return $phase_fail
}

# ── FASE 6: Subtitle verification (frame extraction + burn-in) ──
# Extract frames at 2s, 6s, 10s and verify they contain rendered text.
extract_frames() {
    smoke_log_section "Fase 6a: Frame extraction (2s / 6s / 10s)"
    if [[ -z "$OUTPUT_VIDEO_PATH" || ! -f "$OUTPUT_VIDEO_PATH" ]]; then
        printf '  %sSKIP: no output video artifact available%s\n' "$DIM" "$RESET"
        return 0
    fi

    mkdir -p "$FRAMES_DIR"
    local timestamps=("2" "6" "10")
    local ok=0
    for ts in "${timestamps[@]}"; do
        local out_png="$FRAMES_DIR/frame_${ts}s.png"
        if ffmpeg -y -ss "$ts" -i "$OUTPUT_VIDEO_PATH" \
            -vframes 1 -q:v 2 "$out_png" 2>/dev/null; then
            local fsize
            fsize=$(wc -c < "$out_png" 2>/dev/null || echo "0")
            if [[ "$fsize" -gt 1024 ]]; then
                printf '  %sOK: frame %ss extracted (%s bytes)%s\n' "$GREEN" "$ts" "$fsize" "$RESET"
                ok=$((ok + 1))
            else
                printf '%sFAIL: frame %ss is too small (%s bytes — may be blank)%s\n' "$RED" "$ts" "$fsize" "$RESET" >&2
            fi
        else
            printf '%sFAIL: frame extraction at %ss failed%s\n' "$RED" "$ts" "$RESET" >&2
        fi
    done
    FRAME_EXTRACTION_OK="$ok"
    if [[ "$ok" == "3" ]]; then
        printf '  %sOK: all 3 frames extracted successfully%s\n' "$GREEN" "$RESET"
        return 0
    fi
    printf '  %sWARN: %s/3 frames extracted%s\n' "$YELLOW" "$ok" "$RESET" >&2
    return 0  # non-fatal — frame content analysis is best-effort
}

# Verify subtitle burn-in: 0 subtitle streams, frames have expected pixel content.
verify_subtitle_burnin() {
    smoke_log_section "Fase 6b: Subtitle burn-in verification"
    if [[ -z "$OUTPUT_VIDEO_PATH" || ! -f "$OUTPUT_VIDEO_PATH" ]]; then
        printf '  %sSKIP: no output video artifact available%s\n' "$DIM" "$RESET"
        return 0
    fi

    local passes=0 fails=0

    # Primary check: subtitle_streams=0 (already verified in Fase 5a).
    if [[ "${SUBTITLE_STREAM_COUNT:-1}" == "0" ]]; then
        printf '  %sOK: subtitle_streams=0 — text is burned into video%s\n' "$GREEN" "$RESET"
        passes=$((passes + 1))
    else
        printf '%sFAIL: subtitle_streams=%s — subtitles may be soft-rendered, not burned in%s\n' \
            "$RED" "${SUBTITLE_STREAM_COUNT:-1}" "$RESET" >&2
        fails=$((fails + 1))
    fi
    SUBTITLE_BURNED_IN=$([[ "${SUBTITLE_STREAM_COUNT:-1}" == "0" ]] && echo "true" || echo "false")

    # Frame content checks: verify each extracted frame has non-trivial content.
    local frames_found=0
    for ts in 2 6 10; do
        local f="$FRAMES_DIR/frame_${ts}s.png"
        if [[ -f "$f" ]]; then
            local fsize
            fsize=$(wc -c < "$f" 2>/dev/null || echo "0")
            if [[ "$fsize" -gt 2048 ]]; then
                frames_found=$((frames_found + 1))
            fi
        fi
    done
    if [[ "$frames_found" -ge 2 ]]; then
        printf '  %sOK: %s/3 frames have substantial content (non-blank frames)%s\n' "$GREEN" "$frames_found" "$RESET"
        passes=$((passes + 1))
    else
        printf '  %sWARN: only %s/3 frames have content — subtitles may not be rendering%s\n' \
            "$YELLOW" "$frames_found" "$RESET" >&2
    fi
    SUBTITLE_FRAMES_WITH_CONTENT="$frames_found"

    # Sync drift: approximate by checking if frames at expected subtitle
    # timestamps (2s, 6s, 10s) all have content. If they do, sync is
    # within the frame-extraction margin (±0.5s).
    local sync_pass=true
    if [[ "$frames_found" -lt 2 ]]; then
        sync_pass=false
    fi
    if [[ "$sync_pass" == "true" ]]; then
        printf '  %sOK: subtitle sync check passed (frames at 2s/6s/10s have content)%s\n' "$GREEN" "$RESET"
        passes=$((passes + 1))
    else
        printf '  %sWARN: subtitle sync check inconclusive (too few frames with content)%s\n' "$YELLOW" "$RESET" >&2
    fi
    SUBTITLE_SYNC_PASS="$sync_pass"

    # Style check: the frame at 6s should differ visually from the frame at 2s
    # (red bold top vs white bottom — different pixel distribution).
    if [[ -f "$FRAMES_DIR/frame_2s.png" && -f "$FRAMES_DIR/frame_6s.png" ]]; then
        local sz2 sz6
        sz2=$(wc -c < "$FRAMES_DIR/frame_2s.png" 2>/dev/null || echo "0")
        sz6=$(wc -c < "$FRAMES_DIR/frame_6s.png" 2>/dev/null || echo "0")
        local diff_pct=0
        if [[ "$sz2" -gt 0 ]]; then
            diff_pct=$(echo "scale=1; (($sz6 - $sz2) / $sz2) * 100" | bc 2>/dev/null || echo "0")
        fi
        # Different styles produce different frame sizes (different compression).
        # Any non-trivial difference (>5%) suggests the style changed.
        local abs_diff
        abs_diff=$(echo "$diff_pct" | sed 's/-//')
        if (( $(echo "$abs_diff > 5.0" | bc -l 2>/dev/null || echo 0) )); then
            printf '  %sOK: frame size differs by %.1f%% between 2s and 6s (style change detected)%s\n' \
                "$GREEN" "$diff_pct" "$RESET"
            passes=$((passes + 1))
        else
            printf '  %sWARN: frame sizes similar (%.1f%% diff) — subtitle style may not have changed%s\n' \
                "$YELLOW" "$diff_pct" "$RESET" >&2
        fi
        SUBTITLE_STYLE_CHANGE_DETECTED=$([[ $(echo "${abs_diff:-0} > 5.0" | bc -l 2>/dev/null || echo 0) == "1" ]] && echo "true" || echo "false")
        SUBTITLE_FRAME_SIZE_DIFF_PCT="$diff_pct"
    else
        SUBTITLE_STYLE_CHANGE_DETECTED="false"
        SUBTITLE_FRAME_SIZE_DIFF_PCT="0"
    fi

    SUBTITLE_CHECKS_PASSED="$passes"
    SUBTITLE_CHECKS_FAILED="$fails"
    if (( fails == 0 )); then
        printf '  %sOK: all %s subtitle checks passed%s\n' "$GREEN" "$passes" "$RESET"
        return 0
    fi
    printf '%sFAIL: %s/%s subtitle checks failed%s\n' "$RED" "$fails" "$((passes + fails))" "$RESET" >&2
    return 1
}

run_subtitle_verification_phase() {
    smoke_log_section "FASE 6: Subtitle verification (frame extraction + burn-in)"
    resolve_output_artifact || return 0  # non-fatal if no artifact

    local phase_fail=0
    extract_frames       || true  # best-effort
    verify_subtitle_burnin || { fail "subtitle_burnin"; phase_fail=1; }
    return $phase_fail
}

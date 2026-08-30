#!/usr/bin/env bash
# Source-only helpers for comedian_clips_velox.sh.
# shellcheck shell=bash
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    echo "[ERROR] comedian_clips_velox.sh must be sourced, not executed directly." >&2
    exit 1
fi

comedian_build_velox_payload() {
# STEP 8: BUILD VELOX PAYLOAD
# ══════════════════════════════════════════════════════════════════════
smoke_log_section "Step 8/11: Build Velox payload"

VELOX_MASTER_ASSET_TOKEN="${VELOX_MASTER_ADMIN_TOKEN:-}"
if [[ "$SKIP_VELOX" == "0" && -z "$VELOX_MASTER_ASSET_TOKEN" ]]; then
    printf '%ssetup error: VELOX_MASTER_ADMIN_TOKEN is required to upload clip assets to Velox Master%s\n' "$RED" "$RESET" >&2
    exit 2
fi

VELOX_CLIP_LINKS_JSON="[]"
VELOX_CLIP_DURATIONS_JSON="[]"
VELOX_SUBTITLE_TRACKS_JSON="[]"
VOICEOVER_PATHS="[]"
VOICEOVER_DURATIONS_JSON="[]"
if [[ "$SKIP_VELOX" == "0" ]]; then
    if [[ -z "${VO_REQUEST_PREFIX:-}" ]]; then
        printf '%sFAIL step 8: missing voiceover request prefix%s\n' "$RED" "$RESET" >&2
        exit 1
    fi

    VOICEOVER_ROWS_JSON="[]"
    VO_META_DEADLINE=$(( $(date +%s) + 300 ))
    while (( $(date +%s) < VO_META_DEADLINE )); do
        VOICEOVER_ROWS_JSON=$(sqlite3 -json "$SMOKE_DB" "
            SELECT
                COALESCE(NULLIF(drive_link,''), NULLIF(download_link,''), NULLIF(local_path,'')) AS ref,
                local_path,
                duration_seconds
            FROM voiceovers
            WHERE request_id LIKE '${VO_REQUEST_PREFIX}-scene-%'
              AND lower(status) IN ('ready','generated')
            ORDER BY CAST(substr(request_id, length('${VO_REQUEST_PREFIX}-scene-') + 1) AS INTEGER),
                     created_at,
                     filename;" 2>/dev/null || echo "[]")
        VO_ROW_CT=$(jq -r 'length' <<<"$VOICEOVER_ROWS_JSON" 2>/dev/null || echo 0)
        if (( VO_ROW_CT < SCENE_COUNT )); then
            VOICEOVER_ROWS_JSON=$(sqlite3 -json "$SMOKE_DB" "
                SELECT
                    json_extract(result_json, '$.drive_link') AS ref,
                    json_extract(result_json, '$.local_path') AS local_path,
                    0 AS duration_seconds
                FROM jobs
                WHERE type = 'voiceover.generate_item'
                  AND status = 'SUCCEEDED'
                  AND json_extract(payload_json, '$.request_id') LIKE '${VO_REQUEST_PREFIX}-scene-%'
                  AND COALESCE(json_extract(result_json, '$.local_path'), '') != ''
                ORDER BY CAST(substr(json_extract(payload_json, '$.request_id'), length('${VO_REQUEST_PREFIX}-scene-') + 1) AS INTEGER),
                         created_at;" 2>/dev/null || echo "[]")
            VO_ROW_CT=$(jq -r 'length' <<<"$VOICEOVER_ROWS_JSON" 2>/dev/null || echo 0)
        fi
        (( VO_ROW_CT >= SCENE_COUNT )) && break
        printf '  waiting voiceover metadata: %d/%d\n' "$VO_ROW_CT" "$SCENE_COUNT"
        sleep 5
    done
    VOICEOVER_META="${VEL_E2E_WORK}/voiceover-meta.json"
    VOICEOVER_ROWS_JSON="$VOICEOVER_ROWS_JSON" SCENE_COUNT="$SCENE_COUNT" ROOT_DIR="$ROOT_DIR" VOICEOVER_META="$VOICEOVER_META" python3 - <<'PY'
import json
import os
import subprocess
import sys

rows = json.loads(os.environ.get("VOICEOVER_ROWS_JSON") or "[]")
scene_count = int(os.environ["SCENE_COUNT"])
root = os.environ["ROOT_DIR"]
out_path = os.environ["VOICEOVER_META"]
meta = []
for row in rows:
    ref = (row.get("ref") or "").strip()
    local_path = (row.get("local_path") or "").strip()
    if local_path and not os.path.isabs(local_path):
        local_path = os.path.join(root, local_path)
    duration = float(row.get("duration_seconds") or 0)
    if duration <= 0 and local_path and os.path.isfile(local_path):
        probe = subprocess.run(
            ["ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", local_path],
            check=False,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
        )
        try:
            duration = float(probe.stdout.strip() or "0")
        except ValueError:
            duration = 0
    if ref and duration > 0:
        meta.append({"ref": ref, "local_path": local_path, "duration_seconds": round(duration, 3)})
with open(out_path, "w", encoding="utf-8") as fh:
    json.dump(meta[:scene_count], fh, ensure_ascii=False)
if len(meta) < scene_count:
    print(f"voiceover metadata incomplete: {len(meta)}/{scene_count}", file=sys.stderr)
    sys.exit(1)
PY

    VOICEOVER_PATHS=$(jq -c '[.[].ref]' "$VOICEOVER_META")
    VOICEOVER_DURATIONS_JSON=$(jq -c '[.[].duration_seconds]' "$VOICEOVER_META")
    VO_DURATION_SUM=$(jq -r 'add' <<<"$VOICEOVER_DURATIONS_JSON")
    printf '  voiceover assets: %s%d paths, total %.3fs%s\n' \
        "$GREEN" "$(jq -r 'length' <<<"$VOICEOVER_PATHS")" "$VO_DURATION_SUM" "$RESET"

    VEL_CLIP_ASSETS_TSV="${VEL_E2E_WORK}/velox-clip-assets.tsv"
    VEL_CLIP_DURATIONS_TSV="${VEL_E2E_WORK}/velox-clip-durations.tsv"
    VEL_CLEAN_CLIP_DIR="${VEL_E2E_WORK}/velox-clean-clips"
    mkdir -p "$VEL_CLEAN_CLIP_DIR"
    : > "$VEL_CLIP_ASSETS_TSV"
    : > "$VEL_CLIP_DURATIONS_TSV"
    CLIP_UPLOAD_INDEX=0
    while IFS= read -r clip_path; do
        [[ -n "$clip_path" ]] || continue
        CLIP_UPLOAD_INDEX=$((CLIP_UPLOAD_INDEX + 1))
        if [[ ! -f "$clip_path" && -f "$ROOT_DIR/$clip_path" ]]; then
            clip_path="$ROOT_DIR/$clip_path"
        fi
        if [[ ! -f "$clip_path" ]]; then
            printf '%sFAIL step 8: clip source not readable: %s%s\n' "$RED" "$clip_path" "$RESET" >&2
            exit 1
        fi
        TARGET_DURATION=$(jq -r --argjson idx "$((CLIP_UPLOAD_INDEX - 1))" '.[$idx] // 5' <<<"$VOICEOVER_DURATIONS_JSON")
        CLEAN_CLIP="${VEL_CLEAN_CLIP_DIR}/scene-${CLIP_UPLOAD_INDEX}.clean.mp4"
        ffmpeg -nostdin -y -hide_banner -nostats -loglevel fatal -err_detect ignore_err -stream_loop -1 -i "$clip_path" -t "$TARGET_DURATION" \
            -vf "scale=1280:-2,fps=24,format=yuv420p" \
            -c:v libx264 -preset veryfast -crf 23 \
            -an -movflags +faststart "$CLEAN_CLIP" >"${VEL_E2E_WORK}/ffmpeg-clean.log" 2>&1
        CLEAN_DURATION=$(ffprobe -v error -show_entries format=duration -of default=nw=1:nk=1 "$CLEAN_CLIP" 2>/dev/null | awk '{printf "%.3f", $1 + 0}')
        if [[ -z "$CLEAN_DURATION" || "$CLEAN_DURATION" == "0.000" ]]; then
            printf '%sFAIL step 8: cleaned clip has invalid duration: %s%s\n' "$RED" "$CLEAN_CLIP" "$RESET" >&2
            exit 1
        fi
        UPLOAD_BODY="${VEL_E2E_WORK}/velox-asset-upload-$(basename "$CLEAN_CLIP").json"
        UPLOAD_HTTP=$(curl -s --max-time 60 \
            -o "$UPLOAD_BODY" -w '%{http_code}' \
            -X POST \
            -H "Authorization: Bearer $VELOX_MASTER_ASSET_TOKEN" \
            -F kind=stock_clip \
            -F "file=@${CLEAN_CLIP};type=video/mp4" \
            "${VELOX_MASTER_URL}/api/v1/creator/assets")
        if [[ "$UPLOAD_HTTP" != "201" ]]; then
            printf '%sFAIL step 8: clip asset upload failed HTTP %s for %s%s\n' "$RED" "$UPLOAD_HTTP" "$CLEAN_CLIP" "$RESET" >&2
            if [[ -s "$UPLOAD_BODY" ]]; then
                smoke_echo_safe "$(head -c 400 "$UPLOAD_BODY")" >&2
            fi
            exit 1
        fi
        jq -er .reference "$UPLOAD_BODY" >> "$VEL_CLIP_ASSETS_TSV"
        printf '%s\n' "$CLEAN_DURATION" >> "$VEL_CLIP_DURATIONS_TSV"
    done < <(jq -r '.[]' <<<"$CLIP_SOURCE_PATHS_JSON")
    VELOX_CLIP_LINKS_JSON=$(jq -Rsc 'split("\n") | map(select(length > 0))' "$VEL_CLIP_ASSETS_TSV")
    VELOX_CLIP_DURATIONS_JSON=$(jq -Rsc 'split("\n") | map(select(length > 0) | tonumber)' "$VEL_CLIP_DURATIONS_TSV")

    VOICEOVER_SRT="${VEL_E2E_WORK}/voiceover-subtitles.srt"
    SPEC_FILE="$SPEC_FILE" VOICEOVER_DURATIONS_JSON="$VOICEOVER_DURATIONS_JSON" VOICEOVER_SRT="$VOICEOVER_SRT" python3 - <<'PY'
import json
import os
import re
import textwrap

def ts(seconds):
    ms_total = int(round(seconds * 1000))
    h, rem = divmod(ms_total, 3600000)
    m, rem = divmod(rem, 60000)
    s, ms = divmod(rem, 1000)
    return f"{h:02d}:{m:02d}:{s:02d},{ms:03d}"

def cue_chunks(text, max_words=10, max_chars=72):
    words = text.split()
    chunks = []
    cur = []
    for word in words:
        candidate = " ".join(cur + [word])
        if cur and (len(cur) >= max_words or len(candidate) > max_chars):
            chunks.append(" ".join(cur))
            cur = [word]
        else:
            cur.append(word)
    if cur:
        chunks.append(" ".join(cur))
    return chunks or [text]

def wrap_cue(text):
    lines = textwrap.wrap(text, width=42, break_long_words=False, break_on_hyphens=False)
    return "\n".join(lines[:2] if lines else [text])

with open(os.environ["SPEC_FILE"], encoding="utf-8") as fh:
    spec = json.load(fh)
durations = json.loads(os.environ["VOICEOVER_DURATIONS_JSON"])
offset = 0.0
blocks = []
cue_idx = 1
for idx, scene in enumerate((spec.get("scenes") or [])[:len(durations)], start=1):
    duration = float(durations[idx - 1])
    text = re.sub(r"\s+", " ", (scene.get("text") or "").strip())
    chunks = cue_chunks(text)
    cursor = offset
    scene_end = offset + duration
    weights = [max(1, len(chunk.split())) for chunk in chunks]
    total_weight = sum(weights)
    for chunk_pos, (chunk, weight) in enumerate(zip(chunks, weights)):
        cue_duration = duration * (weight / total_weight)
        cue_start = cursor
        cue_end = scene_end if chunk_pos == len(chunks) - 1 else min(scene_end, cursor + cue_duration)
        if cue_end - cue_start < 0.35:
            cue_end = min(scene_end, cue_start + 0.35)
        blocks.append(f"{cue_idx}\n{ts(cue_start)} --> {ts(cue_end)}\n{wrap_cue(chunk)}\n")
        cue_idx += 1
        cursor = cue_end
    offset = scene_end
with open(os.environ["VOICEOVER_SRT"], "w", encoding="utf-8") as fh:
    fh.write("\n".join(blocks))
PY
    if [[ ! -s "$VOICEOVER_SRT" ]]; then
        printf '%sFAIL step 8: generated voiceover subtitles are empty%s\n' "$RED" "$RESET" >&2
        exit 1
    fi
    SUB_UPLOAD_BODY="${VEL_E2E_WORK}/velox-subtitle-upload.json"
    SUB_UPLOAD_HTTP=$(curl -s --max-time 60 \
        -o "$SUB_UPLOAD_BODY" -w '%{http_code}' \
        -X POST \
        -H "Authorization: Bearer $VELOX_MASTER_ASSET_TOKEN" \
        -F kind=subtitle \
        -F "file=@${VOICEOVER_SRT};type=application/x-subrip" \
        "${VELOX_MASTER_URL}/api/v1/creator/assets")
    if [[ "$SUB_UPLOAD_HTTP" != "201" ]]; then
        printf '%sFAIL step 8: subtitle asset upload failed HTTP %s%s\n' "$RED" "$SUB_UPLOAD_HTTP" "$RESET" >&2
        if [[ -s "$SUB_UPLOAD_BODY" ]]; then
            smoke_echo_safe "$(head -c 400 "$SUB_UPLOAD_BODY")" >&2
        fi
        exit 1
    fi
    SUBTITLE_REF=$(jq -er .reference "$SUB_UPLOAD_BODY")
    VELOX_SUBTITLE_TRACKS_JSON=$(jq -cn --arg source "$SUBTITLE_REF" '[{source: $source, preset: "active_word_pop", font: "Inter"}]')
fi

VELOX_SCENES="[]"
if [[ -s "$SPEC_FILE" ]]; then
    VELOX_SCENES=$(jq -c \
        --argjson default_duration "${VELOX_DEFAULT_SCENE_DURATION_SECONDS:-4}" \
        --argjson fallback_clip_links "$VELOX_CLIP_LINKS_JSON" \
        --argjson fallback_durations "$VELOX_CLIP_DURATIONS_JSON" \
        --argjson voiceover_paths "$VOICEOVER_PATHS" \
        --argjson voiceover_durations "$VOICEOVER_DURATIONS_JSON" \
        --argjson subtitle_tracks "$VELOX_SUBTITLE_TRACKS_JSON" '
        def nonempty($v): if (($v // "") | tostring | length) > 0 then $v else empty end;
        [.scenes | to_entries[]? |
            (($fallback_durations[.key] // .value.duration_seconds // .value.duration // $default_duration) | tonumber) as $clip_duration |
            (($voiceover_durations[.key] // $clip_duration) | tonumber) as $voice_duration |
            (if $voice_duration > $clip_duration then $voice_duration else $clip_duration end) as $duration |
            (nonempty(.value.clip_link) //
             nonempty(.value.bindings.clip.link) //
             nonempty(.value.source.clip_link) //
             nonempty($fallback_clip_links[.key]) //
             "") as $clip_link |
            {
                scene_id: ("scene-" + ((.key + 1) | tostring)),
                index: (.key + 1),
                text: (.value.text // ""),
                clip_link: $clip_link,
                clip: {
                    url: $clip_link,
                    duration_ms: (($clip_duration * 1000 + 0.5) | floor)
                },
                voiceover: {
                    url: ($voiceover_paths[.key] // ""),
                    duration_ms: (($voice_duration * 1000 + 0.5) | floor),
                    language: "it-IT"
                },
                subtitles: {
                    url: ($subtitle_tracks[0].source // ""),
                    format: "srt",
                    language: "it-IT"
                },
                duration_seconds: $duration
            }]
    ' "$SPEC_FILE" 2>/dev/null || echo "[]")
fi

CORRELATION_ID="comedian-clips-${PG_JOB_ID}"
MANIFEST_IDEM="pipelinegen-${PG_JOB_ID}-$(date +%s)"

VELOX_PAYLOAD="$VEL_E2E_WORK/velox-render-request.json"
jq -n \
    --arg idempotency_key "$MANIFEST_IDEM" \
    --arg title "Comedian clips compilation" \
    --arg script_text "$SCRIPT_TEXT" \
    --argjson scenes "$VELOX_SCENES" \
    --argjson voiceover_paths "$VOICEOVER_PATHS" \
    --argjson subtitle_tracks "$VELOX_SUBTITLE_TRACKS_JSON" \
    '{
        idempotency_key: $idempotency_key,
        video_name: $title,
        script_text: $script_text,
        scenes: $scenes,
        voiceover_paths: $voiceover_paths,
        subtitle_tracks: $subtitle_tracks,
        delivery_plan: [{
            destination_id: "comedy_test",
            priority: 1,
            retry_budget: 3
        }]
    }' > "$VELOX_PAYLOAD"

SCENE_CT=$(jq -r '.scenes | length' "$VELOX_PAYLOAD" 2>/dev/null || echo 0)
VO_CT=$(jq -r '.voiceover_paths | length' "$VELOX_PAYLOAD" 2>/dev/null || echo 0)
SUBTRACK_CT=$(jq -r '.subtitle_tracks | length' "$VELOX_PAYLOAD" 2>/dev/null || echo 0)
printf '  payload: %s%d scenes, %d voiceover paths, %d subtitle tracks, %d chars script%s\n' \
    "$YELLOW" "$SCENE_CT" "$VO_CT" "$SUBTRACK_CT" "${#SCRIPT_TEXT}" "$RESET"
if (( SCENE_CT == 0 || VO_CT != SCENE_CT || SUBTRACK_CT == 0 )); then
    printf '%sFAIL step 8: invalid scene/voiceover cardinality (%d/%d)%s\n' "$RED" "$SCENE_CT" "$VO_CT" "$RESET" >&2
    exit 1
fi
PAYLOAD_SHA256=$(sha256sum "$VELOX_PAYLOAD" | awk '{print $1}')
IMMUTABLE_PAYLOAD="${VEL_E2E_WORK}/velox-render-request.${PAYLOAD_SHA256}.json"
cp "$VELOX_PAYLOAD" "$IMMUTABLE_PAYLOAD"
printf '  payload_sha256: %s\n' "$PAYLOAD_SHA256"

# ══════════════════════════════════════════════════════════════════════
}

comedian_submit_velox() {
# STEP 9: SUBMIT TO VELOX MASTER
# ══════════════════════════════════════════════════════════════════════
VELOX_JOB_ID=""
if [[ "$SKIP_VELOX" == "0" ]]; then
    smoke_log_section "Step 9/11: Submit to Velox Master"

    VELOX_SUBMIT="$VEL_E2E_WORK/velox-submit.json"
    VELOX_HTTP=$(curl -s --max-time 30 \
        -o "$VELOX_SUBMIT" -w '%{http_code}' \
        -X POST \
        -H "Authorization: Bearer $VELOX_M2M_TOKEN" \
        -H "Content-Type: application/json" \
        -H "X-Request-ID: $MANIFEST_IDEM" \
        --data-binary "@${VELOX_PAYLOAD}" \
        "${VELOX_MASTER_URL}/api/v1/jobs")

    printf '  Velox submit HTTP: %s\n' "$VELOX_HTTP"

    if [[ "$VELOX_HTTP" == "202" || "$VELOX_HTTP" == "200" ]]; then
        VELOX_JOB_ID=$(jq -r '.job_id // .enqueue.job_id // .job.id // ""' "$VELOX_SUBMIT")
        if [[ -n "$VELOX_JOB_ID" ]]; then
            printf '  Velox job_id: %s%s%s\n' "$YELLOW" "$VELOX_JOB_ID" "$RESET"
            VELOX_IDEMPOTENCY_SUBMIT="$VEL_E2E_WORK/velox-submit-idempotency.json"
            VELOX_IDEMPOTENCY_HTTP=$(curl -s --max-time 30 \
                -o "$VELOX_IDEMPOTENCY_SUBMIT" -w '%{http_code}' \
                -X POST \
                -H "Authorization: Bearer $VELOX_M2M_TOKEN" \
                -H "Content-Type: application/json" \
                -H "X-Request-ID: $MANIFEST_IDEM" \
                --data-binary "@${VELOX_PAYLOAD}" \
                "${VELOX_MASTER_URL}/api/v1/jobs")
            IDEMPOTENCY_JOB_ID=$(jq -r '.job_id // .enqueue.job_id // .job.id // ""' "$VELOX_IDEMPOTENCY_SUBMIT" 2>/dev/null || echo "")
            IDEMPOTENCY_ERROR=$(jq -r '.error // .code // ""' "$VELOX_IDEMPOTENCY_SUBMIT" 2>/dev/null || echo "")
            if [[ ("$VELOX_IDEMPOTENCY_HTTP" == "202" || "$VELOX_IDEMPOTENCY_HTTP" == "200") && "$IDEMPOTENCY_JOB_ID" == "$VELOX_JOB_ID" ]]; then
                printf '  idempotency replay: %sOK same job_id%s\n' "$GREEN" "$RESET"
            elif [[ "$VELOX_IDEMPOTENCY_HTTP" == "409" && "$IDEMPOTENCY_ERROR" == "idempotency_key_reused" ]]; then
                printf '  idempotency replay: %sOK conflict/no duplicate%s\n' "$GREEN" "$RESET"
            else
                printf '%sFAIL step 9: idempotency replay returned HTTP %s job_id=%s, want %s%s\n' \
                    "$RED" "$VELOX_IDEMPOTENCY_HTTP" "$IDEMPOTENCY_JOB_ID" "$VELOX_JOB_ID" "$RESET" >&2
                exit 1
            fi
            # Save tracking info
            jq -n \
                --arg pg "$PG_JOB_ID" \
                --arg vx "$VELOX_JOB_ID" \
                --arg idem "$MANIFEST_IDEM" \
                --arg payload_sha256 "$PAYLOAD_SHA256" \
                --arg status "PENDING" \
                '{
                    pipelinegen_job_id: $pg,
                    velox_job_id: $vx,
                    idempotency_key: $idem,
                    payload_sha256: $payload_sha256,
                    velox_status: $status,
                    submitted_at: (now | todate)
                }' > "${VEL_E2E_WORK}/tracking.json"
        else
            printf '%sWARN: Velox response missing job_id%s\n' "$YELLOW" "$RESET"
        fi
    else
        printf '%sWARN: Velox submit failed HTTP %s — skipping poll%s\n' "$YELLOW" "$VELOX_HTTP" "$RESET"
        if [[ -s "$VELOX_SUBMIT" ]]; then
            smoke_echo_safe "$(head -c 400 "$VELOX_SUBMIT")" >&2
        fi
    fi
else
    printf '%sFAIL step 9: Velox submission unavailable%s\n' "$RED" "$RESET" >&2
    exit 1
fi

# ══════════════════════════════════════════════════════════════════════
}

comedian_poll_velox() {
# STEP 10: POLL VELOX MASTER
# ══════════════════════════════════════════════════════════════════════
VELOX_FINAL_STATUS=""
if [[ -n "$VELOX_JOB_ID" ]]; then
    smoke_log_section "Step 10/11: Poll Velox Master"

    VEL_DEADLINE=$(( $(date +%s) + VELOX_RENDER_POLL_TIMEOUT ))
    VEL_POLL_BODY="${VEL_E2E_WORK}/velox-poll.json"
    while (( $(date +%s) < VEL_DEADLINE )); do
        VEL_POLL_HTTP=$(curl -s --max-time 15 \
            -o "$VEL_POLL_BODY" -w '%{http_code}' \
            -H "Authorization: Bearer $VELOX_M2M_TOKEN" \
            "${VELOX_MASTER_URL}/api/v1/jobs/${VELOX_JOB_ID}")

        if [[ "$VEL_POLL_HTTP" == "200" ]]; then
            VEL_STATUS=$(jq -r '.status // .job.status // .state // .job.state // ""' "$VEL_POLL_BODY" | tr '[:upper:]' '[:lower:]')
            printf '  [%s] status: %s\n' "$(date +%H:%M:%S)" "$VEL_STATUS"

            case "$VEL_STATUS" in
                succeeded|completed)
                    VELOX_FINAL_STATUS="$VEL_STATUS"
                    break ;;
                failed|cancelled|dead_letter|quarantined)
                    VELOX_FINAL_STATUS="$VEL_STATUS"
                    printf '%sFAIL step 10: Velox job %s%s\n' "$RED" "$VEL_STATUS" "$RESET" >&2
                    jq . "$VEL_POLL_BODY" >&2 || true
                    break ;;
            esac
        fi
        sleep "$VELOX_RENDER_POLL_INTERVAL"
    done

    if [[ -z "$VELOX_FINAL_STATUS" ]]; then
        printf '%sFAIL step 10: Velox poll timeout%s\n' "$RED" "$RESET" >&2
        exit 124
    fi
    printf '  Velox final status: %s%s%s\n' "$CYAN" "$VELOX_FINAL_STATUS" "$RESET"
else
    printf '%sFAIL step 10: Velox job_id missing%s\n' "$RED" "$RESET" >&2
    exit 1
fi

# ══════════════════════════════════════════════════════════════════════
}

comedian_verify_final() {
# STEP 11: VERIFY FINAL OUTPUT
# ══════════════════════════════════════════════════════════════════════
smoke_log_section "Step 11/11: Final verification"

# PipelineGen results
printf '  PipelineGen: %sjob=%s status=%s words=%d scenes=%d%s\n' \
    "$GREEN" "$PG_JOB_ID" "$SMOKE_LAST_STATUS" "$WORDS" "$SCENE_COUNT" "$RESET"

# Voiceover results
printf '  Voiceover:   %s%d/%d succeeded%s\n' "$GREEN" "$VO_OK" "${#VO_JOB_IDS[@]}" "$RESET"

# Subtitle results
printf '  Subtitles:   %s%d/%d clips with READY subs%s\n' "$GREEN" "$SUB_OK" "${#CLIP_IDS[@]}" "$RESET"

# Velox results
if [[ -n "$VELOX_FINAL_STATUS" ]]; then
    if [[ "$VELOX_FINAL_STATUS" == "succeeded" || "$VELOX_FINAL_STATUS" == "completed" ]]; then
        if [[ -s "${VEL_E2E_WORK}/velox-poll.json" ]]; then
            ARTIFACT_SIZE=$(jq -r '.result.artifact_size // .result.size // .artifact_size // .artifact.size // 0' "${VEL_E2E_WORK}/velox-poll.json" 2>/dev/null || echo 0)
            ARTIFACT_URL=$(jq -r '.result.artifact_url // .result.output_url // .artifact_url // .artifact.url // ""' "${VEL_E2E_WORK}/velox-poll.json" 2>/dev/null || echo "")
            printf '  Velox:       %sSUCCEEDED (artifact=%s bytes, url=%s)%s\n' \
                "$GREEN" "$ARTIFACT_SIZE" "${ARTIFACT_URL:-(none)}" "$RESET"
        else
            printf '  Velox:       %sSUCCEEDED%s\n' "$GREEN" "$RESET"
        fi
    else
        printf '  Velox:       %s%s%s\n' "$RED" "$VELOX_FINAL_STATUS" "$RESET"
    fi
else
    printf '  Velox:       %sMISSING%s\n' "$RED" "$RESET"
fi

# ══════════════════════════════════════════════════════════════════════
# FINAL RESULT
# ══════════════════════════════════════════════════════════════════════
printf '\n%s===== COMPLETE PIPELINE RESULT =====%s\n' "$CYAN" "$RESET"
printf 'pipelinegen_job_id:  %s\n' "$PG_JOB_ID"
[[ -n "$VELOX_JOB_ID" ]] && printf 'velox_job_id:        %s\n' "$VELOX_JOB_ID"
[[ -f "${VEL_E2E_WORK}/tracking.json" ]] && printf 'tracking:            %s\n' "${VEL_E2E_WORK}/tracking.json"
printf 'specscene:           %s\n' "$SPEC_FILE"
printf 'velox_payload:       %s\n' "$VELOX_PAYLOAD"

OVERALL_OK=1
[[ "$SMOKE_LAST_STATUS" == "completed" || "$SMOKE_LAST_STATUS" == "SUCCEEDED" ]] || OVERALL_OK=0
[[ -n "$VELOX_JOB_ID" ]] || OVERALL_OK=0
[[ "$VELOX_FINAL_STATUS" == "succeeded" || "$VELOX_FINAL_STATUS" == "completed" ]] || OVERALL_OK=0

if (( OVERALL_OK )); then
    printf '\n%sOK: full pipeline passed (generate → voiceover → subtitles → velox submit)%s\n' "$GREEN" "$RESET"
    exit 0
else
    printf '\n%sPARTIAL: pipeline completed with warnings (check steps above)%s\n' "$YELLOW" "$RESET"
    exit 1
fi
}


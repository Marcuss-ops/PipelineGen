#!/usr/bin/env bash
# Source-only helpers for comedian_clips_generation.sh.
# shellcheck shell=bash
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    echo "[ERROR] comedian_clips_generation.sh must be sourced, not executed directly." >&2
    exit 1
fi

comedian_dispatch_script() {
# STEP 2: DISPATCH SCRIPT GENERATE
# ══════════════════════════════════════════════════════════════════════
smoke_log_section "Step 2/11: POST /api/script/generate (5 comedian clips)"

CASE_PREFIX="comedian-clips-$(smoke_gen_uuid)"
IDEMPOTENCY_KEY="$CASE_PREFIX-key"
CLIP_IDS_JSON=$(printf '%s\n' "${CLIP_IDS[@]}" | jq -R . | jq -s .)
CLIP_SOURCE_PATHS_JSON=$(for clip_id in "${CLIP_IDS[@]}"; do
    sqlite3 -json "$SMOKE_DB" \
        "SELECT COALESCE(NULLIF(local_path,''), NULLIF(download_link,''), NULLIF(url,''), NULLIF(drive_link,''), NULLIF(source_url,'')) AS path FROM media_assets WHERE id='${clip_id}' LIMIT 1;" 2>/dev/null \
        | jq -r '.[0].path // empty'
done | jq -R . | jq -s .)

PAYLOAD=$(jq -n \
    --arg case_marker "$CASE_PREFIX" \
    --argjson clip_ids "$CLIP_IDS_JSON" \
    '{
        version: 2,
        preset: "custom",
        items: [{
            id: ($case_marker + "-item"),
            title: ("Comedian clips compilation " + $case_marker),
            language: "it",
            tone: "documentario leggero e ironico",
            style: "Breve montaggio di momenti comici. Collega le battute dei 5 clip in ordine. Tono divertente, conciso, italiano.",
            source: {
                type: "clips",
                topic: "Momenti comici da comici e battute celebri",
                source_text: ("Compilation di 5 clip comici: Broner, Chris Tucker, Frisbee e barzellette, Ricky Gervais, Robin Williams."),
                clip_ids: $clip_ids,
                num_clips: 5,
                grounding_policy: "clips_primary",
                fallback_policy: "strict",
                ordering_strategy: "input_order",
                guidelines: "Segui l ordine dei clip. Non aggiungere dettagli non presenti. Italiano."
            },
            script_params: {
                target_words: 300,
                min_words: 200,
                segment_words: 60,
                skip_quality_gate: true,
                use_memory: false
            },
            output: {
                save_to_db: true,
                generate_timeline: true,
                generate_metadata: false,
                extract_entities: false,
                generate_scene_images: false
            }
        }]
    }')

export SMOKE_IDEMPOTENCY_KEY="$IDEMPOTENCY_KEY"
smoke_curl POST "/api/script/generate" --data "$PAYLOAD" >/dev/null
unset SMOKE_IDEMPOTENCY_KEY
HTTP="$SMOKE_LAST_HTTP"

if [[ "$HTTP" != "202" && "$HTTP" != "200" ]]; then
    printf '%sFAIL step 2: dispatch HTTP %s (expected 200/202)%s\n' "$RED" "$HTTP" "$RESET" >&2
    smoke_echo_safe "$(head -c 600 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi

PG_JOB_ID=$(jq -r '.job_id // ""' "$SMOKE_LAST_BODY")
[[ -n "$PG_JOB_ID" ]] || { echo "FAIL step 2: no job_id" >&2; exit 1; }
printf '  PipelineGen job_id: %s%s%s\n' "$YELLOW" "$PG_JOB_ID" "$RESET"

# ══════════════════════════════════════════════════════════════════════
}

comedian_poll_script() {
# STEP 3: POLL PIPELINEGEN
# ══════════════════════════════════════════════════════════════════════
smoke_log_section "Step 3/11: Poll PipelineGen job"

if ! smoke_poll_terminal "$PG_JOB_ID"; then
    printf '%sFAIL step 3: PipelineGen polling timeout%s\n' "$RED" "$RESET" >&2
    exit 1
fi

if [[ "$SMOKE_LAST_STATUS" != "completed" && "$SMOKE_LAST_STATUS" != "SUCCEEDED" ]]; then
    printf '%sFAIL step 3: PipelineGen status=%s (expected completed/SUCCEEDED)%s\n' \
        "$RED" "$SMOKE_LAST_STATUS" "$RESET" >&2
    exit 1
fi
printf '  PipelineGen status: %s%s%s\n' "$GREEN" "$SMOKE_LAST_STATUS" "$RESET"

# ══════════════════════════════════════════════════════════════════════
}

comedian_assert_script() {
# STEP 4: ASSERT SCRIPT OUTPUT + SPECSCENE
# ══════════════════════════════════════════════════════════════════════
smoke_log_section "Step 4/11: Assert script + specscene"

# Extract the final post-processed specscene from every supported PipelineGen
# response envelope. The canonical batch response nests the item at
# .result.data.items[0].result; older single-item responses are shallower.
SPEC_FILE="${VEL_E2E_WORK}/specscene.json"
jq -e '
    (.result.data.items[0].result.output.final_specscene //
     .result.data.items[0].result.output.specscene //
     .result.items[0].result.output.final_specscene //
     .result.items[0].result.output.specscene //
     .result.output.final_specscene //
     .result.output.specscene // empty)
  ' "$SMOKE_LAST_BODY" > "$SPEC_FILE" 2>/dev/null || true

RESULT=$(jq -c '.result.data.items[0].result // .result.items[0].result // .result.output // .result // empty' "$SMOKE_LAST_BODY")
[[ -n "$RESULT" && "$RESULT" != "null" ]] || { echo "FAIL step 4: missing result" >&2; exit 1; }

SCRIPT_TEXT=$(jq -r '.output.text // .script // .text // .content // empty' <<<"$RESULT")
[[ -n "$SCRIPT_TEXT" ]] || { echo "FAIL step 4: empty script text" >&2; exit 1; }
WORDS=$(printf '%s' "$SCRIPT_TEXT" | wc -w | tr -d ' ')
printf '  script words: %s%s%s\n' "$YELLOW" "$WORDS" "$RESET"

# Italian check
printf '%s' "$SCRIPT_TEXT" | grep -Eiq '\b(il|la|gli|che|per|una|battuta|comico)\b' || {
    printf '%sFAIL step 4: no Italian markers%s\n' "$RED" "$RESET" >&2; exit 1; }

# Specscene validation
if [[ -s "$SPEC_FILE" ]]; then
    SCENE_COUNT=$(jq -r '.scenes | length' "$SPEC_FILE" 2>/dev/null || echo 0)
    printf '  scenes: %s%s%s\n' "$YELLOW" "$SCENE_COUNT" "$RESET"
else
    # Fallback: try extracting from result directly
    jq -c '.output.specscene // .specscene // empty' <<<"$RESULT" > "$SPEC_FILE" 2>/dev/null || true
    if [[ -s "$SPEC_FILE" ]]; then
        SCENE_COUNT=$(jq -r '.scenes | length' "$SPEC_FILE" 2>/dev/null || echo 0)
        printf '  scenes (from result): %s%s%s\n' "$YELLOW" "$SCENE_COUNT" "$RESET"
    else
        printf '%sFAIL step 4: no specscene scenes in PipelineGen result%s\n' "$RED" "$RESET" >&2
        exit 1
    fi
fi

# ══════════════════════════════════════════════════════════════════════
}

comedian_generate_voiceover() {
# STEP 5: GENERATE VOICEOVER FOR ALL SCENES (batch)
# ══════════════════════════════════════════════════════════════════════
smoke_log_section "Step 5/11: Generate voiceover (batch)"

VO_JOB_IDS=()
if [[ -s "$SPEC_FILE" && "$SCENE_COUNT" -gt 0 ]]; then
    VO_REQUEST_PREFIX="comedian-vo-${CASE_PREFIX}"
    VOICEOVER_PROJECT_ID="${VOICEOVER_PROJECT_ID:-comedian-clips-smoke}"
    while IFS= read -r scene_json; do
        [[ -n "$scene_json" ]] || continue
        scene_idx=$(jq -r '.idx' <<<"$scene_json")
        scene_text=$(jq -r '.text // empty' <<<"$scene_json")
        [[ -n "$scene_text" ]] || continue

        VO_REQUEST_ID="${VO_REQUEST_PREFIX}-scene-${scene_idx}"
        VO_PAYLOAD=$(jq -n \
            --arg request_id "$VO_REQUEST_ID" \
            --arg project "$VOICEOVER_PROJECT_ID" \
            --arg folder_id "${VELOX_DRIVE_VOICEOVER_ROOT:-}" \
            --arg text "$scene_text" \
            --arg filename "scene-${scene_idx}.mp3" \
            '{
                request_id: $request_id,
                project: $project,
                items: [{
                    text: $text,
                    language: "it-IT",
                    filename: $filename
                }],
                options: {
                    remove_silence: false,
                    strategy: "verify",
                    parallelism: 1
                }
            } + (if $folder_id != "" then {
                destination: {
                    kind: "explicit",
                    folder_id: $folder_id
                }
            } else {} end)')

        export SMOKE_IDEMPOTENCY_KEY="${VO_REQUEST_ID}"
        smoke_curl POST "/api/media/voiceover/generate" --data "$VO_PAYLOAD" >/dev/null
        unset SMOKE_IDEMPOTENCY_KEY
        VO_HTTP="$SMOKE_LAST_HTTP"

        if [[ "$VO_HTTP" == "202" || "$VO_HTTP" == "200" ]]; then
            VO_PARENT_JOB=$(jq -r '.job_id // .id // ""' "$SMOKE_LAST_BODY")
            if [[ -n "$VO_PARENT_JOB" ]]; then
                VO_JOB_IDS+=("$VO_PARENT_JOB")
                printf '  voiceover scene %s: parent_job=%s %sOK%s\n' "$scene_idx" "$VO_PARENT_JOB" "$GREEN" "$RESET"
            fi
            VO_CHILDREN=$(jq -r '[.jobs[]?.id // .children[]?.id // .result.child_job_ids[]? // .job.result.child_job_ids[]? // empty] | .[]' "$SMOKE_LAST_BODY" 2>/dev/null || true)
            while IFS= read -r child_id; do
                [[ -n "$child_id" ]] && VO_JOB_IDS+=("$child_id")
            done <<< "$VO_CHILDREN"
        else
            printf '%s  voiceover scene %s: HTTP %s%s\n' "$YELLOW" "$scene_idx" "$VO_HTTP" "$RESET"
        fi
    done < <(jq -c '.scenes | to_entries[] | {idx: (.key + 1), text: (.value.text // "")}' "$SPEC_FILE")
else
    printf '%s  No specscene — skipping voiceover generation%s\n' "$DIM" "$RESET"
fi

# ══════════════════════════════════════════════════════════════════════
}

comedian_poll_voiceover() {
# STEP 6: POLL VOICEOVER JOBS
# ══════════════════════════════════════════════════════════════════════
smoke_log_section "Step 6/11: Poll voiceover jobs"

VO_OK=0
if (( ${#VO_JOB_IDS[@]} > 0 )); then
    for vo_job_id in "${VO_JOB_IDS[@]}"; do
        if smoke_poll_terminal "$vo_job_id"; then
            if [[ "$SMOKE_LAST_STATUS" == "completed" || "$SMOKE_LAST_STATUS" == "SUCCEEDED" ]]; then
                VO_OK=$((VO_OK + 1))
                printf '  vo %s: %s%s%s\n' "${vo_job_id:0:16}" "$GREEN" "$SMOKE_LAST_STATUS" "$RESET"
            else
                printf '%s  vo %s: %s%s%s\n' "$YELLOW" "${vo_job_id:0:16}" "$SMOKE_LAST_STATUS" "$RESET"
            fi
        else
            printf '%s  vo %s: poll timeout%s\n' "$YELLOW" "${vo_job_id:0:16}" "$RESET"
        fi
    done
else
    printf '  %sNo voiceover jobs to poll (skipped or batch returned no ID)%s\n' "$DIM" "$RESET"
fi
printf '  voiceover: %s%d/%d succeeded%s\n' "$GREEN" "$VO_OK" "${#VO_JOB_IDS[@]}" "$RESET"
if (( ${#VO_JOB_IDS[@]} == 0 || VO_OK != ${#VO_JOB_IDS[@]} )); then
    printf '%sFAIL step 6: voiceover jobs did not all succeed%s\n' "$RED" "$RESET" >&2
    exit 1
fi

# ══════════════════════════════════════════════════════════════════════
}

comedian_verify_subtitles() {
# STEP 7: VERIFY SUBTITLE ARTIFACTS
# ══════════════════════════════════════════════════════════════════════
smoke_log_section "Step 7/11: Verify subtitle artifacts"

SUB_OK=0
SUB_MISSING=0
for i in "${!CLIP_IDS[@]}"; do
    clip_id="${CLIP_IDS[$i]}"
    ready_count=$(sqlite3 "$SMOKE_DB" \
        "SELECT COUNT(*) FROM asset_subtitle_artifacts WHERE asset_id='${clip_id}' AND status='READY' AND is_current=1;" 2>/dev/null || echo 0)
    formats=$(sqlite3 "$SMOKE_DB" \
        "SELECT GROUP_CONCAT(DISTINCT format) FROM asset_subtitle_artifacts WHERE asset_id='${clip_id}' AND status='READY' AND is_current=1;" 2>/dev/null || echo "")

    if (( ready_count > 0 )); then
        printf '  %s%s%s — %d ready (%s)\n' "$GREEN" "${clip_id:0:30}" "$RESET" "$ready_count" "${formats:-?}"
        SUB_OK=$((SUB_OK + 1))
    else
        total=$(sqlite3 "$SMOKE_DB" \
            "SELECT COUNT(*) FROM asset_subtitle_artifacts WHERE asset_id='${clip_id}' AND is_current=1;" 2>/dev/null || echo 0)
        printf '  %s%s%s — 0 ready (total=%d)\n' "$YELLOW" "${clip_id:0:30}" "$RESET" "$total"
        SUB_MISSING=$((SUB_MISSING + 1))
    fi
done
printf '  subtitle summary: %s%d/%d clips with READY subs%s\n' "$GREEN" "$SUB_OK" "${#CLIP_IDS[@]}" "$RESET"

# ══════════════════════════════════════════════════════════════════════
}


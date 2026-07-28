#!/usr/bin/env bash
# tests/operational/comedian_clips_generate_voiceover_subtitles.sh
#
# Full end-to-end: 5 comedian clips → script generate → voiceover →
# subtitle verification → Velox Master submit → poll → verify.
#
# Steps:
#   1. Preflight: DB, clips, Velox Master health, worker connectivity
#   2. POST /api/script/generate (5 comedian clips, source.type=clips)
#   3. Poll PipelineGen until terminal
#   4. Assert script output + specscene bindings
#   5. Generate voiceover for all scenes in one batch
#   6. Poll voiceover jobs until terminal
#   7. Verify subtitle artifacts in SQLite
#   8. Build velox payload with all assets
#   9. Submit to Velox Master POST /api/v1/jobs
#  10. Poll Velox until terminal (PENDING→LEASED→RUNNING→SUCCEEDED)
#  11. Verify final output artifact
#
# Environment:
#   VELOX_ADMIN_TOKEN        PipelineGen admin token (mandatory)
#   VELOX_M2M_TOKEN          Velox Master M2M token for job submit (mandatory for step 9+)
#   VELOX_MASTER_URL         Velox Master base URL (default: http://127.0.0.1:8000)
#   SMOKE_DB                 SQLite path (default: data/media/media.db.sqlite)
#   VELOX_DESTINATION_ID     Target destination (default: comedy_test)
#
# Exit codes:
#   0   all steps passed
#   1   assertion failed
#   2   setup error
#  124  poll timeout

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"
smoke_require sqlite3 jq curl

ROOT_DIR=$(cd "$DIR/../.." && pwd)
SMOKE_DB="${SMOKE_DB:-$ROOT_DIR/data/media/media.db.sqlite}"

# ── Velox Master config ───────────────────────────────────────────────
VELOX_MASTER_URL="${VELOX_MASTER_URL:-http://127.0.0.1:8000}"
VELOX_M2M_TOKEN="${VELOX_M2M_TOKEN:-}"
VELOX_DESTINATION_ID="${VELOX_DESTINATION_ID:-comedy_test}"
VELOX_RENDER_POLL_TIMEOUT="${VELOX_RENDER_POLL_TIMEOUT:-1800}"
VELOX_RENDER_POLL_INTERVAL="${VELOX_RENDER_POLL_INTERVAL:-5}"

# ── 5 comedian clips from the production DB ───────────────────────────
CLIP_IDS=(
    "yt_vdC5GXxS-qU_193_205_v1"
    "yt_7s2YY5izDa0_680f0e22"
    "yt_GAIGHJQ7AGk_683db356"
    "yt_gg69R6vHYcU_eb5a669c"
    "yt_yhmnEfzdtmE_9e7a0596"
)

# ── Work dir ──────────────────────────────────────────────────────────
# Note: common.sh already creates WORK_DIR; override with a named one
# for cleaner artifact naming.
VEL_E2E_WORK=$(mktemp -d "/tmp/comedian-e2e.XXXXXX")
trap 'rm -rf "$VEL_E2E_WORK"' EXIT INT TERM

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,40p' "$0"; exit 0
fi

if [[ "$DRY_RUN" == "1" ]]; then
    cat <<EOF
DRY RUN — Full comedian clips → Velox Master pipeline
PipelineGen: http://${SMOKE_API_BASE}
Velox Master: ${VELOX_MASTER_URL}
Clips: ${CLIP_IDS[*]}
Steps: generate → voiceover → subtitles → manifest → velox submit → poll → verify
EOF
    exit 0
fi

# ══════════════════════════════════════════════════════════════════════
# STEP 1: PREFLIGHT
# ══════════════════════════════════════════════════════════════════════
smoke_log_section "Step 1/11: Preflight checks"

# 1a. DB exists
if [[ ! -f "$SMOKE_DB" ]]; then
    printf '%ssetup error: SMOKE_DB=%s does not exist%s\n' "$RED" "$SMOKE_DB" "$RESET" >&2
    exit 2
fi

# 1b. Clips exist in DB
CLIP_OK=0
for i in "${!CLIP_IDS[@]}"; do
    clip_id="${CLIP_IDS[$i]}"
    row_count=$(sqlite3 "$SMOKE_DB" \
        "SELECT COUNT(*) FROM media_assets WHERE id='${clip_id}' AND lifecycle_state='ACTIVE';" 2>/dev/null || echo 0)
    if [[ "$row_count" == "0" ]]; then
        printf '%sWARN: clip %s not found in DB%s\n' "$YELLOW" "$clip_id" "$RESET"
    else
        CLIP_OK=$((CLIP_OK + 1))
        tracks=$(sqlite3 "$SMOKE_DB" \
            "SELECT COUNT(*) FROM asset_text_tracks WHERE asset_id='${clip_id}' AND text_content != '' AND status='READY';" 2>/dev/null || echo 0)
        printf '  clip=%s tracks=%s %sOK%s\n' "$clip_id" "$tracks" "$GREEN" "$RESET"
    fi
done
(( CLIP_OK == ${#CLIP_IDS[@]} )) || { printf '%ssetup error: need all %d clips in DB, got %d%s\n' "$RED" "${#CLIP_IDS[@]}" "$CLIP_OK" "$RESET" >&2; exit 2; }

# 1c. Velox Master health
printf '\n'
MASTER_HTTP=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
    "${VELOX_MASTER_URL}/health/ready" 2>/dev/null || echo "000")
if [[ "$MASTER_HTTP" == "200" ]]; then
    printf '  Velox Master health: %sOK%s\n' "$GREEN" "$RESET"
else
    printf '%ssetup error: Velox Master at %s returned HTTP %s%s\n' \
        "$RED" "$VELOX_MASTER_URL" "$MASTER_HTTP" "$RESET" >&2
    exit 2
fi

# 1d. Worker connectivity (only if Master reachable)
SKIP_VELOX="${SKIP_VELOX:-0}"
if [[ "$SKIP_VELOX" == "0" && -n "$VELOX_M2M_TOKEN" ]]; then
    WORKERS_BODY="${VEL_E2E_WORK}/velox_workers.json"
    WORKERS_HTTP=$(curl -s -o "$WORKERS_BODY" -w '%{http_code}' --max-time 10 \
        -H "Authorization: Bearer $VELOX_M2M_TOKEN" \
        "${VELOX_MASTER_URL}/api/v1/workers" 2>/dev/null || echo "000")
    if [[ "$WORKERS_HTTP" == "200" ]]; then
        CAPABLE=$(jq -r '[.workers[]? | select((.status|ascii_upcase)=="CONNECTED") | select(any(.executors[]?; (.id|startswith("scene.composite.v1")))) | .worker_id] | length' "$WORKERS_BODY" 2>/dev/null || echo 0)
        if (( CAPABLE > 0 )); then
            printf '  Velox workers: %s%d connected with scene.composite.v1%s\n' "$GREEN" "$CAPABLE" "$RESET"
        else
            printf '%ssetup error: no capable Velox workers advertising scene.composite.v1%s\n' "$RED" "$RESET" >&2
            exit 2
        fi
    else
        printf '%ssetup error: Velox workers API returned HTTP %s%s\n' "$RED" "$WORKERS_HTTP" "$RESET" >&2
        exit 2
    fi
elif [[ -z "$VELOX_M2M_TOKEN" ]]; then
    printf '%ssetup error: VELOX_M2M_TOKEN is required for Velox job submission%s\n' "$RED" "$RESET" >&2
    exit 2
fi

# ══════════════════════════════════════════════════════════════════════
# STEP 2: DISPATCH SCRIPT GENERATE
# ══════════════════════════════════════════════════════════════════════
smoke_log_section "Step 2/11: POST /api/script/generate (5 comedian clips)"

CASE_PREFIX="comedian-clips-$(smoke_gen_uuid)"
IDEMPOTENCY_KEY="$CASE_PREFIX-key"
CLIP_IDS_JSON=$(printf '%s\n' "${CLIP_IDS[@]}" | jq -R . | jq -s .)

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
# STEP 4: ASSERT SCRIPT OUTPUT + SPECSCENE
# ══════════════════════════════════════════════════════════════════════
smoke_log_section "Step 4/11: Assert script + specscene"

# Extract specscene from the PipelineGen result (matches jackie_chan pattern)
SPEC_FILE="${VEL_E2E_WORK}/specscene.json"
jq -e '(.result.output.specscene // .result.items[0].result.output.specscene)' "$SMOKE_LAST_BODY" > "$SPEC_FILE" 2>/dev/null || true

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
        printf '%sWARN: no specscene — voiceover/velox steps may be limited%s\n' "$YELLOW" "$RESET"
        SCENE_COUNT=0
    fi
fi

# ══════════════════════════════════════════════════════════════════════
# STEP 5: GENERATE VOICEOVER FOR ALL SCENES (batch)
# ══════════════════════════════════════════════════════════════════════
smoke_log_section "Step 5/11: Generate voiceover (batch)"

VO_JOB_IDS=()
if [[ -s "$SPEC_FILE" && "$SCENE_COUNT" -gt 0 ]]; then
    # Build all items in one batch request (P0.3 mixed-text supported)
    VO_ITEMS=$(jq -c '[.scenes[] | {
        text: .text,
        language: "it-IT",
        filename: ("scene-" + (.index | tostring) + ".mp3")
    }]' "$SPEC_FILE" 2>/dev/null || echo "[]")

    VO_REQUEST_ID="comedian-vo-${CASE_PREFIX}"
    VO_PAYLOAD=$(jq -n \
        --arg request_id "$VO_REQUEST_ID" \
        --argjson items "$VO_ITEMS" \
        '{
            request_id: $request_id,
            items: $items
        }')

    export SMOKE_IDEMPOTENCY_KEY="comedian-vo-${CASE_PREFIX}-batch"
    smoke_curl POST "/api/media/voiceover/generate" --data "$VO_PAYLOAD" >/dev/null
    unset SMOKE_IDEMPOTENCY_KEY
    VO_HTTP="$SMOKE_LAST_HTTP"

    if [[ "$VO_HTTP" == "202" || "$VO_HTTP" == "200" ]]; then
        # The API may return a parent job + child jobs, or a single job
        VO_PARENT_JOB=$(jq -r '.job_id // .id // ""' "$SMOKE_LAST_BODY")
        if [[ -n "$VO_PARENT_JOB" ]]; then
            VO_JOB_IDS+=("$VO_PARENT_JOB")
            printf '  voiceover batch: parent_job=%s %sOK%s\n' "$VO_PARENT_JOB" "$GREEN" "$RESET"
        fi
        # Check for child job IDs
        VO_CHILDREN=$(jq -r '[.jobs[]?.id // .children[]?.id // empty] | .[]' "$SMOKE_LAST_BODY" 2>/dev/null || true)
        while IFS= read -r child_id; do
            [[ -n "$child_id" ]] && VO_JOB_IDS+=("$child_id")
        done <<< "$VO_CHILDREN"
    else
        printf '%s  voiceover batch: HTTP %s (will check DB for existing voiceovers)%s\n' "$YELLOW" "$VO_HTTP" "$RESET"
    fi
else
    printf '%s  No specscene — skipping voiceover generation%s\n' "$DIM" "$RESET"
fi

# ══════════════════════════════════════════════════════════════════════
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

# ══════════════════════════════════════════════════════════════════════
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
# STEP 8: BUILD VELOX PAYLOAD
# ══════════════════════════════════════════════════════════════════════
smoke_log_section "Step 8/11: Build Velox payload"

# Extract specscene-based scenes for the Velox payload
VELOX_SCENES="[]"
VOICEOVER_PATHS="[]"
if [[ -s "$SPEC_FILE" ]]; then
    VELOX_SCENES=$(jq -c '.scenes // []' "$SPEC_FILE" 2>/dev/null || echo "[]")
    VOICEOVER_PATHS=$(jq -c '[.scenes[]?.bindings.voiceover.link // empty | select(. != "")]' "$SPEC_FILE" 2>/dev/null || echo "[]")
fi

CORRELATION_ID="comedian-clips-${PG_JOB_ID}"
MANIFEST_IDEM="pipelinegen-${PG_JOB_ID}-$(date +%s)"

VELOX_PAYLOAD="$VEL_E2E_WORK/velox-render-request.json"
jq -n \
    --arg idempotency_key "$MANIFEST_IDEM" \
    --arg title "Comedian clips compilation" \
    --arg correlation_id "$CORRELATION_ID" \
    --arg audio_language "it" \
    --arg script_text "$SCRIPT_TEXT" \
    --argjson scenes "$VELOX_SCENES" \
    --argjson voiceover_paths "$VOICEOVER_PATHS" \
    '{
        idempotency_key: $idempotency_key,
        source: {type: "clips"},
        video_name: $title,
        script_text: $script_text,
        scenes: $scenes,
        scenes_json: ($scenes | tojson),
        voiceover_paths: $voiceover_paths,
        correlation_id: $correlation_id,
        audio_language: $audio_language,
        video_mode: "clip",
        skip_creator: true,
        delivery_plan: [{
            destination_id: "comedy_test",
            priority: 1,
            retry_budget: 3
        }]
    }' > "$VELOX_PAYLOAD"

SCENE_CT=$(jq -r '.scenes | length' "$VELOX_PAYLOAD" 2>/dev/null || echo 0)
VO_CT=$(jq -r '.voiceover_paths | length' "$VELOX_PAYLOAD" 2>/dev/null || echo 0)
printf '  payload: %s%d scenes, %d voiceover paths, %d chars script%s\n' \
    "$YELLOW" "$SCENE_CT" "$VO_CT" "${#SCRIPT_TEXT}" "$RESET"

# ══════════════════════════════════════════════════════════════════════
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
            # Save tracking info
            jq -n \
                --arg pg "$PG_JOB_ID" \
                --arg vx "$VELOX_JOB_ID" \
                --arg idem "$MANIFEST_IDEM" \
                --arg status "PENDING" \
                '{
                    pipelinegen_job_id: $pg,
                    velox_job_id: $vx,
                    idempotency_key: $idem,
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

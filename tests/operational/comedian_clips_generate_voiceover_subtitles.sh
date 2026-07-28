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
#   VELOX_MASTER_ADMIN_TOKEN Velox Master admin token for asset upload + worker preflight
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
smoke_require sqlite3 jq curl ffmpeg ffprobe python3

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
WORKERS_AUTH_TOKEN="${VELOX_MASTER_ADMIN_TOKEN:-${VELOX_ADMIN_TOKEN:-$VELOX_M2M_TOKEN}}"
if [[ "$SKIP_VELOX" == "0" && -n "$WORKERS_AUTH_TOKEN" ]]; then
    WORKERS_BODY="${VEL_E2E_WORK}/velox_workers.json"
    WORKERS_HTTP=$(curl -s -o "$WORKERS_BODY" -w '%{http_code}' --max-time 10 \
        -H "Authorization: Bearer $WORKERS_AUTH_TOKEN" \
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
            -vf "scale=1280:-2,fps=30,format=yuv420p" \
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

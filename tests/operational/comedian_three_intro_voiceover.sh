#!/usr/bin/env bash
# Generate three short English intro segments for current comedian clips and
# submit their voiceovers through the canonical batch endpoint.
#
# This is intentionally limited to script + voiceover. It does not submit a
# render job or write directly to a provider. SQLite is used only to discover
# the current asset IDs; all mutations go through PipelineGen APIs.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"
smoke_require sqlite3 jq curl

ROOT_DIR=$(cd "$DIR/../.." && pwd)
SMOKE_DB="${SMOKE_DB:-$ROOT_DIR/data/media/media.db.sqlite}"
VOICEOVER_PROJECT_ID="${VOICEOVER_PROJECT_ID:-comedian-three-intros}"
VOICEOVER_VOICE="${VOICEOVER_VOICE:-en-US-ChristopherNeural}"
VOICEOVER_GROUP="${VOICEOVER_GROUP:-Comedy}"
DOCS_FOLDER_ID="${COMEDY_DOCS_FOLDER_ID:-${VELOX_DRIVE_SCRIPTS_GENERATE:-}}"

if [[ ! -f "$SMOKE_DB" ]]; then
    printf '%ssetup error: SMOKE_DB=%s does not exist%s\n' "$RED" "$SMOKE_DB" "$RESET" >&2
    exit 2
fi
if [[ "$DRY_RUN" != "1" && -z "$DOCS_FOLDER_ID" ]]; then
    printf '%ssetup error: COMEDY_DOCS_FOLDER_ID or VELOX_DRIVE_SCRIPTS_GENERATE is required%s\n' "$RED" "$RESET" >&2
    exit 2
fi

# Select three current, source-backed comedy clips. The canonical lifecycle
# field is lifecycle_state; lifecycle_status is a retired compatibility field
# and must not make deleted Drive rows eligible for a new job.
CLIPS_JSON=$(sqlite3 -json "$SMOKE_DB" <<'SQL'
WITH candidates AS (
  SELECT
    id,
    name,
    COALESCE(NULLIF(local_path,''), NULLIF(download_link,''), NULLIF(source_url,'')) AS source_ref
  FROM media_assets
  WHERE lifecycle_state = 'ACTIVE'
    AND media_type = 'video'
    AND local_path != ''
    AND lower(COALESCE(category,'')) = 'comedy'
  ORDER BY id
)
SELECT id, name, source_ref
FROM candidates
LIMIT 3;
SQL
)

CLIP_COUNT=$(jq 'length' <<<"$CLIPS_JSON")
if [[ "$CLIP_COUNT" != "3" ]]; then
    printf '%ssetup error: expected 3 active local comedy clips; found %s%s\n' \
        "$RED" "$CLIP_COUNT" "$RESET" >&2
    exit 2
fi

CLIP_IDS_JSON=$(jq -c '[.[].id]' <<<"$CLIPS_JSON")
CLIP_CONTEXT=$(jq -r 'map(.name) | join("; ")' <<<"$CLIPS_JSON")
CASE_PREFIX="comedian-three-intros-$(smoke_gen_uuid)"
IDEMPOTENCY_KEY="$CASE_PREFIX-script"
VOICEOVER_REQUEST_ID="$CASE_PREFIX-voiceover"

ITEMS_JSON=$(jq -c --arg prefix "$CASE_PREFIX" '[.[] | {
      id: ($prefix + "-" + (.id | gsub("[^A-Za-z0-9]+"; "-") | .[0:32])),
      title: ("Comedy clip intro — " + .name),
      language: "en",
      tone: "light, conversational comedy commentary",
        style: "Write one short English INTRO for this clip. Use two or three natural sentences and about 50 to 65 words. Describe lightly what the audience is about to see, with a playful but restrained tone. Do not invent context, quotes, outcomes, or biographical facts.",
      source: {
        type: "clips",
        topic: ("Light English intro for comedy clip " + .name),
        clip_ids: [.id],
        num_clips: 1,
        grounding_policy: "clips_primary",
        fallback_policy: "strict",
        source_text: .name,
        guidelines: "English only. Keep this intro concise, factual, and suitable for voiceover. Never claim a detail that is not grounded in the clip metadata or content."
      },
      script_params: {
        target_words: 60,
        min_words: 20,
        segment_words: 40,
        segments: [{
          id: ("intro-" + (.id | gsub("[^A-Za-z0-9]+"; "-"))),
          topic: ("Light English intro for " + .name),
          source_text: .name,
          target_words: 60,
          min_words: 20,
          max_words: 75
        }],
        skip_quality_gate: true,
        use_memory: false
      },
      output: {
        save_to_db: true,
        generate_timeline: true,
        generate_metadata: true,
        extract_entities: false,
        generate_scene_images: false
      }
    }]' <<<"$CLIPS_JSON")

PAYLOAD=$(jq -n \
    --argjson items "$ITEMS_JSON" \
    --arg docs_folder "$DOCS_FOLDER_ID" \
    '{
      version: 2,
      preset: "custom",
      items: ($items | map(. + {docs: {enabled: true, languages: ["it"], folder_id: $docs_folder}}))
    }')

if [[ "$DRY_RUN" == "1" ]]; then
    printf 'DRY RUN — three comedian intro + voiceover job\n'
    printf 'Database: %s\n' "$SMOKE_DB"
    jq -r '.[] | "- \(.id): \(.name)"' <<<"$CLIPS_JSON"
    printf '\nScript payload:\n'
    jq . <<<"$PAYLOAD"
    printf '\nVoiceover policy: group=%s language=en-US voice=%s, one canonical batch request with 3 required items\n' "$VOICEOVER_GROUP" "$VOICEOVER_VOICE"
    exit 0
fi

smoke_log_section "Generate English intro script for three current comedian clips"
export SMOKE_IDEMPOTENCY_KEY="$IDEMPOTENCY_KEY"
smoke_curl POST "/api/script/generate" --data "$PAYLOAD" >/dev/null
unset SMOKE_IDEMPOTENCY_KEY
if [[ "$SMOKE_LAST_HTTP" != "200" && "$SMOKE_LAST_HTTP" != "202" ]]; then
    printf '%sFAIL: script generate HTTP %s%s\n' "$RED" "$SMOKE_LAST_HTTP" "$RESET" >&2
    smoke_echo_safe "$(head -c 800 "$SMOKE_LAST_BODY")" >&2
    exit 1
fi

PG_JOB_ID=$(jq -r '.job_id // ""' "$SMOKE_LAST_BODY")
[[ -n "$PG_JOB_ID" ]] || { echo "FAIL: script response has no job_id" >&2; exit 1; }
smoke_poll_terminal "$PG_JOB_ID" || { echo "FAIL: script polling timed out" >&2; exit 1; }
[[ "$SMOKE_LAST_STATUS" == "completed" || "$SMOKE_LAST_STATUS" == "SUCCEEDED" ]] || {
    printf '%sFAIL: script status=%s%s\n' "$RED" "$SMOKE_LAST_STATUS" "$RESET" >&2
    exit 1
}

SCRIPT_RESULT_DIR="$WORK_DIR/script-results"
mkdir -p "$SCRIPT_RESULT_DIR"
printf '%s' "$SMOKE_LAST_BODY" > "$SCRIPT_RESULT_DIR/parent.json"
CHILD_IDS=$(sqlite3 -noheader "$SMOKE_DB" \
    "SELECT j.value FROM jobs AS p, json_each(json_extract(p.result_json,'\$.data.child_job_ids')) AS j WHERE p.id='${PG_JOB_ID}';" \
    2>/dev/null || true)
CHILD_INDEX=0
while IFS= read -r child_id; do
    [[ -n "$child_id" ]] || continue
    smoke_poll_terminal "$child_id" || { echo "FAIL: script child polling timed out" >&2; exit 1; }
    [[ "$SMOKE_LAST_STATUS" == "completed" || "$SMOKE_LAST_STATUS" == "SUCCEEDED" ]] || {
        printf '%sFAIL: script child %s status=%s%s\n' "$RED" "$child_id" "$SMOKE_LAST_STATUS" "$RESET" >&2
        exit 1
    }
    smoke_curl GET "/api/jobs/${child_id}/full" >/dev/null
    [[ "$SMOKE_LAST_HTTP" == "200" ]] || { echo "FAIL: script child full result unavailable" >&2; exit 1; }
    CHILD_INDEX=$((CHILD_INDEX + 1))
    printf '%s' "$SMOKE_LAST_BODY" > "$SCRIPT_RESULT_DIR/child-${CHILD_INDEX}.json"
done <<< "$CHILD_IDS"

SCENES_JSON=$(sqlite3 -json "$SMOKE_DB" <<SQL | jq -c '[.[] | {id: ("scene-" + ((.sort_order)|tostring)), text: .narrative_text}]'
WITH ranked AS (
  SELECT title, narrative_text,
         ROW_NUMBER() OVER (ORDER BY created_at ASC, id ASC) AS sort_order
  FROM scripts
  WHERE title LIKE 'Comedy clip intro — %'
    AND language = 'en'
    AND created_at >= datetime('now', '-15 minutes')
)
SELECT title, narrative_text, sort_order
FROM ranked
WHERE sort_order <= 3
ORDER BY sort_order;
SQL
)
SPEC_JSON=$(jq -cn --argjson scenes "$SCENES_JSON" '{version: 1, scenes: $scenes}')
[[ "$SCENES_JSON" != "[]" && -n "$SCENES_JSON" ]] || { echo "FAIL: script result has no specscene" >&2; exit 1; }
SCENE_COUNT=$(jq '(.scenes // []) | length' <<<"$SPEC_JSON")
[[ "$SCENE_COUNT" == "3" ]] || { echo "FAIL: expected 3 intro scenes, got $SCENE_COUNT" >&2; exit 1; }

VO_ITEMS=$(jq -c --arg voice "$VOICEOVER_VOICE" '
  [(.scenes // []) | to_entries[] | {
    text: (.value.text // ""),
    language: "en-US",
    voice: $voice,
    filename: ("comedian-intro-" + ((.key + 1)|tostring) + ".mp3"),
    required: true
  }]' <<<"$SPEC_JSON")
if jq -e 'any(.[]; (.text|length) < 1)' <<<"$VO_ITEMS" >/dev/null; then
    echo "FAIL: one or more intro scenes are empty" >&2
    exit 1
fi

VO_PAYLOAD=$(jq -n \
    --arg request_id "$VOICEOVER_REQUEST_ID" \
    --arg project "$VOICEOVER_PROJECT_ID" \
    --arg group "$VOICEOVER_GROUP" \
    --argjson items "$VO_ITEMS" \
    '{
      request_id: $request_id,
      project: $project,
      items: $items,
      destination: {kind: "group", group: $group},
      options: {remove_silence: false, strategy: "verify", parallelism: 3}
    }')

smoke_log_section "Generate three English voiceovers as one canonical batch"
export SMOKE_IDEMPOTENCY_KEY="$VOICEOVER_REQUEST_ID"
smoke_curl POST "/api/media/voiceover/generate" --data "$VO_PAYLOAD" >/dev/null
unset SMOKE_IDEMPOTENCY_KEY
if [[ "$SMOKE_LAST_HTTP" != "200" && "$SMOKE_LAST_HTTP" != "202" ]]; then
    printf '%sFAIL: voiceover generate HTTP %s%s\n' "$RED" "$SMOKE_LAST_HTTP" "$RESET" >&2
    smoke_echo_safe "$(head -c 800 "$SMOKE_LAST_BODY")" >&2
    exit 1
fi

VO_PARENT_JOB_ID=$(jq -r '.job_id // .id // ""' "$SMOKE_LAST_BODY")
[[ -n "$VO_PARENT_JOB_ID" ]] || { echo "FAIL: voiceover response has no parent job_id" >&2; exit 1; }
smoke_poll_terminal "$VO_PARENT_JOB_ID" || { echo "FAIL: voiceover polling timed out" >&2; exit 1; }
[[ "$SMOKE_LAST_STATUS" == "completed" || "$SMOKE_LAST_STATUS" == "SUCCEEDED" ]] || {
    printf '%sFAIL: voiceover parent status=%s%s\n' "$RED" "$SMOKE_LAST_STATUS" "$RESET" >&2
    exit 1
}

printf '%sOK%s script_job=%s voiceover_parent_job=%s scenes=3 destination=%s language=en-US voice=%s\n' \
    "$GREEN" "$RESET" "$PG_JOB_ID" "$VO_PARENT_JOB_ID" "$VOICEOVER_GROUP" "$VOICEOVER_VOICE"

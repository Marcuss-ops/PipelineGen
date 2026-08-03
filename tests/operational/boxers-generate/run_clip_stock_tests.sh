#!/usr/bin/env bash
# Run the real boxer media-mode suites against the local PipelineGen service.
# Authentication is intentionally delegated to scripts/with-velox-auth.
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TEST_DIR="$REPO_DIR/tests/operational/boxers-generate"
API_BASE="${VELOX_BASE_URL:-${SMOKE_API_BASE:-http://127.0.0.1:8000}}"
DB_PATH="${VELOX_DB:-$REPO_DIR/data/media/media.db.sqlite}"
MODE="${1:-all}"
PAYLOAD="${BOXERS_PAYLOAD:-$TEST_DIR/five_boxers_stock_post_segments.json}"
FULL_OUTPUT="${BOXERS_FULL:-}"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

die() {
  echo "FAIL: $*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "comando mancante: $1"
}

case "$MODE" in
  all|clip-only|stock-only|hybrid|hybrid-translate-voiceover) ;;
  *) die "uso: $0 [all|clip-only|stock-only|hybrid|hybrid-translate-voiceover]" ;;
esac

require_command curl
require_command jq
require_command python3
[[ -f "$DB_PATH" ]] || die "database SQLite assente: $DB_PATH"

echo "PipelineGen: $API_BASE"
scripts/with-velox-auth bash -c 'curl -fsS --max-time 15 -H "Authorization: Bearer ${VELOX_ADMIN_TOKEN}" "'"$API_BASE"'/health"' | jq -e '.ok == true' >/dev/null

run_python_suite() {
  local name="$1"
  shift
  echo "--- $name ---"
  scripts/with-velox-auth env VELOX_BASE_URL="$API_BASE" SMOKE_TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-2700}" \
    python3 "$@"
}

run_clip_only() {
  run_python_suite "clip-only: 5 soggetti, 3 clip reali ciascuno" \
    "$TEST_DIR/clip_only_e2e.py" --subjects all --clips 3
}

run_stock_only() {
  run_python_suite "stock-only: 5 soggetti, testo italiano e 3 binding folder ciascuno" \
    "$TEST_DIR/stock_only_e2e.py" --subjects all --scenes 3
}

run_hybrid() {
  [[ -s "$PAYLOAD" ]] || die "payload hybrid assente: $PAYLOAD"
  jq empty "$PAYLOAD"
  local dispatch="$TMP_DIR/hybrid-dispatch.json"
  local full="$TMP_DIR/hybrid-full.json"
  local key="boxers-hybrid-$(date +%s%N)"

  echo "--- hybrid: intro + stock + post clip ---"
  scripts/with-velox-auth bash -c 'curl -fsS --max-time 30 -X POST "'"$API_BASE"'/api/script/generate" \
    -H "Authorization: Bearer ${VELOX_ADMIN_TOKEN}" \
    -H "Content-Type: application/json" \
    -H "Idempotency-Key: '"$key"'" \
    --data-binary @"'"$PAYLOAD"'"' >"$dispatch"

  local job_id
  job_id="$(jq -r '.job_id // empty' "$dispatch")"
  [[ -n "$job_id" ]] || die "hybrid dispatch senza job_id"

  for _ in $(seq 1 "${HYBRID_POLL_LIMIT:-180}"); do
    scripts/with-velox-auth bash -c 'curl -fsS --max-time 15 -H "Authorization: Bearer ${VELOX_ADMIN_TOKEN}" "'"$API_BASE"'/api/jobs/'"$job_id"'/full"' >"$full"
    status="$(jq -r '.status // .job.status // empty' "$full")"
    case "$status" in
      SUCCEEDED|SUCCEEDED_WITH_WARNINGS|COMPLETED) break ;;
      FAILED|CANCELLED) jq '{id,status,progress,error}' "$full" >&2; die "hybrid job $status" ;;
    esac
    sleep 2
  done

  if [[ -n "$FULL_OUTPUT" ]]; then
    cp "$full" "$FULL_OUTPUT"
  fi

  local expected_scenes expected_stock expected_intro expected_stock_ids
  expected_scenes="$(jq '.items[0].script_params.segments | length' "$PAYLOAD")"
  expected_stock="$(jq '.items[0].output.stock_bindings | length' "$PAYLOAD")"
  expected_stock_ids="$(jq '[.items[0].output.stock_bindings[]?.segment_id] | sort' "$PAYLOAD")"
  expected_intro="$(jq 'if .items[0].media_plan.intro.mode == "manual" then (.items[0].media_plan.intro.clips | length) else (.items[0].media_plan.intro.max_clips // (.items[0].media_plan.intro.clips | length)) end' "$PAYLOAD")"

  jq -e '
    (.result.data.items[0].result.output // .job.result.data.items[0].result.output // .result.data.output) as $o
    | ($o.specscene.scenes | length) == $expected_scenes
    and ([$o.specscene.scenes[] | select(.bindings.stock != null)] | length) == $expected_stock
    and ([$o.specscene.scenes[] | select(.bindings.stock != null) | .segment_id] | sort) == $expected_stock_ids
    and all($o.specscene.scenes[] | select(.segment_id != "intro-hook");
      ((.bindings.stock.asset_id // "") | length) > 0
      and ((.bindings.stock.drive_link // "") | length) > 0
      and ((.bindings.clip.clip_id // "") | length) > 0
    )
    and ([$o.specscene.visual_assignments[] | select(.slot == "intro")] | length) == $expected_intro
    and all($expected_posts[];
      . as $post |
      ([$o.specscene.visual_assignments[]
        | select(.slot == "post_segment" and .segment_id == $post.segment_id)] | length) == $post.expected
    )
  ' --argjson expected_scenes "$expected_scenes" \
    --argjson expected_stock "$expected_stock" \
    --argjson expected_stock_ids "$expected_stock_ids" \
    --argjson expected_intro "$expected_intro" \
    --argjson expected_posts "$(jq '[.items[0].media_plan.post_segments[] | {segment_id, expected: (if .mode == "manual" then (.clips | length) else (.max_clips // (.clips | length)) end)}]' "$PAYLOAD")" \
    "$full" >/dev/null || { jq '{id,status,progress,error}' "$full"; die "hybrid contract non valido"; }

  # Explicit segments are also an editorial boundary. A successful job is
  # not enough when one boxer leaks into the next scene: verify the canonical
  # name is present, all five boxer scenes have a usable amount of prose, and
  # no scene contains another boxer's full name.
  jq -e '
    (.result.data.items[0].result.output // .job.result.data.items[0].result.output // .result.data.output) as $o
    | ($o.specscene.scenes | map({key: .segment_id, value: (.text // "")} ) | from_entries) as $text
    | {
        "boxer-mike-tyson": ["mike tyson", ["muhammad ali", "evander holyfield", "floyd mayweather", "sugar ray robinson"]],
        "boxer-muhammad-ali": ["muhammad ali", ["mike tyson", "evander holyfield", "floyd mayweather", "sugar ray robinson"]],
        "boxer-evander-holyfield": ["evander holyfield", ["mike tyson", "muhammad ali", "floyd mayweather", "sugar ray robinson"]],
        "boxer-floyd-mayweather": ["floyd mayweather", ["mike tyson", "muhammad ali", "evander holyfield", "sugar ray robinson"]],
        "boxer-sugar-ray-robinson": ["sugar ray robinson", ["mike tyson", "muhammad ali", "evander holyfield", "floyd mayweather"]]
      } as $rules
    | all($rules | to_entries[];
        .key as $id
        | .value[0] as $primary
        | .value[1] as $forbidden
        | ($text[$id] | ascii_downcase) as $body
        | ($body | split(" ") | map(select(length > 0)) | length) >= 100
        and ($body | contains($primary))
        and all($forbidden[]; . as $name | (($body | contains($name)) | not))
      )
  ' "$full" >/dev/null || { jq '{id,status,progress,error}' "$full"; die "separazione narrativa non valida"; }

  jq --argjson expected_scenes "$expected_scenes" '{id,status,progress,scenes:$expected_scenes,visual_assignments:(.result.data.items[0].result.output.specscene.visual_assignments | length)}' "$full"
}

run_hybrid_translate_voiceover() {
  [[ -n "${BOXERS_VOICEOVER_FOLDER_ID:-}" ]] || die "BOXERS_VOICEOVER_FOLDER_ID è obbligatorio per hybrid-translate-voiceover"
  local before="$TMP_DIR/boxers-before.json"
  local after="$TMP_DIR/boxers-after.json"
  local translated="$TMP_DIR/boxers-translated-payload.json"

  # First establish the visual contract from the same corrected English
  # segment payload, without translation/TTS. The second job changes only
  # text processing and voiceover destination.
  FULL_OUTPUT="$before" run_hybrid

  jq --arg folder "$BOXERS_VOICEOVER_FOLDER_ID" '
    .items[0].language = "en"
    | .items[0].output.languages = ["it"]
    | .items[0].output.translate_to = "it"
    | .items[0].output.voiceover_folder_id = $folder
    | .items[0].docs.languages = ["it"]
  ' "$PAYLOAD" > "$translated"

  local vo_before
  vo_before=$(date -u +'%Y-%m-%d %H:%M:%S')
  local original_payload="$PAYLOAD"
  PAYLOAD="$translated"
  FULL_OUTPUT="$after" run_hybrid
  PAYLOAD="$original_payload"

  jq -S '
    (.result.data.items[0].result.output // .job.result.data.items[0].result.output // .result.data.output)
    | .specscene.visual_assignments
    | map({scene_id, segment_id, slot, asset_id, position, duration_ms})
  ' "$before" > "$TMP_DIR/visual-before.json"
  jq -S '
    (.result.data.items[0].result.output // .job.result.data.items[0].result.output // .result.data.output)
    | .specscene.visual_assignments
    | map({scene_id, segment_id, slot, asset_id, position, duration_ms})
  ' "$after" > "$TMP_DIR/visual-after.json"
  diff -u "$TMP_DIR/visual-before.json" "$TMP_DIR/visual-after.json" >/dev/null \
    || die "la traduzione ha modificato il contratto visuale"

  jq -e '
    (.status // .job.status) == "SUCCEEDED"
    and ((.result.data.items[0].result.output // .job.result.data.items[0].result.output // .result.data.output)
      | ([.specscene.scenes[]?.bindings.voiceover?] | length) == 6
      and all(.specscene.scenes[]; (.bindings.voiceover.status // "") == "completed")
    )
  ' "$after" >/dev/null || die "voiceover boxer incompleto o job non SUCCEEDED"

  local vo_count
  vo_count=$(sqlite3 "$DB_PATH" \
    "SELECT COUNT(*) FROM voiceovers WHERE (language='it' OR language LIKE 'it-%') AND folder_id='${BOXERS_VOICEOVER_FOLDER_ID}' AND datetime(created_at) > datetime('${vo_before}')" 2>/dev/null || echo 0)
  (( vo_count >= 6 )) || die "voiceovers SQLite insufficienti: ${vo_count}/6"

  local vo_text_rows
  vo_text_rows=$(sqlite3 "$DB_PATH" \
    "SELECT COALESCE(text_preview,'') FROM voiceovers WHERE (language='it' OR language LIKE 'it-%') AND folder_id='${BOXERS_VOICEOVER_FOLDER_ID}' AND datetime(created_at) > datetime('${vo_before}') ORDER BY created_at DESC")
  local vo_text_count
  vo_text_count=$(printf '%s\n' "$vo_text_rows" | awk 'NF {n++} END {print n+0}')
  (( vo_text_count >= 6 )) || die "voiceovers senza text_preview tradotto: ${vo_text_count}/6"
  if ! printf '%s\n' "$vo_text_rows" | while IFS= read -r preview; do
    [[ -n "$preview" ]] || exit 1
    printf '%s\n' "$preview" | grep -Eiq '(^|[^[:alpha:]])(attraverso|costruì|trasformò|rappresenta|chiude|campioni|storie|documentario|pugilato|carriere)([^[:alpha:]]|$)' || exit 1
  done; then
    die "voiceover SQLite non contiene anteprime italiane"
  fi

  local vo_path
  vo_path=$(sqlite3 "$DB_PATH" \
    "SELECT local_path FROM voiceovers WHERE (language='it' OR language LIKE 'it-%') AND folder_id='${BOXERS_VOICEOVER_FOLDER_ID}' AND datetime(created_at) > datetime('${vo_before}') ORDER BY created_at DESC LIMIT 1")
  [[ -s "$vo_path" ]] || die "file voiceover boxer mancante o vuoto"
  command -v ffprobe >/dev/null 2>&1 || die "comando mancante: ffprobe"
  ffprobe -v error -show_entries format=duration,size -show_entries stream=codec_type,sample_rate,channels -of json "$vo_path" \
    | jq -e '((.format.duration // "0")|tonumber)>0 and ((.format.size // "0")|tonumber)>0 and ([.streams[]?|select(.codec_type=="audio")]|length)>=1 and all(.streams[]?|select(.codec_type=="audio"); ((.sample_rate // "0")|tonumber)>0 and (.channels // 0)>0)' >/dev/null \
    || die "file audio voiceover boxer non valido"

  echo "PASS: hybrid-translate-voiceover — timeline invariata, 6 scene voiceover, ${vo_count} record SQLite e audio valido"
}

case "$MODE" in
  all)
    run_clip_only
    run_stock_only
    run_hybrid
    ;;
  clip-only) run_clip_only ;;
  stock-only) run_stock_only ;;
  hybrid) run_hybrid ;;
  hybrid-translate-voiceover) run_hybrid_translate_voiceover ;;
esac

echo "PASS: suite $MODE completata"

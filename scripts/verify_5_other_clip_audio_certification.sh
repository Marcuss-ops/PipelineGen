#!/usr/bin/env bash
set -euo pipefail
umask 077

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
source "$ROOT/tests/operational/lib/common.sh"
source "$ROOT/tests/operational/generate/lib/dispatch.sh"
source "$ROOT/tests/operational/generate/lib/result.sh"

ids=(
  1wGMDsvswocVPyNeOjeePC_TcnLjrO-Zv
  1wCoRHdbLHTIzlOf-bbZmvtX5rS63tM2E
  1veGD5IenqHYHNtPAUUBTkc1tcKW5Eq0R
  1uiGZv9f-vDY8OPXZ6vPxhdLLcqtk9V_j
  1npu4pQ-uup9EDkiK8jVA_Z9Zl288xlc2
)
ids_json=$(printf '%s\n' "${ids[@]}" | jq -R . | jq -s .)
brief='Crea esattamente 5 scene narrative in italiano, una per clip e nello stesso ordine. Ogni scena deve contenere un paragrafo reale di 35-90 parole, senza placeholder e senza unire clip. Usa solo le informazioni indicate; non inventare citazioni o fatti non verificati.
CLIP 1: Tom Holland partecipa a una conversazione o intervista tra attori. Descrivi il suo atteggiamento spontaneo, il ritmo del confronto e il rapporto tra leggerezza e professionalità.
CLIP 2: Robert Downey Jr. è ripreso in un momento di intervista. Racconta la sua presenza scenica, l esperienza e il modo in cui l ironia può rendere vivace una conversazione sul mestiere.
CLIP 3: Jacob Elordi partecipa a un incontro tra attori. Descrivi il tono del dialogo, l ascolto e le riflessioni sul lavoro davanti alla macchina da presa, senza attribuire parole precise.
CLIP 4: Adam Driver interviene in una conversazione professionale. Metti in evidenza concentrazione, disciplina e il contrasto tra serietà del mestiere e naturalezza del confronto.
CLIP 5: Jamie Foxx ricorda un esperienza legata al cinema e alla crescita artistica. Descrivi il tono personale, la memoria e l energia narrativa del momento senza inventare dettagli.
Restituisci solo i cinque paragrafi in ordine.'

payload=$(jq -nc --argjson ids "$ids_json" --arg brief "$brief" --arg folder "${DOC_FOLDER_ID:-}" '{
  version: 2, preset: "custom", force_refresh: true,
  items: [{
    id: "cert-5-other-clips-edge-audio",
    title: "5 Altri Clip Audio Certification", language: "it",
    project: "cert-5-other-clips-edge-audio",
    source: {type: "clips", source_text: $brief, clip_ids: $ids, num_clips: 5,
      grounding_policy: "clips_primary", fallback_policy: "allow_prose", ordering_strategy: "input_order"},
    script_params: {target_words: 350, segment_words: 70, use_memory: false, skip_quality_gate: true},
    output: {save_to_db: true, extract_entities: true, generate_timeline: true,
      voiceover_enabled: true, generate_metadata: false, generate_scene_images: false, render_video: false},
    audio: {mode: "COMBINED_TIMELINE", timing: {mode: "required", boundary: "word", formats: ["json", "srt", "vtt"]}},
    docs: {enabled: true, languages: ["it"], folder_id: $folder}
  }]
}')

export SMOKE_TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-1800}"
export SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-1800}"
generate_dispatch "$payload" "cert-5-other-clips-$(smoke_gen_uuid)"
printf 'JOB_ID=%s\n' "$GENERATE_JOB_ID"
generate_poll_and_fetch
export GENERATE_REQUIRE_SCRIPT=1
generate_extract_result
result="$GENERATE_RESULT"
scenes=$(jq '(.scenes // .output.specscene.scenes // []) | length' <<<"$result")
accepted=$(jq '(.source_trace.accepted_clip_ids // .source.accepted_clip_ids // []) | length' <<<"$result")
voiceovers=$(jq '[.scenes[].voiceover.it.id // empty] | length' <<<"$result")
doc=$(jq -r '.documents.it.link // empty' <<<"$result")
render_plan=$(jq 'if has("render_plan") then .render_plan else null end' <<<"$result")
printf 'SCENES=%s/5 ACCEPTED_CLIPS=%s/5 VOICEOVERS=%s/5 DOC=%s VIDEO_RENDER_PLAN=%s\n' "$scenes" "$accepted" "$voiceovers" "$([[ -n "$doc" ]] && echo PASS || echo FAIL)" "$render_plan"
[[ "$scenes" == 5 && "$accepted" == 5 && "$voiceovers" == 5 && -n "$doc" && "$render_plan" == null ]]
printf 'CERTIFIED_5_SCENES_AUDIO_ONLY\n'

#!/usr/bin/env bash
# verify_10_clip_audio_certification.sh — real 10-clip audio-only battery.
#
# The battery intentionally exercises the explicit clips source, not catalog
# search. It certifies one accepted clip -> one scene -> one Edge TTS voiceover
# and one Rust master M4A, while RenderVideo remains false.
#
# Usage:
#   scripts/verify_10_clip_audio_certification.sh <clip_id>... (exactly 10)
#   DOC_FOLDER_ID=<drive-folder> scripts/verify_10_clip_audio_certification.sh <10 ids>
#   scripts/verify_10_clip_audio_certification.sh --dry <10 ids>
#
# The report keeps these axes independent:
#   clip total duration, clip used duration, Edge TTS duration, scene duration.
set -euo pipefail
umask 077

for early_arg in "$@"; do
    case "$early_arg" in
        --dry|-h|--help) export SMOKE_DRY_RUN=1 ;;
    esac
done

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
SMOKE_LIB="$ROOT/tests/operational/lib/common.sh"
GENERATE_LIB_DIR="$ROOT/tests/operational/generate/lib"
[[ -f "$SMOKE_LIB" ]] || { echo "setup error: $SMOKE_LIB not found" >&2; exit 2; }
ORIGINAL_ARGS=("$@")
set --
# shellcheck disable=SC1091
source "$SMOKE_LIB"
# shellcheck disable=SC1091
source "$GENERATE_LIB_DIR/dispatch.sh"
# shellcheck disable=SC1091
source "$GENERATE_LIB_DIR/result.sh"
set -- "${ORIGINAL_ARGS[@]}"

DRY=0
IDS=()
while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry) DRY=1; shift ;;
        -h|--help) sed -n '2,18p' "${BASH_SOURCE[0]}"; exit 0 ;;
        *) IDS+=("$1"); shift ;;
    esac
done
[[ "${#IDS[@]}" -eq 10 ]] || {
    echo "setup error: exactly 10 clip IDs are required (got ${#IDS[@]})" >&2
    exit 2
}
ids_json=$(printf '%s\n' "${IDS[@]}" | jq -R . | jq -s .)
doc_folder=${DOC_FOLDER_ID:-}
run_name=${CERT_RUN_NAME:-10 Clip Audio Certification}
run_id=${CERT_RUN_ID:-cert-10-clips-edge-audio}
source_brief=$(printf '%s\n' \
    'Write exactly 10 Italian narration scenes, one per clip, using the clips in input_order. Each scene must be a real editorial paragraph of 35-100 words, never a placeholder such as "The" or "Scene N". Use the following verified clip briefs as grounding; do not invent people or events beyond them.' \
    'CLIP 1 11MRzjKA3o7OZGmYZGJMPTGxM_eNt6qAX: Paul Giamatti condivide riflessioni durante un incontro tra attori. La sua presenza scenica appare naturale anche fuori dal palcoscenico, mentre il tono spontaneo e il ritmo della conversazione mostrano il talento e l energia di un artista abituato a trasformare un momento informale in una storia coinvolgente.' \
    'CLIP 2 11_5vtQgxOfFdBnsC8FQnCu0UQ2WpfO-c: Andrew Scott partecipa a una conversazione vivace e parla delle proprie esperienze nel settore artistico. Il testo deve raccontare il suo approccio al mestiere, l attenzione alle sfumature della performance e il modo in cui una risposta personale può rendere interessante anche un semplice dialogo tra colleghi.' \
    'CLIP 3 128AEiKFTwEZJO4dBUbhe4hu7bzbLAR-W: una conversazione tra attori procede con scambi vivaci e un tono professionale ma rilassato. Descrivi il valore dell esperienza condivisa, la naturalezza del confronto e l energia che nasce quando persone abituate al palcoscenico discutono del proprio lavoro davanti al pubblico.' \
    'CLIP 4 1jDRQz8zDFjg86RpgSuxUIHuIE-DKtuET: il momento mostra l energia di uno scambio tra colleghi durante una discussione informale. Racconta il ritmo della conversazione, la curiosità reciproca e il modo in cui l umorismo spontaneo rende più vicino il rapporto tra gli attori e chi ascolta.' \
    'CLIP 5 1HX3_LUx4Yg-mLaQkukKPrKTlEibuPz98: Tom Holland risponde in modo schietto e giocoso durante un quiz dedicato alla collaborazione con Zendaya. Evidenzia il ritmo leggero delle domande, la complicità del contesto e la capacità dell attore di coinvolgere il pubblico mantenendo una naturalezza divertente anche quando il tema riguarda il lavoro sul set.' \
    'CLIP 6 1w_wGC43vY4wGtoBOnCErB_r-Hls_bdtO: Adam Sandler appare rilassato in un altro momento di intervista e sembra pronto a condividere aneddoti divertenti con gli altri partecipanti al tavolo rotondo. Descrivi il suo umorismo, la familiarità del tono e il contrasto tra la semplicità della conversazione e la ricchezza delle esperienze raccontate.' \
    'CLIP 7 1dRv4WFUcXgLUf3QvqheZ9pCqvJY3KcFu: Adam Sandler continua a parlare del mondo dello spettacolo con osservazioni spontanee e ricordi personali. Il testo deve collegare il sorriso, il ritmo informale e la sua capacità di trasformare un commento quotidiano in un momento riconoscibile, caldo e divertente per gli altri attori e per il pubblico.' \
    'CLIP 8 1Pwng-iqQAVS5VZJmVNGHMBLQBFLsNyOU: Jeffrey Wright riflette sul processo creativo e sulla disciplina necessaria per recitare. Presenta il suo punto di vista con un tono acuto ma accessibile, mostrando come la preparazione, l ascolto dei colleghi e la precisione del lavoro possano convivere con la spontaneità di una conversazione tra professionisti.' \
    'CLIP 9 1xWrSFJSg5K5D_hZTxKILhnddvFytWaHI: Jeffrey Wright approfondisce con gli stessi colleghi alcuni temi del proprio percorso professionale. Racconta una riflessione personale sulla collaborazione, sulla costruzione dei personaggi e sulle scelte che rendono credibile una performance, senza attribuire al filmato dichiarazioni non presenti nel brief.' \
    'CLIP 10 1qo69v-Kwuouxyr38-rdsqbS1G53exsFW: Andrew Scott torna a parlare in una fase diversa dell evento e affronta la complessità del ruolo dell attore moderno. Il testo deve concentrarsi sulle sfumature della performance, sull equilibrio tra tecnica e sensibilità e sul valore di un confronto capace di lasciare una conclusione riflessiva.' \
    'Return only the 10 scene paragraphs in order. Preserve one-to-one clip binding and do not merge clips.')

payload=$(jq -nc \
    --argjson ids "$ids_json" \
    --arg folder "$doc_folder" \
    --arg run_name "$run_name" \
    --arg run_id "$run_id" \
    --arg brief "$source_brief" \
    '{
      version: 2,
      preset: "custom",
      force_refresh: true,
      items: [{
        id: $run_id,
        title: $run_name,
        language: "it",
        project: $run_id,
        source: {
          type: "clips",
          source_text: $brief,
          clip_ids: $ids,
          num_clips: 10,
          grounding_policy: "clips_primary",
          fallback_policy: "allow_prose",
          ordering_strategy: "input_order"
        },
        script_params: {
          target_words: 800,
          segment_words: 80,
          use_memory: false,
          skip_quality_gate: true
        },
        output: {
          save_to_db: true,
          extract_entities: true,
          generate_timeline: true,
          voiceover_enabled: true,
          generate_metadata: false,
          generate_scene_images: false,
          render_video: false
        },
        audio: {
          mode: "COMBINED_TIMELINE",
          timing: {mode: "required", boundary: "word", formats: ["json", "srt", "vtt"]}
        },
        docs: {enabled: true, languages: ["it"], folder_id: $folder}
      }]
    }')

if [[ "$DRY" == 1 ]]; then
    jq . <<<"$payload"
    exit 0
fi

export SMOKE_TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-1800}"
export SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-1800}"
key="${run_id}-$(smoke_gen_uuid)"
generate_dispatch "$payload" "$key"
generate_poll_and_fetch
export GENERATE_REQUIRE_SCRIPT=0
generate_extract_result

result="$GENERATE_RESULT"
accepted=$(jq '[((.source_trace.accepted_clip_ids // .source.accepted_clip_ids // [])[]) ] | length' <<<"$result")
scenes=$(jq '[((.scenes // .output.specscene.scenes // [])[]?)] | length' <<<"$result")
[[ "$accepted" == 10 ]] || { echo "FAIL: accepted clips=$accepted (want 10)" >&2; exit 1; }
[[ "$scenes" == 10 ]] || { echo "FAIL: scenes=$scenes (want 10)" >&2; exit 1; }

format_us() {
    local us="$1" total_ms ms sec min
    total_ms=$((us / 1000)); ms=$((total_ms % 1000)); sec=$(((total_ms / 1000) % 60)); min=$((total_ms / 60000))
    printf '%02d:%02d.%03d' "$min" "$sec" "$ms"
}

failures=0
clip_sum_us=0
vo_total_us=0
timeline_sum_us=0
previous_end_us=0
printf '\n10 CLIP AUDIO CERTIFICATION\n'
printf '%-5s %-13s %-13s %-13s %-13s %s\n' SCENE 'CLIP TOTAL' 'CLIP USED' 'EDGE VO' 'SCENE' 'WORDS'

for ((i = 0; i < 10; i++)); do
    scene=$(jq -c "(.scenes // .output.specscene.scenes // [])[$i]" <<<"$result")
    clip_id=$(jq -r '.clip.id // empty' <<<"$scene")
    expected_id=$(jq -r "(.source_trace.accepted_clip_ids // .source.accepted_clip_ids // [])[$i] // empty" <<<"$result")
    [[ "$clip_id" == "$expected_id" ]] || { echo "FAIL scene $((i+1)): clip order $clip_id != $expected_id" >&2; failures=$((failures+1)); }
    [[ -n "$clip_id" ]] || { echo "FAIL scene $((i+1)): missing clip binding" >&2; failures=$((failures+1)); }

    clip_total=$(jq -r '.clip.duration // 0' <<<"$scene")
    clip_total_one_us=$(awk -v s="$clip_total" 'BEGIN {printf "%.0f", s*1000000}')
    used_us=$(jq -r ".canonical_timeline.segments[$i].video.source_duration_us // 0" <<<"$result")
    source_in_us=$(jq -r ".canonical_timeline.segments[$i].video.source_in_us // 0" <<<"$result")
    if [[ "$used_us" -le 0 ]]; then
        used_us="$clip_total_one_us"
        source_in_us=0
    fi
    scene_us=$(jq -r ".canonical_timeline.segments[$i].duration_us // 0" <<<"$result")
    vo_us=$(jq -r ".scenes[$i].voiceover.it.duration // 0" <<<"$result" | awk '{printf "%.0f", $1*1000000}')
    words=$(jq ".scenes[$i].voiceover.it.timing.words // [] | length" <<<"$result")
    [[ "$clip_total_one_us" -gt 0 ]] || { echo "FAIL scene $((i+1)): clip total duration unknown" >&2; failures=$((failures+1)); }
    [[ "$used_us" -gt 0 ]] || { echo "FAIL scene $((i+1)): clip used duration unknown" >&2; failures=$((failures+1)); }
    [[ "$vo_us" -gt 0 && "$words" -gt 0 ]] || { echo "FAIL scene $((i+1)): Edge timing incomplete" >&2; failures=$((failures+1)); }

    clip_sum_us=$((clip_sum_us + clip_total_one_us))
    vo_total_us=$((vo_total_us + vo_us))
    timeline_sum_us=$((timeline_sum_us + scene_us))
    end_us=$(( $(jq -r ".canonical_timeline.segments[$i].timeline_start_us // 0" <<<"$result") + scene_us ))
    [[ "$end_us" -eq $((previous_end_us + scene_us)) ]] || { echo "FAIL scene $((i+1)): non-contiguous timeline" >&2; failures=$((failures+1)); }
    previous_end_us="$end_us"
    printf '%-5d %-13s %-13s %-13s %-13s %s\n' "$((i+1))" "$(format_us "$clip_total_one_us")" "$(format_us "$used_us")" "$(format_us "$vo_us")" "$(format_us "$scene_us")" "$words"
    printf '      clip=%s source_in=%s\n' "$clip_id" "$(format_us "$source_in_us")"
    first=$(jq -r ".scenes[$i].voiceover.it.timing.words[0].start_us // 0" <<<"$result")
    last=$(jq -r ".scenes[$i].voiceover.it.timing.words[-1].end_us // 0" <<<"$result")
    printf '      Edge boundaries: %s → %s\n' "$(format_us "$first")" "$(format_us "$last")"
done

timeline_us=$(jq -r '.canonical_timeline.duration_us // 0' <<<"$result")
final_audio_us=$(jq -r '.final_audio.duration_us // ((.final_audio.duration_ms // 0) * 1000) // 0' <<<"$result")
[[ "$timeline_sum_us" == "$timeline_us" ]] || { echo "FAIL: timeline sum != canonical duration" >&2; failures=$((failures+1)); }
[[ "$vo_total_us" == "$timeline_us" ]] || { echo "FAIL: Edge VO sum != canonical duration" >&2; failures=$((failures+1)); }
if [[ "$final_audio_us" -gt 0 ]]; then
    delta=$((final_audio_us - timeline_us)); delta=${delta#-}
    [[ "$delta" -le 100000 ]] || { echo "FAIL: final audio differs from timeline by ${delta}us" >&2; failures=$((failures+1)); }
else
    echo "FAIL: final_audio duration missing" >&2; failures=$((failures+1))
fi
[[ "$(jq 'has("render_plan") and .render_plan != null' <<<"$result")" == false ]] || { echo "FAIL: RenderPlan is not nil" >&2; failures=$((failures+1)); }
[[ "$(jq 'has("render_job") and .render_job != null' <<<"$result")" == false ]] || { echo "FAIL: video render job exists" >&2; failures=$((failures+1)); }
doc_ok=$(jq -e '.documents.it.id? and .documents.it.link?' <<<"$result" >/dev/null 2>&1; echo $?)
[[ "$doc_ok" == 0 ]] || { echo "FAIL: Google Doc reference missing" >&2; failures=$((failures+1)); }

printf '\nTOTAL SOURCE CLIPS = %s (%dus)\n' "$(format_us "$clip_sum_us")" "$clip_sum_us"
printf 'TOTAL EDGE TTS     = %s (%dus)\n' "$(format_us "$vo_total_us")" "$vo_total_us"
printf 'CANONICAL TIMELINE = %s (%dus)\n' "$(format_us "$timeline_us")" "$timeline_us"
printf 'FINAL AUDIO        = %s (%dus)\n' "$(format_us "$final_audio_us")" "$final_audio_us"
printf 'CLIPS=%s/10 SCENES=%s/10 VOICEOVERS=%s/10 DOC=%s VIDEO_RENDER_JOBS=0\n' "$accepted" "$scenes" "$(jq '[.scenes[].voiceover.it.id // empty] | length' <<<"$result")" "$([[ "$doc_ok" == 0 ]] && echo PASS || echo FAIL)"
if [[ "$failures" -ne 0 ]]; then
    printf 'CERTIFICATION FAILED (%d assertions)\n' "$failures" >&2
    exit 1
fi
printf 'CERTIFIED\n'

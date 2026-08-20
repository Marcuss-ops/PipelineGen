#!/usr/bin/env bash
# Test script.generate + clip.render on ten indexed Drive clips.
#
# Default: dry-run.  --apply submits script.generate (no DB/document write).
# --render additionally renders each clip under DRIVE_FOLDER_ID with:
#   - watermark text: testComedy
#   - burned-in subtitles
#   - 1080x1920 @ 60 fps
#   - execution.require_gpu=true (rejects the software fallback)
# Each clip gets its own temporary subfolder, scheduled for Drive Trash after
# 24 hours. Originals are never overwritten.
#
# Usage:
#   scripts/test_comedy_10_clips.sh                 # print payloads
#   scripts/test_comedy_10_clips.sh --apply         # generate the script
#   scripts/test_comedy_10_clips.sh --apply --render

set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
API_BASE="${VELOX_BASE_URL:-http://127.0.0.1:8000}"
SUBTITLE_LANGUAGE="${SUBTITLE_LANGUAGE:-en}"
APPLY=0
RENDER=0
DRIVE_FOLDER_ID="${DRIVE_FOLDER_ID:-1ST6FxPuRaxwBOIz39MAN8Jj4gDv509-K}"
POLL_LIMIT="${POLL_LIMIT:-180}"
POLL_SECONDS="${POLL_SECONDS:-2}"
RUN_ID="test-comedy-10-clips-$(date -u +%Y%m%dT%H%M%SZ)"
OUT_DIR="${TEST_COMEDY_OUT_DIR:-$(mktemp -d /tmp/test-comedy-10-clips.XXXXXX)}"

CLIP_IDS=(
  yt_BSnPkOM-38M_25_85_v1
  yt_ul3x2_RoBZQ_113_165_v1
  yt_d-QswCgdbO0_605_660_v1
  yt_JeUFrZtKkn8_358_395_v1
  yt_BSnPkOM-38M_500_515_v1
  yt_ISpsfZxhnbs_175_200_v1
  yt_2Hls8pQuLgM_461_500_v1
  yt_sB_JZLp521g_142_175_v1
  yt_JeUFrZtKkn8_880_924_v1
  yt_lPts1HI3vGo_393_447_v1
)

TITLES=(
  "Audizione in corridoio e tensione spontanea"
  "Incontro con Emerald Fennell e ricordi di set"
  "Bob Dylan, nastri segreti e paura di esporsi"
  "La gara di bolognese raccontata con ironia"
  "Una ragazza interrompe il risveglio della scena"
  "Karaoke, Kid Cudi e una scelta fuori programma"
  "Una domanda sul primo bacio evita la risposta facile"
  "Fellini, Toby Dammit e il bluff dell'attore"
  "Un trucco per il tatuaggio diventa un aneddoto"
  "Musical, Sweet Charity e il dettaglio del colore degli occhi"
)

DESCRIPTIONS=(
  "Durante un’audizione in corridoio, l’attore affronta una situazione improvvisata con attenzione e nervosismo controllato. Il momento mostra come una risposta semplice possa trasformarsi in una prova di presenza scenica, tra ascolto, esitazione e naturalezza davanti agli altri."
  "La conversazione con Emerald Fennell ricorda un incontro di lavoro diventato rapidamente informale. Il racconto mette al centro la collaborazione, l’energia degli scambi e il modo in cui un episodio di set può rivelare il carattere delle persone coinvolte."
  "Il racconto sui nastri segreti di Bob Dylan costruisce una piccola storia di timore e curiosità. L’intervento alterna memoria personale e osservazione sul mestiere, mostrando come una scelta artistica possa sembrare rischiosa prima di diventare un’occasione di libertà."
  "Una gara di bolognese diventa il punto di partenza per parlare di competitività e umorismo. Il ritmo leggero della scena trasforma un gesto quotidiano in un ricordo riconoscibile, con una comicità basata più sull’entusiasmo che sulla ricerca della battuta perfetta."
  "Un’interruzione inattesa cambia il tono del momento e rende la scena più viva. L’episodio mostra la capacità di reagire rapidamente, mantenere il personaggio e trasformare un piccolo imprevisto in una situazione comica senza perdere il filo della performance."
  "Il karaoke sulle note di Kid Cudi porta la conversazione verso un terreno spontaneo e imprevedibile. La scena racconta il piacere di uscire dal copione, condividere una scelta musicale e usare l’ironia per rendere più vicino il rapporto con il pubblico."
  "Una domanda sul primo bacio riceve una risposta prudente e divertita. Il momento funziona perché lascia spazio al non detto, mostrando come il controllo dell’immagine pubblica possa convivere con l’umorismo e con una complicità evidente tra intervistatore e ospite."
  "Il riferimento a Fellini e Toby Dammit diventa una riflessione sul confine tra recitazione e inganno. La scena mette in evidenza il valore del bluff, della misura e della capacità di suggerire più di quanto venga spiegato apertamente."
  "Un consiglio sul tatuaggio viene raccontato come un aneddoto assurdo ma concreto. Il tono resta leggero mentre la storia mostra come un’esperienza personale possa diventare materiale narrativo, soprattutto quando il dettaglio inatteso cambia completamente l’interpretazione del gesto."
  "Il confronto passa dai musical a Sweet Charity e arriva a un dettaglio fisico curioso. La scena mostra come riferimenti tecnici, memoria cinematografica e osservazioni personali possano convivere in una conversazione rapida, precisa e accessibile."
)

usage() {
  sed -n '2,16p' "${BASH_SOURCE[0]}"
  cat <<'USAGE'

Options:
  --apply    submit script.generate
  --render   with --apply, render all ten clips to DRIVE_FOLDER_ID
  -h|--help  show this help
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --apply) APPLY=1; shift ;;
    --render) RENDER=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

(( RENDER == 0 || APPLY == 1 )) || { echo "--render requires --apply" >&2; exit 2; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }
command -v curl >/dev/null || { echo "curl is required" >&2; exit 2; }
command -v python3 >/dev/null || { echo "python3 is required" >&2; exit 2; }
command -v systemd-run >/dev/null || { echo "systemd-run is required for 24h cleanup" >&2; exit 2; }
mkdir -p "$OUT_DIR"

for i in "${!CLIP_IDS[@]}"; do
  words=$(wc -w <<<"${DESCRIPTIONS[$i]}")
  (( words <= 50 )) || { echo "description $((i+1)) has $words words" >&2; exit 1; }
done

clip_ids_json=$(printf '%s\n' "${CLIP_IDS[@]}" | jq -R . | jq -s .)
source_text=""
for i in "${!CLIP_IDS[@]}"; do
  source_text+="CLIP $((i+1)) — ${CLIP_IDS[$i]} — ${DESCRIPTIONS[$i]}"
  source_text+=$'\n'
done

  jq -n \
  --arg run_id "$RUN_ID" \
  --arg folder "$DRIVE_FOLDER_ID" \
  --argjson clip_ids "$clip_ids_json" \
  --arg source_text "$source_text" \
  '{version:2,preset:"custom",force_refresh:true,items:[{
    id:$run_id,title:"Test comedy — dieci clip",language:"it",
    tone:"documentario breve, ironico e preciso",
    style:"Scrivi una scena italiana autonoma per ogni clip. Usa solo il brief associato, massimo 50 parole per scena, nessuna invenzione e mantieni l ordine input_order.",
    source:{type:"clips",clip_ids:$clip_ids,num_clips:10,source_text:$source_text,
      grounding_policy:"clips_primary",fallback_policy:"strict",ordering_strategy:"input_order"},
    script_params:{target_words:500,min_words:100,segment_words:50,use_memory:false,force_refresh:true},
    output:{save_to_db:false,extract_entities:false,generate_metadata:false,generate_timeline:true,voiceover_enabled:false,render_video:false,drive_folder_id:$folder},
    docs:{enabled:true,languages:["it"],folder_id:$folder}
  }]}' > "$OUT_DIR/script_generate_payload.json"

render_payload() {
  local clip_id="$1" index="$2" folder="$3"
  jq -n --arg clip "$clip_id" --arg lang "$SUBTITLE_LANGUAGE" --arg folder "$folder" \
    '{source_asset_id:$clip,
      watermark:{enabled:true,text:"testComedy",position:"top_right",opacity:0.85,margin_px:40},
      transcript:{mode:"reuse",language:$lang,persist:false},
      subtitles:{enabled:true,mode:"burn",style_id:"shorts-v1"},
      output:{contract:"velox-editing-clip-v1",width:1080,height:1920,fps_num:24,fps_den:1},
      audio:{mode:"copy_if_compatible"},destination:{drive_folder_id:$folder},
      execution:{require_gpu:true}}' > "$OUT_DIR/render_${index}_${clip_id}.json"
}

ensure_test_folder() {
  local clip_id="$1" index="$2" name folder_json folder_id unit
  name="testComedy_${RUN_ID}_video_${index}_${clip_id}"
  folder_json="$OUT_DIR/folder_${index}.json"
  python3 "$ROOT/scripts/tools/drive_test_folder.py" ensure \
    --parent "$DRIVE_FOLDER_ID" --name "$name" > "$folder_json"
  folder_id=$(jq -r '.id // empty' "$folder_json")
  [[ -n "$folder_id" ]] || { cat "$folder_json" >&2; return 1; }
  unit="pipelinegen-test-comedy-${RUN_ID}-${index}"
  systemd-run --user --quiet --collect --unit="$unit" --on-active=24h \
    /usr/bin/python3 "$ROOT/scripts/tools/drive_test_folder.py" trash --folder-id "$folder_id" >/dev/null
  printf '%s\n' "$folder_id"
}

auth_post_file() {
  local url="$1" file="$2" response="$3" request_id="${4:-$RUN_ID}"
  "$ROOT/scripts/with-velox-auth" bash -c \
    'curl -sS --max-time 60 -X POST "$1" -H "Authorization: Bearer ${VELOX_ADMIN_TOKEN}" -H "Content-Type: application/json" -H "X-Request-ID: $3" -H "Idempotency-Key: $3" --data-binary @"$2"' \
    bash "$url" "$file" "$request_id" > "$response"
}

auth_get() {
  local url="$1" response="$2"
  "$ROOT/scripts/with-velox-auth" bash -c \
    'curl -fsS --max-time 60 -H "Authorization: Bearer ${VELOX_ADMIN_TOKEN}" "$1"' \
    bash "$url" > "$response"
}

poll_job() {
  local job_id="$1" label="$2" response status
  response="$OUT_DIR/${label}_${job_id}_full.json"
  for attempt in $(seq 1 "$POLL_LIMIT"); do
    auth_get "$API_BASE/api/jobs/$job_id/full" "$response"
    status=$(jq -r '.status // .job.status // empty' "$response")
    echo "[$label] attempt=$attempt status=$status"
    case "$status" in
      SUCCEEDED|SUCCEEDED_WITH_WARNINGS|COMPLETED) printf '%s\n' "$response"; return 0 ;;
      FAILED|CANCELLED|DEAD_LETTER|dead_letter) jq . "$response" >&2; return 1 ;;
    esac
    sleep "$POLL_SECONDS"
  done
  echo "timeout polling $label job $job_id" >&2
  return 124
}

echo "Output directory: $OUT_DIR"
echo "script.generate payload: $OUT_DIR/script_generate_payload.json"
jq '.items[0] | {id,title,language,clip_count:(.source.clip_ids|length),descriptions:(.source.source_text|split("\n")|map(select(length>0))|length),save_to_db:.output.save_to_db}' "$OUT_DIR/script_generate_payload.json"

if (( APPLY == 0 )); then
  echo "Dry-run: no API job submitted. Add --apply to test generation."
  if (( RENDER == 1 )); then echo "Internal error: render requested in dry-run" >&2; exit 2; fi
  exit 0
fi

auth_post_file "$API_BASE/api/script/generate" "$OUT_DIR/script_generate_payload.json" "$OUT_DIR/script_generate_response.json"
script_job=$(jq -r '.job_id // empty' "$OUT_DIR/script_generate_response.json")
[[ -n "$script_job" ]] || { jq . "$OUT_DIR/script_generate_response.json" >&2; exit 1; }
script_result=$(poll_job "$script_job" script | tail -n 1)
echo "script.generate completed: $script_result"
doc_id=$(jq -r '[.. | objects | select(.documents?.it?.id?) | .documents.it.id] | last // empty' "$script_result")
[[ -n "$doc_id" ]] || echo "warning: generated script did not return a Google Doc id" >&2

if (( RENDER == 0 )); then
  echo "Script test completed. Add --render with DRIVE_FOLDER_ID to render watermark/subtitle clips."
  exit 0
fi

for i in "${!CLIP_IDS[@]}"; do
  n=$(printf '%02d' "$((i+1))")
  clip="${CLIP_IDS[$i]}"
  folder=$(ensure_test_folder "$clip" "$n")
  render_payload "$clip" "$n" "$folder"
  auth_post_file "$API_BASE/api/clips/render" "$OUT_DIR/render_${n}_${clip}.json" "$OUT_DIR/render_${n}_${clip}_response.json" "${RUN_ID}-render-${n}-${clip}"
  job=$(jq -r '.job_id // empty' "$OUT_DIR/render_${n}_${clip}_response.json")
  [[ -n "$job" ]] || { jq . "$OUT_DIR/render_${n}_${clip}_response.json" >&2; exit 1; }
  result=$(poll_job "$job" "render_${n}_${clip}" | tail -n 1)
  generated_link=$(jq -r '[.. | objects | select(.asset?.drive_link?) | .asset.drive_link] | last // empty' "$result")
  original_link=$(jq -r --arg clip "$clip" '[.. | objects | select(.clip?.id == $clip and .clip.drive_link?) | .clip.drive_link] | first // empty' "$script_result")
  if [[ -n "$doc_id" && -n "$original_link" && -n "$generated_link" ]]; then
    python3 "$ROOT/scripts/tools/drive_test_folder.py" replace-doc-link \
      --document-id "$doc_id" --old-link "$original_link" --new-link "$generated_link" \
      > "$OUT_DIR/doc_replace_${n}.json"
  fi
  jq -c --arg clip "$clip" --argjson n "$((i+1))" \
    '{clip_number:$n,clip_id:$clip,status:(.status//.job.status),backend:(.result.render.backend//.job.result.render.backend//"unknown"),watermark:(.result.watermark//.job.result.watermark//"check_output"),subtitles:(.result.subtitles//.job.result.subtitles//"check_output"),output:(.result.asset.drive_link//.job.result.asset.drive_link//"")}' \
    "$result"
done

echo "Rendered 10 clips with testComedy + burned subtitles; outputs are in $OUT_DIR and the configured Drive folder."

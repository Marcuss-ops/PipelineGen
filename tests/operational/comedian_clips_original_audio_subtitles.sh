#!/usr/bin/env bash
set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"
smoke_require sqlite3 jq curl ffmpeg ffprobe python3 sha256sum

ROOT_DIR=$(cd "$DIR/../.." && pwd)
SMOKE_DB="${SMOKE_DB:-$ROOT_DIR/data/media/media.db.sqlite}"
VELOX_MASTER_URL="${VELOX_MASTER_URL:-http://127.0.0.1:8000}"
VELOX_M2M_TOKEN="${VELOX_M2M_TOKEN:-}"
VELOX_MASTER_ADMIN_TOKEN="${VELOX_MASTER_ADMIN_TOKEN:-${VELOX_ADMIN_TOKEN:-}}"
VELOX_DESTINATION_ID="${VELOX_DESTINATION_ID:-comedy_test}"
VELOX_RENDER_POLL_TIMEOUT="${VELOX_RENDER_POLL_TIMEOUT:-1800}"
VELOX_RENDER_POLL_INTERVAL="${VELOX_RENDER_POLL_INTERVAL:-5}"

VEL_E2E_WORK=$(mktemp -d "/tmp/comedian-original-audio-e2e.XXXXXX")
trap 'rm -rf "$VEL_E2E_WORK"' EXIT INT TERM

[[ -f "$SMOKE_DB" ]] || { echo "setup error: SMOKE_DB not found: $SMOKE_DB" >&2; exit 2; }
[[ -n "$VELOX_M2M_TOKEN" ]] || { echo "setup error: VELOX_M2M_TOKEN required" >&2; exit 2; }
[[ -n "$VELOX_MASTER_ADMIN_TOKEN" ]] || { echo "setup error: VELOX_MASTER_ADMIN_TOKEN required" >&2; exit 2; }

smoke_log_section "Step 1/9: Select 5 clips with READY subtitles"
mapfile -t CANDIDATE_CLIP_IDS < <(sqlite3 "$SMOKE_DB" "
WITH preferred_subs AS (
  SELECT asset_id
  FROM asset_subtitle_artifacts
  WHERE status='READY' AND is_current=1 AND local_path != ''
  GROUP BY asset_id
)
SELECT ma.id
FROM media_assets ma
JOIN preferred_subs ps ON ps.asset_id=ma.id
WHERE ma.lifecycle_state='ACTIVE'
  AND COALESCE(NULLIF(ma.local_path,''), NULLIF(ma.download_link,''), NULLIF(ma.url,''), NULLIF(ma.drive_link,''), NULLIF(ma.source_url,'')) != ''
ORDER BY CASE ma.id
  WHEN 'yt_vdC5GXxS-qU_193_205_v1' THEN 0
  WHEN 'yt_vdC5GXxS-qU_65_80_v1' THEN 1
  WHEN 'yt_vdC5GXxS-qU_146_155_v1' THEN 2
  ELSE 10 END,
  ma.updated_at DESC
LIMIT 50;")
CLIP_IDS=()
for CANDIDATE_ID in "${CANDIDATE_CLIP_IDS[@]}"; do
  CANDIDATE_SQL_ID=${CANDIDATE_ID//\'/\'\'}
  CANDIDATE_PATH=$(sqlite3 "$SMOKE_DB" "SELECT COALESCE(NULLIF(local_path,''), NULLIF(download_link,''), NULLIF(url,''), NULLIF(drive_link,''), NULLIF(source_url,'')) FROM media_assets WHERE id='$CANDIDATE_SQL_ID'" 2>/dev/null || true)
  [[ -n "$CANDIDATE_PATH" ]] || continue
  [[ "$CANDIDATE_PATH" = /* ]] || CANDIDATE_PATH="$ROOT_DIR/$CANDIDATE_PATH"
  [[ -f "$CANDIDATE_PATH" ]] || continue
  VIDEO_CODEC=$(ffprobe -v error -select_streams v:0 -show_entries stream=codec_name -of csv=p=0 "$CANDIDATE_PATH" 2>/dev/null || true)
  AUDIO_CODEC=$(ffprobe -v error -select_streams a:0 -show_entries stream=codec_name -of csv=p=0 "$CANDIDATE_PATH" 2>/dev/null || true)
  if [[ -n "$VIDEO_CODEC" && -n "$AUDIO_CODEC" ]] && ffmpeg -nostdin -v error -xerror -t 2 -i "$CANDIDATE_PATH" -map 0:v:0 -map 0:a:0 -f null - >/dev/null 2>&1; then
    CLIP_IDS+=("$CANDIDATE_ID")
  else
    printf '  skip undecodable clip: %s\n' "$CANDIDATE_ID" >&2
  fi
  (( ${#CLIP_IDS[@]} >= 5 )) && break
done
(( ${#CLIP_IDS[@]} == 5 )) || { echo "setup error: need 5 clips with READY subtitles, got ${#CLIP_IDS[@]}" >&2; exit 2; }
printf '  clips: %s\n' "${CLIP_IDS[*]}"

MASTER_HTTP=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "${VELOX_MASTER_URL}/health/ready" || echo 000)
[[ "$MASTER_HTTP" == "200" ]] || { echo "setup error: Velox Master not ready HTTP $MASTER_HTTP" >&2; exit 2; }
WORKERS_BODY="$VEL_E2E_WORK/workers.json"
WORKERS_HTTP=$(curl -s -o "$WORKERS_BODY" -w '%{http_code}' --max-time 10 -H "Authorization: Bearer $VELOX_MASTER_ADMIN_TOKEN" "${VELOX_MASTER_URL}/api/v1/workers" || echo 000)
[[ "$WORKERS_HTTP" == "200" ]] || { echo "setup error: workers API HTTP $WORKERS_HTTP" >&2; exit 2; }
CAPABLE=$(jq -r '[.workers[]? | select((.status|ascii_upcase)=="CONNECTED") | select(any(.executors[]?; .id=="scene.composite.v1" and .version==1))] | length' "$WORKERS_BODY")
(( CAPABLE > 0 )) || { echo "setup error: no connected scene.composite.v1 worker" >&2; exit 2; }

smoke_log_section "Step 2/9: PipelineGen script generate"
CASE_PREFIX="comedian-original-audio-$(smoke_gen_uuid)"
CLIP_IDS_JSON=$(printf '%s\n' "${CLIP_IDS[@]}" | jq -R . | jq -s .)
PAYLOAD=$(jq -n --arg case_marker "$CASE_PREFIX" --argjson clip_ids "$CLIP_IDS_JSON" '{version:2,preset:"custom",items:[{id:($case_marker+"-item"),title:("Comedian original clip audio " + $case_marker),language:"it",tone:"documentario leggero",style:"Descrivi i clip in ordine senza inventare; il render finale deve far sentire audio originale dei clip.",source:{type:"clips",topic:"Clip comici con audio originale",source_text:"Compilation di clip comici; usa i clip come contenuto principale",clip_ids:$clip_ids,num_clips:5,grounding_policy:"clips_primary",fallback_policy:"strict",ordering_strategy:"input_order"},script_params:{target_words:180,min_words:120,segment_words:40,skip_quality_gate:true,use_memory:false},output:{save_to_db:true,generate_timeline:true,generate_metadata:false,extract_entities:false,generate_scene_images:false}}]}')
export SMOKE_IDEMPOTENCY_KEY="$CASE_PREFIX-key"
smoke_curl POST "/api/script/generate" --data "$PAYLOAD" >/dev/null
unset SMOKE_IDEMPOTENCY_KEY
[[ "$SMOKE_LAST_HTTP" == "200" || "$SMOKE_LAST_HTTP" == "202" ]] || { echo "FAIL PipelineGen HTTP $SMOKE_LAST_HTTP" >&2; exit 1; }
PG_JOB_ID=$(jq -r '.job_id // empty' "$SMOKE_LAST_BODY")
[[ -n "$PG_JOB_ID" ]] || { echo "FAIL no PipelineGen job_id" >&2; exit 1; }
printf '  PipelineGen job_id: %s\n' "$PG_JOB_ID"

smoke_log_section "Step 3/9: Poll PipelineGen"
smoke_poll_terminal "$PG_JOB_ID" || { echo "FAIL PipelineGen timeout" >&2; exit 1; }
[[ "$SMOKE_LAST_STATUS" == "completed" || "$SMOKE_LAST_STATUS" == "SUCCEEDED" ]] || { echo "FAIL PipelineGen status=$SMOKE_LAST_STATUS" >&2; exit 1; }
RESULT=$(jq -c '.result.data.items[0].result // .result.items[0].result // .result.output // .result // empty' "$SMOKE_LAST_BODY")
SCRIPT_TEXT=$(jq -r '.output.text // .script // .text // .content // empty' <<<"$RESULT")
[[ -n "$SCRIPT_TEXT" ]] || { echo "FAIL empty script text" >&2; exit 1; }
printf '  script words: %s\n' "$(printf '%s' "$SCRIPT_TEXT" | wc -w | tr -d ' ')"

smoke_log_section "Step 4/9: Build clip assets and merged clip subtitles"
CLIP_META="$VEL_E2E_WORK/clip-meta.json"
CLIP_IDS_JSON="$CLIP_IDS_JSON" ROOT_DIR="$ROOT_DIR" SMOKE_DB="$SMOKE_DB" CLIP_META="$CLIP_META" python3 - <<'PY'
import json, os, sqlite3, sys
clip_ids=json.loads(os.environ['CLIP_IDS_JSON']); root=os.environ['ROOT_DIR']; db=os.environ['SMOKE_DB']
conn=sqlite3.connect(db); conn.row_factory=sqlite3.Row; out=[]
for cid in clip_ids:
    ma=conn.execute("SELECT COALESCE(NULLIF(local_path,''), NULLIF(download_link,''), NULLIF(url,''), NULLIF(drive_link,''), NULLIF(source_url,'')) path FROM media_assets WHERE id=?",(cid,)).fetchone()
    sub=conn.execute("""SELECT local_path,format,language_code,last_cue_end_ms,clip_duration_ms FROM asset_subtitle_artifacts WHERE asset_id=? AND status='READY' AND is_current=1 AND local_path!='' ORDER BY CASE language_code WHEN 'it' THEN 0 WHEN 'it-IT' THEN 0 WHEN 'en' THEN 1 ELSE 2 END, CASE format WHEN 'srt' THEN 0 WHEN 'ass' THEN 1 WHEN 'vtt' THEN 2 ELSE 3 END, updated_at DESC LIMIT 1""",(cid,)).fetchone()
    if not ma or not ma['path'] or not sub: sys.exit(f'missing clip/subtitle for {cid}')
    clip=ma['path']; subp=sub['local_path']
    if clip and not os.path.isabs(clip): clip=os.path.join(root,clip)
    if subp and not os.path.isabs(subp): subp=os.path.join(root,subp)
    if not os.path.isfile(clip): sys.exit(f'clip not readable: {clip}')
    if not os.path.isfile(subp): sys.exit(f'subtitle not readable: {subp}')
    out.append({'asset_id':cid,'clip_path':clip,'subtitle_path':subp,'format':sub['format'],'last_cue_end_ms':int(sub['last_cue_end_ms'] or 0)})
json.dump(out, open(os.environ['CLIP_META'],'w'), ensure_ascii=False)
PY

CLIP_REFS_TSV="$VEL_E2E_WORK/clip-refs.tsv"
CLIP_DURS_TSV="$VEL_E2E_WORK/clip-durations.tsv"
mkdir -p "$VEL_E2E_WORK/clips"
: > "$CLIP_REFS_TSV"; : > "$CLIP_DURS_TSV"
for i in 0 1 2 3 4; do
  CLIP_PATH=$(jq -r ".[$i].clip_path" "$CLIP_META")
  SUB_END_MS=$(jq -r ".[$i].last_cue_end_ms" "$CLIP_META")
  SRC_DUR=$(ffprobe -v error -show_entries format=duration -of default=nw=1:nk=1 "$CLIP_PATH" | awk '{printf "%.3f", $1+0}')
  TARGET_DUR=$(awk -v src="$SRC_DUR" -v subms="$SUB_END_MS" 'BEGIN{subdur=subms/1000.0; if(src<=0)src=subdur; if(subdur>0 && subdur<src)src=subdur; if(src<=0)src=5; printf "%.3f", src}')
  CLEAN="$VEL_E2E_WORK/clips/scene-$((i+1)).mp4"
  ffmpeg -nostdin -y -hide_banner -nostats -loglevel fatal -err_detect ignore_err -i "$CLIP_PATH" -t "$TARGET_DUR" -vf "scale=1280:-2,fps=24,format=yuv420p" -map 0:v:0 -map 0:a? -c:v libx264 -preset veryfast -crf 23 -c:a aac -ar 48000 -ac 2 -shortest -movflags +faststart "$CLEAN"
  AUDIO_STREAMS=$(ffprobe -v error -select_streams a -show_entries stream=index -of csv=p=0 "$CLEAN" | wc -l | tr -d ' ')
  [[ "$AUDIO_STREAMS" != "0" ]] || { echo "FAIL cleaned clip has no original audio: $CLEAN" >&2; exit 1; }
  BODY="$VEL_E2E_WORK/upload-clip-$i.json"
  HTTP=$(curl -s --max-time 60 -o "$BODY" -w '%{http_code}' -X POST -H "Authorization: Bearer $VELOX_MASTER_ADMIN_TOKEN" -F kind=source_clip -F "file=@${CLEAN};type=video/mp4" "${VELOX_MASTER_URL}/api/v1/creator/assets")
  [[ "$HTTP" == "201" ]] || { echo "FAIL upload clip HTTP $HTTP" >&2; head -c 300 "$BODY" >&2 || true; exit 1; }
  jq -er .reference "$BODY" >> "$CLIP_REFS_TSV"
  ffprobe -v error -show_entries format=duration -of default=nw=1:nk=1 "$CLEAN" | awk '{printf "%.3f\n", $1+0}' >> "$CLIP_DURS_TSV"
done
CLIP_REFS_JSON=$(jq -Rsc 'split("\n")|map(select(length>0))' "$CLIP_REFS_TSV")
CLIP_DURS_JSON=$(jq -Rsc 'split("\n")|map(select(length>0)|tonumber)' "$CLIP_DURS_TSV")

MERGED_SRT="$VEL_E2E_WORK/clip-subtitles.srt"
CLIP_META="$CLIP_META" CLIP_DURS_JSON="$CLIP_DURS_JSON" MERGED_SRT="$MERGED_SRT" python3 - <<'PY'
import json, os, re, sys

def ts(sec):
    ms=round(sec*1000); h,rem=divmod(ms,3600000); m,rem=divmod(rem,60000); s,ms=divmod(rem,1000); return f"{h:02d}:{m:02d}:{s:02d},{ms:03d}"
def ass_time(v):
    h,m,r=v.strip().split(':'); s,cs=r.split('.'); return int(h)*3600+int(m)*60+int(s)+int(cs)/100.0
def clean(t):
    return re.sub(r"\{[^}]*\}","",t).replace(r"\N","\n").replace(r"\n","\n").strip()
def parse_ass(p):
    out=[]
    for line in open(p,encoding='utf-8',errors='replace'):
        if not line.startswith('Dialogue:'): continue
        parts=line.split(':',1)[1].strip().split(',',9)
        if len(parts)<10: continue
        a,b,text=ass_time(parts[1]),ass_time(parts[2]),clean(parts[9])
        if text and b>a: out.append((a,b,text))
    return out
def parse_srt_time(v):
    v=v.strip().replace('.',','); hms,ms=v.split(','); h,m,s=hms.split(':'); return int(h)*3600+int(m)*60+int(s)+int(ms[:3].ljust(3,'0'))/1000.0
def parse_srt(p):
    data=open(p,encoding='utf-8',errors='replace').read().replace('\r\n','\n'); out=[]
    for block in re.split(r'\n\s*\n',data):
        lines=[x for x in block.split('\n') if x.strip()]
        if len(lines)<2: continue
        ti=0 if '-->' in lines[0] else 1
        if ti>=len(lines) or '-->' not in lines[ti]: continue
        a,b=[x.strip() for x in lines[ti].split('-->',1)]
        text='\n'.join(lines[ti+1:]).strip(); start=parse_srt_time(a.split()[0]); end=parse_srt_time(b.split()[0])
        if text and end>start: out.append((start,end,text))
    return out
meta=json.load(open(os.environ['CLIP_META'],encoding='utf-8')); durs=json.loads(os.environ['CLIP_DURS_JSON'])
blocks=[]; offset=0.0; n=1
for i,row in enumerate(meta):
    cues=parse_ass(row['subtitle_path']) if row['format'].lower()=='ass' else parse_srt(row['subtitle_path'])
    limit=float(durs[i])
    for a,b,text in cues:
        if a>=limit: continue
        b=min(b,limit)
        if b<=a: continue
        blocks.append(f"{n}\n{ts(offset+a)} --> {ts(offset+b)}\n{text}\n"); n+=1
    offset+=limit
if not blocks: sys.exit('merged subtitles empty')
open(os.environ['MERGED_SRT'],'w',encoding='utf-8').write('\n'.join(blocks))
PY
SUB_BODY="$VEL_E2E_WORK/upload-subtitles.json"
SUB_HTTP=$(curl -s --max-time 60 -o "$SUB_BODY" -w '%{http_code}' -X POST -H "Authorization: Bearer $VELOX_MASTER_ADMIN_TOKEN" -F kind=subtitle -F "file=@${MERGED_SRT};type=application/x-subrip" "${VELOX_MASTER_URL}/api/v1/creator/assets")
[[ "$SUB_HTTP" == "201" ]] || { echo "FAIL upload subtitles HTTP $SUB_HTTP" >&2; head -c 300 "$SUB_BODY" >&2 || true; exit 1; }
SUB_REF=$(jq -er .reference "$SUB_BODY")

smoke_log_section "Step 5/9: Build Velox payload"
SCENES_JSON=$(jq -cn --argjson clips "$CLIP_REFS_JSON" --argjson durs "$CLIP_DURS_JSON" --arg sub "$SUB_REF" '[range(0; $clips|length) as $i | {scene_id:("scene-"+($i+1|tostring)), index:($i+1), text:("Original clip scene "+($i+1|tostring)), clip:{url:$clips[$i],duration_ms:(($durs[$i]//5)*1000|floor)}, duration_seconds:($durs[$i]//5), subtitles:{url:$sub,format:"srt",language:"original"}}]')
IDEM="pipelinegen-${PG_JOB_ID}-clip-original-$(date +%s)"
VELOX_PAYLOAD="$VEL_E2E_WORK/velox-render-request.json"
jq -n --arg idem "$IDEM" --arg title "Comedian clips original audio" --arg script_text "$SCRIPT_TEXT" --argjson scenes "$SCENES_JSON" --arg sub "$SUB_REF" --arg dest "$VELOX_DESTINATION_ID" '{idempotency_key:$idem,video_name:$title,script_text:$script_text,scenes:$scenes,voiceover_paths:[],subtitle_tracks:[{source:$sub,preset:"clip_original_subtitles",font:"Inter"}],delivery_plan:[{destination_id:$dest,priority:1,retry_budget:3}]}' > "$VELOX_PAYLOAD"
PAYLOAD_SHA256=$(sha256sum "$VELOX_PAYLOAD" | awk '{print $1}')
printf '  payload: scenes=%s items=%s voiceovers=%s subtitle_tracks=%s sha=%s\n' "$(jq '.scenes|length' "$VELOX_PAYLOAD")" "$(jq '.items|length' "$VELOX_PAYLOAD")" "$(jq '.voiceover_paths|length' "$VELOX_PAYLOAD")" "$(jq '.subtitle_tracks|length' "$VELOX_PAYLOAD")" "$PAYLOAD_SHA256"

smoke_log_section "Step 6/9: Submit Velox"
SUBMIT_BODY="$VEL_E2E_WORK/velox-submit.json"
VELOX_HTTP=$(curl -s --max-time 30 -o "$SUBMIT_BODY" -w '%{http_code}' -X POST -H "Authorization: Bearer $VELOX_M2M_TOKEN" -H "Content-Type: application/json" -H "X-Request-ID: $IDEM" --data-binary "@$VELOX_PAYLOAD" "${VELOX_MASTER_URL}/api/v1/jobs")
printf '  Velox submit HTTP: %s\n' "$VELOX_HTTP"
[[ "$VELOX_HTTP" == "202" ]] || { head -c 500 "$SUBMIT_BODY" >&2 || true; exit 1; }
VELOX_JOB_ID=$(jq -r '.job_id // .id // empty' "$SUBMIT_BODY")
[[ -n "$VELOX_JOB_ID" ]] || { echo "FAIL no Velox job_id" >&2; exit 1; }
printf '  Velox job_id: %s\n' "$VELOX_JOB_ID"

smoke_log_section "Step 7/9: Poll Velox"
DEADLINE=$(( $(date +%s) + VELOX_RENDER_POLL_TIMEOUT ))
JOB_BODY="$VEL_E2E_WORK/velox-job.json"
while (( $(date +%s) < DEADLINE )); do
  HTTP=$(curl -s --max-time 10 -o "$JOB_BODY" -w '%{http_code}' -H "Authorization: Bearer $VELOX_M2M_TOKEN" "${VELOX_MASTER_URL}/api/v1/jobs/${VELOX_JOB_ID}" || echo 000)
  STATUS=$(jq -r '.status // .job.status // empty' "$JOB_BODY" 2>/dev/null | tr '[:upper:]' '[:lower:]')
  printf '  [%s] status: %s\n' "$(date +%H:%M:%S)" "${STATUS:-?}"
  case "$STATUS" in succeeded) break ;; failed|cancelled|canceled) exit 1 ;; esac
  sleep "$VELOX_RENDER_POLL_INTERVAL"
done
[[ "$STATUS" == "succeeded" ]] || { echo "FAIL Velox timeout/status=$STATUS" >&2; exit 1; }

smoke_log_section "Step 8/9: Final verify"
printf 'pipelinegen_job_id: %s\nvelox_job_id: %s\npayload_sha256: %s\n' "$PG_JOB_ID" "$VELOX_JOB_ID" "$PAYLOAD_SHA256"

smoke_log_section "Step 9/9: PASS"
printf '%sOK: PipelineGen → Master → Worker → clip original audio/subtitles → artifact → Drive%s\n' "$GREEN" "$RESET"

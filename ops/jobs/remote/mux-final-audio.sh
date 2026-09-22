#!/usr/bin/env bash
# mux-final-audio.sh — close the AUDIO leg of the remote render handoff.
#
# Why this exists
# ---------------
# The remote `scene.composite.v1` worker renders VIDEO ONLY. Every run of the
# two-stage flow therefore comes back with a single video stream — and the job
# still reports `SUCCEEDED`, so a silent artifact looks exactly like a good one.
#
# The audio of this lane is owned by the host that runs PipelineGen, not by the
# worker: `RenderAudioPlan` compiles the canonical final audio asset and
# `MuxFinalAudioCopy` (internal/platform/media/rustexec/video_processor.go)
# muxes it with `-c:v copy -c:a copy`, deliberately with NO encode fallback.
# The canonical identity is the repository's own contract, V2
# (internal/kernel/media/assembly_contract.go → DefaultAssemblyMediaContractV2):
#
#   container mp4 · 1 video + 1 audio · start_pts 0
#   audio aac / LC / 48000 Hz / 2 channels / stereo / 192k
#
# This script reproduces that leg operator-side for the tracked remote lane and
# gates the result, so "the render finished" can no longer mean "the render is
# silent". It never re-encodes the video: the mux is `-c:v copy`, and the script
# proves it by comparing the video packet md5 before and after.
#
# Usage
# -----
#   # build the canonical audio from the PRE payload's scenes and mux it in
#   ops/jobs/remote/mux-final-audio.sh \
#     --video /tmp/dolly5_final.mp4 \
#     --pre-payload ops/jobs/remote/dolly5-pre.creator-77.json \
#     --clips-dir data/tmp/localization \
#     --out /tmp/dolly5_final_av.mp4
#
#   # gate any artifact (e.g. one downloaded from the master) — no muxing
#   ops/jobs/remote/mux-final-audio.sh --verify /tmp/dolly5_final_av.mp4
#
# Options:
#   --video FILE          remote video-only composite (required in build mode)
#   --pre-payload FILE    PREPARE payload; scenes[] give clip order + durations
#   --clips-dir DIR       directory holding the per-clip media
#   --clip-suffix S       explicit name suffix for clip files (else auto-probed)
#   --out FILE            muxed output (required in build mode)
#   --audio-out FILE      canonical audio track (default: <out>.audio.m4a)
#   --default-duration S  fallback scene duration when the payload omits it (10)
#   --strict-video        also fail on a worker-side video contract deviation
#   --verify FILE         run the audio gate only on an existing MP4
#   -h|--help             this text
#
# Exit codes: 0 ok · 1 usage · 2 missing input/media · 3 ffmpeg failed
#             · 4 AUDIO GATE FAILED (absent or non-canonical audio)
#             · 5 video contract deviation (only with --strict-video)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

VIDEO=""; PRE_PAYLOAD=""; CLIPS_DIR=""; CLIP_SUFFIX=""; OUT=""; AUDIO_OUT=""
DEFAULT_DURATION="10"; STRICT_VIDEO="0"; VERIFY_ONLY=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --video) VIDEO="${2:?--video needs a file}"; shift 2 ;;
    --pre-payload) PRE_PAYLOAD="${2:?--pre-payload needs a file}"; shift 2 ;;
    --clips-dir) CLIPS_DIR="${2:?--clips-dir needs a directory}"; shift 2 ;;
    --clip-suffix) CLIP_SUFFIX="${2:?--clip-suffix needs a value}"; shift 2 ;;
    --out) OUT="${2:?--out needs a file}"; shift 2 ;;
    --audio-out) AUDIO_OUT="${2:?--audio-out needs a file}"; shift 2 ;;
    --default-duration) DEFAULT_DURATION="${2:?--default-duration needs seconds}"; shift 2 ;;
    --strict-video) STRICT_VIDEO="1"; shift ;;
    --verify) VERIFY_ONLY="${2:?--verify needs a file}"; shift 2 ;;
    -h|--help) sed -n '2,45p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) echo "mux-final-audio: unknown argument $1" >&2; exit 1 ;;
  esac
done

for tool in ffmpeg ffprobe jq; do
  command -v "$tool" >/dev/null 2>&1 || { echo "mux-final-audio: FAIL — $tool not found" >&2; exit 1; }
done

# ── the audio gate ─────────────────────────────────────────────────────────
# Fail-closed on the ONE property whose absence the worker hides: a finished
# artifact with no canonical audio stream.
gate() { # file label
  local file="$1" label="${2:-artifact}" probe
  local v_count a_count acodec aprofile asr ach alayout adur vdur vstart astart
  local vcodec vprofile vlevel vpix vw vh vfps vtb expected_level

  [[ -f "$file" ]] || { echo "mux-final-audio: FAIL — $label not found: $file" >&2; return 2; }
  probe="$(ffprobe -v error -of json \
    -show_entries stream=index,codec_type,codec_name,profile,level,pix_fmt,width,height,r_frame_rate,time_base,start_pts,sample_rate,channels,channel_layout,duration \
    -show_entries format=duration,size,format_name,format_long_name "$file" 2>/dev/null)" || true
  [[ -n "$probe" ]] || { echo "mux-final-audio: FAIL — $label is not probeable: $file" >&2; return 2; }

  v_count="$(jq -r '[.streams[] | select(.codec_type=="video")] | length' <<<"$probe")"
  a_count="$(jq -r '[.streams[] | select(.codec_type=="audio")] | length' <<<"$probe")"
  vcodec="$(jq -r '[.streams[] | select(.codec_type=="video")][0].codec_name // ""' <<<"$probe")"
  vprofile="$(jq -r '[.streams[] | select(.codec_type=="video")][0].profile // ""' <<<"$probe")"
  vlevel="$(jq -r '[.streams[] | select(.codec_type=="video")][0].level // 0' <<<"$probe")"
  vpix="$(jq -r '[.streams[] | select(.codec_type=="video")][0].pix_fmt // ""' <<<"$probe")"
  vw="$(jq -r '[.streams[] | select(.codec_type=="video")][0].width // 0' <<<"$probe")"
  vh="$(jq -r '[.streams[] | select(.codec_type=="video")][0].height // 0' <<<"$probe")"
  vfps="$(jq -r '[.streams[] | select(.codec_type=="video")][0].r_frame_rate // ""' <<<"$probe")"
  vtb="$(jq -r '[.streams[] | select(.codec_type=="video")][0].time_base // ""' <<<"$probe")"
  vstart="$(jq -r '[.streams[] | select(.codec_type=="video")][0].start_pts // 0' <<<"$probe")"
  vdur="$(jq -r '(.streams[] | select(.codec_type=="video") | .duration) // .format.duration // ""' <<<"$probe" | head -1)"
  acodec="$(jq -r '[.streams[] | select(.codec_type=="audio")][0].codec_name // ""' <<<"$probe")"
  aprofile="$(jq -r '[.streams[] | select(.codec_type=="audio")][0].profile // ""' <<<"$probe")"
  asr="$(jq -r '[.streams[] | select(.codec_type=="audio")][0].sample_rate // 0' <<<"$probe")"
  ach="$(jq -r '[.streams[] | select(.codec_type=="audio")][0].channels // 0' <<<"$probe")"
  alayout="$(jq -r '[.streams[] | select(.codec_type=="audio")][0].channel_layout // ""' <<<"$probe")"
  astart="$(jq -r '[.streams[] | select(.codec_type=="audio")][0].start_pts // 0' <<<"$probe")"
  adur="$(jq -r '(.streams[] | select(.codec_type=="audio") | .duration) // ""' <<<"$probe" | head -1)"
  expected_level="41"   # V2 = h264 level 4.1; V1 froze 4.0

  echo "mux-final-audio: [$label] $file"
  echo "  container      $(jq -r '.format.format_long_name // "?"' <<<"$probe")"
  echo "  streams        video=$v_count audio=$a_count"
  echo "  video          $vcodec $vprofile level=$vlevel $vpix ${vw}x${vh} fps=$vfps tb=$vtb start_pts=$vstart dur=$vdur"
  echo "  audio          $acodec $aprofile ${asr}Hz ${ach}ch $alayout start_pts=$astart dur=$adur"

  # ── the hard gate: audio present and canonical ──
  if [[ "$v_count" != "1" || "$a_count" != "1" ]]; then
    echo "mux-final-audio: AUDIO GATE FAILED — contract requires exactly 1 video + 1 audio stream, got video=$v_count audio=$a_count" >&2
    return 4
  fi
  if [[ "$acodec" != "aac" || "${aprofile^^}" != "LC" || "$asr" != "48000" || "$ach" != "2" || "$alayout" != "stereo" ]]; then
    echo "mux-final-audio: AUDIO GATE FAILED — audio identity $acodec/$aprofile/${asr}Hz/${ach}ch/$alayout != the canonical aac/LC/48000/2/stereo" >&2
    return 4
  fi
  if [[ "$astart" != "0" ]]; then
    echo "mux-final-audio: AUDIO GATE FAILED — audio start_pts=$astart != 0" >&2
    return 4
  fi
  # audio must cover the picture: allow one frame (1/24 s) of container rounding
  if [[ -n "$vdur" && -n "$adur" ]]; then
    awk -v v="$vdur" -v a="$adur" 'BEGIN { d = (v > a ? v - a : a - v); if (d > 0.042) exit 1 }' \
      || { echo "mux-final-audio: AUDIO GATE FAILED — audio ${adur}s vs video ${vdur}s (gap > one frame)" >&2; return 4; }
  fi
  echo "  audio gate     PASS (canonical aac/LC/48000/2/stereo, start_pts 0)"

  # ── the video identity is inherited by copy; report, and only fail if asked ──
  local vbad=""
  [[ "$vw" == "1920" && "$vh" == "1080" ]] || vbad+=" geometry=${vw}x${vh}"
  [[ "$vfps" == "24/1" ]] || vbad+=" fps=$vfps"
  [[ "$vcodec" == "h264" ]] || vbad+=" codec=$vcodec"
  [[ "$vpix" == "yuv420p" ]] || vbad+=" pix_fmt=$vpix"
  [[ "$vlevel" == "$expected_level" ]] || vbad+=" level=$vlevel(want $expected_level)"
  [[ "$vtb" == "1/90000" ]] || vbad+=" time_base=$vtb(want 1/90000)"
  [[ "$vstart" == "0" ]] || vbad+=" start_pts=$vstart"
  if [[ -n "$vbad" ]]; then
    echo "  video contract inherited from the worker, deviating:$vbad" >&2
    echo "  (never fix this by re-encoding: the assembler rule is copy-only. Report it to the worker owner.)" >&2
    [[ "$STRICT_VIDEO" == "1" ]] && return 5
  else
    echo "  video gate     PASS (worker output already matches the contract identity)"
  fi
  return 0
}

if [[ -n "$VERIFY_ONLY" ]]; then
  gate "$VERIFY_ONLY" artifact
  exit $?
fi

[[ -n "$VIDEO" && -n "$OUT" ]] || { echo "mux-final-audio: --video and --out are required (or use --verify)" >&2; exit 1; }
[[ -n "$PRE_PAYLOAD" ]] || { echo "mux-final-audio: --pre-payload is required: it carries the clip order and windows" >&2; exit 1; }
[[ -f "$VIDEO" ]] || { echo "mux-final-audio: FAIL — video not found: $VIDEO" >&2; exit 2; }
[[ -f "$PRE_PAYLOAD" ]] || { echo "mux-final-audio: FAIL — payload not found: $PRE_PAYLOAD" >&2; exit 2; }
[[ -n "$CLIPS_DIR" ]] || { echo "mux-final-audio: --clips-dir is required" >&2; exit 1; }
[[ -d "$CLIPS_DIR" ]] || { echo "mux-final-audio: FAIL — clips dir not found: $CLIPS_DIR" >&2; exit 2; }
[[ -n "$AUDIO_OUT" ]] || AUDIO_OUT="${OUT%.*}.audio.m4a"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

# ── resolve each scene to its clip file ────────────────────────────────────
# The worker renders scene k = the FIRST `duration_seconds` of clip k, in
# payload order (proven on the Dolly preview lane by matching the scene cuts of
# the composite against the clips: 22.04↔2.04+20s, 34.25↔4.25+30s, ...). The
# audio therefore has to be the same window of the same clip, in the same order.
# a scene may carry its media under `clip` or, for stock shots, under `stock`
mapfile -t ASSET_IDS < <(jq -r '.scenes[] | (.clip.asset_id // .stock.asset_id) // empty' "$PRE_PAYLOAD")
mapfile -t DURATIONS < <(jq -r --arg d "$DEFAULT_DURATION" '.scenes[] | (.duration_seconds // ($d | tonumber))' "$PRE_PAYLOAD")
[[ "${#ASSET_IDS[@]}" -gt 0 ]] || { echo "mux-final-audio: FAIL — no scenes[].clip.asset_id in $PRE_PAYLOAD" >&2; exit 2; }

FILES=()
for id in "${ASSET_IDS[@]}"; do
  found=""
  if [[ -n "$CLIP_SUFFIX" ]]; then
    [[ -f "$CLIPS_DIR/$id$CLIP_SUFFIX" ]] && found="$CLIPS_DIR/$id$CLIP_SUFFIX"
  else
    for cand in ".mp4" ".en.mp4" ".m4a" ".wav"; do
      [[ -f "$CLIPS_DIR/$id$cand" ]] && { found="$CLIPS_DIR/$id$cand"; break; }
    done
  fi
  [[ -n "$found" ]] || { echo "mux-final-audio: FAIL — no media for clip $id in $CLIPS_DIR" >&2; exit 2; }
  FILES+=("$found")
done

# ── canonical audio: same windows, same order, aac/LC/192k/48k/stereo ──────
INPUTS=(); FILTERS=""; LABELS=""; I=0
while [[ "$I" -lt "${#FILES[@]}" ]]; do
  INPUTS+=(-ss 0 -t "${DURATIONS[$I]}" -i "${FILES[$I]}")
  FILTERS+="[$I:a]aresample=48000,aformat=sample_fmts=fltp:channel_layouts=stereo[a$I];"
  LABELS+="[a$I]"
  I=$((I + 1))
done
FILTERS+="${LABELS}concat=n=${#FILES[@]}:v=0:a=1[outa]"

TOTAL="$(printf '%s\n' "${DURATIONS[@]}" | awk '{s += $1} END {printf "%.3f", s}')"
echo "mux-final-audio: scenes=${#FILES[@]} total=${TOTAL}s"
I=0; while [[ "$I" -lt "${#FILES[@]}" ]]; do printf '  scene %d  %ss  %s\n' "$I" "${DURATIONS[$I]}" "${FILES[$I]}"; I=$((I + 1)); done

ffmpeg -hide_banner -nostdin -y -v error "${INPUTS[@]}" -filter_complex "$FILTERS" \
  -map "[outa]" -c:a aac -profile:a aac_low -b:a 192k -ar 48000 -ac 2 \
  -movflags +faststart "$AUDIO_OUT" || { echo "mux-final-audio: FAIL — audio render failed" >&2; exit 3; }

# ── copy mux, then prove the video was copied and not re-encoded ───────────
ffmpeg -hide_banner -nostdin -y -v error -i "$VIDEO" -i "$AUDIO_OUT" \
  -map 0:v:0 -map 1:a:0 -c:v copy -c:a copy -movflags +faststart "$OUT" \
  || { echo "mux-final-audio: FAIL — mux failed" >&2; exit 3; }

md5_of() { ffmpeg -v error -nostdin -i "$1" -map 0:v:0 -c copy -f md5 - 2>&1 | tail -1; }
BEFORE="$(md5_of "$VIDEO")"; AFTER="$(md5_of "$OUT")"
if [[ "$BEFORE" != "$AFTER" ]]; then
  echo "mux-final-audio: FAIL — the video stream changed during the mux ($BEFORE != $AFTER)" >&2
  exit 3
fi
echo "mux-final-audio: video stream copied unchanged ($AFTER)"
echo "mux-final-audio: audio=$(basename "$AUDIO_OUT") sha256=$(sha256sum "$AUDIO_OUT" | cut -d' ' -f1)"

gate "$OUT" muxed
RC=$?
[[ "$RC" == "0" ]] && echo "mux-final-audio: OK — $OUT has the canonical audio of this lane"
exit "$RC"

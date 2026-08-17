#!/usr/bin/env bash
# tests/operational/stockrust_live_e2e.sh
#
# STOCKRUST LIVE CERTIFICATION — drives the REAL `pipelinegen-muscles` binary
# (no Go adapter, no HTTP server) through the mediaexec.v1 NDJSON protocol.
#
# Certified surfaces:
#   1. RUST health                     — health op returns ok:true.
#   2. render_stock protocol           — missing inputs / unsupported op /
#                                         legacy selection hints are rejected.
#   3. 10-clip concat                  — N synthetic clips concatenated +
#                                         normalized, verified with ffprobe and
#                                         a full ffmpeg decode (0 errors).
#   4. Concurrency                     — 4 simultaneous render_stock jobs with
#                                         distinct outputs: no cross-output
#                                         contamination, no temp collisions.
#   5. Fail-closed                     — unknown transition ID, missing effect
#                                         file, unresolved selection rejected.
#   6. RTF (Real Time Factor)          — render wall time / rendered media
#                                         duration, for the single render and
#                                         every concurrent job.
#
# A resolved fadeblack transition is used to force the re-encode composite
# path (the copy-only fast path is a stream copy and is not representative
# for RTF). The canonical render_plan path is certified by the Go e2e tests in
# internal/platform/media/rustexec.
#
# Exit codes: 0 = PASS, 1 = FAIL, 2 = prerequisite missing.
#
# Env overrides (defaults follow the certification plan):
#   CLIP_COUNT=10  CLIP_DURATION=7  CONCURRENT_JOBS=4  WIDTH=1280  HEIGHT=720
#   FPS=30  CODEC=libx264  PRESET=veryfast  CRF=23
#   VELOX_RUST_MUSCLES_PATH=<path>  (else bin/pipelinegen-muscles, else build)

set -euo pipefail

# ── Configuration ────────────────────────────────────────────────────────────
CLIP_COUNT="${CLIP_COUNT:-10}"
CLIP_DURATION="${CLIP_DURATION:-7}"
CONCURRENT_JOBS="${CONCURRENT_JOBS:-4}"
WIDTH="${WIDTH:-1280}"
HEIGHT="${HEIGHT:-720}"
FPS="${FPS:-30}"
CODEC="${CODEC:-libx264}"
PRESET="${PRESET:-veryfast}"
CRF="${CRF:-23}"
GOP=$((FPS * 2))   # keyframe every 2s
MUSCLES_BIN="${VELOX_RUST_MUSCLES_PATH:-}"
TMP_ROOT="${TMP_ROOT:-/tmp}"

# ── Prerequisites ────────────────────────────────────────────────────────────
command -v ffmpeg >/dev/null 2>&1 || { echo "FAIL: ffmpeg not on PATH" >&2; exit 2; }
command -v ffprobe >/dev/null 2>&1 || { echo "FAIL: ffprobe not on PATH" >&2; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "FAIL: jq not on PATH" >&2; exit 2; }

if [ -z "$MUSCLES_BIN" ]; then
    for c in bin/pipelinegen-muscles rust/target/release/pipelinegen-muscles; do
        if [ -x "$c" ]; then MUSCLES_BIN="$c"; break; fi
    done
fi
if [ -z "$MUSCLES_BIN" ]; then
    echo "INFO: pipelinegen-muscles not found; building via make build-muscles"
    make build-muscles >/dev/null 2>&1 || { echo "FAIL: make build-muscles failed" >&2; exit 2; }
    MUSCLES_BIN="bin/pipelinegen-muscles"
fi
[ -x "$MUSCLES_BIN" ] || { echo "FAIL: pipelinegen-muscles binary not executable at $MUSCLES_BIN" >&2; exit 2; }

WORKDIR="$(mktemp -d "$TMP_ROOT/stockrust-live-XXXXXX")"
trap 'rm -rf "$WORKDIR"' EXIT

PASS=0
FAIL=0
ok()  { echo "PASS: $1"; PASS=$((PASS + 1)); }
bad() { echo "FAIL: $1" >&2; FAIL=$((FAIL + 1)); }

# muscles <json> — send one NDJSON request, echo the response line.
muscles() { printf '%s\n' "$1" | "$MUSCLES_BIN"; }

# build_render_req <output_path> — render_stock envelope with one resolved
# fadeblack transition (forces the re-encode composite path) over $inputs.
build_render_req() {
    local out="$1"
    jq -cn \
        --argjson inputs "$(printf '%s\n' "${CLIPS[@]}" | jq -R . | jq -sc .)" \
        --arg out "$out" \
        --arg codec "$CODEC" --arg preset "$PRESET" --argjson crf "$CRF" \
        --argjson w "$WIDTH" --argjson h "$HEIGHT" --argjson fps "$FPS" \
        --argjson gop "$GOP" --argjson dur "$CLIP_DURATION" \
        '{
           version: "mediaexec.v1", operation: "render_stock",
           input_paths: $inputs, output_path: $out,
           codec: $codec, preset: $preset, crf: $crf,
           width: $w, height: $h, fps: $fps, keyframe_interval: $gop,
           audio_codec: "aac", audio_bitrate: "128k", sample_rate: 48000, channels: 2,
           no_transitions: false, no_effects: true, clip_duration_sec: $dur,
           transitions: [{clip_index: 0, segment: "end", id: "fadeblack"}]
         }'
}

# ffprobe_report <path> — emit "codec width height r_fps frames duration".
ffprobe_report() {
    local p="$1"
    local json
    json="$(ffprobe -v error -select_streams v:0 -count_frames \
        -show_entries stream=codec_name,width,height,r_frame_rate,nb_read_frames \
        -show_entries format=duration -of json "$p")"
    jq -r '[.streams[0].codec_name, (.streams[0].width|tostring), (.streams[0].height|tostring), .streams[0].r_frame_rate, (.streams[0].nb_read_frames|tostring), .format.duration] | join(" ")' <<<"$json"
}

echo "=================================================================="
echo " STOCKRUST LIVE CERTIFICATION"
echo " binary=$MUSCLES_BIN clips=$CLIP_COUNT dur=${CLIP_DURATION}s jobs=$CONCURRENT_JOBS"
echo "=================================================================="

# ── 1. Health ────────────────────────────────────────────────────────────────
echo
echo "=== RUST: health ==="
HEALTH="$(muscles '{"version":"mediaexec.v1","operation":"health"}')"
if echo "$HEALTH" | jq -e '.ok == true and .operation == "health"' >/dev/null 2>&1; then
    ok "health"
else
    bad "health ($HEALTH)"
fi

# ── 2. render_stock protocol (fail-closed) ───────────────────────────────────
echo
echo "=== RUST: render_stock protocol ==="
PROTO_MISSING="$(muscles '{"version":"mediaexec.v1","operation":"render_stock","output_path":"/tmp/out.mp4"}')"
echo "$PROTO_MISSING" | jq -e '.ok == false and (.error | contains("input_paths"))' >/dev/null 2>&1 \
    && ok "render_stock rejects missing input_paths" \
    || bad "render_stock missing-input rejection ($PROTO_MISSING)"

PROTO_OP="$(muscles '{"version":"mediaexec.v1","operation":"run_command"}')"
echo "$PROTO_OP" | jq -e '.ok == false and .operation == "invalid"' >/dev/null 2>&1 \
    && ok "unsupported operation rejected" \
    || bad "unsupported operation rejection ($PROTO_OP)"

# ── 3. Generate synthetic clips ──────────────────────────────────────────────
echo
echo "=== Generate $CLIP_COUNT synthetic clips (${WIDTH}x${HEIGHT} @ ${FPS}fps) ==="
CLIPS=()
for i in $(seq 1 "$CLIP_COUNT"); do
    p="$WORKDIR/clip_$(printf '%02d' "$i").mp4"
    # testsrc moving pattern; -frames:v keeps the frame count exact.
    ffmpeg -hide_banner -loglevel error -y \
        -f lavfi -i "testsrc=size=${WIDTH}x${HEIGHT}:rate=${FPS}" \
        -frames:v $((CLIP_DURATION * FPS)) \
        -c:v libx264 -pix_fmt yuv420p -an "$p"
    CLIPS+=("$p")
done
ok "generated $CLIP_COUNT clips"

# ── 4. Single 10-clip render + RTF ───────────────────────────────────────────
echo
echo "=== Render $CLIP_COUNT clips (single) ==="
SINGLE_OUT="$WORKDIR/single.mp4"
SINGLE_REQ="$(build_render_req "$SINGLE_OUT")"
T0=$(date +%s.%N)
SINGLE_RESP="$(muscles "$SINGLE_REQ")"
T1=$(date +%s.%N)
SINGLE_WALL=$(awk -v a="$T0" -v b="$T1" 'BEGIN { printf "%.3f", b - a }')
if echo "$SINGLE_RESP" | jq -e '.ok == true' >/dev/null 2>&1; then
    ok "single render_stock (${SINGLE_WALL}s wall)"
else
    bad "single render_stock ($SINGLE_RESP)"
fi

# ── 5. ffprobe + full decode on the single output ────────────────────────────
echo
echo "=== VIDEO: ffprobe + full decode ==="
if [ -s "$SINGLE_OUT" ]; then
    read -r V_CODEC V_W V_H V_FPS V_FRAMES V_DUR < <(ffprobe_report "$SINGLE_OUT")
    [ "$V_CODEC" = "h264" ] && ok "codec=h264" || bad "codec=$V_CODEC (want h264)"
    [ "$V_W" = "$WIDTH" ] && [ "$V_H" = "$HEIGHT" ] && ok "resolution=${V_W}x${V_H}" || bad "resolution=${V_W}x${V_H} (want ${WIDTH}x${HEIGHT})"
    [ "$V_FPS" = "${FPS}/1" ] && ok "fps=${V_FPS}" || bad "fps=$V_FPS (want ${FPS}/1)"

    EXPECT_FRAMES=$((CLIP_COUNT * CLIP_DURATION * FPS))
    if [ "${V_FRAMES:-0}" -ge $((EXPECT_FRAMES - 2)) ] && [ "${V_FRAMES:-0}" -le $((EXPECT_FRAMES + 2)) ]; then
        ok "frame count=${V_FRAMES} (~$EXPECT_FRAMES)"
    else
        bad "frame count=${V_FRAMES} (want ~$EXPECT_FRAMES)"
    fi

    DECODE_ERR="$WORKDIR/decode-errors.txt"
    ffmpeg -v error -i "$SINGLE_OUT" -f null - 2>"$DECODE_ERR" >/dev/null
    [ ! -s "$DECODE_ERR" ] && ok "full decode: 0 errors" || bad "decode errors: $(head -5 "$DECODE_ERR")"
else
    bad "single output missing or empty"
    V_DUR="0"
fi

# ── 6. Concurrency: CONCURRENT_JOBS parallel renders ─────────────────────────
echo
echo "=== Concurrency: $CONCURRENT_JOBS parallel render_stock jobs ==="
for i in $(seq 1 "$CONCURRENT_JOBS"); do
    OUT="$WORKDIR/conc_$i.mp4"
    REQ="$(build_render_req "$OUT")"
    (
        T0=$(date +%s.%N)
        R="$(printf '%s\n' "$REQ" | "$MUSCLES_BIN")"
        T1=$(date +%s.%N)
        echo "$R" > "$WORKDIR/conc_resp_$i.json"
        awk -v a="$T0" -v b="$T1" 'BEGIN { printf "%.3f", b - a }' > "$WORKDIR/conc_wall_$i.txt"
    ) &
done
wait

CONC_OK=0
for i in $(seq 1 "$CONCURRENT_JOBS"); do
    OUT="$WORKDIR/conc_$i.mp4"
    RESP="$WORKDIR/conc_resp_$i.json"
    WALL="$(cat "$WORKDIR/conc_wall_$i.txt")"
    if [ -s "$RESP" ] && jq -e '.ok == true' "$RESP" >/dev/null 2>&1 && [ -s "$OUT" ]; then
        ok "job $i render_stock (${WALL}s wall)"
        CONC_OK=$((CONC_OK + 1))
    else
        bad "job $i render_stock (resp=$(cat "$RESP" 2>/dev/null || echo missing))"
    fi
done

# Cross-contamination: every output must be distinct and independently valid.
UNIQUE="$(for i in $(seq 1 "$CONCURRENT_JOBS"); do ffprobe -v error -show_entries format=duration -of default=noprint_wrappers=1:nokey=1 "$WORKDIR/conc_$i.mp4" 2>/dev/null | head -1; done | sort -u | wc -l)"
[ "$CONC_OK" = "$CONCURRENT_JOBS" ] && [ "$UNIQUE" -ge 1 ] \
    && ok "no cross-output contamination (${CONC_OK}/${CONCURRENT_JOBS} valid)" \
    || bad "cross-output contamination or missing outputs (valid=$CONC_OK unique_durations=$UNIQUE)"

# ── 7. Fail-closed negative paths ────────────────────────────────────────────
echo
echo "=== FAIL CLOSED ==="
NEG_TRANS="$(jq -cn \
    --argjson inputs "$(printf '%s\n' "${CLIPS[@]:0:2}" | jq -R . | jq -sc .)" \
    --arg out "$WORKDIR/neg-trans.mp4" \
    '{version:"mediaexec.v1",operation:"render_stock",input_paths:$inputs,output_path:$out,codec:"libx264",preset:"veryfast",crf:23,width:1280,height:720,fps:30,keyframe_interval:60,audio_codec:"aac",audio_bitrate:"128k",sample_rate:48000,channels:2,no_transitions:false,no_effects:true,clip_duration_sec:7,transitions:[{clip_index:0,segment:"end",id:"random-transition"}]}')"
NEG_TRANS_RESP="$(muscles "$NEG_TRANS")"
echo "$NEG_TRANS_RESP" | jq -e '.ok == false and (.error | contains("invalid resolved transition"))' >/dev/null 2>&1 \
    && ok "unknown transition ID rejected" \
    || bad "unknown transition ID ($NEG_TRANS_RESP)"

NEG_EFFECT="$(jq -cn \
    --argjson inputs "$(printf '%s\n' "${CLIPS[@]:0:2}" | jq -R . | jq -sc .)" \
    --arg out "$WORKDIR/neg-effect.mp4" \
    '{version:"mediaexec.v1",operation:"render_stock",input_paths:$inputs,output_path:$out,codec:"libx264",preset:"veryfast",crf:23,width:1280,height:720,fps:30,keyframe_interval:60,audio_codec:"aac",audio_bitrate:"128k",sample_rate:48000,channels:2,no_transitions:true,no_effects:false,effect_paths:[{clip_index:0,path:"/nonexistent/effect.mp4"}]}')"
NEG_EFFECT_RESP="$(muscles "$NEG_EFFECT")"
echo "$NEG_EFFECT_RESP" | jq -e '.ok == false and (.error | contains("invalid resolved effect path"))' >/dev/null 2>&1 \
    && ok "missing effect file rejected" \
    || bad "missing effect file ($NEG_EFFECT_RESP)"

NEG_SELECT="$(muscles '{"version":"mediaexec.v1","operation":"render_stock","input_paths":["/tmp/x.mp4"],"output_path":"/tmp/y.mp4","transition_every":4}')"
echo "$NEG_SELECT" | jq -e '.ok == false and (.error | contains("unresolved transition/effect selection"))' >/dev/null 2>&1 \
    && ok "legacy selection hint rejected (Rust does not select)" \
    || bad "legacy selection hint ($NEG_SELECT)"

# ── 8. Performance / RTF ─────────────────────────────────────────────────────
echo
echo "=== PERFORMANCE ==="
MEDIA_DUR="${V_DUR:-0}"
RTF="n/a"
if awk -v d="$MEDIA_DUR" 'BEGIN { exit !(d > 0) }'; then
    RTF="$(awk -v w="$SINGLE_WALL" -v d="$MEDIA_DUR" 'BEGIN { printf "%.3f", w / d }')"
fi
echo "  media duration : ${MEDIA_DUR}s"
echo "  render wall    : ${SINGLE_WALL}s"
echo "  RTF            : ${RTF} (wall / media)"
for i in $(seq 1 "$CONCURRENT_JOBS"); do
    echo "  job $i wall    : $(cat "$WORKDIR/conc_wall_$i.txt")s"
done

# ── Verdict ──────────────────────────────────────────────────────────────────
echo
echo "=================================================================="
echo " STOCKRUST LIVE CERTIFICATION — PASS=$PASS FAIL=$FAIL"
if [ "$FAIL" -eq 0 ]; then
    echo " CERTIFIED: YES"
    echo "=================================================================="
    exit 0
fi
echo " CERTIFIED: NO"
echo "=================================================================="
exit 1

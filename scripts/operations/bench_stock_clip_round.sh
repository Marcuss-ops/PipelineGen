#!/bin/bash
# Bench: stock pipeline clip extraction - POST-fix (1 batch + N probes) vs
# PRE-fix (N sequential cuts + N probes). Wraps ffmpeg/ffprobe via a PATH
# wrapper directory (/tmp/stock-bench/wrap/) for invocation counting, plus
# a background sampler tracking peak CPU% + RAM% throughout both scenarios.
#
#   N=30 (default, ~75s wall) - the canonical scaling-tier receipt.
#   N=351 (full verdict round) - CI-only scope; on ffmpeg 4.4.2 single-CLI
#   graph depth, the post-fix batch may exceed the graph-depth ceiling
#   (see §12.5.4 in docs/operations/stock-e2e-runbook.md).
#
# godlike/06 SSOT: this script is the canonical bench operator-facing
# artifact; §12.5.3 in the runbook references its exact invocation.

set -uo pipefail

N=${N:-30}
SRC_DUR=${SRC_DUR:-150}
WRAP=/tmp/stock-bench/wrap
SUBDIR=/tmp/stock-bench
LOG=$SUBDIR/subprocess.log
SOURCE=$SUBDIR/source.mp4
CLIPDIR=$SUBDIR/post_clips
PREDIR=$SUBDIR/pre_clips
HASHFILE=$SUBDIR/hashes.txt
PEAK_LOG=$SUBDIR/peak.log
RESULT=$SUBDIR/result.json

rm -rf "$WRAP" "$CLIPDIR" "$PREDIR"
mkdir -p "$WRAP" "$CLIPDIR" "$PREDIR"
: > "$LOG"; : > "$PEAK_LOG"; : > "$HASHFILE"

# Build wrappers
cat > "$WRAP/ffmpeg" <<'EOF'
#!/bin/bash
echo "$(date +%s%N) ffmpeg $@" >> /tmp/stock-bench/subprocess.log
${FFMPEG_BIN:-"${FFMPEG_BIN:-/usr/bin/ffmpeg}"} "$@" 2>/dev/null
EOF
cat > "$WRAP/ffprobe" <<'EOF'
#!/bin/bash
echo "$(date +%s%N) ffprobe $@" >> /tmp/stock-bench/subprocess.log
${FFPROBE_BIN:-"${FFPROBE_BIN:-/usr/bin/ffprobe}"} "$@" 2>/dev/null
EOF
chmod +x "$WRAP/ffmpeg" "$WRAP/ffprobe"
export PATH="$WRAP:$PATH"

CLIP_DUR=$(awk -v n="$N" -v d="$SRC_DUR" 'BEGIN{print d/n}')
echo "[setup] N=$N SRC_DUR=${SRC_DUR}s CLIP_DUR=${CLIP_DUR}s" >&2

# Generate source (no wrapper, just direct)
"${FFMPEG_BIN:-/usr/bin/ffmpeg}" -y -hide_banner -loglevel error \
  -f lavfi -i "testsrc=duration=${SRC_DUR}:size=640x480:rate=30" \
  -pix_fmt yuv420p -c:v libx264 -preset ultrafast "$SOURCE" 2>"$SUBDIR/source.err"

# Background CPU/RAM peak sampler - runs throughout both scenarios,
# samples every 200ms. Output: "cpu_pct ram_pct" per line in PEAK_LOG.
( while true; do
    ps -eo pcpu,pmem,comm --no-headers 2>/dev/null \
      | awk '$3 ~ /^(ffmpeg|ffprobe)$/ {print $1, $2}' \
      >> "$PEAK_LOG" 2>/dev/null
    sleep 0.2
  done ) >/dev/null 2>&1 &
SAMPLER_PID=$!
# Cleanup trap guarantees sampler exit even on early error / SIGINT.
trap 'kill "$SAMPLER_PID" 2>/dev/null || true' EXIT

# === POST-fix scenario: 1 ffmpeg batch (filter_complex) + N ffprobe validations ===
echo "[post-fix] starting batch..." >&2
POST_START=$(date +%s.%N)
FILTER=""
MAP_ARGS=()
for ((i=0; i<N; i++)); do
  printf -v SS '%.4f' "$(awk -v i="$i" -v cd="$CLIP_DUR" 'BEGIN{print i*cd}')"
  printf -v EE '%.4f' "$(awk -v i="$i" -v cd="$CLIP_DUR" 'BEGIN{print (i+1)*cd}')"
  PART="[0:v]trim=start=${SS}:end=${EE},setpts=PTS-STARTPTS[v${i}]"
  if [ -z "$FILTER" ]; then FILTER="$PART"; else FILTER="${FILTER};${PART}"; fi
  OUT="$CLIPDIR/clip_$(printf '%03d' "$i").mp4"
  MAP_ARGS+=( -map "[v${i}]" -c:v libx264 -preset ultrafast -an "$OUT" )
done
echo "[post-fix] filter=$FILTER" >&2
echo "[post-fix] map_args_count=${#MAP_ARGS[@]}" >&2
ffmpeg -y -hide_banner -loglevel error -i "$SOURCE" -filter_complex "$FILTER" "${MAP_ARGS[@]}"

for ((i=0; i<N; i++)); do
  OUT="$CLIPDIR/clip_$(printf '%03d' "$i").mp4"
  ffprobe -v quiet -print_format json -show_format -show_streams "$OUT" >/dev/null 2>&1
done
POST_END=$(date +%s.%N)
POST_WALL=$(awk -v s="$POST_START" -v e="$POST_END" 'BEGIN{printf "%.4f", e-s}')

# === PRE-fix scenario: N sequential cuts (one ffmpeg each) + N ffprobe validations ===
echo "[pre-fix] starting sequential..." >&2
PRE_START=$(date +%s.%N)
for ((i=0; i<N; i++)); do
  printf -v SS '%.4f' "$(awk -v i="$i" -v cd="$CLIP_DUR" 'BEGIN{print i*cd}')"
  printf -v EE '%.4f' "$(awk -v i="$i" -v cd="$CLIP_DUR" 'BEGIN{print (i+1)*cd}')"
  OUT="$PREDIR/clip_$(printf '%03d' "$i").mp4"
  ffmpeg -y -hide_banner -loglevel error -ss "$SS" -i "$SOURCE" -to "$EE" -c:v libx264 -preset ultrafast -an "$OUT" 2>/dev/null
  ffprobe -v quiet -print_format json -show_format -show_streams "$OUT" >/dev/null 2>&1
done
PRE_END=$(date +%s.%N)
PRE_WALL=$(awk -v s="$PRE_START" -v e="$PRE_END" 'BEGIN{printf "%.4f", e-s}')

# Stop the background sampler BEFORE computing peak (cleanup trap will also fire).
kill "$SAMPLER_PID" 2>/dev/null || true
trap - EXIT

# Compute peak CPU/RAM from collected samples (max of column 1 / column 2).
PEAK_CPU=$(awk '$1+0 > m {m=$1+0} END{printf "%.2f", m+0}' "$PEAK_LOG" 2>/dev/null)
PEAK_RAM=$(awk '$2+0 > m {m=$2+0} END{printf "%.2f", m+0}' "$PEAK_LOG" 2>/dev/null)
: "${PEAK_CPU:=0.00}"
: "${PEAK_RAM:=0.00}"

# Hash outputs
{ echo "# BENCH N=$N SRC_DUR=${SRC_DUR}s"; echo "# POST clips ($(ls "$CLIPDIR"/*.mp4 2>/dev/null | wc -l)):"; sha256sum "$CLIPDIR"/*.mp4 2>/dev/null | sort; echo "# PRE clips ($(ls "$PREDIR"/*.mp4 2>/dev/null | wc -l)):"; sha256sum "$PREDIR"/*.mp4 2>/dev/null | sort; } > "$HASHFILE"

# Counts from log
TOTAL_FFMPEG=$(grep -c ' ffmpeg ' "$LOG" 2>/dev/null | head -1 || echo 0)
TOTAL_FFPROBE=$(grep -c ' ffprobe ' "$LOG" 2>/dev/null | head -1 || echo 0)
POST_FFMPEG=1   # exactly 1 batch call
POST_FFPROBE=$N
PRE_FFMPEG=$((TOTAL_FFMPEG - POST_FFMPEG))
PRE_FFPROBE=$((TOTAL_FFPROBE - POST_FFPROBE))

cat > "$RESULT" <<EOF
{
  "params": {"n": $N, "src_dur_sec": $SRC_DUR, "clip_dur_sec": $CLIP_DUR},
  "post_fix": {
    "ffmpeg_invocations": $POST_FFMPEG,
    "ffprobe_invocations": $POST_FFPROBE,
    "wall_sec": $POST_WALL,
    "clips_produced": $(ls "$CLIPDIR"/*.mp4 2>/dev/null | wc -l)
  },
  "pre_fix": {
    "ffmpeg_invocations": $PRE_FFMPEG,
    "ffprobe_invocations": $PRE_FFPROBE,
    "wall_sec": $PRE_WALL,
    "clips_produced": $(ls "$PREDIR"/*.mp4 2>/dev/null | wc -l)
  },
  "totals": {
    "ffmpeg": $TOTAL_FFMPEG,
    "ffprobe": $TOTAL_FFPROBE,
    "subprocess": $((TOTAL_FFMPEG + TOTAL_FFPROBE))
  },
  "peak_cpu_pct": $PEAK_CPU,
  "peak_ram_pct": $PEAK_RAM
}
EOF
echo "[done] result saved to $RESULT" >&2
cat "$RESULT" >&2

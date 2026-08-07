#!/bin/bash
# Bench: stock pipeline clip extraction - POST-fix (1 batch + N probes) vs
# PRE-fix (N sequential cuts + N probes). The source and Drive operations are
# local fixture copies: no network, SQLite, or Drive mutation is performed.
# Wraps ffmpeg/ffprobe via a PATH wrapper directory for invocation counting,
# and samples peak ffmpeg/ffprobe CPU and RAM separately for each scenario.
#
#   N=30 (default) - the canonical scaling-tier receipt.
#   N=351 - optional large run; use an explicit timeout on hosts where the
#            sequential PRE scenario is expected to run for a long time.

set -euo pipefail

N=${N:-30}
SRC_DUR=${SRC_DUR:-150}
FFMPEG=${FFMPEG_BIN:-/usr/bin/ffmpeg}
FFPROBE=${FFPROBE_BIN:-/usr/bin/ffprobe}
SUBDIR=/tmp/stock-bench
WRAP=$SUBDIR/wrap
LOG=$SUBDIR/subprocess.log
SOURCE=$SUBDIR/source.mp4
CLIPDIR=$SUBDIR/post_clips
PREDIR=$SUBDIR/pre_clips
POST_SOURCE_DIR=$SUBDIR/post_downloads
PRE_SOURCE_DIR=$SUBDIR/pre_downloads
DRIVE_DIR=$SUBDIR/drive_fixture
HASHFILE=$SUBDIR/hashes.txt
POST_PEAK_LOG=$SUBDIR/post_peak.log
PRE_PEAK_LOG=$SUBDIR/pre_peak.log
RESULT=$SUBDIR/result.json
LOCK=$SUBDIR.lock
POST_SAMPLER=""
PRE_SAMPLER=""

# Serialize the fixed receipt directory so concurrent runs cannot delete or
# overwrite one another's fixture files. The lock is removed by cleanup.
if ! mkdir "$LOCK" 2>/dev/null; then
  echo "benchmark already running: $SUBDIR" >&2
  exit 1
fi
cleanup() {
  [ -z "$POST_SAMPLER" ] || kill "$POST_SAMPLER" 2>/dev/null || true
  [ -z "$PRE_SAMPLER" ] || kill "$PRE_SAMPLER" 2>/dev/null || true
  rmdir "$LOCK" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# The benchmark owns only this temporary receipt directory.
rm -rf "$SUBDIR"
mkdir -p "$WRAP" "$CLIPDIR" "$PREDIR" "$POST_SOURCE_DIR" "$PRE_SOURCE_DIR" "$DRIVE_DIR/post" "$DRIVE_DIR/pre"
: > "$LOG"
: > "$POST_PEAK_LOG"
: > "$PRE_PEAK_LOG"
: > "$HASHFILE"

# Build wrappers. The wrapper log counts actual FFmpeg/ffprobe subprocesses,
# while the fixture operation log below counts the download/upload seams.
cat > "$WRAP/ffmpeg" <<'EOF'
#!/bin/bash
printf '%s ffmpeg phase=%s %s\n' "$(date +%s%N)" "${BENCH_PHASE:-unknown}" "$*" >> /tmp/stock-bench/subprocess.log
exec "${FFMPEG_BIN:-/usr/bin/ffmpeg}" "$@"
EOF
cat > "$WRAP/ffprobe" <<'EOF'
#!/bin/bash
printf '%s ffprobe phase=%s %s\n' "$(date +%s%N)" "${BENCH_PHASE:-unknown}" "$*" >> /tmp/stock-bench/subprocess.log
exec "${FFPROBE_BIN:-/usr/bin/ffprobe}" "$@"
EOF
chmod +x "$WRAP/ffmpeg" "$WRAP/ffprobe"
export PATH="$WRAP:$PATH"

fixture_download() {
  local phase=$1
  local index=$2
  local destination=$3
  mkdir -p "$(dirname "$destination")"
  cp "$SOURCE" "$destination"
  printf '%s operation=download phase=%s index=%s path=%s\n' "$(date +%s%N)" "$phase" "$index" "$destination" >> "$LOG"
  printf '%s\n' "$destination"
}

fixture_upload() {
  local phase=$1
  local index=$2
  local source=$3
  local destination=$4
  mkdir -p "$(dirname "$destination")"
  cp "$source" "$destination"
  printf '%s operation=drive_upload phase=%s index=%s path=%s\n' "$(date +%s%N)" "$phase" "$index" "$destination" >> "$LOG"
}

start_sampler() {
  local output=$1
  (
    while true; do
      ps -eo pcpu,pmem,comm --no-headers 2>/dev/null \
        | awk '$3 ~ /^(ffmpeg|ffprobe)$/ {print $1, $2}' >> "$output" 2>/dev/null || true
      sleep 0.2
    done
  ) >/dev/null 2>&1 &
  printf '%s\n' "$!"
}

stop_sampler() {
  local pid=$1
  kill "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
}

peak_value() {
  local column=$1
  local file=$2
  awk -v c="$column" '($c+0)>m {m=$c+0} END {printf "%.2f", m+0}' "$file"
}

count_log() {
  local pattern=$1
  grep -c "$pattern" "$LOG" || true
}

CLIP_DUR=$(awk -v n="$N" -v d="$SRC_DUR" 'BEGIN{print d/n}')
printf '[setup] N=%s SRC_DUR=%ss CLIP_DUR=%ss\n' "$N" "$SRC_DUR" "$CLIP_DUR" >&2

# Generate one immutable synthetic source outside both measured scenarios.
"$FFMPEG" -y -hide_banner -loglevel error \
  -f lavfi -i "testsrc=duration=${SRC_DUR}:size=640x480:rate=30" \
  -pix_fmt yuv420p -c:v libx264 -preset ultrafast "$SOURCE"

# === POST-fix: one fixture download, one batch FFmpeg, N probes, N uploads ===
printf '[post-fix] starting batch...\n' >&2
export BENCH_PHASE=post
POST_SOURCE=$(fixture_download post batch "$POST_SOURCE_DIR/source.mp4")
POST_START=$(date +%s.%N)
POST_SAMPLER=$(start_sampler "$POST_PEAK_LOG")
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
ffmpeg -y -hide_banner -loglevel error -i "$POST_SOURCE" -filter_complex "$FILTER" "${MAP_ARGS[@]}"
for ((i=0; i<N; i++)); do
  OUT="$CLIPDIR/clip_$(printf '%03d' "$i").mp4"
  ffprobe -v quiet -print_format json -show_format -show_streams "$OUT" >/dev/null
  fixture_upload post "$i" "$OUT" "$DRIVE_DIR/post/clip_$(printf '%03d' "$i").mp4"
done
POST_END=$(date +%s.%N)
stop_sampler "$POST_SAMPLER"
POST_SAMPLER=""
POST_WALL=$(awk -v s="$POST_START" -v e="$POST_END" 'BEGIN{printf "%.4f", e-s}')
POST_CPU=$(peak_value 1 "$POST_PEAK_LOG")
POST_RAM=$(peak_value 2 "$POST_PEAK_LOG")

# === PRE-fix: N fixture downloads, N sequential FFmpeg, N probes, N uploads ===
printf '[pre-fix] starting sequential...\n' >&2
export BENCH_PHASE=pre
PRE_START=$(date +%s.%N)
PRE_SAMPLER=$(start_sampler "$PRE_PEAK_LOG")
for ((i=0; i<N; i++)); do
  printf -v SS '%.4f' "$(awk -v i="$i" -v cd="$CLIP_DUR" 'BEGIN{print i*cd}')"
  printf -v EE '%.4f' "$(awk -v i="$i" -v cd="$CLIP_DUR" 'BEGIN{print (i+1)*cd}')"
  DOWNLOADED=$(fixture_download pre "$i" "$PRE_SOURCE_DIR/source_$(printf '%03d' "$i").mp4")
  OUT="$PREDIR/clip_$(printf '%03d' "$i").mp4"
  ffmpeg -y -hide_banner -loglevel error -ss "$SS" -i "$DOWNLOADED" -to "$EE" -c:v libx264 -preset ultrafast -an "$OUT"
  ffprobe -v quiet -print_format json -show_format -show_streams "$OUT" >/dev/null
  fixture_upload pre "$i" "$OUT" "$DRIVE_DIR/pre/clip_$(printf '%03d' "$i").mp4"
done
PRE_END=$(date +%s.%N)
stop_sampler "$PRE_SAMPLER"
PRE_SAMPLER=""
PRE_WALL=$(awk -v s="$PRE_START" -v e="$PRE_END" 'BEGIN{printf "%.4f", e-s}')
PRE_CPU=$(peak_value 1 "$PRE_PEAK_LOG")
PRE_RAM=$(peak_value 2 "$PRE_PEAK_LOG")

POST_FFMPEG=$(grep -c ' ffmpeg phase=post ' "$LOG" || true)
POST_FFPROBE=$(grep -c ' ffprobe phase=post ' "$LOG" || true)
PRE_FFMPEG=$(grep -c ' ffmpeg phase=pre ' "$LOG" || true)
PRE_FFPROBE=$(grep -c ' ffprobe phase=pre ' "$LOG" || true)
TOTAL_DOWNLOADS=$(count_log ' operation=download ')
TOTAL_UPLOADS=$(count_log ' operation=drive_upload ')
POST_DOWNLOADS=$(grep -c ' operation=download phase=post ' "$LOG" || true)
PRE_DOWNLOADS=$(grep -c ' operation=download phase=pre ' "$LOG" || true)
POST_UPLOADS=$(grep -c ' operation=drive_upload phase=post ' "$LOG" || true)
PRE_UPLOADS=$(grep -c ' operation=drive_upload phase=pre ' "$LOG" || true)
TOTAL_FFMPEG=$((POST_FFMPEG + PRE_FFMPEG))
TOTAL_FFPROBE=$((POST_FFPROBE + PRE_FFPROBE))
if [ "$POST_FFMPEG" -ne 1 ] || [ "$POST_FFPROBE" -ne "$N" ] || \
   [ "$PRE_FFMPEG" -ne "$N" ] || [ "$PRE_FFPROBE" -ne "$N" ]; then
  echo "subprocess count mismatch: post ffmpeg=$POST_FFMPEG ffprobe=$POST_FFPROBE; pre ffmpeg=$PRE_FFMPEG ffprobe=$PRE_FFPROBE" >&2
  exit 1
fi

# Hash both output sets and verify fixture upload counts are complete.
{
  echo "# BENCH N=$N SRC_DUR=${SRC_DUR}s"
  echo "# POST clips ($(find "$CLIPDIR" -name '*.mp4' | wc -l)):"
  sha256sum "$CLIPDIR"/*.mp4 | sort
  echo "# PRE clips ($(find "$PREDIR" -name '*.mp4' | wc -l)):"
  sha256sum "$PREDIR"/*.mp4 | sort
} > "$HASHFILE"

if [ "$POST_DOWNLOADS" -ne 1 ] || [ "$PRE_DOWNLOADS" -ne "$N" ] || \
   [ "$POST_UPLOADS" -ne "$N" ] || [ "$PRE_UPLOADS" -ne "$N" ]; then
  echo "fixture operation count mismatch" >&2
  exit 1
fi

cat > "$RESULT" <<EOF
{
  "params": {"n": $N, "src_dur_sec": $SRC_DUR, "clip_dur_sec": $CLIP_DUR},
  "measurement": {"network": false, "sqlite": false, "drive": false, "fixture_root": "$SUBDIR"},
  "post_fix": {
    "download_invocations": $POST_DOWNLOADS,
    "ffmpeg_invocations": $POST_FFMPEG,
    "ffprobe_invocations": $POST_FFPROBE,
    "drive_upload_invocations": $POST_UPLOADS,
    "wall_sec": $POST_WALL,
    "peak_ffmpeg_cpu_pct": $POST_CPU,
    "peak_ffmpeg_ram_pct": $POST_RAM,
    "clips_produced": $(find "$CLIPDIR" -name '*.mp4' | wc -l)
  },
  "pre_fix": {
    "download_invocations": $PRE_DOWNLOADS,
    "ffmpeg_invocations": $PRE_FFMPEG,
    "ffprobe_invocations": $PRE_FFPROBE,
    "drive_upload_invocations": $PRE_UPLOADS,
    "wall_sec": $PRE_WALL,
    "peak_ffmpeg_cpu_pct": $PRE_CPU,
    "peak_ffmpeg_ram_pct": $PRE_RAM,
    "clips_produced": $(find "$PREDIR" -name '*.mp4' | wc -l)
  },
  "totals": {
    "downloads": $TOTAL_DOWNLOADS,
    "ffmpeg": $TOTAL_FFMPEG,
    "ffprobe": $TOTAL_FFPROBE,
    "drive_uploads": $TOTAL_UPLOADS,
    "subprocess": $((TOTAL_FFMPEG + TOTAL_FFPROBE))
  }
}
EOF
printf '[done] result saved to %s\n' "$RESULT" >&2
cat "$RESULT" >&2

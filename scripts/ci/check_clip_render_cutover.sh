#!/usr/bin/env bash
set -euo pipefail

# Permanent cutover gate: production clip rendering must cross the shared
# RenderingGen queue boundary. Rust/Chronon and backend-selector symbols are
# allowed only in tests, migration documentation, and the queue adapter's
# implementation; they must not be wired from production application code.
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT"

fail=0
production_paths=(internal/app internal/capabilities internal/platform cmd)

check_absent() {
  local pattern=$1
  local label=$2
  local hits
  hits=$(rg -n "$pattern" "${production_paths[@]}" \
    --glob '*.go' \
    --glob '!**/*_test.go' \
    --glob '!internal/platform/renderinggen/queue_client.go' \
    --glob '!internal/app/wiring/clip_render_runtime.go' \
    --glob '!internal/app/wiring/chronon_clip_renderer.go' \
    --glob '!internal/app/wiring/chronon_clip_render_support.go' \
    --glob '!internal/capabilities/localization/renderer_media.go' \
    --glob '!internal/capabilities/multilingual/renderer_media.go' \
    --glob '!internal/capabilities/localization/adapters/render.go' \
    --glob '!internal/capabilities/cliprender/adapters/cliprender_plan.go' \
    --glob '!internal/capabilities/cliprender/backend.go' \
    --glob '!internal/capabilities/cliprender/worker_result.go' \
    --glob '!internal/capabilities/cliprender/worker.go' \
    --glob '!internal/platform/media/rustexec/clip_renderer.go' \
    --glob '!internal/platform/media/rustexec/protocol.go' \
    || true)
  if [[ -n "$hits" ]]; then
    echo "FAIL: $label"
    echo "$hits" | sed 's/^/  /'
    fail=1
  else
    echo "PASS: $label"
  fi
}

check_absent 'NewChrononClipRenderExecutor|chrononClipRenderExecutor' 'direct local Chronon clip executor'
check_absent 'NewClipRendererWithExecutor|\.RenderClip\(' 'direct Rust RenderClip production caller'
check_absent 'BackendCudaNative|BackendFFmpegFallback|cuda_native|ffmpeg_fallback' 'production CUDA/FFmpeg clip fallback selector'

if [[ ! -f internal/app/wiring/clip_render_runtime.go ]] || \
   ! rg -q 'RenderingGenExecutor' internal/app/wiring/clip_render_runtime.go; then
  echo 'FAIL: shared RenderingGen clip runtime is missing'
  fail=1
else
  echo 'PASS: shared RenderingGen clip runtime is present'
fi

if (( fail )); then
  echo 'CLIP_RENDER_CUTOVER=FAIL'
  exit 1
fi
echo 'CLIP_RENDER_CUTOVER=PASS'

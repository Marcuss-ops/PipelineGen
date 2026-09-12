#!/usr/bin/env bash
set -euo pipefail

# Permanent cutover gate: production clip rendering must cross the shared
# RenderingGen queue boundary. Rust/Chronon and backend-selector symbols are
# allowed only in tests, migration documentation, and the queue adapter's
# implementation; they must not be wired from production application code.
# The retired direct-local Chronon implementation is stricter: those source
# files must not exist at all after the queue-only cutover.
#
# PATH B CUDA hybrid (DEMOLISHED): the cuda_native backend and its GPU
# compositing machinery were removed from the codebase — certified Chronon
# owns GPU compositing on the Chronon executor. The symbols below must be
# absent from BOTH the Go production tree and the Rust muscles source:
#   - Go: BackendCudaNative | cuda_native (no exemptions)
#   - Rust: cuda_native, scale_cuda, overlay_cuda, hwupload_cuda,
#     render_backend, the removed GPU graph builders, and any reference to
#     the chronon3d binary/engine (Rust must never invoke or embed Chronon)
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
    --glob '!internal/capabilities/localization/adapters/render.go' \
    --glob '!internal/capabilities/cliprender/adapters/cliprender_plan.go' \
    --glob '!internal/capabilities/cliprender/backend.go' \
    --glob '!internal/capabilities/cliprender/worker_result.go' \
    --glob '!internal/capabilities/cliprender/worker.go' \
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

# check_absent_strict has NO exemptions: after the PATH B demolition these
# symbols must not exist anywhere in production Go, not even in comments.
check_absent_strict() {
  local pattern=$1
  local label=$2
  local hits
  hits=$(rg -n "$pattern" "${production_paths[@]}" --glob '*.go' --glob '!**/*_test.go' || true)
  if [[ -n "$hits" ]]; then
    echo "FAIL: $label"
    echo "$hits" | sed 's/^/  /'
    fail=1
  else
    echo "PASS: $label"
  fi
}

# check_absent_rust enforces the boundary inside the Rust muscles: the GPU
# compositing symbols must never reappear and the Rust code must never
# reference the chronon3d binary/engine (Rust executes the software baseline;
# Chronon is reached only through the RenderingGen queue from Go).
check_absent_rust() {
  local pattern=$1
  local label=$2
  local hits
  hits=$(rg -n -i "$pattern" rust \
    --glob '!target/**' \
    --glob '!**/Cargo.lock' \
    || true)
  if [[ -n "$hits" ]]; then
    echo "FAIL: $label"
    echo "$hits" | sed 's/^/  /'
    fail=1
  else
    echo "PASS: $label"
  fi
}

legacy_local_files=(
  internal/app/wiring/chronon_clip_renderer.go
  internal/app/wiring/chronon_clip_render_support.go
)
for legacy in "${legacy_local_files[@]}"; do
  if [[ -e "$legacy" ]]; then
    echo "FAIL: retired local Chronon file must not exist: $legacy"
    fail=1
  else
    echo "PASS: retired local Chronon file absent: $legacy"
  fi
done

# Wave C gate (single-pass overlays). The FFmpeg overlay compositor is the last
# remaining "encode the clip a second time" path. It is retained ONLY as the
# PIPELINEGEN_CLIP_SINGLE_PASS_OVERLAY=0 fallback until the single-pass overlay
# carries a GPU artifact equivalence certificate. Until then exactly ONE
# production caller may exist (the composition root wiring) plus the single
# definition file: a new caller means a new second-transcode path and must be
# rejected. When the certificate lands and the compositor is deleted, this gate
# keeps passing with zero hits — it permanently forbids the regression.
check_ffmpeg_overlay_compositor_callers() {
  # Two definition sites (the composite pass and its constructor wrapper) plus
  # the single composition-root caller. Anything else is a new second-transcode
  # caller and fails the gate.
  local allowed="internal/capabilities/cliprender/adapters/cliprender_overlay.go
internal/capabilities/cliprender/adapters/constructors.go
internal/app/wiring/registry_internal_modules.go"
  local -a hits
  mapfile -t hits < <(rg -l 'NewFFmpegOverlayCompositor' "${production_paths[@]}" \
    --glob '*.go' --glob '!**/*_test.go' || true)
  local offender=0
  local file
  for file in "${hits[@]}"; do
    [[ -z "$file" ]] && continue
    if ! grep -qxF "$file" <<< "$allowed"; then
      echo "FAIL: new FFmpeg overlay compositor caller (a second transcode path): $file"
      offender=1
    fi
  done
  if (( offender )); then
    fail=1
  else
    echo 'PASS: no new FFmpeg overlay compositor caller (overlays composite inside the single Chronon pass)'
  fi
}

check_absent 'NewChrononClipRenderExecutor|chrononClipRenderExecutor' 'direct local Chronon clip executor'
check_absent 'CHRONON_RENDER_SOCKET|CHRONON_SOCKET_PATH|exec\.Command[^\n]*chronon' 'direct local Chronon process/socket wiring'
check_absent 'NewClipRendererWithExecutor|\.RenderClip\(' 'direct Rust RenderClip production caller'
check_absent 'BackendFFmpegFallback|ffmpeg_fallback' 'production FFmpeg clip fallback selector'
check_absent_strict 'BackendCudaNative|cuda_native' 'PATH B CUDA hybrid backend (demolished — must not exist anywhere)'
check_ffmpeg_overlay_compositor_callers
check_absent_rust 'chronon3d|CHRONON_' 'Rust must never reference the Chronon engine/binary'
check_absent_rust 'cuda_native|scale_cuda|overlay_cuda|hwupload_cuda|render_backend|append_video_args_cuda|build_gpu_filter_graph|gpu_native_eligible|has_alpha_pixel_format' 'Rust GPU compositing machinery (demolished with PATH B)'

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
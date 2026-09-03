#!/usr/bin/env bash
# Visible entrypoint for clip recreation through PipelineGen -> RenderingGen
# -> Chronon3d. The delegated script fails unless the final backend is
# exactly chronon_vulkan; this path never invokes Rust.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ $# -lt 1 || $# -gt 2 ]]; then
  echo "usage: $0 SOURCE_ASSET_ID [PAYLOAD.json]" >&2
  echo "example: $0 18Amwc3aF8I5jjo9R1-PPsOh0vvx94Jl3" >&2
  exit 2
fi

exec "$ROOT/scripts/run_clip_render_chronon.sh" "$@"

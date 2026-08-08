#!/usr/bin/env bash
# Start the PipelineGen embedding server on port 8001.
# Device policy: PIPELINEGEN_EMBEDDING_DEVICE=auto|cpu|cuda and optional
# PIPELINEGEN_EMBEDDING_REQUIRE_GPU=1. CUDA mode fails closed when unavailable.
# Skips SigLIP and CLAP heavy models by default (set SKIP_SIGLIP=0 to enable).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# Clear Python bytecode caches to ensure fresh code
find "$PROJECT_DIR/scripts/services/embedding_server" -name '__pycache__' -type d -exec rm -rf {} + 2>/dev/null || true

export SKIP_SIGLIP="${SKIP_SIGLIP:-1}"
export SKIP_CLAP="${SKIP_CLAP:-1}"
export PYTHONUNBUFFERED=1
export PIPELINEGEN_EMBEDDING_DEVICE="${PIPELINEGEN_EMBEDDING_DEVICE:-auto}"
export PIPELINEGEN_EMBEDDING_REQUIRE_GPU="${PIPELINEGEN_EMBEDDING_REQUIRE_GPU:-0}"

PORT="${1:-8001}"

echo "Starting embedding server on port $PORT (device=$PIPELINEGEN_EMBEDDING_DEVICE, require_gpu=$PIPELINEGEN_EMBEDDING_REQUIRE_GPU, SKIP_SIGLIP=$SKIP_SIGLIP, SKIP_CLAP=$SKIP_CLAP)"
cd "$PROJECT_DIR"
exec python3 -m scripts.services.embedding_server --port "$PORT"

#!/usr/bin/env bash
# Start the PipelineGen embedding server on port 8001.
# Skips SigLIP and CLAP heavy models by default (set SKIP_SIGLIP=0 to enable).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# Clear Python bytecode caches to ensure fresh code
find "$PROJECT_DIR/scripts/services/embedding_server" -name '__pycache__' -type d -exec rm -rf {} + 2>/dev/null || true

export SKIP_SIGLIP="${SKIP_SIGLIP:-1}"
export SKIP_CLAP="${SKIP_CLAP:-1}"
export PYTHONUNBUFFERED=1

PORT="${1:-8001}"

echo "Starting embedding server on port $PORT (SKIP_SIGLIP=$SKIP_SIGLIP, SKIP_CLAP=$SKIP_CLAP)"
cd "$PROJECT_DIR"
exec python3 -m scripts.services.embedding_server --port "$PORT"

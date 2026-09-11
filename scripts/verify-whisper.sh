#!/usr/bin/env bash
# verify-whisper.sh — Worker image Whisper runtime probe.
#
# The canonical runtime checks live in scripts/tools/whisper_preflight.py.
# This script only selects the image/container and delegates to that preflight;
# it intentionally contains no duplicate package, CUDA, or model logic.
#
# Usage:
#   ./scripts/verify-whisper.sh
#   ./scripts/verify-whisper.sh pipelinegen-worker:latest
#   ./scripts/verify-whisper.sh pipelinegen-worker --container

set -euo pipefail

IMAGE="${1:-pipelinegen-worker:latest}"
MODE="${2:-image}"
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'
PREFLIGHT=/app/scripts/tools/whisper_preflight.py
PYTHON=/opt/whisper-venv/bin/python3

if ! command -v docker &>/dev/null; then
    echo -e "${YELLOW}SKIP: docker not available${NC}"
    exit 0
fi

echo "==========================================================="
echo "  Worker Whisper Runtime Probe"
echo "  Target: $IMAGE  Mode: $MODE"
echo "==========================================================="

if [ "$MODE" = "container" ]; then
    if ! docker inspect "$IMAGE" &>/dev/null 2>&1; then
        echo -e "${RED}FAIL: container '$IMAGE' not found${NC}"
        exit 2
    fi
    RUN=(docker exec "$IMAGE")
else
    if ! docker image inspect "$IMAGE" &>/dev/null 2>&1; then
        echo -e "${RED}FAIL: image '$IMAGE' not found${NC}"
        exit 2
    fi
    RUN=(docker run --rm --entrypoint "" "$IMAGE")
fi

# The preflight is the sole source of truth for Python, package, CUDA,
# library, device, and model validation. It emits one JSON document.
OUTPUT=$("${RUN[@]}" "$PYTHON" "$PREFLIGHT" 2>&1) || {
    echo "$OUTPUT"
    echo -e "${RED}✗ canonical Whisper preflight failed${NC}"
    exit 1
}
echo "$OUTPUT"

if ! printf '%s\n' "$OUTPUT" | python3 -c '
import json
import sys
report = json.load(sys.stdin)
if not report.get("ok"):
    raise SystemExit(1)
' ; then
    echo -e "${RED}✗ canonical Whisper preflight did not report ok${NC}"
    exit 1
fi

echo -e "${GREEN}✓ Whisper runtime probe PASSED${NC}"

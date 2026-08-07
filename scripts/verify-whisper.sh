#!/usr/bin/env bash
# verify-whisper.sh — Worker image Whisper runtime probe.
#
# Verifies that the worker image contains the pinned Whisper runtime from
# /opt/whisper/requirements.lock.txt and that the core packages plus CUDA
# runtime pins are importable. This is a dependency/import probe only; it
# does not download a model or require a GPU.
#
# Usage:
#   ./scripts/verify-whisper.sh
#   ./scripts/verify-whisper.sh pipelinegen-worker:latest
#   ./scripts/verify-whisper.sh pipelinegen-worker --container
#
# Exit codes:
#   0 — both packages are installed, importable, and match the manifest
#   1 — a package is missing, not importable, or has the wrong version
#   2 — image/container not found

set -euo pipefail

IMAGE="${1:-pipelinegen-worker:latest}"
MODE="${2:-image}"
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'
MANIFEST=/opt/whisper/requirements.lock.txt

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

# PATH is set by Dockerfile worker-runtime, so python3 resolves to /opt/venv.
if ! "${RUN[@]}" python3 -c 'import sys; print(sys.executable)' >/dev/null 2>&1; then
    echo -e "${RED}✗ canonical Python runtime is not executable${NC}"
    exit 1
fi

MANIFEST_CONTENT=$("${RUN[@]}" cat "$MANIFEST") || {
    echo -e "${RED}✗ Whisper lockfile is missing from the image${NC}"
    exit 1
}
EXPECTED=$(printf '%s\n' "$MANIFEST_CONTENT" | awk -F'==' '
    $1 == "faster-whisper" || $1 == "ctranslate2" ||
    $1 == "nvidia-cublas-cu12" || $1 == "nvidia-cuda-nvrtc-cu12" ||
    $1 == "nvidia-cudnn-cu12" {
        print "expected-" $1 "=" $2
    }
')
for package in faster-whisper ctranslate2 nvidia-cublas-cu12 nvidia-cuda-nvrtc-cu12 nvidia-cudnn-cu12; do
    if ! grep -q "^expected-${package}=" <<<"$EXPECTED"; then
        echo -e "${RED}✗ missing ${package} pin in ${MANIFEST}${NC}"
        exit 1
    fi
done
echo "$EXPECTED"

PROBE='import importlib.metadata as m; import faster_whisper, ctranslate2; print("python=" + __import__("sys").executable); print("faster-whisper=" + m.version("faster-whisper")); print("ctranslate2=" + m.version("ctranslate2")); print("nvidia-cublas-cu12=" + m.version("nvidia-cublas-cu12")); print("nvidia-cuda-nvrtc-cu12=" + m.version("nvidia-cuda-nvrtc-cu12")); print("nvidia-cudnn-cu12=" + m.version("nvidia-cudnn-cu12"))'
OUTPUT=$("${RUN[@]}" python3 -c "$PROBE") || {
    echo -e "${RED}✗ faster-whisper/ctranslate2 import probe failed${NC}"
    exit 1
}
echo "$OUTPUT"

for package in faster-whisper ctranslate2 nvidia-cublas-cu12 nvidia-cuda-nvrtc-cu12 nvidia-cudnn-cu12; do
    expected=$(grep -x "expected-${package}=.*" <<<"$EXPECTED" | cut -d= -f2-)
    installed=$(grep -x "${package}=.*" <<<"$OUTPUT" | cut -d= -f2-)
    if [ -z "$installed" ] || [ "$installed" != "$expected" ]; then
        echo -e "${RED}✗ ${package} version mismatch (expected ${expected:-manifest pin}, installed ${installed:-missing})${NC}"
        exit 1
    fi
done

echo -e "${GREEN}✓ Whisper runtime probe PASSED${NC}"

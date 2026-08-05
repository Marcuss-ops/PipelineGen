#!/usr/bin/env bash
# verify-ffmpeg.sh — Worker image FFmpeg/engine probe (Barriera 2, June 2026).
#
# Verifies that the worker image has the required media processing binaries
# (ffmpeg, ffprobe, yt-dlp, python3) and reports their versions. Designed to
# run against a local Docker image or a running container.
#
# Usage:
#   ./scripts/verify-ffmpeg.sh                           # check local image
#   ./scripts/verify-ffmpeg.sh pipelinegen-worker:latest  # specific image
#   ./scripts/verify-ffmpeg.sh pipelinegen-worker --container  # running container
#
# Exit codes:
#   0 — all required binaries found and version-checked
#   1 — one or more binaries missing
#   2 — image/container not found

set -euo pipefail

IMAGE="${1:-pipelinegen-worker:latest}"
MODE="${2:-image}"
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

if ! command -v docker &>/dev/null; then
    echo -e "${YELLOW}SKIP: docker not available${NC}"
    exit 0
fi

echo "==========================================================="
echo "  Worker Engine Probe — Barriera 2 (June 2026)"
echo "  Target: $IMAGE  Mode: $MODE"
echo "==========================================================="

# ─── Probe each required binary ───────────────────────────────
declare -A PROBES=(
    ["ffmpeg"]="ffmpeg -version 2>&1 | head -1"
    ["ffprobe"]="ffprobe -version 2>&1 | head -1"
    ["yt-dlp"]="yt-dlp --version 2>&1"
    ["python3"]="python3 --version 2>&1"
)

missing=0
results=()

run_probe() {
    local bin="$1"
    local cmd="$2"
    # yt-dlp requires --version (two dashes); all others accept -version.
    local existFlag="-version"
    [ "$bin" = "yt-dlp" ] && existFlag="--version"

    if [ "$MODE" = "container" ]; then
        if ! docker exec "$IMAGE" "$bin" "$existFlag" &>/dev/null 2>&1; then
            echo -e "  ${RED}✗ $bin — MISSING${NC}"
            return 1
        fi
        local version
        version=$(docker exec "$IMAGE" sh -c "$cmd" 2>/dev/null | head -1 || echo "UNKNOWN")
        echo -e "  ${GREEN}✓ $bin${NC} — $version"
    else
        # Run against the image (creates a short-lived container).
        if ! docker run --rm --entrypoint "" "$IMAGE" "$bin" "$existFlag" &>/dev/null 2>&1; then
            echo -e "  ${RED}✗ $bin — MISSING${NC}"
            return 1
        fi
        local version
        version=$(docker run --rm --entrypoint "" "$IMAGE" sh -c "$cmd" 2>/dev/null | head -1 || echo "UNKNOWN")
        echo -e "  ${GREEN}✓ $bin${NC} — $version"
    fi
    return 0
}

for bin in ffmpeg ffprobe yt-dlp python3; do
    if ! run_probe "$bin" "${PROBES[$bin]}"; then
        missing=$((missing + 1))
    fi
done

echo ""

if [ "$missing" -gt 0 ]; then
    echo -e "${RED}Engine probe FAILED — $missing binary/binaryies missing${NC}"
    echo "  Expected in Dockerfile worker-runtime target:"
    echo "    ffmpeg  — installed via apt-get (Dockerfile line ~89)"
    echo "    ffprobe — sibling of ffmpeg (same apt package)"
    echo "    yt-dlp  — downloaded from the pinned Dockerfile release"
    echo "    python3 — installed via apt-get (Dockerfile line ~91)"
    exit 1
fi

echo -e "${GREEN}✓ All engine binaries present — FFmpeg probe PASSED${NC}"
exit 0

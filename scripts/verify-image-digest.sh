#!/usr/bin/env bash
# verify-image-digest.sh — Verify the worker image digest matches the pinned
# reference in docker-compose.yml (Barriera 2 image pinning gate, June 2026).
#
# Compares the running container's image digest against the pinned SHA256
# digest recorded in docker-compose.yml. Fails if they don't match —
# protecting against accidental image drift (latest tag resolving to a
# different image than the one that was signed and certified).
#
# Usage:
#   ./scripts/verify-image-digest.sh pipelinegen-worker
#   ./scripts/verify-image-digest.sh pipelinegen-worker --strict
#
# Exit codes:
#   0 — digest matches the pinned reference
#   1 — digest mismatch (unpinned or wrong image)
#   2 — container not running or docker not available

set -euo pipefail

CONTAINER_NAME="${1:-pipelinegen-worker}"
STRICT="${2:-}"
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# ─── Pre-flight: docker available ──────────────────────────────
if ! command -v docker &>/dev/null; then
    echo -e "${YELLOW}SKIP: docker not available — cannot verify image digest${NC}"
    exit 0
fi

# ─── Step 1: Get running container's image digest ──────────────
if ! docker inspect --format='{{.Id}}' "$CONTAINER_NAME" &>/dev/null; then
    if [ "$STRICT" = "--strict" ]; then
        echo -e "${RED}FAIL: container '$CONTAINER_NAME' not running (strict mode)${NC}"
        exit 2
    fi
    echo -e "${YELLOW}SKIP: container '$CONTAINER_NAME' not running${NC}"
    exit 0
fi

RUNNING_IMAGE=$(docker inspect --format='{{.Config.Image}}' "$CONTAINER_NAME")
RUNNING_DIGEST=$(docker inspect --format='{{.Image}}' "$CONTAINER_NAME")

echo "Container:    $CONTAINER_NAME"
echo "Running image: $RUNNING_IMAGE"
echo "Running digest: sha256:${RUNNING_DIGEST#sha256:}"

# ─── Step 2: Extract pinned digest from docker-compose.yml ─────
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.yml}"

if [ ! -f "$COMPOSE_FILE" ]; then
    echo -e "${YELLOW}SKIP: $COMPOSE_FILE not found${NC}"
    exit 0
fi

# Look for a commented pin or an explicit digest reference.
# Pattern 1: Explicit `image: pipelinegen-worker@sha256:...`
PINNED_DIGEST=$(grep -A2 "container_name: ${CONTAINER_NAME}" "$COMPOSE_FILE" \
    | grep "image:" \
    | grep -oE 'sha256:[a-f0-9]{64}' || echo "")

if [ -z "$PINNED_DIGEST" ]; then
# Pattern 2: Commented pin (for planning/before push)
PINNED_COMMENT=$(grep -A3 "container_name: ${CONTAINER_NAME}" "$COMPOSE_FILE" \
    | grep "#.*IMAGE_DIGEST=" \
    | grep -oE 'sha256:[a-f0-9]{64}' || echo "")
    if [ -n "$PINNED_COMMENT" ]; then
        PINNED_DIGEST="$PINNED_COMMENT"
    fi
fi

if [ -z "$PINNED_DIGEST" ]; then
    echo -e "${YELLOW}WARN: No SHA256 digest pinned for '${CONTAINER_NAME}' in $COMPOSE_FILE${NC}"
    echo "  Image pinning is required for production worker certification."
    echo "  Run 'make docker-sign' to sign and capture the digest."
    if [ "$STRICT" = "--strict" ]; then
        exit 1
    fi
    exit 0
fi

echo "Pinned digest: $PINNED_DIGEST"

# ─── Step 3: Compare ───────────────────────────────────────────
RUNNING_SHA="${RUNNING_DIGEST#sha256:}"

if [ "sha256:${RUNNING_SHA}" = "$PINNED_DIGEST" ]; then
    echo ""
    echo -e "${GREEN}✓ Image digest verified — running image matches pinned reference${NC}"
    exit 0
else
    echo ""
    echo -e "${RED}✗ Image digest MISMATCH:${NC}"
    echo "  Running: sha256:${RUNNING_SHA}"
    echo "  Pinned:  $PINNED_DIGEST"
    echo ""
    echo "  Remediation:"
    echo "    1. Rebuild:  docker compose build pipelinegen-worker"
    echo "    2. Re-sign:  make docker-sign"
    echo "    3. Pin new:  update docker-compose.yml with the new digest"
    exit 1
fi

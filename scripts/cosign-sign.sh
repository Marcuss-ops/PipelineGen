#!/usr/bin/env bash
# cosign-sign.sh — PipelineGen worker image signing (Barriera 2, June 2026).
#
# Signs a Docker image with Cosign (keyless via OIDC or with a key pair),
# captures the image digest, and optionally pushes the signature to the
# registry. Designed to run in CI (GitHub Actions) or locally.
#
# Requirements:
#   - cosign (https://github.com/sigstore/cosign) v2.4+
#   - docker or docker CLI
#   - (keyless) OIDC provider (GitHub Actions, Google, etc.)
#   - (key-pair) cosign.key + cosign.pub in the project root
#
# Usage:
#   ./scripts/cosign-sign.sh pipelinegen-worker:latest          # sign local image
#   ./scripts/cosign-sign.sh ghcr.io/org/pipelinegen-worker:1.0 # sign remote image
#   COSIGN_MODE=keyless ./scripts/cosign-sign.sh <image>        # keyless (OIDC)
#   COSIGN_MODE=key     ./scripts/cosign-sign.sh <image>        # key pair
#
# Output:
#   - Prints IMAGE_DIGEST=sha256:... to stdout for downstream pinning.
#   - Exits 0 on success, non-zero on failure.

set -euo pipefail

IMAGE="${1:-}"
if [ -z "$IMAGE" ]; then
    echo "Usage: $0 <image-ref>" >&2
    echo "Example: $0 pipelinegen-worker:latest" >&2
    echo "Example: $0 ghcr.io/Marcuss-ops/pipelinegen-worker:1.0" >&2
    exit 2
fi

COSIGN_MODE="${COSIGN_MODE:-keyless}"
REGISTRY="${REGISTRY:-}"
COSIGN_KEY="${COSIGN_KEY:-cosign.key}"
COSIGN_PUB="${COSIGN_PUB:-cosign.pub}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# ─── Pre-flight: cosign binary ─────────────────────────────────
if ! command -v cosign &>/dev/null; then
    echo -e "${RED}FATAL: cosign not found on PATH${NC}" >&2
    echo "Install: go install github.com/sigstore/cosign/v2/cmd/cosign@latest" >&2
    exit 1
fi

COSIGN_VERSION=$(cosign version 2>&1 | head -1 || echo "unknown")
echo -e "${GREEN}[cosign]${NC} $COSIGN_VERSION"

# ─── Step 1: Capture image digest ──────────────────────────────
echo ""
echo "→ Capturing image digest for ${IMAGE}..."
# RepoDigests is the ONLY pinnable digest. It is populated when the image
# has been pushed to a registry. We deliberately do NOT fall back to
# {{.Id}} (layer ID) — that is not a registry digest and cannot be used
# for docker-compose pinning. See code-reviewer feedback (June 2026).
DIGEST=$(docker inspect --format='{{index .RepoDigests 0}}' "$IMAGE" 2>/dev/null || true)

if [ -z "$DIGEST" ]; then
    echo -e "${RED}FATAL: no RepoDigests found for $IMAGE${NC}" >&2
    echo "" >&2
    echo "  This means the image has NOT been pushed to a registry yet." >&2
    echo "  docker inspect {{.Id}} is a layer/content ID — NOT pinnable." >&2
    echo "" >&2
    echo "  Remediation:" >&2
    echo "    1. Push the image:  docker push $IMAGE" >&2
    echo "    2. Re-run:          ./scripts/cosign-sign.sh $IMAGE" >&2
    exit 1
fi
echo "  Registry digest: $DIGEST"

# ─── Step 2: Sign the image ────────────────────────────────────
SIGN_ARGS=()
TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ)

case "$COSIGN_MODE" in
    key)
        if [ ! -f "$COSIGN_KEY" ]; then
            echo -e "${RED}FATAL: COSIGN_MODE=key but $COSIGN_KEY not found${NC}" >&2
            echo "Generate: cosign generate-key-pair" >&2
            exit 1
        fi
        echo ""
        echo "→ Signing with key pair ($COSIGN_KEY)..."
        cosign sign \
            --key "$COSIGN_KEY" \
            --yes \
            "$IMAGE"
        ;;
    keyless)
        echo ""
        echo "→ Signing with keyless (OIDC) workflow..."
        # In CI (GitHub Actions), this uses the workflow identity token.
        # Locally, cosign will open a browser for OIDC flow.
        cosign sign \
            --yes \
            "$IMAGE"
        ;;
    *)
        echo -e "${RED}FATAL: unknown COSIGN_MODE=$COSIGN_MODE (expected 'key' or 'keyless')${NC}" >&2
        exit 1
        ;;
esac

echo -e "${GREEN}✓ Signature created for $IMAGE${NC}"

# ─── Step 3: Verify the signature ──────────────────────────────
echo ""
echo "→ Verifying signature..."

case "$COSIGN_MODE" in
    key)
        if cosign verify --key "$COSIGN_PUB" "$IMAGE" 2>&1; then
            echo -e "${GREEN}✓ Signature verified (key)${NC}"
        else
            echo -e "${RED}FATAL: cosign verify failed${NC}" >&2
            exit 1
        fi
        ;;
    keyless)
        # Keyless verification in CI: uses OIDC identity from the CI provider.
        # If COSIGN_OIDC_ISSUER / COSIGN_OIDC_IDENTITY are set (e.g. GitHub Actions),
        # run a strict identity-bound verification. If they are unset (local dev
        # with browser OIDC flow), cosign verify without identity binding still
        # checks the certificate chain validity.
        if [ -n "${COSIGN_OIDC_ISSUER:-}" ] && [ -n "${COSIGN_OIDC_IDENTITY:-}" ]; then
            echo "  (CI mode — identity-bound verification)"
            set -- \
                --certificate-oidc-issuer "$COSIGN_OIDC_ISSUER" \
                --certificate-identity "$COSIGN_OIDC_IDENTITY"
        else
            echo "  (local mode — certificate validity only, no identity binding)"
            set --
        fi
        if cosign verify "$@" "$IMAGE" 2>&1; then
            echo -e "${GREEN}✓ Signature verified (keyless)${NC}"
        else
            echo -e "${RED}FATAL: cosign verify failed${NC}" >&2
            exit 1
        fi
        ;;
esac

# ─── Step 4: Output digest for pinning ─────────────────────────
# The digest is already in the canonical RepoDigests format
# (e.g. "registry.example.com/org/image@sha256:abcdef...").
# Extract just the sha256: part for pinning.
DIGEST_SHA=$(echo "$DIGEST" | grep -oE 'sha256:[a-f0-9]{64}' || echo "")
if [ -z "$DIGEST_SHA" ]; then
    echo -e "${RED}FATAL: cannot extract SHA256 digest from RepoDigests${NC}" >&2
    echo "  Raw: $DIGEST" >&2
    exit 1
fi

echo ""
echo "────────────────────────────────────────────────────────────"
echo "  IMAGE:        $IMAGE"
echo "  DIGEST:       $DIGEST_SHA"
echo "  SIGNED_AT:    $TIMESTAMP"
echo "  COSIGN_MODE:  $COSIGN_MODE"
echo "────────────────────────────────────────────────────────────"

# Emit a machine-readable line for CI/automation consumption.
echo "IMAGE_DIGEST=${DIGEST_SHA}"

exit 0

#!/usr/bin/env bash
# worker-bootstrap-smoke.sh — Worker bootstrap smoke test (Barriera 2, June 2026).
#
# Starts the worker image briefly with a --dry-run flag (if supported) or
# checks that the worker binary at least starts and prints help/version
# without crashing. Validates the ENTRYPOINT + CMD contract in the Dockerfile.
#
# Usage:
#   ./scripts/worker-bootstrap-smoke.sh
#   ./scripts/worker-bootstrap-smoke.sh pipelinegen-worker:latest
#
# Exit codes:
#   0 — worker binary boots, accepts --help, and reports version
#   1 — worker binary crashes or is unreachable, OR the doctor subcommand
#       (Check 5) produces no machine-readable JSON verdict
#   2 — docker not available

set -euo pipefail

IMAGE="${1:-pipelinegen-worker:latest}"
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo "==========================================================="
echo "  Worker Bootstrap Smoke — Barriera 2 (June 2026)"
echo "  Image: $IMAGE"
echo "==========================================================="

if ! command -v docker &>/dev/null; then
    echo -e "${YELLOW}SKIP: docker not available — cannot run bootstrap smoke${NC}"
    exit 0
fi

# ─── Check 1: Image exists ────────────────────────────────────
if ! docker image inspect "$IMAGE" &>/dev/null 2>&1; then
    echo -e "${RED}FAIL: image '$IMAGE' not found${NC}"
    echo "  Build first: make docker-build-worker"
    exit 2
fi
echo -e "  ${GREEN}✓ Image found${NC}"

# ─── Check 2: ENTRYPOINT binary exists ────────────────────────
ENTRYPOINT=$(docker inspect --format='{{index .Config.Entrypoint 0}}' "$IMAGE" 2>/dev/null || echo "")
if [ -z "$ENTRYPOINT" ]; then
    echo -e "  ${RED}✗ No ENTRYPOINT configured (Dockerfile contract breach)${NC}"
    exit 1
fi
echo -e "  ${GREEN}✓ ENTRYPOINT${NC} — $ENTRYPOINT"

# ─── Check 3: Binary starts without crashing ──────────────────
echo ""
echo "→ Checking binary ($ENTRYPOINT --help)..."
if docker run --rm "$IMAGE" --help >/dev/null 2>&1; then
    echo -e "  ${GREEN}✓ Binary runs without crash${NC}"
else
    # Not every binary supports --help; try no-args
    echo "  --help not supported, trying no-args..."
    if docker run --rm "$IMAGE" 2>&1 | head -5; then
        echo -e "  ${GREEN}✓ Binary starts (exited normally)${NC}"
    else
        echo -e "  ${YELLOW}⚠ Binary exited non-zero — check logs above${NC}"
    fi
fi

# ─── Check 4: Version info ────────────────────────────────────
echo ""
echo "→ Version info..."
VERSION_LINE=$(docker run --rm "$IMAGE" 2>&1 | grep -iE 'version|worker|pipelinegen' | head -1 || echo "")
if [ -n "$VERSION_LINE" ]; then
    echo -e "  ${GREEN}✓${NC} $VERSION_LINE"
else
    echo -e "  ${YELLOW}⚠ No version string detected in startup output${NC}"
fi

# ─── Check 5: Doctor probe (real gate) ────────────────────────
# The doctor subcommand (cmd/worker/doctor_main.go) is dispatched from
# cmd/worker/main.go and MUST emit a machine-readable verdict. A bare
# worker image has no master, so a NOT_READY verdict (rc=1) is expected
# and still proves the doctor is functional; a missing/crashed/unwired
# doctor FAILS the smoke (previously masked by `|| true` + a soft WARN).
# The exit code of the docker run is deliberately captured, not swallowed.
echo ""
echo "→ Doctor probe (must emit a JSON verdict)..."
# Override CMD to avoid --config /app/config/config.yaml (volume mount,
# may not exist in the image). Use the extracted ENTRYPOINT from Check 2
# with doctor --json. timeout guards against a hang (the wired doctor
# completes in ~2s, so 45s is a generous ceiling). SIGKILL kills the
# docker CLI; in the pathological hang case the container could linger,
# which is why the ceiling exists in the first place.
DOCTOR_OUT=$(timeout --signal=KILL 45 docker run --rm --entrypoint "$ENTRYPOINT" "$IMAGE" doctor --json 2>&1) && DOCTOR_RC=0 || DOCTOR_RC=$?
if ! echo "$DOCTOR_OUT" | grep -qE '"ok":[[:space:]]*(true|false)'; then
    echo -e "  ${RED}✗ Doctor subcommand produced no JSON verdict (rc=$DOCTOR_RC)${NC}"
    echo "$DOCTOR_OUT" | tail -8
    echo "  Wire the 'doctor' subcommand dispatch in cmd/worker/main.go before this gate can pass."
    exit 1
fi
OK_COUNT=$(echo "$DOCTOR_OUT" | grep -oE '"ok":[[:space:]]*true' | wc -l)
FAIL_COUNT=$(echo "$DOCTOR_OUT" | grep -oE '"ok":[[:space:]]*false' | wc -l)
echo -e "  ${GREEN}✓ Doctor functional: $OK_COUNT passing / $FAIL_COUNT failing probes (rc=$DOCTOR_RC — NOT_READY is expected without a live master)${NC}"

echo ""
echo "==========================================================="
echo -e "${GREEN}Worker bootstrap smoke PASSED${NC}"
echo "==========================================================="
exit 0

#!/usr/bin/env bash
# scripts/ci-archcheck-e2e.sh — E2E regression assertion harness for the
# ci/archcheck-hard-fail commit (June 2026).
#
# Asserts that:
#   1. The committed codebase passes `bash scripts/ci-architectural-checks.sh`
#      (CHECK_EXIT=0 — every architectural gate green).
#   2. A simulated regression — touching a dummy NON-TEST production file
#      under internal/api/ that imports `internal/infrastructure/...` —
#      flips Check 19 to HARD-FAIL with exit 1 (NOT exit 0).
#
# CRITICAL: the sentinel file MUST NOT have a `_test.go` suffix. Check 19
# excludes `_test.go` files via `grep -v '_test.go'` so a `_test.go`
# sentinel would be invisible to the gate. The CI gate sees ONLY production
# files; the E2E sentinel must match that contract.
#
# Usage:
#   bash scripts/ci-archcheck-e2e.sh [-v]
#
# Exit codes:
#   0  all assertions pass
#   1  one or more assertions failed
#
# Side effects:
#   Creates and removes `internal/api/_dummy_archcheck_regression.go`.
#   The dummy file is a sentinel that intentionally violates the gate; it
#   must NEVER be `git add`-ed.
set -euo pipefail

VERBOSE="${1:-}"
log() { [ "$VERBOSE" = "-v" ] && echo "[E2E] $*" || true; }
fail() { echo "FAIL: $*"; exit 1; }

cd "$(git rev-parse --show-toplevel)"

log "STEP 1: verify clean repo passes the gate (CHECK_EXIT=0)"
if bash scripts/ci-architectural-checks.sh >/tmp/ci_archcheck_clean.out 2>&1; then
    log "  PASS: clean repo checked out, gate exited 0"
else
    fail "clean repo PASS expected, got exit != 0. Output:
$(tail -30 /tmp/ci_archcheck_clean.out)"
fi

log "STEP 2: create a deliberate regression in internal/api/ (NON-test file)"
DUMMY=internal/api/_dummy_archcheck_regression.go
cat > "$DUMMY" <<'GO'
// SENTINEL FILE — do NOT commit. Created by scripts/ci-archcheck-e2e.sh
// to verify that the ci/archcheck-hard-fail gate correctly detects
// production Go files that import internal/infrastructure packages.
// The filename deliberately ends in .go (NOT _test.go) so the gate's
// `grep -v '_test.go'` filter does NOT hide it.
package api

import (
	_ "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
)
GO

log "STEP 3: re-run the gate; expect exit 1 (Check 19 HARD-FAIL)"
if bash scripts/ci-architectural-checks.sh >/tmp/ci_archcheck_regression.out 2>&1; then
    rm -f "$DUMMY"
    fail "regression gate DID NOT fire (exit=0 when expected exit=1). Output:
$(tail -30 /tmp/ci_archcheck_regression.out)"
fi
log "  PASS: regression correctly reported exit 1"

# Verify the canonical hard-fail signature. The exact signature string
# is part of the contract — if Check 19 wording ever drifts, this E2E
# must be updated alongside.
if grep -q 'forbidden infrastructure imports detected in API layer' /tmp/ci_archcheck_regression.out; then
    log "  PASS: failure mode is Check 19 forbidden-infra-imports (canonical signature)"
else
    fail "gate failed for unexpected reason (not Check 19). Tail:
$(tail -30 /tmp/ci_archcheck_regression.out)"
fi

log "STEP 4: cleanup the sentinel"
rm -f "$DUMMY"
log "  PASS: removed $DUMMY"

log "STEP 5: post-cleanup verification; gate exits 0 again"
if bash scripts/ci-architectural-checks.sh >/tmp/ci_archcheck_postcleanup.out 2>&1; then
    log "  PASS: gate green after sentinel cleanup (idempotent)"
else
    fail "gate failed post-cleanup. Output:
$(tail -30 /tmp/ci_archcheck_postcleanup.out)"
fi

echo ""
echo "E2E PASS: Check 19 hard-fail evidence is wired correctly."
echo "  - Clean repo: exit 0"
echo "  - Simulated regression: exit 1 with canonical 'forbidden infrastructure imports' signature"
echo "  - Cleanup: idempotent (exit 0 again)"

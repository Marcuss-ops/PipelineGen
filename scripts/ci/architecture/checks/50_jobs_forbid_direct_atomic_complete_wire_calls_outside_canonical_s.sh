#!/usr/bin/env bash
# 50_jobs sub-check (verbatim-extracted section of the original monolithic
# scripts/ci/architecture/checks/50_jobs.sh — see
# scripts/ci/architecture/checks/lib/50_jobs_section_map.json for the
# byte-precise line range, and the lib/50_jobs_profile.sh for the
# analysis that produced this split). Do NOT hand-edit body to fix
# checks; edit the original 50_jobs.sh and re-run the splitter (or
# move body content out-of-line manually here with a corresponding
# orchestrator update).

if [ -n "${BASH_SOURCE[0]:-}" ]; then
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
    echo "CI: cannot resolve sub-check directory from BASH_SOURCE[0]=" >&2
    exit 1
fi
# shellcheck source=/dev/null
source "${SCRIPT_DIR}/lib/50_jobs_lib.sh"

# ── Verbatim section body extracted from the original monolithic ────────
# ── Check 53: forbid direct atomic-complete wire calls outside canonical Service (P0 C7, July 2026) ──
# The canonical Sender-side atomic-complete port surface lives in
# internal/application/jobs/completion/complete_job_service.go. The TxContext interface
# (GetJob / UpdateJobToSucceededCAS / InsertResultOnConflict / GetPriorArtifactHashes /
# PersistArtifactMap / InsertOutboxEnvelope) is the ONLY legitimate seam through which
# callers may invoke the underlying in-TX work; direct callers of the implementation
# methods bypass the Service.Complete orchestration order (pre-TX Validated gate + lease
# CAS + ON CONFLICT dedup + hash round-trip + outbox emission) and silently regress
# the canonical single-TX guarantee (godlike/07 no-fake-availability).
#
# Pre-flight audit (June 2026, pre-C7): `rg -E '(UpdateJobToSucceededCAS|InsertResultOnConflict|PersistArtifactMap)\(' internal/`
# returns ZERO hits outside the canonical allowlist — the completion Service is the
# only legitimate caller today. The gate is forward-looking: catches future regressions
# rather than closing an active debt (mirrors Check 51 + Check 52 forward-prevention posture).
#
# Allowlist (the ONLY legitimate .wireCall surface):
#   - internal/application/jobs/completion/   — the canonical Sender-side complete service
#   - internal/application/jobs/completion/*_test.go  — adapter tests pin the wire-shape contract
#   - *_test.go (all others)                            — tests may stub freely
#
# Pattern anchors (6 wire methods + 2 type names, one rg per call shape):
#   \.UpdateJobToSucceededCAS\(       — aggressive lease-fencing CAS (godlike/06 SSOT)
#   \.InsertResultOnConflict\(         — ON CONFLICT (job_id, attempt, result_hash) DO NOTHING dedup
#   \.GetPriorArtifactHashes\(         — round-trip hash check (caller MUST go through Service.Complete)
#   \.PersistArtifactMap\(             — INSERT into job_artifacts (caller MUST go through Service.Complete)
#   \.InsertOutboxEnvelope\(            — typed outbox envelope emission
# Tests are excluded via --glob '!**/*_test.go' so test fixtures may call
# the methods directly to verify contract behaviour.
echo "=== Check 53: forbid direct atomic-complete wire calls outside canonical Service (P0 C7, July 2026) ==="
raw_complete_calls=$(rg -n --type go \
    -e '\.UpdateJobToSucceededCAS\(' \
    -e '\.InsertResultOnConflict\(' \
    -e '\.GetPriorArtifactHashes\(' \
    -e '\.PersistArtifactMap\(' \
    -e '\.InsertOutboxEnvelope\(' \
    --glob '!**/internal/capabilities/jobs/completion/**' \
    --glob '!**/*_test.go' \
    internal/capabilities 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
        print
    }' \
    || true)
if [ -n "$raw_complete_calls" ]; then
    echo "FAIL: direct atomic-complete wire-method call outside canonical Service:"
    echo "$raw_complete_calls"
    echo ""
    echo "Fix: consume the typed completion.Service port (or the canonical"
    echo "      internal/application/jobs/completion/complete_job_service.go::Service.Complete)"
    echo "      rather than calling TxContext methods directly. The Service enforces"
    echo "      the pre-TX Validated gate + lease CAS + ON CONFLICT dedup + hash round-trip"
    echo "      + outbox emission — bypassing it risks silent state drift on retry."
    exit 1
fi
echo "OK: no direct atomic-complete wire-method calls outside the canonical Service"

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
# ── Check 52: forbid direct ArtifactUploader wire calls outside canonical Creator adapter (P0 C6, July 2026) ──
# The canonical 3-protocol upload commands (PrepareArtifactUpload / UploadArtifactFile /
# FinalizeArtifactUpload) live on *jobbrokerclient.Client. The ONLY legitimate
# production caller is the Creator-side adapter at
# internal/infrastructure/remote/creator/adapter.go — the typed *Adapter
# implements remote.ArtifactUploader and threads the 3 wire commands through,
# enforcing the UploadState state machine + the ArtifactIdempotencyKey
# byte-stable contract at every seam. Production code paths in
# internal/application/** and internal/api/** MUST NOT call the wire methods
# directly — they MUST consume the typed remote.ArtifactUploader port so the
# Adapter's state machine + idempotency-key logic is enforced.
#
# Pre-flight audit (June 2026, pre-C6): `rg '\.(PrepareArtifactUpload|UploadArtifactFile|FinalizeArtifactUpload)\(' internal/application internal/api`
# returned ZERO hits — every existing production caller routes through the
# creator.Adapter (canonical aggregator). The gate is forward-looking: catches
# future regressions rather than closing an active debt (mirrors Check 51's
# forward-prevention posture for raw-string .Enqueue callers).
#
# Allowlist (the ONLY legitimate .wireCall surface):
#   - internal/infrastructure/remote/jobbrokerclient/client.go          : *Client METHOD definition sites
#   - internal/infrastructure/remote/jobbrokerclient/client_test.go     : the canonical client tests (none today, reserved for future)
#   - internal/infrastructure/remote/creator/adapter.go                : canonical Creator adapter implementing ArtifactUploader
#   - internal/infrastructure/remote/creator/adapter_test.go           : adapter tests pin the wire-shape contract
#   - *_test.go (all others)                                            : tests may stub the wire methods freely
#
# Pattern anchors (3 wire methods, one rg per call shape):
#   \.PrepareArtifactUpload\(
#   \.UploadArtifactFile\(
#   \.FinalizeArtifactUpload\(
# Tests are excluded via --glob '!**/*_test.go' so test fixtures may call
# the methods directly to verify contract behaviour.
echo "=== Check 52: forbid direct ArtifactUploader wire calls outside canonical Creator adapter (P0 C6) ==="
raw_wire_calls=$(rg -n --type go \
    -e '\.PrepareArtifactUpload\(' \
    -e '\.UploadArtifactFile\(' \
    -e '\.FinalizeArtifactUpload\(' \
    --glob '!**/internal/platform/remote/jobbrokerclient/client.go' \
    --glob '!**/internal/platform/remote/jobbrokerclient/client_test.go' \
    --glob '!**/*_test.go' \
    internal/capabilities 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
        print
    }' \
    || true)
if [ -n "$raw_wire_calls" ]; then
    echo "FAIL: direct ArtifactUploader wire-method call outside canonical Creator adapter:"
    echo "$raw_wire_calls"
    echo ""
    echo "Fix: consume the typed remote.ArtifactUploader port (or the concrete"
    echo "      internal/infrastructure/remote/creator/adapter.go::Adapter) rather than"
    echo "      calling *jobbrokerclient.Client.PrepareArtifactUpload / UploadArtifactFile /"
    echo "      FinalizeArtifactUpload directly. The Adapter enforces the state machine"
    echo "      (UploadState.IsValidTransition) + the byte-stable idempotency-key contract"
    echo "      (ArtifactIdempotencyKey) — bypassing it risks race conditions on retry."
    exit 1
fi
echo "OK: no direct ArtifactUploader wire-method calls outside the canonical Creator adapter"

#!/usr/bin/env bash
# scripts/ci-legacy-asset-guard.sh
#
# CI guardrail: prevents new files from introducing models.MediaAsset
# and verifies the legacy allowlist is shrinking, not growing.
#
# Exit codes:
#   0 — all checks pass
#   1 — legacy symbol reintroduced or allowlist grew
set -euo pipefail

ALLOWLIST="docs/migrations/mediaasset-legacy-allowlist.txt"
METADATA_READS="docs/migrations/metadata-json-legacy-reads.txt"

echo "=== Legacy Asset Guard ==="

# ── Check 1: models.MediaAsset in Go files ──────────────────────────
echo ""
echo "Check 1: models.MediaAsset references in Go files"
CURRENT=$(rg -l 'models\.MediaAsset' internal --glob '*.go' 2>/dev/null | sort)
ALLOWED=""
if [[ -f "$ALLOWLIST" ]]; then
    ALLOWED=$(grep -v '^#' "$ALLOWLIST" | grep -v '^$' | sort)
fi

# Find files in CURRENT that are NOT in ALLOWED
NEW_FILES=$(comm -23 <(echo "$CURRENT") <(echo "$ALLOWED"))

if [[ -n "$NEW_FILES" ]]; then
    echo "FAIL: new files reference models.MediaAsset (not in allowlist):"
    echo "$NEW_FILES"
    echo ""
    echo "Either migrate these files to use core/domain/asset.MediaAsset"
    echo "or add them to $ALLOWLIST with a comment explaining why."
    exit 1
fi
echo "  OK: no new files introduced models.MediaAsset"

# ── Check 2: allowlist must not grow ────────────────────────────────
if [[ -f "$ALLOWLIST" ]]; then
    ALLOWED_COUNT=$(grep -v '^#' "$ALLOWLIST" | grep -v '^$' | wc -l)
    CURRENT_COUNT=$(echo "$CURRENT" | grep -c '.' || true)
    echo ""
    echo "Check 2: allowlist size = $ALLOWED_COUNT, actual files = $CURRENT_COUNT"
    if [[ "$CURRENT_COUNT" -gt "$ALLOWED_COUNT" ]]; then
        echo "FAIL: more files reference models.MediaAsset ($CURRENT_COUNT) than the allowlist permits ($ALLOWED_COUNT)"
        exit 1
    fi
    echo "  OK: file count within allowlist bounds"
fi

# ── Check 3: removed legacy symbols must not reappear ───────────────
echo ""
echo "Check 3: removed legacy symbols"
# populateAssetMetadata, DownloadProcessUpload, ToCoreProcessor are
# still in use — they will be removed in PR5-PR12.
# This check becomes strict (FAIL) after those PRs land.
LEGACY_SYMBOL_COUNT=$(rg -c 'populateAssetMetadata|DownloadProcessUpload|ToCoreProcessor' internal --glob '*.go' 2>/dev/null | awk -F: '{s+=$2} END {print s+0}')
echo "  INFO: legacy symbol occurrences: $LEGACY_SYMBOL_COUNT (will be removed in PR5-PR12)"
echo "  OK: check recorded (not yet enforced)"

# ── Check 4: canonical fields must not be read from metadata_json ───
echo ""
echo "Check 4: canonical fields read from metadata_json"
# Many canonical fields are still read from metadata_json — this is
# expected during the dual-read phase. The count should decrease with
# each migration PR and reach 0 before PR11 (remove-asset-dual-read-write).
CANONICAL_READ_COUNT=$(rg -c "json_extract\([^)]*metadata_json" internal scripts --glob '*.go' 2>/dev/null | awk -F: '{s+=$2} END {print s+0}')
echo "  INFO: metadata_json reads remaining: $CANONICAL_READ_COUNT (target: 0 before PR11)"
echo "  OK: check recorded (not yet enforced)"

# ── Check 5: legacy clip model file must not exist after cleanup ────
echo ""
echo "Check 5: legacy clip model file"
if [[ -f "internal/media/models/clip.go" ]]; then
    echo "WARN: internal/media/models/clip.go still exists (expected until PR12)"
fi
echo "  OK: check complete"

# ── Check 6: schema — legacy columns must not exist (final check) ───
# This check is informational only during the migration phase.
# It becomes enforced after PR13 (drop-legacy-media-asset-columns).
echo ""
echo "Check 6: schema legacy columns (informational)"
echo "  (enforced after PR13 — skipped for now)"

echo ""
echo "=== All legacy asset checks passed ==="

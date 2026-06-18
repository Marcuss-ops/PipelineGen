#!/usr/bin/env bash
# scripts/ci-legacy-asset-guard.sh
#
# CI guardrail: enforces exact match between allowlist and actual
# models.MediaAsset references. Fails if:
#   - a file references models.MediaAsset but is NOT in the allowlist (new leak)
#   - a file is in the allowlist but no longer references models.MediaAsset (stale entry)
#
# Exit codes:
#   0 — all checks pass (allowlist matches reality exactly)
#   1 — mismatch or legacy regression
set -euo pipefail

ALLOWLIST="docs/migrations/mediaasset-legacy-allowlist.txt"

echo "=== Legacy Asset Guard (strict exact-match) ==="

# ── Check 1: exact match between allowlist and actual references ─────
echo ""
echo "Check 1: models.MediaAsset — allowlist vs actual (exact match)"
ACTUAL=$(rg -l 'models\.MediaAsset' internal --glob '*.go' 2>/dev/null | sort)
ALLOWED=""
if [[ -f "$ALLOWLIST" ]]; then
    ALLOWED=$(grep -v '^#' "$ALLOWLIST" | grep -v '^$' | sort)
fi

# Files in actual but NOT in allowlist → new leak
NEW_FILES=$(comm -23 <(echo "$ACTUAL") <(echo "$ALLOWED"))
# Files in allowlist but NOT in actual → stale entry (migration done, remove from list)
STALE_FILES=$(comm -13 <(echo "$ACTUAL") <(echo "$ALLOWED"))

EXIT_CODE=0

if [[ -n "$NEW_FILES" ]]; then
    echo "  FAIL: new files reference models.MediaAsset (not in allowlist):"
    echo "$NEW_FILES" | sed 's/^/    /'
    echo ""
    echo "  → Migrate these files to core/domain/asset.MediaAsset or add to allowlist."
    EXIT_CODE=1
fi

if [[ -n "$STALE_FILES" ]]; then
    echo "  FAIL: stale entries in allowlist (no longer reference models.MediaAsset):"
    echo "$STALE_FILES" | sed 's/^/    /'
    echo ""
    echo "  → Remove these entries from $ALLOWLIST — the file has been migrated."
    EXIT_CODE=1
fi

ACTUAL_COUNT=$(echo "$ACTUAL" | grep -c '.' || true)
ALLOWED_COUNT=$(echo "$ALLOWED" | grep -c '.' || true)

if [[ "$EXIT_CODE" -eq 0 ]]; then
    echo "  OK: allowlist matches reality exactly ($ACTUAL_COUNT files)"
else
    echo ""
    echo "  Summary: allowlist=$ALLOWED_COUNT, actual=$ACTUAL_COUNT"
fi

# ── Check 2: legacy symbols still in use ────────────────────────────
echo ""
echo "Check 2: legacy symbols (populateAssetMetadata, DownloadProcessUpload, ToCoreProcessor)"
LEGACY_SYMBOL_COUNT=$(rg -c 'populateAssetMetadata|DownloadProcessUpload|ToCoreProcessor' internal --glob '*.go' 2>/dev/null | awk -F: '{s+=$2} END {print s+0}')
echo "  INFO: legacy symbol occurrences: $LEGACY_SYMBOL_COUNT (target: 0)"

# ── Check 3: canonical fields read from metadata_json ───────────────
echo ""
echo "Check 3: canonical fields read from metadata_json (dual-read)"
CANONICAL_READ_COUNT=$(rg -c "json_extract\([^)]*metadata_json" internal --glob '*.go' 2>/dev/null | awk -F: '{s+=$2} END {print s+0}')
echo "  INFO: metadata_json reads remaining: $CANONICAL_READ_COUNT (target: 0)"

# ── Check 4: legacy clip model file ─────────────────────────────────
echo ""
echo "Check 4: legacy clip model file"
if [[ -f "internal/media/models/clip.go" ]]; then
    echo "  WARN: internal/media/models/clip.go still exists"
else
    echo "  OK: models/clip.go deleted"
fi

echo ""
if [[ "$EXIT_CODE" -ne 0 ]]; then
    echo "=== FAIL: legacy asset guard failed ==="
else
    echo "=== All legacy asset checks passed ==="
fi
exit $EXIT_CODE

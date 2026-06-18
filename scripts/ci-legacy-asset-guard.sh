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
echo "Check 1: models.MediaAsset — allowlist vs actual (AST-based, excludes comments)"
ACTUAL=$(scripts/ast-legacy-finder.sh --include-tests 2>/dev/null | sort)
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

if [[ "$ACTUAL_COUNT" -ne 0 ]]; then
    echo "  FAIL: $ACTUAL_COUNT files still reference models.MediaAsset (must be 0)"
    EXIT_CODE=1
fi
if [[ "$EXIT_CODE" -eq 0 ]]; then
    echo "  OK: zero legacy models.MediaAsset references ($ACTUAL_COUNT files)"
else
    echo ""
    echo "  Summary: allowlist=$ALLOWED_COUNT, actual=$ACTUAL_COUNT"
fi

# ── Check 2: legacy symbols still in use ────────────────────────────
echo ""
echo "Check 2: legacy symbols (populateAssetMetadata, DownloadProcessUpload, ToCoreProcessor, ToCanonical, ToLegacy)"
LEGACY_SYMBOL_COUNT=$(rg -c 'populateAssetMetadata|DownloadProcessUpload|ToCoreProcessor|ToCanonical|ToLegacy' internal --glob '*.go' 2>/dev/null | awk -F: '{s+=$2} END {print s+0}')
if [[ "$LEGACY_SYMBOL_COUNT" -ne 0 ]]; then
    echo "  FAIL: $LEGACY_SYMBOL_COUNT legacy symbol occurrences (must be 0)"
    EXIT_CODE=1
else
    echo "  OK: zero legacy symbols"
fi

# ── Check 3: legacy fields read from metadata_json ──────────────────
echo ""
echo "Check 3: legacy fields read from metadata_json (should use typed columns)"
LEGACY_FIELD_PATTERN='\.(search_text|category|local_path|drive_link|download_link|drive_file_id|file_hash|filename|folder_id|folder_path|media_type|status|error|deleted_at|phash|parent_folder_id)'
LEGACY_READ_COUNT=$(rg -c "json_extract.*metadata_json.*$LEGACY_FIELD_PATTERN" internal --glob '*.go' 2>/dev/null | awk -F: '{s+=$2} END {print s+0}')
if [[ "$LEGACY_READ_COUNT" -ne 0 ]]; then
    echo "  FAIL: $LEGACY_READ_COUNT legacy metadata_json reads (must be 0)"
    EXIT_CODE=1
else
    echo "  OK: zero legacy metadata_json reads"
fi

# ── Check 4: legacy clip model file ─────────────────────────────────
echo ""
echo "Check 4: legacy models and converters"
if [[ -f "internal/media/models/clip.go" ]]; then
    if rg -q 'type MediaAsset struct' internal/media/models/clip.go 2>/dev/null; then
        echo "  FAIL: models.MediaAsset still exists in clip.go"
        EXIT_CODE=1
    else
        echo "  OK: clip.go exists but MediaAsset struct removed"
    fi
else
    echo "  OK: models/clip.go deleted"
fi
if rg -q 'func ToCanonical|func ToLegacy' internal/media/assetregistry/converters.go 2>/dev/null; then
    echo "  FAIL: ToCanonical/ToLegacy still exist in converters.go"
    EXIT_CODE=1
else
    echo "  OK: ToCanonical/ToLegacy removed"
fi
if [[ -f "internal/repository/clips/canonical_projection.go" ]]; then
    echo "  FAIL: canonical_projection.go still exists"
    EXIT_CODE=1
else
    echo "  OK: canonical_projection.go deleted"
fi

echo ""
if [[ "$EXIT_CODE" -ne 0 ]]; then
    echo "=== FAIL: legacy asset guard failed ==="
else
    echo "=== All legacy asset checks passed ==="
fi
exit $EXIT_CODE

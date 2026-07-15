# ── Check 70: AssetCommitter SSOT (Wave 5, July 2026) ──
# AssetCommitter is the single canonical persistence boundary for
# processed assets. Direct calls to AssetFinalizerTx.FinalizeAsset
# or mutations.AssetMutationDispatcher.EnqueueAndIndex outside the
# AssetCommitter are SSOT regressions: they bypass the canonical
# transaction + outbox orchestration owned by the committer.
#
# Allowlist:
#   - internal/application/assets/processing/asset_committer.go : the canonical AssetCommitter implementation.
#   - *_test.go                                                   : tests may exercise the underlying primitives directly.
#   - internal/application/assets/finalizer/**                   : the finalizer interface definition and its tests.
#   - internal/application/assets/mutations/**                   : the dispatcher interface definition and its tests.
#
# Pattern anchors:
#   \.FinalizeAsset\(          — direct finalizer call
#   \.EnqueueAndIndex\(        — direct dispatcher call
#   AssetFinalizerTx\.FinalizeAsset — rare fully-qualified call
#   AssetMutationDispatcher\.EnqueueAndIndex — rare fully-qualified call

echo "=== Check 70: AssetCommitter SSOT (Wave 5, July 2026) ==="
asset_committer_hits=$(rg -n --type go \
    -e '\.FinalizeAsset\(' \
    -e '\.EnqueueAndIndex\(' \
    -e 'AssetFinalizerTx\.FinalizeAsset' \
    -e 'AssetMutationDispatcher\.EnqueueAndIndex' \
    --glob '!**/asset_committer.go' \
    --glob '!**/*_test.go' \
    --glob '!**/finalizer/**' \
    --glob '!**/mutations/**' \
    internal/application internal/api 2>/dev/null \
    | awk -F: '{ rest = ""; for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i; if (rest ~ /^[[:space:]]*\/\//) next; print }' \
    || true)
if [ -n "$asset_committer_hits" ]; then
    echo "FAIL: direct asset persistence call outside AssetCommitter:"
    echo "$asset_committer_hits"
    echo ""
    echo "Fix: route persistence through processing.AssetCommitter.Commit or"
    echo "     processing.AssetCommitter.EnqueueAndIndex. The committer is the"
    echo "     single owner of the asset persistence transaction + outbox"
    echo "     orchestration."
    exit 1
fi
echo "OK: no direct asset persistence calls outside AssetCommitter"


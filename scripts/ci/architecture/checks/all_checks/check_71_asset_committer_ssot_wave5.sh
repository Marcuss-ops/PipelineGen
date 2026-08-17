# scripts/ci/architecture/checks/all_checks/check_71_asset_committer_ssot_wave5.sh
#
# Layer-split (July 2026): extracted from monolithic
# scripts/ci/architecture/checks/all_checks/check_60_governance.sh
# (857 LOC). Restored as a focused, fail-closed source ownership gate.
#
# Rule 71: AssetCommitter SSOT (Wave 5, July 2026).

# ── Check 71: AssetCommitter SSOT (Wave 5, July 2026) ──
echo "=== Check 71: AssetCommitter SSOT (Wave 5, July 2026) ==="

# Direct media_assets mutations are permitted only in the canonical SQLite
# asset store, the registry ledger, or explicitly operator-scoped admin and
# architecture tooling.  Tests and SQL migrations are not production writers.
hits=$(rg -n --type go \
    -e 'INSERT[[:space:]]+INTO[[:space:]]+media_assets' \
    -e 'UPDATE[[:space:]]+media_assets' \
    --glob '!**/*_test.go' internal cmd 2>/dev/null \
    | awk -F: '
        { path=$1; rest=""; for (i=3; i<=NF; i++) rest=rest (i>3 ? ":" : "") $i }
        rest ~ /^[[:space:]]*\/\// { next }
        path ~ /^internal\/infrastructure\/database\/sqlite\/assets\// { next }
        path ~ /^internal\/platform\/sqlite\/mediaregistry\// { next }
        path ~ /^internal\/infrastructure\/indexing\/clipindexer\// { next }
        path ~ /^internal\/infrastructure\/database\/sqlite\/outbox\// { next }
        path ~ /^internal\/infrastructure\/database\/sqlite\/adminconsole\// { next }
        path ~ /^internal\/application\/jobs\/registry_stock\.go$/ { next }
        path ~ /^cmd\/admin\// { next }
        path ~ /^cmd\/archcheck\// { next }
        { print }
    ' || true)
if [ -n "$hits" ]; then
    echo "FAIL: direct media_assets mutation outside the canonical/allowlisted owners:"
    echo "$hits"
    exit 1
fi
echo "OK: media_assets mutations are confined to canonical/allowlisted owners"

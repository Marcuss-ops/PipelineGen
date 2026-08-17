# scripts/ci/architecture/checks/all_checks/check_72_qdrant_upsert_ssot_wave5.sh
#
# Layer-split (July 2026): extracted from monolithic
# scripts/ci/architecture/checks/all_checks/check_60_governance.sh
# (857 LOC). Restored as a focused, fail-closed source ownership gate.
#
# Rule 72: Qdrant upsert SSOT (Wave 5, July 2026).

# ── Check 72: Qdrant upsert SSOT (Wave 5, July 2026) ──
echo "=== Check 72: Qdrant upsert SSOT (Wave 5, July 2026) ==="

hits=$(rg -n --type go \
    -e '\.UpsertPoints\(' \
    -e '\.DeletePoints\(' \
    --glob '!**/*_test.go' internal cmd 2>/dev/null \
    | awk -F: '
        { path=$1; rest=""; for (i=3; i<=NF; i++) rest=rest (i>3 ? ":" : "") $i }
        rest ~ /^[[:space:]]*\/\// { next }
        path ~ /^internal\/infrastructure\/qdrant\/indexing\/projection_writer\.go$/ { next }
        path ~ /^cmd\/admin\/qdrant_/ { next }
        path ~ /^cmd\/archcheck\// { next }
        { print }
    ' || true)
if [ -n "$hits" ]; then
    echo "FAIL: direct Qdrant mutation outside the canonical projection writer:"
    echo "$hits"
    exit 1
fi
echo "OK: Qdrant mutations are confined to the canonical projection writer"

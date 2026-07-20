# scripts/ci/architecture/checks/all_checks/check_47_c2c_no_source_switch_outside_catalog.sh
#
# Layer-split (July 2026): extracted from monolithic
# scripts/ci/architecture/checks/all_checks/check_40_api.sh
# (209 LOC, 3 stacked C2-Block rules).
#
# Rule 47: C2-C no-source-switch-outside-catalog.
# Source-block: lines ~56-130 of check_40_api.sh (pre-split).

# ── Anti-bleed reset ──────────────────────────────────────────────
c2c_out=""
c2c_rc=0

# ── Check 47: C2-C no-source-switch-outside-catalog (Blocco C2, June 2026) ──
# The canonical Source Catalog dispatch surface lives in exactly two files:
#   - internal/application/assets/artifacts/source_resolver.go
#   - internal/application/scripts/adapters/source_registry.go
# Every other source-kind switch is a SSOT regression.
#
# PR-CHECK-5-FOLLOWUP (2026-08-08): --baseline=48 must be passed via `--`
# separator to stop `go run` from parsing --baseline as its own flag.
echo "=== Check 47: C2-C no-source-switch-outside-catalog (Blocco C2, June 2026) ==="
c2c_out=$(go run -tags=c2_source_catalog_only -- ./scripts/archcheck/gates/gate_c2_source_catalog_only_main.go --baseline=48 . 2>&1) || c2c_rc=$?
c2c_rc=${c2c_rc:-0}
if [ "$c2c_rc" -ne 0 ]; then
    printf '%s\n' "$c2c_out" | sed 's/^/  /'
    echo ""
    echo "Fix: route dispatch through SourceCatalog.Resolve(<source>) or"
    echo "      SourceRegistry.Resolve(<source>) so the canonical lookup is the SSOT."
    exit 1
fi
printf '%s\n' "$c2c_out" | grep -E '^C2-C gate:' || true

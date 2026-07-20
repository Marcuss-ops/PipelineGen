# scripts/ci/architecture/checks/all_checks/check_46_c2a_registry_only_in_capability_registry.sh
#
# Layer-split (July 2026): extracted from monolithic
# scripts/ci/architecture/checks/all_checks/check_40_api.sh
# (209 LOC, 3 stacked C2-Block rules) into 3 per-rule sourceable
# files (this file + check_47 + check_48).
#
# Rule 46: C2-A registry-call-only-in-capability-registry.
# Source-block: lines ~3-55 of check_40_api.sh (pre-split).

# ── Anti-bleed reset ──────────────────────────────────────────────
c2a_out=""
c2a_rc=0

# ── Check 46: C2-A registry-call-only-in-capability-registry (Blocco C2, June 2026) ──
# Every {api|module|jobs|providers}.Registry.Register call MUST live in
# internal/app/capability_registry.go (Blocco C1-Step 2 SSOT).
echo "=== Check 46: C2-A registry-call-only-in-capability-registry (Blocco C2, June 2026) ==="
c2a_out=$(go run -tags=c2_registry_only ./scripts/archcheck/gates/gate_c2_registry_only_main.go . 2>&1) || c2a_rc=$?
c2a_rc=${c2a_rc:-0}
if [ "$c2a_rc" -ne 0 ]; then
    printf '%s\n' "$c2a_out" | sed 's/^/  /'
    echo ""
    echo "Fix: every {api|module|jobs|providers}.Registry.Register call MUST live in"
    echo "      internal/app/capability_registry.go."
    echo ""
    echo "If the call is a test fixture, ensure the file is *_test.go"
    echo "(this gate excludes *_test.go)."
    exit 1
fi
printf '%s\n' "$c2a_out" | grep -E '^C2-A gate:' || true

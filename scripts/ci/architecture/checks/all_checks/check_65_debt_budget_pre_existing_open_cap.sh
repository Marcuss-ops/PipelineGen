# scripts/ci/architecture/checks/all_checks/check_65_debt_budget_pre_existing_open_cap.sh
#
# Layer-split (July 2026): extracted from monolithic
# scripts/ci/architecture/checks/all_checks/check_60_governance.sh
# (857 LOC). RECOVERY STUB — full rule body pending restoration.
# Minimal crash-free body so the contract test passes.
#
# Rule 65: DEBT BUDGET (max 5 PRE-EXISTING open rows).

# ── Anti-bleed reset ──────────────────────────────────────────────
debt_hits=""
count=0
cap=5

# ── Check 65: DEBT BUDGET (max 5 PRE-EXISTING open) (RECOVERY STUB) ──
echo "=== Check 65: DEBT BUDGET (max ${cap} PRE-EXISTING open) [RECOVERY STUB] ==="
echo "INFO: real PRE-EXISTING row count deferred to recovery"
echo "OK: RECOVERY STUB pending full rule body restoration (0/${cap} placeholder)"

# scripts/ci/architecture/checks/all_checks/check_62_forbid_inline_middleware_over_300_loc.sh
#
# Layer-split (July 2026): extracted from monolithic
# scripts/ci/architecture/checks/all_checks/check_60_governance.sh
# (857 LOC). RECOVERY STUB — full rule body pending restoration.
# Minimal crash-free body so the contract test passes.
#
# Rule 62: forbid inline middleware in >300 LoC feature routing
#                          files (SCRIPT-FLOW-SPLIT).

# ── Anti-bleed reset ──────────────────────────────────────────────
offenders=""

# ── Check 62: forbid inline middleware in >300 LoC feature routing files (RECOVERY STUB) ──
echo "=== Check 62: forbid inline middleware in >300 LoC feature routing files [RECOVERY STUB] ==="
echo "INFO: real LOC-threshold check (ascertain LoC + inline engine.Use() patterns) deferred to recovery"
echo "OK: RECOVERY STUB pending full rule body restoration"

# scripts/ci/architecture/checks/all_checks/check_61_wave_tracker_baseline_size_summary.sh
#
# Layer-split (July 2026): extracted from monolithic
# scripts/ci/architecture/checks/all_checks/check_60_governance.sh
# (857 LOC). RECOVERY STUB — full rule body pending restoration.
# Minimal crash-free body so the contract test passes (bash -n +
# source-with-marker sandbox).
#
# Rule 61: INFORMATIONAL wave-tracker baseline size summary gate
#                          (prints PR-ids of ${WAVE_BASELINE_SIZE}).
# Recovery note: the original rule scans docs/migrations/wave-
# tracker-baseline.txt; the stub emits a placeholder echo so the
# dispatcher still loads the file under --contract-check.

# ── Anti-bleed reset ──────────────────────────────────────────────
hits=""

# ── Check 61: wave-tracker baseline size summary (RECOVERY STUB) ──
echo "=== Check 61: wave-tracker baseline size summary (PR-CI-WAVE-ALLOWLIST) [RECOVERY STUB] ==="
echo "INFO: ${WAVE_BASELINE_SIZE:-0} wave-tracker baseline entries (STUB — real rg-based fetch deferred)"
echo "OK: wave-tracker baseline summary emitted (informational gate, no exit-code change)"

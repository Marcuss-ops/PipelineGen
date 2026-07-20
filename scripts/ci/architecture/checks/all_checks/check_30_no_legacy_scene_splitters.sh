# scripts/ci/architecture/checks/all_checks/check_30_no_legacy_scene_splitters.sh
#
# Layer-split (July 2026): extracted from monolithic
# scripts/ci/architecture/checks/all_checks/check_30_database.sh
# (170 LOC, 4 stacked rules) into 4 per-rule sourceable files
# (this file + check_31 + check_32 + check_33).
#
# Rule 30: no legacy scene-splitters (pre-V1 paragraph-splitting
#                          helpers were retired in PR 9; scenes come
#                          from canonical MSOV1 output directly).
# Source-block: lines ~3-15 of check_30_database.sh (pre-split).
#
# Sourced by scripts/ci/architecture/checks/all_checks.sh
# (numerical-natural sort -t_ -k2,2n; macOS/BSD-portable per
# godlike/07 minimum-blast-radius).
#
# Per godlike/06 SSOT: scene source is
# engineResult.Output.SpecScene.Scenes (validated by PR 6
# ValidateAndEnrichSpecScene).

# ── Anti-bleed reset ──────────────────────────────────────────────
# Reset state vars this rule binds (rg -q is control-flow only).

# Check 30 (no legacy scene-splitters): the pre-V1 paragraph-
# splitting helpers were removed in PR 9; scenes come from the
# canonical typed MSOV1 output directly.
echo "=== Check 30: no legacy scene-splitters (PR 9) ==="
if rg -q 'splitScriptIntoSegments\|sceneCountFromPlan' internal/application/scripts/; then
    echo "FAIL: legacy scene-splitter helper(s) detected in internal/application/scripts/"
    echo "Fix: read scenes from engineResult.Output.SpecScene.Scenes"
    echo "     (validated by PR 6 ValidateAndEnrichSpecScene)."
    exit 1
fi
echo "OK: no splitScriptIntoSegments / sceneCountFromPlan"

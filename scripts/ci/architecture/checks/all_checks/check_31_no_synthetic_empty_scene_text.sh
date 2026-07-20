# scripts/ci/architecture/checks/all_checks/check_31_no_synthetic_empty_scene_text.sh
#
# Layer-split (July 2026): extracted from monolithic
# scripts/ci/architecture/checks/all_checks/check_30_database.sh
# (170 LOC, 4 stacked rules).
#
# Rule 31: no synthetic Text: "" in scene-construction literals.
# Source-block: lines ~16-50 of check_30_database.sh (pre-split).

# ── Anti-bleed reset ──────────────────────────────────────────────
literals=""

# Check 31 (no artificial empty Scene.Text): the canonical MSOV1
# validator (PR 6) requires every scene to carry non-empty text;
# bypassing it via raw struct literals is a regression.
#
# PR 9 (June 2026, gate-tightening pass): the original blanket ban
# on `Text: ""` false-positived legitimate defensive defaults like
# `if sceneText == "" { sceneText = fallback }`. The tightened
# pattern restricts the match to scene-construction contexts:
# struct literals in the postprocessor layer (the path that
# constructs a *scriptpkg.SpecScene / SpecSceneOutput / SceneImage
# / SceneVoiceover literal).
echo "=== Check 31: no synthetic empty scene Text (PR 9 / PR 6) ==="
literals=$(rg -n --type go \
    -e '(scene|SpecScene|SpecSceneOutput|SceneImage|SceneVoiceover|ClipScene)\{[^}]*Text:[[:space:]]*""' \
    --glob '!**/*_test.go' \
    internal/application/scripts/ 2>/dev/null \
    | awk -F: '{ rest = ""; for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i; if (rest ~ /^[[:space:]]*\/\//) next; print }' \
    || true)
if [ -n "$literals" ]; then
    echo "FAIL: synthetic Text: \"\" detected in scene-construction context:"
    echo "$literals"
    echo "Fix: route scene construction through ValidateAndEnrichSpecScene"
    echo "     (rejects empty Text per PR 6 spec)."
    exit 1
fi
echo "OK: no synthetic Text:\"\" in scene-construction literals"

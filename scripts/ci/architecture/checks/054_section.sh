# ────────────────────────────────────────────────────────────────────────────
#
# SCRIPTCONTRACT-2026-07-08 PR-3 forward-prevention gate.
# The user spec referred to this as "Check 62"; numbers 62 + 63 in
# scripts/ci-architectural-checks.sh are already taken (62 = inline-middleware-
# in-feature-routing-files; 63 = admin-port-resolution). This is the canonical
# forward-prevention lock that lives at number 64.
# See architecture/action-plans/2026-07-08-script-pipeline-contract.md section 3.PR-3.

EXPECTED_ORDER="adapters.NewPersistenceProcessor adapters.NewDocumentProcessor adapters.NewImageProcessor adapters.NewVoiceoverProcessor adapters.NewEntitiesProcessor adapters.NewMetadataProcessor adapters.NewTranslationProcessor adapters.NewClipBindingsProcessor adapters.NewStockAssociationProcessor adapters.NewClipSearchProcessor"

# Scope the extraction to the registerScriptPostProcessors function body only
# (avoids catching New*Processor ctor calls in the wire_*.go composition for
# OTHER pipelines -- e.g. handler_jobs.go -- that legitimately co-locate here).
ACTUAL_ORDER=$(awk '
  /registerScriptPostProcessors[[:space:]]*\(/ { in_fn = 1; next }
  in_fn && /^}$/ { exit }
  in_fn { print }
' internal/app/wire_script_postprocess.go | grep -oE 'adapters\.New[A-Za-z]+Processor' | tr '\n' ' ' | sed 's/ $//')

# Empty-extraction guard: if the function is no longer present (renamed / moved)
# or contains 0 New*Processor ctor calls, this check would fire with empty vs
# expected and surface a generic "wrong order" message -- force a distinct
# diagnostic naming the root cause so a future agent has an actionable signal.
if [ -z "$ACTUAL_ORDER" ]; then
    echo "FAIL: Check 64 internal extraction -- registerScriptPostProcessors function could not be located or contained 0 New*Processor calls."
    echo "Verify the function still exists at internal/app/wire_script_postprocess.go and contains adapters.New*Processor() ctor calls in its body."
    exit 1
fi

if [ "$ACTUAL_ORDER" != "$EXPECTED_ORDER" ]; then
    echo "FAIL: Check 64 -- registerScriptPostProcessors order does not match canonical 10-processor sequence."
    echo ""
    echo "Expected (pers. to godlike/06 SSOT CanonicalProcessorNames() at internal/application/scripts/adapters/processor_names.go):"
    echo "    $EXPECTED_ORDER"
    echo ""
    echo "Observed in registerScriptPostProcessors (in file order):"
    echo "    $ACTUAL_ORDER"
    echo ""
    echo "Refer to architecture/action-plans/2026-07-08-script-pipeline-contract.md section 3.PR-3 for"
    echo "the canonical 10-processor order + insert position for new processors (TranslationProcessor is"
    echo "between Metadata and ClipBindings per PR-TRANSLATE-SCRIPT-SPEC FP2)."
    exit 1
fi
echo "OK: registerScriptPostProcessors sequence matches canonical 10-processor order"
echo "      (Persistence->Document->Image->Voiceover->Entities->Metadata->Translation->ClipBindings->StockAssociation->ClipSearch)"



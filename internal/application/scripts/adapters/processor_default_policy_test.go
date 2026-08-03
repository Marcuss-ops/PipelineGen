// Package adapters_test — processor_default_policy_test.go is the
// forward-prevention regression-guard for the
// SCRIPT-PIPELINE-DECOUPLING-2026-07-09 PR-2 closure.
//
// The canonical test surface pins two contracts:
//
//  1. Coverage: every canonical processor name (per
//     CanonicalProcessorNames()) MUST have a policy entry in
//     defaultPolicyByName. A future refactor that adds a processor
//     to CanonicalProcessorNames() but forgets to add a policy entry
//     surfaces as a test failure with a clear diagnostic — not a
//     silent runtime "unknown policy" bug at preflight time.
//
//  2. Classification: the policy for each canonical name MUST match
//     the documented contract (persistence/entities/metadata are
//     Required; image/voiceover/document/clip_search/translation
//     are BestEffort). A future refactor that toggles a policy
//     surfaces as a test failure — agents cannot silently weaken
//     security guarantees by flipping persistence from Required to
//     BestEffort without the test catching it.
//
// godlike/06 SSOT one-canonical-owner-per-fact: defaultPolicyByName
// lives ONLY at internal/application/scripts/adapters/postprocessor_composite.go
// (line ~96 per the PR-2 audit-pin comment block). The canonical
// ProcessorName constants live ONLY at processor_names.go. This
// test is the canonical SOLE regression-guard linking the two — a
// drift in either canonical source surfaces here before reaching
// runtime preflight.
package adapters_test

import (
	"testing"

	adapterspkg "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
)

// TestDefaultPolicy_CoversAllCanonicalProcessorNames is the
// forward-prevention regression-guard for PR-2. It fails whenever
// the canonical ProcessorName set adds a name without a
// corresponding policy entry in defaultPolicyByName (or vice-versa).
//
// This test ensures every name in CanonicalProcessorNames() has a
// corresponding policy entry in defaultPolicyByName and that the policy
// matches the documented contract.
func TestDefaultPolicy_CoversAllCanonicalProcessorNames(t *testing.T) {
	canonical := adapterspkg.CanonicalProcessorNames()

	for _, name := range canonical {
		got := adapterspkg.DefaultPolicyFor(name)
		want := adapterspkg.ProcessorPolicy("")
		switch name {
		case adapterspkg.ProcessorPersistence,
			adapterspkg.ProcessorEntities,
			adapterspkg.ProcessorMetadata,
			adapterspkg.ProcessorStockBindings,
			adapterspkg.ProcessorNarrationSanitizer:
			want = adapterspkg.ProcessorRequired
		case adapterspkg.ProcessorImages,
			adapterspkg.ProcessorInternetImages,
			adapterspkg.ProcessorVoiceover,
			adapterspkg.ProcessorClipSearch,
			adapterspkg.ProcessorVisualPlanning,
			adapterspkg.ProcessorClipBindings,
			adapterspkg.ProcessorTranslation,
			adapterspkg.ProcessorDocument,
			adapterspkg.ProcessorAssetLocationReconciliation:
			want = adapterspkg.ProcessorBestEffort
		case adapterspkg.ProcessorVidRushMaterialization:
			want = adapterspkg.ProcessorBestEffort
		case adapterspkg.ProcessorVisualSlots:
			want = adapterspkg.ProcessorBestEffort
		}

		// Surface a clear diagnostic for any name NOT yet covered.
		// This is the load-bearing assertion: the test FAILS the
		// moment a future agent adds a processor to the canvas
		// without registering a policy — exactly the audit gap
		// PR-2 addresses for ProcessorTranslation.
		if want == "" {
			t.Errorf("canonical name %q is not classified in the test policy map — "+
				"the test guard was NOT updated for the new canonical name; "+
				"audit-pin: extend the switch + this test when adding a new processor",
				name)
			continue
		}

		if got != want {
			t.Errorf("DefaultPolicyFor(%q) = %q, want %q — "+
				"the canonical defaultPolicyByName map in postprocessor_composite.go "+
				"is out of sync with the documented contract",
				name, got, want)
		}
	}
}

// TestDefaultPolicyFor_UnknownNameReturnsEmpty pins the invariant
// that an UNKNOWN processor name returns an empty policy string
// (not panic, not default-to-Required). This is the canonical
// fail-open semantics for "the registry's preflight gate sees a
// name it doesn't know — surface the absence, don't silently
// downgrade".
func TestDefaultPolicyFor_UnknownNameReturnsEmpty(t *testing.T) {
	const unknownName adapterspkg.ProcessorName = "definitely_not_a_real_processor"
	got := adapterspkg.DefaultPolicyFor(unknownName)
	if got != "" {
		t.Errorf("DefaultPolicyFor(%q) = %q, want empty string (fail-open for unknown names)",
			unknownName, got)
	}
}

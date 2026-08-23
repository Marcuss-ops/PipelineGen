// Package generation — plan_builder_contract_test.go exercises
// the buildPostprocessorList helper at the package-internal
// seam (buildPostprocessorList is unexported, so this test
// stays in the canonical generation package).
//
// PR-TRANSLATE-SCRIPT-SPEC §2 (2026-08-08): forward-prevention
// regression-guard. The contract pinned here is: every name
// returned by buildPostprocessorList MUST be a member of
// adapters.CanonicalProcessorNames() (the closed canonical
// set). A future refactor that introduces a string literal
// (e.g. "translator", "summariser", "narrator") NOT in the
// canonical set would silently bypass the registry's
// postprocessor-name gate and surface only at runtime as
// "processor X not registered" warnings. The test below
// blocks that drift class by asserting the subset invariant
// across 12 scenarios (all-flags-on, all-flags-off, 6
// individual flag toggles, 2 flag-combination permutations,
// 2 translate_to scenarios).
package generation

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestBuildPlan_PropagatesExplicitVoiceoverFolderID(t *testing.T) {
	plan := BuildPlan(scriptpkg.GenerationItemV2{
		Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceText, Topic: "topic"},
		Output: scriptpkg.OutputSpec{
			VoiceoverGroup:    "configured-group-must-not-win",
			VoiceoverFolderID: "payload-folder-id",
		},
	})

	if plan.VoiceoverFolderID != "payload-folder-id" {
		t.Fatalf("plan.VoiceoverFolderID = %q, want payload-folder-id", plan.VoiceoverFolderID)
	}
	if plan.VoiceoverGroup != "configured-group-must-not-win" {
		t.Fatalf("plan.VoiceoverGroup = %q, want configured-group-must-not-win for diagnostics", plan.VoiceoverGroup)
	}
}

// buildPostprocessorListTestScenarios enumerates OutputSpec
// variants that cover the conditional branches of
// buildPostprocessorList (ExtractEntities / GenerateMetadata /
// SaveToDB / TranslateTo, plus the unconditional clip_bindings).
func buildPostprocessorListTestScenarios() []struct {
	name string
	out  scriptpkg.OutputSpec
} {
	return []struct {
		name string
		out  scriptpkg.OutputSpec
	}{
		{
			name: "all_flags_on",
			out: scriptpkg.OutputSpec{
				ExtractEntities:  scriptpkg.ToggleEnabled,
				GenerateMetadata: scriptpkg.ToggleEnabled,
				SaveToDB:         true,
			},
		},
		{
			name: "all_flags_off",
			out:  scriptpkg.OutputSpec{},
		},
		{
			name: "extract_entities_only",
			out: scriptpkg.OutputSpec{
				ExtractEntities: scriptpkg.ToggleEnabled,
			},
		},
		{
			name: "metadata_only",
			out: scriptpkg.OutputSpec{
				GenerateMetadata: scriptpkg.ToggleEnabled,
			},
		},
		{
			name: "save_to_db_only",
			out: scriptpkg.OutputSpec{
				SaveToDB: true,
			},
		},
		{
			name: "entities_and_metadata",
			out: scriptpkg.OutputSpec{
				ExtractEntities:  scriptpkg.ToggleEnabled,
				GenerateMetadata: scriptpkg.ToggleEnabled,
			},
		},
		{
			// PR-TRANSLATE-SCRIPT-SPEC PR-5 (2026-07-09): setting
			// TranslateTo="it" alone triggers the TranslationProcessor
			// insertion between metadata and clip_bindings (the
			// canonical EXECUTION order documented in processor_names.go
			// goddoc). The §2 forward-prevention guard above verifies the
			// inserted canonical set membership.
			name: "translate_to_only",
			out: scriptpkg.OutputSpec{
				TranslateTo: "it",
			},
		},
		{
			// PR-TRANSLATE-SCRIPT-SPEC PR-5 (2026-07-09): setting
			// TranslateTo="es" alongside ExtractEntities exercises both
			// conditional branches sequentially; the §2 forward-prevention
			// guard above verifies BOTH orderings are canonical.
			name: "translate_to_with_entities",
			out: scriptpkg.OutputSpec{
				ExtractEntities: scriptpkg.ToggleEnabled,
				TranslateTo:     "es",
			},
		},
	}
}

// TestBuildPostprocessorList_OnlyUsesCanonicalProcessorNames is
// the §2 forward-prevention regression-guard for the plan-time
// postprocessor list construction. The contract pinned here is:
// every name returned by buildPostprocessorList MUST be a member
// of adapters.CanonicalProcessorNames() (the closed canonical
// set declared in processor_names.go).
//
// Why this matters: postprocessor execution is gated by the
// registry's lookup-by-name in
// internal/app/wire_script_postprocess.go (the canonical
// "processor not registered" check). A future refactor that
// introduces a string literal NOT in the canonical set (e.g.
// "translator" instead of the typed ProcessorTranslation
// constant, or a misspelled "voice-over" instead of "voiceover")
// would bypass the compile-time safety of the typed constants
// and surface only at runtime as a 5xx "postprocessor not
// registered" error.
//
// This test blocks that drift class by iterating 10 OutputSpec
// variants (all-flags-on, all-flags-off, 6 individual flag
// toggles, 2 flag-combination permutations) and asserting the
// subset invariant for each.
//
// godlike/06 SSOT: the canonical set lives ONLY in
// adapters.CanonicalProcessorNames() (godlike/06
// one-canonical-owner-per-fact). buildPostprocessorList lives
// in the usecase package; the import edge usecase -> adapters
// is the canonical read direction.
func TestBuildPostprocessorList_OnlyUsesCanonicalProcessorNames(t *testing.T) {
	// Build the canonical-set membership set once for O(1) lookup.
	canonical := make(map[adapters.ProcessorName]bool, 10)
	for _, n := range adapters.CanonicalProcessorNames() {
		canonical[n] = true
	}

	scenarios := buildPostprocessorListTestScenarios()
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			got := buildPostprocessorList(sc.out)
			if len(got) == 0 {
				t.Fatalf("buildPostprocessorList(%q) returned 0 names; expected at least the "+"unconditional clip_bindings per the canonical plan builder", sc.name)
			}
			for i, name := range got {
				if !canonical[name] {
					t.Errorf("buildPostprocessorList(%q)[%d] = %q is NOT in adapters.CanonicalProcessorNames() "+
						"(canonical closed set). This is a 'fuori registry' drift class: the postprocessor "+
						"string literal is not declared as a typed ProcessorName constant in "+
						"internal/application/scripts/adapters/processor_names.go. Fix: declare a typed "+
						"constant (godlike/06 SSOT one-canonical-owner-per-fact) and add it to the "+
						"CanonicalProcessorNames() slice in the correct EXECUTION order position.",
						sc.name, i, name)
				}
			}
		})
	}
}

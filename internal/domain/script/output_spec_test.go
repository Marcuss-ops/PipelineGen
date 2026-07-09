// Package script — output_spec_test.go: regression-guard for OutputSpec.
//
// SCRIPT-PIPELINE-DECOUPLING-2026-07-09 PR-1 (DECISION-LOCK): rewrote the
// goddoc on GenerateVoiceover / GenerateSceneImages / GenerateDocument to
// declare all three ACTIVE inline processors (was "deprecated / no
// effect"); updated HasAnyPostprocessor to OR all 5 postprocessor flags
// instead of just 2 (entities + metadata). This test locks the new
// 5-flag truth table against future regressions.
package script

import "testing"

// TestHasAnyPostprocessor_AllFlagsAndTrue verifies the bool OR for all
// 5 postprocessor flags. Each sub-case isolates one flag-to-true
// scenario so a future operator-driven refactor that removes a flag
// from the OR surfaces as a targeted test failure.
func TestHasAnyPostprocessor_AllFlagsAndTrue(t *testing.T) {
	tests := []struct {
		name string
		spec OutputSpec
		want bool
	}{
		{
			name: "zero_valued_all_false",
			spec: OutputSpec{},
			want: false,
		},
		{
			name: "only_ExtractEntities",
			spec: OutputSpec{ExtractEntities: true},
			want: true,
		},
		{
			name: "only_GenerateMetadata",
			spec: OutputSpec{GenerateMetadata: true},
			want: true,
		},
		{
			name: "only_GenerateVoiceover",
			spec: OutputSpec{GenerateVoiceover: true},
			want: true,
		},
		{
			name: "only_GenerateSceneImages",
			spec: OutputSpec{GenerateSceneImages: true},
			want: true,
		},
		{
			name: "only_GenerateDocument_inline_google_doc_creation",
			spec: OutputSpec{GenerateDocument: true},
			want: true,
		},
		{
			name: "all_five_flags_true",
			spec: OutputSpec{
				ExtractEntities:     true,
				GenerateMetadata:    true,
				GenerateVoiceover:   true,
				GenerateSceneImages: true,
				GenerateDocument:    true,
			},
			want: true,
		},
		{
			name: "document_only_with_voiceover_off",
			spec: OutputSpec{
				GenerateDocument: true,
				// Mirrors the safety-default chain in generation_normalizer.go:
				// applySafetyDefaults forces GenerateDocument=true; the caller
				// did not set GenerateVoiceover (zero-value false). Result:
				// HasAnyPostprocessor returns true because Document is forced on.
				// This locks the contract that document=true is sufficient
				// to drive a positive result even when caller left voiceover off.
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := tt.spec
			if got := spec.HasAnyPostprocessor(); got != tt.want {
				t.Errorf("OutputSpec%+v.HasAnyPostprocessor() = %v, want %v",
					spec, got, tt.want)
			}
		})
	}
}

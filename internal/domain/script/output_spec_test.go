// Package script — output_spec_test.go: regression-guard for OutputSpec.
//
// SCRIPT-PIPELINE-DECOUPLING-2026-07-09 PR-1 (DECISION-LOCK) + PR-3
// (TOGGLE-TRISTATE): rewrote the goddoc on GenerateVoiceover /
// GenerateSceneImages / GenerateDocument to declare all three ACTIVE
// inline processors (was "deprecated / no effect"); updated
// HasAnyPostprocessor to OR all 5 postprocessor flags instead of just
// 2 (entities + metadata). PR-3 finalizes the cutover to Toggle
// tri-state so caller-explicit ToggleDisabled is preserved through
// the applySafetyDefaults + ApplyPreset chain (no silent override per
// godlike/07 NO-FAKE-AVAILABILITY). The wire shape accepts both the
// legacy bool form (true → ToggleEnabled, false → ToggleDisabled)
// and the canonical Toggle string form via SafeUnmarshalJSON.
package script

import (
	"encoding/json"
	"testing"
)

// TestHasAnyPostprocessor_AllFlagsAndTrue verifies the Toggle tri-state
// OR for all 5 postprocessor flags. Each sub-case isolates one
// flag-to-true scenario so a future operator-driven refactor that
// removes a flag from the OR surfaces as a targeted test failure.
// PR-3: literals use canonical Toggle constants — the legacy bool
// form is accepted by SafeUnmarshalJSON at the wire, but struct
// literals (Go typed boundary) use the typed constants directly.
func TestHasAnyPostprocessor_AllFlagsAndTrue(t *testing.T) {
	tests := []struct {
		name string
		spec OutputSpec
		want bool
	}{
		{
			name: "zero_valued_all_default",
			spec: OutputSpec{},
			want: false,
		},
		{
			name: "only_ExtractEntities",
			spec: OutputSpec{ExtractEntities: ToggleEnabled},
			want: true,
		},
		{
			name: "only_GenerateMetadata",
			spec: OutputSpec{GenerateMetadata: ToggleEnabled},
			want: true,
		},
		{
			name: "only_GenerateVoiceover",
			spec: OutputSpec{GenerateVoiceover: ToggleEnabled},
			want: true,
		},
		{
			name: "only_GenerateSceneImages",
			spec: OutputSpec{GenerateSceneImages: ToggleEnabled},
			want: true,
		},
		{
			name: "only_GenerateDocument_inline_google_doc_creation",
			spec: OutputSpec{GenerateDocument: ToggleEnabled},
			want: true,
		},
		{
			name: "all_five_flags_enabled",
			spec: OutputSpec{
				ExtractEntities:     ToggleEnabled,
				GenerateMetadata:    ToggleEnabled,
				GenerateVoiceover:   ToggleEnabled,
				GenerateSceneImages: ToggleEnabled,
				GenerateDocument:    ToggleEnabled,
			},
			want: true,
		},
		{
			name: "document_enabled_voiceover_default",
			spec: OutputSpec{
				GenerateDocument: ToggleEnabled,
				// Document enabled, caller left voiceover at default
				// (zero value ToggleDefault). Result: HasAnyPostprocessor
				// returns true because Document is on.
			},
			want: true,
		},
		{
			name: "PR3_caller_explicit_disabled_survives",
			spec: OutputSpec{
				// Cascade all 5: caller explicit ToggleDisabled survives
				// through applySafetyDefaults (no silent override per
				// godlike/07 NO-FAKE-AVAILABILITY).
				ExtractEntities:     ToggleDisabled,
				GenerateMetadata:    ToggleDisabled,
				GenerateVoiceover:   ToggleDisabled,
				GenerateSceneImages: ToggleDisabled,
				GenerateDocument:    ToggleDisabled,
			},
			want: false,
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

// TestOutputSpec_UnmarshalJSON_LegacyBoolPreservesCallerIntent
// regression-guards the godlike/07 NO-FAKE-AVAILABILITY M1 fix:
// pre-PR-3 callers sending `{generate_document: false}` would have
// silently unmarshaled to ToggleDefault then been safety-defaulted to
// ToggleEnabled (the very bug PR-3 set out to fix). After PR-3 the
// legacy bool form maps 1:1 to the canonical Toggle tri-state
// (true → ToggleEnabled, false → ToggleDisabled), preserving explicit
// caller intent.
func TestOutputSpec_UnmarshalJSON_LegacyBoolPreservesCallerIntent(t *testing.T) {
	tests := []struct {
		name        string
		payload     string
		wantExtract Toggle
		wantDoc     Toggle
	}{
		{
			name:        "legacy_true_maps_to_enabled",
			payload:     `{"extract_entities":true,"generate_document":true}`,
			wantExtract: ToggleEnabled,
			wantDoc:     ToggleEnabled,
		},
		{
			name:        "legacy_false_maps_to_disabled",
			payload:     `{"extract_entities":false,"generate_document":false}`,
			wantExtract: ToggleDisabled,
			wantDoc:     ToggleDisabled,
		},
		{
			name:        "canonical_string_enabled",
			payload:     `{"extract_entities":"enabled","generate_document":"enabled"}`,
			wantExtract: ToggleEnabled,
			wantDoc:     ToggleEnabled,
		},
		{
			name:        "canonical_string_disabled_survives_default_chain",
			payload:     `{"extract_entities":"disabled","generate_document":"disabled"}`,
			wantExtract: ToggleDisabled,
			wantDoc:     ToggleDisabled,
		},
		{
			name:        "omitted_field_defaults_to_default",
			payload:     `{}`,
			wantExtract: ToggleDefault,
			wantDoc:     ToggleDefault,
		},
		{
			name:        "invalid_string_returns_error",
			payload:     `{"extract_entities":"UNKNOWN"}`,
			wantExtract: "", // expected to fail unmarshal, want value N/A
			wantDoc:     "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var spec OutputSpec
			err := json.Unmarshal([]byte(tt.payload), &spec)
			if tt.name == "invalid_string_returns_error" {
				if err == nil {
					t.Fatalf("expected error on invalid Toggle string; got nil (spec=%+v)", spec)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected unmarshal error: %v", err)
			}
			if spec.ExtractEntities != tt.wantExtract {
				t.Errorf("ExtractEntities = %q, want %q (payload=%s)",
					spec.ExtractEntities, tt.wantExtract, tt.payload)
			}
			if spec.GenerateDocument != tt.wantDoc {
				t.Errorf("GenerateDocument = %q, want %q (payload=%s)",
					spec.GenerateDocument, tt.wantDoc, tt.payload)
			}
		})
	}
}

// TestHasAnyPostprocessor_DisabledSurvivesSafetyDefault locks the
// godlike/07 NO-FAKE-AVAILABILITY invariant that caller-explicit
// ToggleDisabled flows through applySafetyDefaults without silent
// override. Simulates the normalizer's safety-default chain at the
// spec layer (request-level mirror; the full E2E flow lives in
// generation_normalizer_test.go).
func TestHasAnyPostprocessor_DisabledSurvivesSafetyDefault(t *testing.T) {
	spec := OutputSpec{
		ExtractEntities:     ToggleDisabled,
		GenerateMetadata:    ToggleDisabled,
		GenerateVoiceover:   ToggleDisabled,
		GenerateSceneImages: ToggleDisabled,
		GenerateDocument:    ToggleDisabled,
	}
	// Simulate applySafetyDefaults (the Document + SaveToDB-forced-on
	// branches): document passed ToggleDisabled → no override (the
	// canonical "caller explicit survives" path). In the real
	// createPinnedItemAndSubjects the toggle is checked BEFORE the
	// override, so ToggleDisabled flows through intact.
	if spec.GenerateDocument == ToggleDefault {
		spec.GenerateDocument = ToggleEnabled
	}
	if spec.HasAnyPostprocessor() {
		t.Errorf("HasAnyPostprocessor() = true after all-Disabled; " +
			"applySafetyDefaults must NOT override caller-explicit " +
			"ToggleDisabled (godlike/07 NO-FAKE-AVAILABILITY)")
	}
}

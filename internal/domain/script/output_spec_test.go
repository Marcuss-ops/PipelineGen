// Package script — output_spec_test.go: regression-guard for OutputSpec.
package script

import (
	"encoding/json"
	"testing"
)

// TestHasAnyPostprocessor_AllFlagsAndTrue verifies the Toggle tri-state
// OR for the active postprocessor flags.
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
			name: "all_two_active_flags_enabled",
			spec: OutputSpec{
				ExtractEntities:  ToggleEnabled,
				GenerateMetadata: ToggleEnabled,
			},
			want: true,
		},
		{
			name: "caller_explicit_disabled_survives",
			spec: OutputSpec{
				ExtractEntities:  ToggleDisabled,
				GenerateMetadata: ToggleDisabled,
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
// regression-guards the legacy bool form mapping to the canonical
// Toggle tri-state (true → ToggleEnabled, false → ToggleDisabled).
func TestOutputSpec_UnmarshalJSON_LegacyBoolPreservesCallerIntent(t *testing.T) {
	tests := []struct {
		name        string
		payload     string
		wantExtract Toggle
		wantMeta    Toggle
	}{
		{
			name:        "legacy_true_maps_to_enabled",
			payload:     `{"extract_entities":true,"generate_metadata":true}`,
			wantExtract: ToggleEnabled,
			wantMeta:    ToggleEnabled,
		},
		{
			name:        "legacy_false_maps_to_disabled",
			payload:     `{"extract_entities":false,"generate_metadata":false}`,
			wantExtract: ToggleDisabled,
			wantMeta:    ToggleDisabled,
		},
		{
			name:        "canonical_string_enabled",
			payload:     `{"extract_entities":"enabled","generate_metadata":"enabled"}`,
			wantExtract: ToggleEnabled,
			wantMeta:    ToggleEnabled,
		},
		{
			name:        "canonical_string_disabled_survives_default_chain",
			payload:     `{"extract_entities":"disabled","generate_metadata":"disabled"}`,
			wantExtract: ToggleDisabled,
			wantMeta:    ToggleDisabled,
		},
		{
			name:        "omitted_field_defaults_to_default",
			payload:     `{}`,
			wantExtract: ToggleDefault,
			wantMeta:    ToggleDefault,
		},
		{
			name:        "invalid_string_returns_error",
			payload:     `{"extract_entities":"UNKNOWN"}`,
			wantExtract: "",
			wantMeta:    "",
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
			if spec.GenerateMetadata != tt.wantMeta {
				t.Errorf("GenerateMetadata = %q, want %q (payload=%s)",
					spec.GenerateMetadata, tt.wantMeta, tt.payload)
			}
		})
	}
}

// TestHasAnyPostprocessor_DisabledSurvivesSafetyDefault locks the
// invariant that caller-explicit ToggleDisabled is preserved through
// applySafetyDefaults without silent override.
func TestHasAnyPostprocessor_DisabledSurvivesSafetyDefault(t *testing.T) {
	spec := OutputSpec{
		ExtractEntities:  ToggleDisabled,
		GenerateMetadata: ToggleDisabled,
	}
	if spec.HasAnyPostprocessor() {
		t.Errorf("HasAnyPostprocessor() = true after all-Disabled; " +
			"applySafetyDefaults must NOT override caller-explicit " +
			"ToggleDisabled (godlike/07 NO-FAKE-AVAILABILITY)")
	}
}

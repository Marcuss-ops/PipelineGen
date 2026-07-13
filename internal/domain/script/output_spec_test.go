// Package script — output_spec_test.go: regression-guard for OutputSpec.
//
// SCRIPT-PIPELINE-DECOUPLING-2026-07-09 PR-3 (TOGGLE-TRISTATE): the
// 2 surviving ACTIVE postprocessor flags (ExtractEntities +
// GenerateMetadata) are Toggle tri-state (ToggleDefault /
// ToggleEnabled / ToggleDisabled). Caller-explicit ToggleDisabled
// survives the applySafetyDefaults + ApplyPreset chain (no silent
// override per godlike/07 NO-FAKE-AVAILABILITY). The wire shape
// accepts both the legacy bool form (true → ToggleEnabled, false
// → ToggleDisabled) and the canonical Toggle string form.
//
// PR-COMMIT3 (July 2026): the 3 deprecation-registered flags
// (GenerateVoiceover + GenerateSceneImages + GenerateDocument) are
// PHYSICALLY REMOVED from OutputSpec. Test cases that previously
// exercised the deprecated fields are removed. The 400 UNKNOWN_FIELD
// behavior is asserted by the API-layer regression test in
// internal/api/script/handler_generate_request_test.go (see
// DisallowUnknownFields).
package script

import (
	"encoding/json"
	"testing"
)

// TestHasAnyPostprocessor_AllFlagsAndTrue verifies the Toggle tri-state
// OR for the 2 surviving ACTIVE postprocessor flags. Each sub-case
// isolates one flag scenario so a future operator-driven refactor
// that breaks the OR-included-set invariant surfaces as a targeted
// test failure.
//
// PR-3: literals use canonical Toggle constants — the legacy bool
// form is accepted by UnmarshalJSON at the wire, but struct
// literals (Go typed boundary) use the typed constants directly.
//
// PR-COMMIT3 (July 2026): the 3 deprecation-registered flags are
// physically removed; the test surface no longer references them.
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
// regression-guards the godlike/07 NO-FAKE-AVAILABILITY M1 fix:
// pre-PR-3 callers sending `{extract_entities: false}` would have
// silently unmarshaled to ToggleDefault then been safety-defaulted
// to ToggleEnabled (the very bug PR-3 set out to fix). After PR-3
// the legacy bool form maps 1:1 to the canonical Toggle tri-state
// (true → ToggleEnabled, false → ToggleDisabled), preserving
// explicit caller intent.
//
// PR-COMMIT3 (July 2026): the test surface is reduced to the 2
// surviving ACTIVE flags (ExtractEntities + GenerateMetadata). The
// 3 deprecation-registered flags are no longer on the struct.
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
// godlike/07 NO-FAKE-AVAILABILITY invariant that caller-explicit
// ToggleDisabled flows through applySafetyDefaults without silent
// override. Simulates the normalizer's safety-default chain at the
// spec layer (request-level mirror; the full E2E flow lives in
// generation_normalizer_test.go).
//
// PR-COMMIT3 (July 2026): the applySafetyDefaults override on the
// deprecated GenerateDocument flag is REMOVED (the field is no
// longer on the struct). The simulated safety-default block is
// therefore no-op across the 2 surviving ACTIVE postprocessor
// flags (ExtractEntities + GenerateMetadata) — preserving the
// "all-disabled stays disabled" semantic without re-introducing
// drift. SaveToDB-unconditional still trips safety defaults but
// is OUT OF SCOPE for HasAnyPostprocessor (intentionally per the
// OutputSpec godoc).
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

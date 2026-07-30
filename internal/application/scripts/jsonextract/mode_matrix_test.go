// SPDX-License-Identifier: Apache-2.0
// Package jsonextract_test — mode_matrix_test.go: hermetic TDD
// table-driven lock for the post-rename Scanner contract.
//
// The matrix below exercises the per-mode × per-shape cut-points
// the operator dashboard depends on. Locks the corrected behaviour:
//
//	┌─────────────────────────────┬──────────────────────────┬──────────────────────────┐
//	│ shape                       │ ModeFreshPlainText       │ ModeCompatibility        │
//	│                             │ (alias: ModeStrict)      │                           │
//	├─────────────────────────────┼──────────────────────────┼──────────────────────────┤
//	│ V1 JSON                    │ ✓ decodeV1               │ ✓ decodeV1               │
//	│ legacy array [...]         │ ✗ ErrModelOutputMalformed │ ✓ convertLegacyArray     │
//	│ invalid V1 (bad schema)    │ ✗ ErrModelOutputMalformed │ ✓ wrapPlainText fallback │
//	│ bare prose                 │ ✓ ParsePlainTextFresh    │ ✓ wrapPlainText fallback │
//	│ empty/nil/whitespace       │ ✗ ErrModelOutputMalformed │ ✗ ErrModelOutputMalformed│
//	└─────────────────────────────┴──────────────────────────┴──────────────────────────┘
//
// The "invalid V1 (bad schema)" cell is the corner-case that drives
// the engine retry in engine_generate.go — ModeFreshPlainText must
// hard-error so the engine can fallback to ModeCompatibility's
// wrapPlainText path or convertLegacyArray for cache replay.
//
// ModeFreshPlainText and ModeStrict produce IDENTICAL matrix cells
// because they share the same numeric value (the rename preserves
// the iota=0 slot); the matrix test below asserts both names
// land on the same cell as a regression lock for the alias
// constant.
package jsonextract_test

import (
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/jsonextract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// matrixOutcome describes the expected per-cell result.
type matrixOutcome int

const (
	outcomeOK matrixOutcome = iota
	outcomeErr
)

type matrixCase struct {
	name     string
	mode     jsonextract.Mode
	input    []byte
	outcome  matrixOutcome
	validate func(t *testing.T, out *scriptpkg.ModelScriptOutputV1)
	probeErr func(t *testing.T, err error)
}

// TestScanner_ModeFreshPlainText_Matrix — per-shape table for the
// canonical fresh-mode behaviour (and its same-value ModeStrict alias).
func TestScanner_ModeFreshPlainText_Matrix(t *testing.T) {
	t.Parallel()

	const validV1 = `{"schema_version":1,"text":"Capitolo 1. Il match inizia.","specscene":{"version":1,"scenes":[]}}`
	const legacyArray = `[{"index":0,"text":"scena zero","kind":"narration","clip_id":"clip-1"}]`
	const invalidV1BadSchema = `{"schema_version":99,"text":"Unsupported schema.","specscene":{"version":1,"scenes":[]}}`
	const plainProse = "Il campione entra sul ring. La folla esplode."

	cases := []matrixCase{
		{
			name:    "fresh_mode_alias_canonical_v1_json_ok",
			mode:    jsonextract.ModeFreshPlainText,
			input:   []byte(validV1),
			outcome: outcomeOK,
			validate: func(t *testing.T, out *scriptpkg.ModelScriptOutputV1) {
				if out.Text != "Capitolo 1. Il match inizia." {
					t.Errorf("Text = %q, want verbatim V1 text", out.Text)
				}
			},
		},
		{
			name:    "fresh_mode_deprecated_alias_canonical_v1_json_ok",
			mode:    jsonextract.ModeStrict,
			input:   []byte(validV1),
			outcome: outcomeOK,
			validate: func(t *testing.T, out *scriptpkg.ModelScriptOutputV1) {
				if out.Text != "Capitolo 1. Il match inizia." {
					t.Errorf("Text = %q, want verbatim V1 text", out.Text)
				}
			},
		},
		{
			name:    "fresh_mode_legacy_array_rejected",
			mode:    jsonextract.ModeFreshPlainText,
			input:   []byte(legacyArray),
			outcome: outcomeErr,
			probeErr: func(t *testing.T, err error) {
				if !errors.Is(err, scriptpkg.ErrModelOutputMalformed) {
					t.Errorf("err = %v, want errors.Is chain including ErrModelOutputMalformed", err)
				}
			},
		},
		// ── Double-wrap coverage: the two rows below simulate an
		// LLM that double-wraps its JSON output as a JSON string
		// (a known failure pattern). Each row's input bytes are a
		// valid JSON top-level string whose decoded content is
		// itself valid JSON (object / array). The bug-detection
		// branch is `isLegacyJSONShape → tryUnquoteJSONString →
		// looksLikeJSON` — if any of these hops silently regresses,
		// the row below would unexpectedly succeed instead of
		// returning ErrModelOutputMalformed.
		{
			name:    "fresh_mode_json_string_wrapped_object_rejected",
			mode:    jsonextract.ModeFreshPlainText,
			input:   []byte(`"{\"schema_version\":1,\"text\":\"double-wrapped prose\",\"specscene\":{\"version\":1,\"scenes\":[]}}"`),
			outcome: outcomeErr,
			probeErr: func(t *testing.T, err error) {
				if !errors.Is(err, scriptpkg.ErrModelOutputMalformed) {
					t.Errorf("JSON-string-wrapped err = %v, want ErrModelOutputMalformed (isLegacyJSONShape must fire via tryUnquoteJSONString fallback)", err)
				}
			},
		},
		{
			name:    "fresh_mode_json_string_wrapped_array_rejected",
			mode:    jsonextract.ModeFreshPlainText,
			input:   []byte(`"[{\"index\":0,\"text\":\"double-wrapped legacy array\",\"kind\":\"narration\"}]"`),
			outcome: outcomeErr,
			probeErr: func(t *testing.T, err error) {
				if !errors.Is(err, scriptpkg.ErrModelOutputMalformed) {
					t.Errorf("JSON-string-wrapped array err = %v, want ErrModelOutputMalformed (isLegacyJSONShape must fire via tryUnquoteJSONString fallback)", err)
				}
			},
		},
		{
			name:    "fresh_mode_invalid_v1_envelope_rejected",
			mode:    jsonextract.ModeFreshPlainText,
			input:   []byte(invalidV1BadSchema),
			outcome: outcomeErr,
			probeErr: func(t *testing.T, err error) {
				if !errors.Is(err, scriptpkg.ErrModelOutputMalformed) {
					t.Errorf("err = %v, want ErrModelOutputMalformed (JSON envelope must not be silently wrapped as prose per godlike/07 NO-FAKE-AVAILABILITY)", err)
				}
			},
		},
		{
			name:    "fresh_mode_plain_prose_wrapped",
			mode:    jsonextract.ModeFreshPlainText,
			input:   []byte(plainProse),
			outcome: outcomeOK,
			validate: func(t *testing.T, out *scriptpkg.ModelScriptOutputV1) {
				if out.Text != plainProse {
					t.Errorf("Text = %q, want verbatim prose", out.Text)
				}
				if len(out.SpecScene.Scenes) != 0 {
					t.Errorf("len(SpecScene.Scenes) = %d, want 0", len(out.SpecScene.Scenes))
				}
			},
		},
		{
			name:    "fresh_mode_nil_rejected",
			mode:    jsonextract.ModeFreshPlainText,
			input:   nil,
			outcome: outcomeErr,
			probeErr: func(t *testing.T, err error) {
				if !errors.Is(err, scriptpkg.ErrModelOutputMalformed) {
					t.Errorf("nil err = %v, want ErrModelOutputMalformed", err)
				}
			},
		},
		{
			name:    "fresh_mode_empty_rejected",
			mode:    jsonextract.ModeFreshPlainText,
			input:   []byte{},
			outcome: outcomeErr,
			probeErr: func(t *testing.T, err error) {
				if !errors.Is(err, scriptpkg.ErrModelOutputMalformed) {
					t.Errorf("empty err = %v, want ErrModelOutputMalformed", err)
				}
			},
		},
		{
			name:    "fresh_mode_whitespace_rejected",
			mode:    jsonextract.ModeFreshPlainText,
			input:   []byte("   \n\t  "),
			outcome: outcomeErr,
			probeErr: func(t *testing.T, err error) {
				if !errors.Is(err, scriptpkg.ErrModelOutputMalformed) {
					t.Errorf("whitespace err = %v, want ErrModelOutputMalformed", err)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			scanner := jsonextract.NewScanner(tc.mode)
			out, err := scanner.Scan(tc.input, "fresh")
			switch tc.outcome {
			case outcomeOK:
				if err != nil {
					t.Fatalf("Scan returned err = %v, want nil", err)
				}
				if out == nil {
					t.Fatal("Scan returned nil output, want non-nil")
				}
				if tc.validate != nil {
					tc.validate(t, out)
				}
			case outcomeErr:
				if err == nil {
					t.Fatalf("Scan returned nil err, want error (mode=%d, shape=%q)", tc.mode, tc.input)
				}
				if tc.probeErr != nil {
					tc.probeErr(t, err)
				}
			default:
				t.Fatalf("unhandled outcome %d", tc.outcome)
			}
		})
	}
}

// TestScanner_ModeCompatibility_Matrix — per-shape table for the
// compatibility-mode behaviour.
func TestScanner_ModeCompatibility_Matrix(t *testing.T) {
	t.Parallel()

	const validV1 = `{"schema_version":1,"text":"Compatibility V1.","specscene":{"version":1,"scenes":[]}}`
	const legacyArray = `[{"index":0,"text":"scena legacy.","kind":"narration","clip_id":"clip-legacy-1"}]`
	const invalidV1BadSchema = `{"schema_version":99,"text":"Bad schema.","specscene":{"version":1,"scenes":[]}}`
	const plainProse = "Compat prose fallback into wrapPlainText."

	cases := []matrixCase{
		{
			name:    "compat_mode_v1_json_decode_directly",
			mode:    jsonextract.ModeCompatibility,
			input:   []byte(validV1),
			outcome: outcomeOK,
			validate: func(t *testing.T, out *scriptpkg.ModelScriptOutputV1) {
				if out.Text != "Compatibility V1." {
					t.Errorf("Text = %q, want verbatim V1 text", out.Text)
				}
			},
		},
		{
			name:    "compat_mode_legacy_array_converted",
			mode:    jsonextract.ModeCompatibility,
			input:   []byte(legacyArray),
			outcome: outcomeOK,
			validate: func(t *testing.T, out *scriptpkg.ModelScriptOutputV1) {
				if out.Text != "scena legacy." {
					t.Errorf("Text = %q, want verbatim legacy scene text", out.Text)
				}
				if len(out.SpecScene.Scenes) != 1 {
					t.Errorf("len(SpecScene.Scenes) = %d, want 1", len(out.SpecScene.Scenes))
				}
			},
		},
		{
			name:    "compat_mode_invalid_v1_falls_through_to_plain_text",
			mode:    jsonextract.ModeCompatibility,
			input:   []byte(invalidV1BadSchema),
			outcome: outcomeOK,
			validate: func(t *testing.T, out *scriptpkg.ModelScriptOutputV1) {
				if out.Text != "Bad schema." {
					t.Errorf("Text = %q, want fallback prose text", out.Text)
				}
			},
		},
		{
			name:    "compat_mode_plain_prose_wrap",
			mode:    jsonextract.ModeCompatibility,
			input:   []byte(plainProse),
			outcome: outcomeOK,
			validate: func(t *testing.T, out *scriptpkg.ModelScriptOutputV1) {
				if out.Text != plainProse {
					t.Errorf("Text = %q, want verbatim prose", out.Text)
				}
			},
		},
		{
			name:    "compat_mode_nil_rejected",
			mode:    jsonextract.ModeCompatibility,
			input:   nil,
			outcome: outcomeErr,
			probeErr: func(t *testing.T, err error) {
				if !errors.Is(err, scriptpkg.ErrModelOutputMalformed) {
					t.Errorf("nil err = %v, want ErrModelOutputMalformed", err)
				}
			},
		},
		{
			name:    "compat_mode_empty_rejected",
			mode:    jsonextract.ModeCompatibility,
			input:   []byte{},
			outcome: outcomeErr,
			probeErr: func(t *testing.T, err error) {
				if !errors.Is(err, scriptpkg.ErrModelOutputMalformed) {
					t.Errorf("empty err = %v, want ErrModelOutputMalformed", err)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			scanner := jsonextract.NewScanner(tc.mode)
			out, err := scanner.Scan(tc.input, "cache")
			switch tc.outcome {
			case outcomeOK:
				if err != nil {
					t.Fatalf("Scan returned err = %v, want nil", err)
				}
				if out == nil {
					t.Fatal("Scan returned nil output, want non-nil")
				}
				if tc.validate != nil {
					tc.validate(t, out)
				}
			case outcomeErr:
				if err == nil {
					t.Fatalf("Scan returned nil err, want error (mode=%d, shape=%q)", tc.mode, tc.input)
				}
				if tc.probeErr != nil {
					tc.probeErr(t, err)
				}
			default:
				t.Fatalf("unhandled outcome %d", tc.outcome)
			}
		})
	}
}

// TestScanner_ModeAliasNumericalIdentity — locks that
// ModeFreshPlainText and ModeStrict resolve to the same numeric
// value (so the deprecated alias can be removed in a future breaking
// PR without operator-dashboard breakage).
func TestScanner_ModeAliasNumericalIdentity(t *testing.T) {
	t.Parallel()

	if jsonextract.ModeFreshPlainText != jsonextract.ModeStrict {
		t.Errorf("ModeStrict must keep the same numeric value as ModeFreshPlainText for backward compat; got fresh=%d strict=%d",
			jsonextract.ModeFreshPlainText, jsonextract.ModeStrict)
	}
}

// SPDX-License-Identifier: Apache-2.0
// Package jsonextract — legacy_converter_test.go: hermetic TDD test
// surface pinning the canonical wire-shape contracts of
// legacy_converter.go after PR-5 (the Flip) of the
// LLM-PLAIN-TEXT-CONTRACT wave.
//
// PR-5 (the Flip):
//   - wrapPlainText becomes the canonical PRIMARY entry point for
//     fresh-mode plain-prose LLM output (was previously the
//     last-resort ModeCompatibility fallback).
//   - New exported gate ParsePlainTextFresh wraps wrapPlainText with
//     a typed-sentinel envelope (ErrModelOutputMalformed on legacy
//     JSON shapes; clean prose wraps cleanly).
//
// The tests below lock both contracts so a future refactor that
// re-flipps the primary path surfaces as test failure.

package jsonextract

import (
	"errors"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// TestFreshGeneration_WrapsPlainTextIntoModelScriptOutputV1 —
// canonical contract test for ParsePlainTextFresh on raw prose.
//
// Invariants pinned:
//  1. Returns (*ModelScriptOutputV1, nil) — no error on bare prose.
//  2. SchemaVersion == 1 (canonical V1 envelope).
//  3. Text field == input text verbatim (after JSON-envelope
//     stripping which is a no-op for prose input).
//  4. SpecScene.Scenes == empty (no scene partition on raw prose
//     — partition happens downstream via SceneSynthesizer.FromText).
//
// godlike/06 SSOT: this test is the SOLE contract test for the
// ParsePlainTextFresh prose path. A future refactor that silently
// fabricates a default scene slice here would surface as test
// failure (the equality assertion on len(SpecScene.Scenes) == 0 is
// load-bearing).
func TestFreshGeneration_WrapsPlainTextIntoModelScriptOutputV1(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "single-paragraph-prose",
			input: "Ciao mondo. Oggi parliamo di boxe.",
		},
		{
			name:  "multi-paragraph-prose",
			input: "Primo paragrafo introduttivo.\n\nSecondo paragrafo centrale.\n\nTerzo paragrafo di chiusura.",
		},
		{
			name:  "long-prose-no-newlines",
			input: "Lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor incididunt ut labore et dolore magna aliqua.",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := ParsePlainTextFresh([]byte(tc.input))
			if err != nil {
				t.Fatalf("ParsePlainTextFresh(%q) returned err = %v, want nil", tc.input, err)
			}
			if out == nil {
				t.Fatalf("ParsePlainTextFresh(%q) returned nil output on bare prose", tc.input)
			}
			if got, want := out.SchemaVersion, 1; got != want {
				t.Errorf("SchemaVersion = %d, want %d", got, want)
			}
			if got, want := out.Text, tc.input; got != want {
				t.Errorf("Text = %q, want %q", got, want)
			}
			if got := len(out.SpecScene.Scenes); got != 0 {
				t.Errorf("len(SpecScene.Scenes) = %d, want 0 (scene partition must happen downstream via SceneSynthesizer.FromText)",
					got)
			}
			if got, want := out.SpecScene.Version, 1; got != want {
				t.Errorf("SpecScene.Version = %d, want %d", got, want)
			}
		})
	}
}

// TestFreshGeneration_RejectsLegacyJSONEnvelope — pinned contract for
// the godlike/07 NO-FAKE-AVAILABILITY enforcement. A fresh-mode
// payload that LOOKS like a legacy V1 JSON envelope MUST be rejected
// with ErrModelOutputMalformed (so the operator dashboard surfaces
// the LLM is honouring the deprecated V1 contract — NOT a silent
// prose-wrap that would mask the behaviour change).
//
// The test exercises the canonical 3 legacy shapes observed in
// production ollama responses before June 2026:
//   - canonical V1 object
//   - legacy array
//   - bare JSON string with schema_version key
func TestFreshGeneration_RejectsLegacyJSONEnvelope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "canonical-v1-object",
			input: `{"schema_version":1,"specscene":{"version":1,"scenes":[]},"text":"Ciao mondo."}`,
		},
		{
			name:  "legacy-array",
			input: `[{"index":0,"text":"scena zero","kind":"narration","clip_id":"clip-1"}]`,
		},
		{
			name:  "bare-string-with-schema-key",
			input: `"{\"schema_version\":1,\"text\":\"nascondiamo il contratto\"}"`,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParsePlainTextFresh([]byte(tc.input))
			if err == nil {
				t.Fatalf("ParsePlainTextFresh(%q) returned nil err on legacy JSON envelope; want ErrModelOutputMalformed",
					tc.input)
			}
			if !errors.Is(err, scriptpkg.ErrModelOutputMalformed) {
				t.Errorf("err = %v, want errors.Is chain including ErrModelOutputMalformed", err)
			}
			// Operator-dashboard message must surface the LLM contract hint
			// so the operator dashboard surfaces the deprecated contract use.
			if !strings.Contains(err.Error(), "legacy JSON") {
				t.Errorf("err message = %q, want to contain %q for operator dashboard grep",
					err.Error(), "legacy JSON")
			}
		})
	}
}

// TestFreshGeneration_RejectsEmptyInput — pinned contract for the
// godlike/07 typed-error NO-FAKE-AVAILABILITY enforcement on empty
// input. The fresh-mode path must fail-closed at the seam, not
// silently emit an empty ModelScriptOutputV1 (which downstream
// validators would treat as valid).
func TestFreshGeneration_RejectsEmptyInput(t *testing.T) {
	t.Parallel()

	if _, err := ParsePlainTextFresh(nil); !errors.Is(err, scriptpkg.ErrModelOutputMalformed) {
		t.Errorf("ParsePlainTextFresh(nil) err = %v, want ErrModelOutputMalformed", err)
	}
	if _, err := ParsePlainTextFresh([]byte{}); !errors.Is(err, scriptpkg.ErrModelOutputMalformed) {
		t.Errorf("ParsePlainTextFresh([]byte{}) err = %v, want ErrModelOutputMalformed", err)
	}
	if _, err := ParsePlainTextFresh([]byte("   \n\t  ")); !errors.Is(err, scriptpkg.ErrModelOutputMalformed) {
		t.Errorf("ParsePlainTextFresh(whitespace-only) err = %v, want ErrModelOutputMalformed", err)
	}
}

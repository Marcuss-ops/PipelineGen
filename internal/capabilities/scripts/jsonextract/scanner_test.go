// SPDX-License-Identifier: Apache-2.0
// Package jsonextract_test — scanner_test.go: hermetic TDD test surface
// pinning the canonical Scanner.ModeStrict contract after PR-5 (the
// Flip) of the LLM-PLAIN-TEXT-CONTRACT wave.
//
// PR-4 (ModeStrict Flip):
//   - Pre-PR-4: ModeStrict required valid V1 JSON only; any failure
//     returned ErrModelOutputMalformed.
//   - Post-PR-4: ModeStrict tries decodeV1 first, then falls back to
//     ParsePlainTextFresh (the canonical PRIMARY path for plain prose
//     per the fresh-mode LLM contract).
//
// The tests below lock the post-PR-4 contract so a future refactor
// that re-flips the primary path surfaces as test failure.
//
// godlike/06 SSOT (one canonical owner per fact): Scanner is the
// canonical router; ParsePlainTextFresh lives ONLY at
// legacy_converter.go. These tests exercise the router contract only
// (the delegate's contract is locked by legacy_converter_test.go).
package jsonextract_test

import (
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/jsonextract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestScanner_ModeStrict_ValidV1JSON_DecodesDirectly — canonical
// contract test for the ModeStrict JSON fast-lane. When the LLM
// emits a valid V1 JSON envelope, the scanner decodes it directly
// without invoking the plain-text fallback.
func TestScanner_ModeStrict_ValidV1JSON_DecodesDirectly(t *testing.T) {
	t.Parallel()

	scanner := jsonextract.NewScanner(jsonextract.ModeStrict)
	raw := []byte(`{"schema_version":1,"text":"Capitolo 1. L'allenamento inizia.","specscene":{"version":1,"scenes":[]}}`)

	out, err := scanner.Scan(raw, "fresh")
	if err != nil {
		t.Fatalf("ModeStrict Scan on valid V1 JSON returned err = %v, want nil", err)
	}
	if out == nil {
		t.Fatal("ModeStrict Scan returned nil output on valid V1 JSON")
	}
	if out.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", out.SchemaVersion)
	}
	if out.Text != "Capitolo 1. L'allenamento inizia." {
		t.Errorf("Text = %q, want %q", out.Text, "Capitolo 1. L'allenamento inizia.")
	}
	if len(out.SpecScene.Scenes) != 0 {
		t.Errorf("len(SpecScene.Scenes) = %d, want 0", len(out.SpecScene.Scenes))
	}
}

// TestScanner_ModeStrict_PlainProse_FallsBackToParsePlainTextFresh —
// canonical contract test for the PR-4 flip. When the LLM emits raw
// narrative prose (no JSON at all), ModeStrict delegates to
// ParsePlainTextFresh which wraps the prose into a
// ModelScriptOutputV1 with empty scenes.
//
// godlike/07 NO-FAKE-AVAILABILITY: this test verifies the prose IS
// wrapped (not rejected with ErrModelOutputMalformed as the pre-PR-4
// contract required). The fallback is the PRIMARY path per the fresh-
// mode LLM contract — rejecting plain prose silently clobbers the
// user's narrative output.
func TestScanner_ModeStrict_PlainProse_FallsBackToParsePlainTextFresh(t *testing.T) {
	t.Parallel()

	scanner := jsonextract.NewScanner(jsonextract.ModeStrict)
	raw := []byte("Il campione entra sul ring. La folla esplode.")

	out, err := scanner.Scan(raw, "fresh")
	if err != nil {
		t.Fatalf("ModeStrict Scan on plain prose returned err = %v, want nil (PR-4 flip: plain prose IS the primary path)", err)
	}
	if out == nil {
		t.Fatal("ModeStrict Scan returned nil output on plain prose")
	}
	if out.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", out.SchemaVersion)
	}
	if out.Text != "Il campione entra sul ring. La folla esplode." {
		t.Errorf("Text = %q, want verbatim prose", out.Text)
	}
	if len(out.SpecScene.Scenes) != 0 {
		t.Errorf("len(SpecScene.Scenes) = %d, want 0 (scene partition happens downstream via SceneSynthesizer.FromText)", len(out.SpecScene.Scenes))
	}
}

// TestScanner_ModeStrict_InvalidJSON_RejectedByParsePlainTextFresh —
// canonical contract test: when the LLM emits JSON that fails
// decodeV1 (e.g. unsupported schema_version), ModeStrict falls back
// to ParsePlainTextFresh, which MUST reject the invalid JSON as
// ErrModelOutputMalformed (it IS a JSON envelope, even if invalid).
//
// godlike/07 NO-FAKE-AVAILABILITY: the plain-text fallback is for
// raw prose (no JSON framing at all). An invalid JSON envelope
// means the LLM is still honouring a structured output contract, so
// the failure must surface as ErrModelOutputMalformed — NOT a silent
// prose wrap that would mask the behaviour change from the operator.
func TestScanner_ModeStrict_InvalidJSON_RejectedByParsePlainTextFresh(t *testing.T) {
	t.Parallel()

	scanner := jsonextract.NewScanner(jsonextract.ModeStrict)
	// schema_version=99 fails Validate, causing decodeV1 to return an error.
	// The scanner falls through to ParsePlainTextFresh, which detects the
	// JSON envelope and returns ErrModelOutputMalformed.
	raw := []byte(`{"schema_version":99,"text":"Unsupported schema.","specscene":{"version":1,"scenes":[]}}`)

	_, err := scanner.Scan(raw, "fresh")
	if err == nil {
		t.Fatal("ModeStrict Scan on JSON-shaped invalid input returned nil err; want ErrModelOutputMalformed (JSON envelope must not be silently wrapped as prose)")
	}
	if !errors.Is(err, scriptpkg.ErrModelOutputMalformed) {
		t.Errorf("err = %v, want errors.Is chain including ErrModelOutputMalformed", err)
	}
}

// TestScanner_ModeStrict_EmptyInput_ReturnsError — canonical contract
// test: empty input must fail-closed with ErrModelOutputMalformed.
// Neither decodeV1 nor ParsePlainTextFresh can produce valid output
// from empty bytes.
func TestScanner_ModeStrict_EmptyInput_ReturnsError(t *testing.T) {
	t.Parallel()

	scanner := jsonextract.NewScanner(jsonextract.ModeStrict)

	_, err := scanner.Scan(nil, "fresh")
	if !errors.Is(err, scriptpkg.ErrModelOutputMalformed) {
		t.Errorf("ModeStrict Scan(nil) err = %v, want errors.Is chain including ErrModelOutputMalformed", err)
	}

	_, err = scanner.Scan([]byte{}, "fresh")
	if !errors.Is(err, scriptpkg.ErrModelOutputMalformed) {
		t.Errorf("ModeStrict Scan([]byte{}) err = %v, want errors.Is chain including ErrModelOutputMalformed", err)
	}
}

// TestScanner_NilScanner_DefaultsToModeStrict — canonical contract
// test: a nil *Scanner defaults to ModeStrict (the zero-value
// behavior documented in Scanner.Scan's nil-receiver guard).
//
// godlike/07 NO-FAKE-AVAILABILITY: a nil scanner must NOT panic and
// must default to ModeStrict semantics (the fresh-mode contract
// is the canonical default per PR-4 flip).
func TestScanner_NilScanner_DefaultsToModeStrict(t *testing.T) {
	t.Parallel()

	var scanner *jsonextract.Scanner // nil

	raw := []byte("Prose text without a scanner instance.")
	out, err := scanner.Scan(raw, "fresh")
	if err != nil {
		t.Fatalf("nil Scanner.Scan on plain prose returned err = %v, want nil", err)
	}
	if out == nil {
		t.Fatal("nil Scanner.Scan returned nil output")
	}
	if out.Text != "Prose text without a scanner instance." {
		t.Errorf("Text = %q, want verbatim prose", out.Text)
	}
}

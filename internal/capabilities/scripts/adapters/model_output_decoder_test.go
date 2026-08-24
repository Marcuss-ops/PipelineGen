// Package adapters_test — model_output_decoder_test.go exercises the
// unified jsonextract.Scanner in ModeFreshPlainText (canonical; alias
// ModeStrict).
//
// P0.8 (June 2026): merged old decoder tests into jsonextract.Scanner.
// DL-MODECOMPAT-REMOVAL (August 2026): ModeCompatibility tests removed;
// ModeFreshPlainText is the sole canonical mode.
//
// PR1 follow-up: package name normalised from `scripts_test` to
// `adapters_test` so this file can coexist with `compat_adapters.go`
// (which declares `package adapters`). The previous `scripts_test`
// declaration triggered a Go build error when the test file was
// compiled alongside the production file in the same directory.
package adapters_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/jsonextract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── ModeStrict: valid object accepted ───────────────────────────────

func TestScannerStrictValidObject(t *testing.T) {
	raw := []byte(`{
		"schema_version": 1,
		"text": "Complete script text.",
		"specscene": {
			"version": 1,
			"scenes": [
				{
					"id": "scene-1",
					"index": 0,
					"text": "Opening scene.",
					"kind": "narration"
				},
				{
					"id": "scene-2",
					"index": 1,
					"text": "Clip scene.",
					"kind": "clip",
					"bindings": {
						"clip": {
							"clip_id": "clip-abc"
						}
					}
				}
			]
		}
	}`)

	output, err := jsonextract.NewScanner(jsonextract.ModeStrict).Scan(raw, "test")
	if err != nil {
		t.Fatalf("expected valid decode, got: %v", err)
	}
	if output.SchemaVersion != 1 {
		t.Errorf("schema_version: %d", output.SchemaVersion)
	}
	if output.Text != "Complete script text." {
		t.Errorf("text: %s", output.Text)
	}
	if len(output.SpecScene.Scenes) != 2 {
		t.Fatalf("expected 2 scenes, got %d", len(output.SpecScene.Scenes))
	}
	if output.SpecScene.Scenes[0].Kind != scriptpkg.SceneNarration {
		t.Errorf("scene 0 kind: %s", output.SpecScene.Scenes[0].Kind)
	}
	if output.SpecScene.Scenes[1].Bindings.Clip == nil {
		t.Fatal("scene 1: clip binding is nil")
	}
	if output.SpecScene.Scenes[1].Bindings.Clip.ClipID != "clip-abc" {
		t.Errorf("scene 1 clip_id: %s", output.SpecScene.Scenes[1].Bindings.Clip.ClipID)
	}
}

// ── ModeStrict: valid object with empty scenes ──────────────────────

func TestScannerStrictValidEmptyScenes(t *testing.T) {
	raw := []byte(`{
		"schema_version": 1,
		"text": "Pure prose.",
		"specscene": {
			"version": 1,
			"scenes": []
		}
	}`)

	output, err := jsonextract.NewScanner(jsonextract.ModeStrict).Scan(raw, "test")
	if err != nil {
		t.Fatalf("expected valid decode with empty scenes, got: %v", err)
	}
	if output.Text != "Pure prose." {
		t.Errorf("text: %s", output.Text)
	}
}

// ── ModeStrict: fenced JSON accepted ────────────────────────────────

func TestScannerStrictFencedJSON(t *testing.T) {
	raw := []byte("```json\n{\n  \"schema_version\": 1,\n  \"text\": \"Fenced output.\",\n  \"specscene\": { \"version\": 1, \"scenes\": [] }\n}\n```")

	output, err := jsonextract.NewScanner(jsonextract.ModeStrict).Scan(raw, "test")
	if err != nil {
		t.Fatalf("expected valid decode for fenced JSON, got: %v", err)
	}
	if output.Text != "Fenced output." {
		t.Errorf("text: %s", output.Text)
	}
}

// ── ModeStrict: fenced JSON with leading instruction ────────────────

func TestScannerStrictFencedWithLeadingText(t *testing.T) {
	raw := []byte("Here is the generated script output:\n\n```json\n{\n  \"schema_version\": 1,\n  \"text\": \"Output with lead-in.\",\n  \"specscene\": { \"version\": 1, \"scenes\": [] }\n}\n```")

	output, err := jsonextract.NewScanner(jsonextract.ModeStrict).Scan(raw, "test")
	if err != nil {
		t.Fatalf("expected valid decode with leading text, got: %v", err)
	}
	if output.Text != "Output with lead-in." {
		t.Errorf("text: %s", output.Text)
	}
}

// ── ModeStrict: bare prose is the PRIMARY path (PR-5 LLM-PLAIN-TEXT
// contract). It is wrapped into a canonical V1 envelope, not an error.

func TestScannerStrictBareProse(t *testing.T) {
	raw := []byte("This is just plain prose, no JSON at all.")

	output, err := jsonextract.NewScanner(jsonextract.ModeStrict).Scan(raw, "test")
	if err != nil {
		t.Fatalf("ModeStrict: bare prose must be wrapped, got error: %v", err)
	}
	if output.Text != string(raw) {
		t.Errorf("text: got %q, want %q", output.Text, string(raw))
	}
	if len(output.SpecScene.Scenes) != 0 {
		t.Errorf("expected 0 scenes for plain prose, got %d", len(output.SpecScene.Scenes))
	}
}

// ── ModeStrict: malformed JSON-shaped input rejected ────────────────
// Non-JSON input is treated as plain prose (PR-5 primary path) and
// therefore succeeds; only inputs that look like JSON but fail to
// decode/validate produce an error.

func TestScannerStrictMalformedJSON(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{"unclosed brace", []byte(`{"schema_version": 1, "text": "bad"`)},
		{"trailing comma", []byte(`{"schema_version": 1, "text": "bad",}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := jsonextract.NewScanner(jsonextract.ModeStrict).Scan(tt.raw, "test")
			if err == nil {
				t.Fatal("expected error for malformed JSON in ModeStrict")
			}
		})
	}
}

func TestScannerStrictPlainTextNotJSONSucceeds(t *testing.T) {
	raw := []byte(`not json`)
	output, err := jsonextract.NewScanner(jsonextract.ModeStrict).Scan(raw, "test")
	if err != nil {
		t.Fatalf("expected plain text to wrap, got error: %v", err)
	}
	if output.Text != string(raw) {
		t.Errorf("text: got %q, want %q", output.Text, string(raw))
	}
}

// ── ModeStrict: unsupported schema_version rejected ─────────────────

func TestScannerStrictUnsupportedVersion(t *testing.T) {
	raw := []byte(`{"schema_version": 99, "text": "New format", "specscene": {"version": 1, "scenes": []}}`)

	_, err := jsonextract.NewScanner(jsonextract.ModeStrict).Scan(raw, "test")
	if err == nil {
		t.Fatal("expected error for unsupported schema_version")
	}
	if !errors.Is(err, scriptpkg.ErrModelOutputMalformed) {
		t.Errorf("expected ErrModelOutputMalformed, got %v", err)
	}
}

// ── ModeStrict: missing text rejected ───────────────────────────────

func TestScannerStrictMissingText(t *testing.T) {
	raw := []byte(`{"schema_version": 1, "text": "", "specscene": {"version": 1, "scenes": []}}`)

	_, err := jsonextract.NewScanner(jsonextract.ModeStrict).Scan(raw, "test")
	if err == nil {
		t.Fatal("expected error for missing text")
	}
}

// ── ModeStrict: JSON-string wrapped text is unwrapped ──────────────

func TestScannerStrictUnwrapsNestedJSONStringText(t *testing.T) {
	raw := []byte(`{
		"schema_version": 1,
		"text": "{\"schema_version\":1,\"text\":\"The global recognition of Jackie Chan is a tapestry woven from physical comedy and resilience.\",\"specscene\":{\"version\":1,\"scenes\":[]}}",
		"specscene": {
			"version": 1,
			"scenes": []
		}
	}`)

	output, err := jsonextract.NewScanner(jsonextract.ModeStrict).Scan(raw, "test")
	if err != nil {
		t.Fatalf("expected nested JSON-string text to decode, got: %v", err)
	}
	want := "The global recognition of Jackie Chan is a tapestry woven from physical comedy and resilience."
	if output.Text != want {
		t.Fatalf("text = %q, want %q", output.Text, want)
	}
}

// ── ModeStrict: nested scene text is unwrapped ──────────────────────

func TestScannerStrictUnwrapsNestedSceneText(t *testing.T) {
	raw := []byte(`{
		"schema_version": 1,
		"text": "Complete script text.",
		"specscene": {
			"version": 1,
			"scenes": [
				{
					"id": "scene-1",
					"index": 0,
					"text": "{\"schema_version\":1,\"text\":\"Jackie Chan lands a perfect stunt without the wrapper noise.\",\"specscene\":{\"version\":1,\"scenes\":[]}}",
					"kind": "narration",
					"bindings": {}
				}
			]
		}
	}`)

	output, err := jsonextract.NewScanner(jsonextract.ModeStrict).Scan(raw, "test")
	if err != nil {
		t.Fatalf("expected nested scene text to decode, got: %v", err)
	}
	want := "Jackie Chan lands a perfect stunt without the wrapper noise."
	if got := output.SpecScene.Scenes[0].Text; got != want {
		t.Fatalf("scene text = %q, want %q", got, want)
	}
}

// ── ModeStrict: duplicate scene IDs rejected ────────────────────────

func TestScannerStrictDuplicateSceneIDs(t *testing.T) {
	raw := []byte(`{
		"schema_version": 1,
		"text": "Text.",
		"specscene": {
			"version": 1,
			"scenes": [
				{"id": "scene-1", "index": 0, "text": "A.", "kind": "narration"},
				{"id": "scene-1", "index": 1, "text": "B.", "kind": "narration"}
			]
		}
	}`)

	_, err := jsonextract.NewScanner(jsonextract.ModeStrict).Scan(raw, "test")
	if err == nil {
		t.Fatal("expected error for duplicate scene IDs")
	}
	if !strings.Contains(err.Error(), "duplicate scene id") {
		t.Errorf("error should mention duplicate scene id: %v", err)
	}
}

// ── ModeStrict: empty input rejected ────────────────────────────────

func TestScannerStrictEmpty(t *testing.T) {
	_, err := jsonextract.NewScanner(jsonextract.ModeStrict).Scan(nil, "test")
	if err == nil {
		t.Fatal("expected error for nil input")
	}
	_, err = jsonextract.NewScanner(jsonextract.ModeStrict).Scan([]byte{}, "test")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

// ── Fuzz: scanner doesn't panic on garbage ──────────────────────────

func TestScannerFuzz(t *testing.T) {
	inputs := [][]byte{
		nil,
		{},
		[]byte("x"),
		[]byte("{"),
		[]byte("{}"),
		[]byte(`{"schema_version": 1}`),
		[]byte(`{"schema_version": 1, "text": null}`),
		[]byte(`{"schema_version": 1, "text": "ok", "specscene": null}`),
		[]byte(strings.Repeat("{", 1000)),
		[]byte(strings.Repeat("a", 10000)),
		[]byte("\n\n\n\n{"),
		[]byte("```json\n\n```"),
	}

	for i, input := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("input %d: Scanner panicked: %v", i, r)
				}
			}()
			_, _ = jsonextract.NewScanner(jsonextract.ModeStrict).Scan(input, "test")
		}()
	}
}

// ── ModeStrict: non-V1 JSON is an error ─────────────────────────────

func TestScannerStrictNonV1JSON(t *testing.T) {
	raw := []byte(`{"key": "value"}`)
	_, err := jsonextract.NewScanner(jsonextract.ModeStrict).Scan(raw, "test")
	if err == nil {
		t.Fatal("ModeStrict: non-V1 JSON must error")
	}
}

// ── JSON round-trip through scanner ─────────────────────────────────

func TestScannerRoundTripCanonical(t *testing.T) {
	canonical := scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Round-trip test.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes:  []scriptpkg.SpecScene{},
		},
	}

	encoded, err := json.Marshal(canonical)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	decoded, err := jsonextract.NewScanner(jsonextract.ModeStrict).Scan(encoded, "test")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if decoded.Text != canonical.Text {
		t.Errorf("text: %q vs %q", decoded.Text, canonical.Text)
	}
}

// Package scripts_test — model_output_decoder_test.go exercises the
// canonical DecodeModelOutput and the compatibility
// LegacyArrayToOutput decoder.
package scripts_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	compatpkg "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/compat"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

var testLog = zap.NewNop()

// ── Canonical decoder: valid object accepted ──────────────────────

func TestDecodeModelOutputValidObject(t *testing.T) {
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

	output, err := scripts.DecodeModelOutput(raw, testLog)
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

// ── Canonical decoder: valid object with empty scenes ──────────────

func TestDecodeModelOutputValidEmptyScenes(t *testing.T) {
	raw := []byte(`{
		"schema_version": 1,
		"text": "Pure prose.",
		"specscene": {
			"version": 1,
			"scenes": []
		}
	}`)

	output, err := scripts.DecodeModelOutput(raw, testLog)
	if err != nil {
		t.Fatalf("expected valid decode with empty scenes, got: %v", err)
	}
	if output.Text != "Pure prose." {
		t.Errorf("text: %s", output.Text)
	}
}

// ── Canonical decoder: fenced JSON accepted ────────────────────────

func TestDecodeModelOutputFencedJSON(t *testing.T) {
	raw := []byte("```json\n{\n  \"schema_version\": 1,\n  \"text\": \"Fenced output.\",\n  \"specscene\": { \"version\": 1, \"scenes\": [] }\n}\n```")

	output, err := scripts.DecodeModelOutput(raw, testLog)
	if err != nil {
		t.Fatalf("expected valid decode for fenced JSON, got: %v", err)
	}
	if output.Text != "Fenced output." {
		t.Errorf("text: %s", output.Text)
	}
}

// ── Canonical decoder: fenced JSON with leading instruction ────────

func TestDecodeModelOutputFencedWithLeadingText(t *testing.T) {
	raw := []byte("Here is the generated script output:\n\n```json\n{\n  \"schema_version\": 1,\n  \"text\": \"Output with lead-in.\",\n  \"specscene\": { \"version\": 1, \"scenes\": [] }\n}\n```")

	output, err := scripts.DecodeModelOutput(raw, testLog)
	if err != nil {
		t.Fatalf("expected valid decode with leading text, got: %v", err)
	}
	if output.Text != "Output with lead-in." {
		t.Errorf("text: %s", output.Text)
	}
}

// ── Canonical decoder: bare prose rejected ─────────────────────────

func TestDecodeModelOutputBareProse(t *testing.T) {
	raw := []byte("This is just plain prose, no JSON at all.")

	_, err := scripts.DecodeModelOutput(raw, testLog)
	if err == nil {
		t.Fatal("expected error for bare prose")
	}
	if !errors.Is(err, scriptpkg.ErrModelOutputMalformed) {
		t.Errorf("expected ErrModelOutputMalformed, got %v", err)
	}
}

// ── Canonical decoder: malformed JSON rejected ─────────────────────

func TestDecodeModelOutputMalformedJSON(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{"unclosed brace", []byte(`{"schema_version": 1, "text": "bad"`)},
		{"trailing comma", []byte(`{"schema_version": 1, "text": "bad",}`)},
		{"not JSON at all", []byte(`not json`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := scripts.DecodeModelOutput(tt.raw, testLog)
			if err == nil {
				t.Fatal("expected error for malformed JSON")
			}
		})
	}
}

// ── Canonical decoder: unsupported schema_version rejected ─────────

func TestDecodeModelOutputUnsupportedVersion(t *testing.T) {
	raw := []byte(`{"schema_version": 99, "text": "New format", "specscene": {"version": 1, "scenes": []}}`)

	_, err := scripts.DecodeModelOutput(raw, testLog)
	if err == nil {
		t.Fatal("expected error for unsupported schema_version")
	}
	if !errors.Is(err, scriptpkg.ErrModelOutputMalformed) {
		t.Errorf("expected ErrModelOutputMalformed, got %v", err)
	}
}

// ── Canonical decoder: missing text rejected ───────────────────────

func TestDecodeModelOutputMissingText(t *testing.T) {
	raw := []byte(`{"schema_version": 1, "text": "", "specscene": {"version": 1, "scenes": []}}`)

	_, err := scripts.DecodeModelOutput(raw, testLog)
	if err == nil {
		t.Fatal("expected error for missing text")
	}
}

// ── Canonical decoder: duplicate scene IDs rejected ────────────────

func TestDecodeModelOutputDuplicateSceneIDs(t *testing.T) {
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

	_, err := scripts.DecodeModelOutput(raw, testLog)
	if err == nil {
		t.Fatal("expected error for duplicate scene IDs")
	}
	if !strings.Contains(err.Error(), "duplicate scene id") {
		t.Errorf("error should mention duplicate scene id: %v", err)
	}
}

// ── Canonical decoder: empty input rejected ────────────────────────

func TestDecodeModelOutputEmpty(t *testing.T) {
	_, err := scripts.DecodeModelOutput(nil, testLog)
	if err == nil {
		t.Fatal("expected error for nil input")
	}
	_, err = scripts.DecodeModelOutput([]byte{}, testLog)
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

// ── Fuzz: decode doesn't panic on garbage ──────────────────────────

func TestDecodeModelOutputFuzz(t *testing.T) {
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
					t.Errorf("input %d: DecodeModelOutput panicked: %v", i, r)
				}
			}()
			_, _ = scripts.DecodeModelOutput(input, testLog)
		}()
	}
}

// ── Legacy decoder: valid array accepted ───────────────────────────

func TestLegacyArrayToOutputValid(t *testing.T) {
	raw := []byte(`[
		{"index": 0, "text": "First scene.", "kind": "narration"},
		{"index": 1, "text": "Second scene.", "kind": "clip", "clip_id": "clip-123"}
	]`)

	output, err := compatpkg.LegacyArrayToOutput(raw)
	if err != nil {
		t.Fatalf("expected valid legacy array decode, got: %v", err)
	}
	if output.SchemaVersion != 1 {
		t.Errorf("schema_version: %d", output.SchemaVersion)
	}
	if output.Text == "" {
		t.Error("text should be concatenated from scenes")
	}
	if !strings.Contains(output.Text, "First scene.") {
		t.Errorf("text should contain first scene: %s", output.Text)
	}
	if !strings.Contains(output.Text, "Second scene.") {
		t.Errorf("text should contain second scene: %s", output.Text)
	}
	if len(output.SpecScene.Scenes) != 2 {
		t.Fatalf("expected 2 scenes, got %d", len(output.SpecScene.Scenes))
	}

	// Scene 0: narration, no bindings.
	s0 := output.SpecScene.Scenes[0]
	if s0.ID != "legacy-scene-0" {
		t.Errorf("scene 0 id: %s", s0.ID)
	}
	if s0.Index != 0 {
		t.Errorf("scene 0 index: %d", s0.Index)
	}
	if s0.Kind != scriptpkg.SceneNarration {
		t.Errorf("scene 0 kind: %s", s0.Kind)
	}

	// Scene 1: clip binding.
	s1 := output.SpecScene.Scenes[1]
	if s1.ID != "legacy-scene-1" {
		t.Errorf("scene 1 id: %s", s1.ID)
	}
	if s1.Kind != scriptpkg.SceneClip {
		t.Errorf("scene 1 kind: %s", s1.Kind)
	}
	if s1.Bindings.Clip == nil {
		t.Fatal("scene 1: clip binding is nil")
	}
	if s1.Bindings.Clip.ClipID != "clip-123" {
		t.Errorf("scene 1 clip_id: %s", s1.Bindings.Clip.ClipID)
	}
}

// ── Legacy decoder: valid array with alternate fields ──────────────

func TestLegacyArrayToOutputAlternateFields(t *testing.T) {
	raw := []byte(`[
		{
			"index": 0,
			"content": "Scene from content field.",
			"title": "My Scene",
			"kind": "voice",
			"image_prompt": "A picture of a mountain",
			"image_url": "https://example.com/img.png"
		}
	]`)

	output, err := compatpkg.LegacyArrayToOutput(raw)
	if err != nil {
		t.Fatalf("expected valid legacy decode: %v", err)
	}

	scene := output.SpecScene.Scenes[0]
	if scene.Text != "Scene from content field." {
		t.Errorf("text should come from content field: %s", scene.Text)
	}
	if scene.Title != "My Scene" {
		t.Errorf("title: %s", scene.Title)
	}
	if scene.Kind != scriptpkg.SceneNarration {
		// "voice" → SceneNarration
		t.Errorf("kind 'voice' should map to narration, got %s", scene.Kind)
	}
	if scene.Bindings.Image == nil {
		t.Fatal("image binding is nil")
	}
	if scene.Bindings.Image.Prompt != "A picture of a mountain" {
		t.Errorf("image prompt: %s", scene.Bindings.Image.Prompt)
	}
}

// ── Legacy decoder: empty array rejected ───────────────────────────

func TestLegacyArrayToOutputEmpty(t *testing.T) {
	_, err := compatpkg.LegacyArrayToOutput([]byte(`[]`))
	if err == nil {
		t.Fatal("expected error for empty legacy array")
	}
}

// ── Legacy decoder: not an array rejected ──────────────────────────

func TestLegacyArrayToOutputNotArray(t *testing.T) {
	_, err := compatpkg.LegacyArrayToOutput([]byte(`{"key": "value"}`))
	if err == nil {
		t.Fatal("expected error for non-array input")
	}
}

// ── IsLegacyArrayOutput heuristic ───────────────────────────────────

func TestIsLegacyArrayOutput(t *testing.T) {
	tests := []struct {
		raw      []byte
		expected bool
	}{
		{[]byte(`[{"index":0}]`), true},
		{[]byte(`  [  ]`), true},
		{[]byte(`{"schema_version":1}`), false},
		{[]byte(`prose`), false},
		{[]byte{}, false},
		{nil, false},
	}
	for i, tt := range tests {
		if got := compatpkg.IsLegacyArrayOutput(tt.raw); got != tt.expected {
			t.Errorf("input %d: IsLegacyArrayOutput(%q) = %v, want %v",
				i, string(tt.raw), got, tt.expected)
		}
	}
}

// ── JSON round-trip through both decoders ──────────────────────────

func TestDecodeRoundTripCanonical(t *testing.T) {
	canonical := scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Round-trip test.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes:  []scriptpkg.SpecScene{},
		},
	}

	// Marshal to JSON, then decode.
	encoded, err := json.Marshal(canonical)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	decoded, err := scripts.DecodeModelOutput(encoded, testLog)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if decoded.Text != canonical.Text {
		t.Errorf("text: %q vs %q", decoded.Text, canonical.Text)
	}
}

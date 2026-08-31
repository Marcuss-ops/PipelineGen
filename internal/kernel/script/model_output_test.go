// Package script_test — model_output_test.go exercises the canonical
// model output contracts: ModelScriptOutputV1, SpecSceneOutput,
// SpecScene, and bindings.
package script_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── JSON round-trip ────────────────────────────────────────────────

func TestModelScriptOutputV1RoundTrip(t *testing.T) {
	output := script.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "This is the complete generated script text.",
		SpecScene: script.SpecSceneOutput{
			Version: 1,
			Scenes: []script.SpecScene{
				{
					ID:    "scene-1",
					Index: 0,
					Text:  "Opening narration.",
					Title: "Opening",
					Kind:  script.SceneNarration,
				},
				{
					ID:    "scene-2",
					Index: 1,
					Text:  "Scene with a clip.",
					Kind:  script.SceneClip,
					Bindings: script.SceneBindings{
						Clip: &script.ClipBinding{
							ClipID:    "clip-abc123",
							ClipTitle: "Example Clip",
							DriveLink: "https://drive.google.com/file/d/abc123",
							StartMs:   5000,
							EndMs:     15000,
						},
					},
				},
				{
					ID:    "scene-3",
					Index: 2,
					Text:  "Scene with an image.",
					Kind:  script.SceneImage,
					Bindings: script.SceneBindings{
						Image: &script.ImageBinding{
							ImageID: "img-001",
							Prompt:  "A beautiful sunset over the ocean",
							URL:     "https://example.com/sunset.png",
							Status:  "generated",
						},
					},
				},
				{
					ID:    "scene-4",
					Index: 3,
					Text:  "Scene with voiceover.",
					Kind:  script.SceneMixed,
					Bindings: script.SceneBindings{
						Voiceover: &script.VoiceoverBinding{
							Status:     "completed",
							Link:       "https://example.com/audio.mp3",
							DurationMs: 12000,
						},
					},
				},
			},
		},
	}

	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded script.ModelScriptOutputV1
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.SchemaVersion != 1 {
		t.Errorf("expected schema_version 1, got %d", decoded.SchemaVersion)
	}
	if decoded.Text != output.Text {
		t.Errorf("text mismatch: %q vs %q", decoded.Text, output.Text)
	}
	if decoded.SpecScene.Version != 1 {
		t.Errorf("expected specscene version 1, got %d", decoded.SpecScene.Version)
	}
	if len(decoded.SpecScene.Scenes) != 4 {
		t.Fatalf("expected 4 scenes, got %d", len(decoded.SpecScene.Scenes))
	}

	// Scene 2: check clip binding survived.
	s2 := decoded.SpecScene.Scenes[1]
	if s2.Bindings.Clip == nil {
		t.Fatal("scene-2: clip binding is nil after round-trip")
	}
	if s2.Bindings.Clip.ClipID != "clip-abc123" {
		t.Errorf("scene-2 clip_id: %s", s2.Bindings.Clip.ClipID)
	}
	if s2.Bindings.Clip.StartMs != 5000 {
		t.Errorf("scene-2 start_ms: %d", s2.Bindings.Clip.StartMs)
	}

	// Scene 3: check image binding.
	s3 := decoded.SpecScene.Scenes[2]
	if s3.Bindings.Image == nil {
		t.Fatal("scene-3: image binding is nil after round-trip")
	}
	if s3.Bindings.Image.Prompt != "A beautiful sunset over the ocean" {
		t.Errorf("scene-3 prompt: %s", s3.Bindings.Image.Prompt)
	}

	// Scene 4: check voiceover binding.
	s4 := decoded.SpecScene.Scenes[3]
	if s4.Bindings.Voiceover == nil {
		t.Fatal("scene-4: voiceover binding is nil after round-trip")
	}
	if s4.Bindings.Voiceover.DurationMs != 12000 {
		t.Errorf("scene-4 duration_ms: %d", s4.Bindings.Voiceover.DurationMs)
	}
}

// ── Scene kind validation ──────────────────────────────────────────

func TestSceneKindValid(t *testing.T) {
	tests := []struct {
		kind     script.SceneKind
		expected bool
	}{
		{script.SceneNarration, true},
		{script.SceneClip, true},
		{script.SceneImage, true},
		{script.SceneMixed, true},
		{script.SceneKind("unknown"), false},
		{script.SceneKind(""), false},
		{script.SceneKind("Narration"), false}, // case-sensitive
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			if got := tt.kind.Valid(); got != tt.expected {
				t.Errorf("Valid() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// ── Validation: ModelScriptOutputV1 ────────────────────────────────

func TestModelScriptOutputV1ValidateValid(t *testing.T) {
	output := script.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Valid script text.",
		SpecScene: script.SpecSceneOutput{
			Version: 1,
			Scenes:  []script.SpecScene{},
		},
	}
	if err := output.Validate(); err != nil {
		t.Errorf("expected valid output, got: %v", err)
	}
}

func TestModelScriptOutputV1ValidateUnsupportedVersion(t *testing.T) {
	output := script.ModelScriptOutputV1{
		SchemaVersion: 99,
		Text:          "Some text.",
	}
	err := output.Validate()
	if err == nil {
		t.Fatal("expected error for unsupported schema_version")
	}
	if !errors.Is(err, script.ErrModelOutputMalformed) {
		t.Errorf("expected ErrModelOutputMalformed, got %v", err)
	}
	if !strings.Contains(err.Error(), "unsupported schema_version") {
		t.Errorf("error message should mention schema_version: %v", err)
	}
}

func TestModelScriptOutputV1ValidateMissingText(t *testing.T) {
	output := script.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "",
	}
	err := output.Validate()
	if err == nil {
		t.Fatal("expected error for missing text")
	}
	if !strings.Contains(err.Error(), "text is required") {
		t.Errorf("error should mention text is required: %v", err)
	}
}

func TestModelScriptOutputV1ValidateWhitespaceText(t *testing.T) {
	output := script.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "   \n  \t  ",
	}
	err := output.Validate()
	if err == nil {
		t.Fatal("expected error for whitespace-only text")
	}
}

func TestModelScriptOutputV1ValidateBadSpecScene(t *testing.T) {
	output := script.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Some text.",
		SpecScene: script.SpecSceneOutput{
			Version: 2, // unsupported
			Scenes:  nil,
		},
	}
	err := output.Validate()
	if err == nil {
		t.Fatal("expected error for bad specscene")
	}
	if !strings.Contains(err.Error(), "specscene") {
		t.Errorf("error should mention specscene: %v", err)
	}
}

// ── Validation: SpecScene ──────────────────────────────────────────

func TestSpecSceneValidateValid(t *testing.T) {
	scene := script.SpecScene{
		ID:    "scene-1",
		Index: 0,
		Text:  "Scene text.",
		Kind:  script.SceneNarration,
	}
	if err := scene.Validate(); err != nil {
		t.Errorf("expected valid scene, got: %v", err)
	}
}

func TestSpecSceneValidateMissingID(t *testing.T) {
	scene := script.SpecScene{
		ID:    "",
		Index: 0,
		Text:  "Scene text.",
		Kind:  script.SceneNarration,
	}
	err := scene.Validate()
	if err == nil {
		t.Fatal("expected error for missing ID")
	}
	if !strings.Contains(err.Error(), "id is required") {
		t.Errorf("error should mention id: %v", err)
	}
}

func TestSpecSceneValidateMissingText(t *testing.T) {
	scene := script.SpecScene{
		ID:    "scene-1",
		Index: 0,
		Text:  "",
		Kind:  script.SceneNarration,
	}
	err := scene.Validate()
	if err == nil {
		t.Fatal("expected error for missing text")
	}
}

func TestSpecSceneValidateUnknownKind(t *testing.T) {
	scene := script.SpecScene{
		ID:    "scene-1",
		Index: 0,
		Text:  "Scene text.",
		Kind:  script.SceneKind("unknown"),
	}
	err := scene.Validate()
	if err == nil {
		t.Fatal("expected error for unknown kind")
	}
	if !strings.Contains(err.Error(), "unknown scene kind") {
		t.Errorf("error should mention unknown scene kind: %v", err)
	}
}

// ── Validation: SpecSceneOutput ────────────────────────────────────

func TestSpecSceneOutputValidateDuplicateID(t *testing.T) {
	out := script.SpecSceneOutput{
		Version: 1,
		Scenes: []script.SpecScene{
			{ID: "scene-1", Index: 0, Text: "First.", Kind: script.SceneNarration},
			{ID: "scene-1", Index: 1, Text: "Second.", Kind: script.SceneNarration},
		},
	}
	err := out.Validate()
	if err == nil {
		t.Fatal("expected error for duplicate scene IDs")
	}
	if !strings.Contains(err.Error(), "duplicate scene id") {
		t.Errorf("error should mention duplicate scene id: %v", err)
	}
}

func TestSpecSceneOutputValidateDuplicateSegmentID(t *testing.T) {
	out := script.SpecSceneOutput{
		Version: 1,
		Scenes: []script.SpecScene{
			{ID: "scene-a", SegmentID: "segment-a", Index: 0, Text: "first", Kind: script.SceneStock},
			{ID: "scene-b", SegmentID: "segment-a", Index: 1, Text: "second", Kind: script.SceneStock},
		},
	}

	err := out.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate segment_id") {
		t.Fatalf("expected duplicate segment_id validation error, got %v", err)
	}
}

func TestSpecSceneOutputValidateIndexMismatch(t *testing.T) {
	out := script.SpecSceneOutput{
		Version: 1,
		Scenes: []script.SpecScene{
			{ID: "scene-1", Index: 5, Text: "First.", Kind: script.SceneNarration},
		},
	}
	err := out.Validate()
	if err == nil {
		t.Fatal("expected error for index mismatch")
	}
	if !strings.Contains(err.Error(), "index mismatch") {
		t.Errorf("error should mention index mismatch: %v", err)
	}
}

func TestSpecSceneOutputValidateEmptyOK(t *testing.T) {
	out := script.SpecSceneOutput{
		Version: 1,
		Scenes:  []script.SpecScene{},
	}
	if err := out.Validate(); err != nil {
		t.Errorf("empty scenes should be valid: %v", err)
	}
}

// ── JSON: omitempty binding behaviour ──────────────────────────────

func TestSpecSceneBindingsAlwaysPresent(t *testing.T) {
	scene := script.SpecScene{
		ID:    "scene-1",
		Index: 0,
		Text:  "Narration only.",
		Kind:  script.SceneNarration,
		// Bindings left zero — should appear as {} per plan §5.2.
	}
	data, err := json.Marshal(scene)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	raw := string(data)
	// The bindings key is always present; individual sub-fields (clip,
	// image, voiceover) are omitted when nil via pointer+omitempty.
	if !strings.Contains(raw, `"bindings"`) {
		t.Errorf("bindings key should always be present, got: %s", raw)
	}
	// Sub-bindings should NOT appear when nil.
	if strings.Contains(raw, `"clip"`) {
		t.Errorf("nil clip binding should be omitted: %s", raw)
	}
	if strings.Contains(raw, `"image"`) {
		t.Errorf("nil image binding should be omitted: %s", raw)
	}
	if strings.Contains(raw, `"voiceover"`) {
		t.Errorf("nil voiceover binding should be omitted: %s", raw)
	}
}

func TestSpecSceneBindingsPresentWhenSet(t *testing.T) {
	scene := script.SpecScene{
		ID:    "scene-1",
		Index: 0,
		Text:  "With clip.",
		Kind:  script.SceneClip,
		Bindings: script.SceneBindings{
			Clip: &script.ClipBinding{
				ClipID: "clip-1",
			},
		},
	}
	data, err := json.Marshal(scene)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	raw := string(data)
	if !strings.Contains(raw, `"bindings"`) {
		t.Errorf("non-zero bindings should be present, got: %s", raw)
	}
	if !strings.Contains(raw, `"clip_id"`) {
		t.Errorf("clip binding should contain clip_id: %s", raw)
	}
}

// ── Fuzz: Model output validation doesn't panic ────────────────────

func TestModelOutputValidateFuzz(t *testing.T) {
	// Fuzz-like: feed edge-case inputs to Validate and check for panics.
	inputs := []script.ModelScriptOutputV1{
		{},
		{SchemaVersion: 1, Text: "ok"},
		{SchemaVersion: 0, Text: "ok", SpecScene: script.SpecSceneOutput{Version: 1}},
		{SchemaVersion: 1, Text: "ok", SpecScene: script.SpecSceneOutput{Version: 1,
			Scenes: []script.SpecScene{
				{}, // all zero
				{ID: "s1", Index: 0, Text: "t", Kind: ""},
				{ID: "s2", Index: 1, Text: "t", Kind: "bogus"},
			},
		}},
	}
	for i, input := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("input %d: Validate panicked: %v", i, r)
				}
			}()
			_ = input.Validate()
		}()
	}
}

// ── JSON: round-trip with empty SpecScene ──────────────────────────

func TestModelScriptOutputV1RoundTripEmptyScenes(t *testing.T) {
	output := script.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Pure prose, no scenes.",
		SpecScene: script.SpecSceneOutput{
			Version: 1,
			Scenes:  nil,
		},
	}
	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// specscene with empty/null scenes should still marshal.
	var decoded script.ModelScriptOutputV1
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Text != "Pure prose, no scenes." {
		t.Errorf("text mismatch")
	}
}

// ── ModelOutputError unwrapping ────────────────────────────────────

func TestModelOutputErrorUnwrap(t *testing.T) {
	err := &script.ModelOutputError{
		Details: []string{"missing text", "unsupported version"},
	}
	if !errors.Is(err, script.ErrModelOutputMalformed) {
		t.Error("ModelOutputError should unwrap to ErrModelOutputMalformed")
	}
	if !strings.Contains(err.Error(), "missing text") {
		t.Error("error message should contain details")
	}
}

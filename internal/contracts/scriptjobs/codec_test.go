package scriptjobs_test

import (
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/contracts/scriptjobs"
)

func TestGeneratePayloadRoundTrip(t *testing.T) {
	spec := scriptjobs.GenerationSpec{
		Topic:      "The future of AI",
		SourceText: "Artificial intelligence is transforming...",
		Guidelines: "Keep it technical but accessible.",
		ClipIDs:    []string{"clip-a", "clip-b"},
		NumClips:   3,
		Title:      "AI Revolution",
		OutputName: "ai-revolution",
		Language:   "en",
		Tone:       "professional",
		Style:      "cinematic",
		Model:      "llama3.2",
		TargetWords:       1500,
		Duration:          300,
		MinWords:          800,
		SentencesPerImage: 8,
		ImagesPerScene:    2,
		ExtractEntities:     true,
		ArtlistSearch:       true,
		StockSearch:         false,
		GenerateMetadata:    true,
		GenerateVoiceover:   true,
		VoiceoverGroup:      "default",
		GenerateSceneImages: true,
		Languages:           []string{"en", "it"},
		TranscriptPolicy:    "auto",
		OrderingStrategy:    "relevance",
		SaveToDB:            true,
		GenerateTimeline:    true,
		ForceRefresh:        false,
		PromptVersion:       "v3",
		EditorPromptVersion: "v2",
		QAPromptVersion:     "v1",
	}
	minScore := 0.7
	spec.MinQualityScore = &minScore
	minWords := 20
	spec.MinTranscriptWords = &minWords

	payload := scriptjobs.NewGeneratePayload(scriptjobs.PresetCustom, spec)

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded scriptjobs.GeneratePayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Version != 1 {
		t.Errorf("expected version 1, got %d", decoded.Version)
	}
	if decoded.Preset != scriptjobs.PresetCustom {
		t.Errorf("expected PresetCustom, got %s", decoded.Preset)
	}
	if decoded.Spec.Topic != "The future of AI" {
		t.Errorf("topic mismatch: %s", decoded.Spec.Topic)
	}
	if len(decoded.Spec.ClipIDs) != 2 {
		t.Errorf("expected 2 clip IDs, got %d", len(decoded.Spec.ClipIDs))
	}
	if decoded.Spec.MinQualityScore == nil || *decoded.Spec.MinQualityScore != 0.7 {
		t.Error("MinQualityScore mismatch")
	}
	if decoded.Spec.MinTranscriptWords == nil || *decoded.Spec.MinTranscriptWords != 20 {
		t.Error("MinTranscriptWords mismatch")
	}
	if decoded.Spec.SentencesPerImage != 8 {
		t.Errorf("SentencesPerImage mismatch: %d", decoded.Spec.SentencesPerImage)
	}
}

func TestGeneratePayloadDecodeEmpty(t *testing.T) {
	p, err := scriptjobs.DecodeGeneratePayload(nil)
	if err != nil {
		t.Fatalf("empty payload should decode: %v", err)
	}
	if p.Version != 0 {
		t.Errorf("empty payload version should be 0, got %d", p.Version)
	}
}

func TestGeneratePayloadDecodeRawJSON(t *testing.T) {
	raw := json.RawMessage(`{"version":1,"preset":"with_images","spec":{"topic":"test","language":"en"}}`)
	p, err := scriptjobs.DecodeGeneratePayload(raw)
	if err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if p.Spec.Topic != "test" {
		t.Errorf("topic mismatch: %s", p.Spec.Topic)
	}
	if p.Preset != scriptjobs.PresetWithImages {
		t.Errorf("preset mismatch: %s", p.Preset)
	}
}

func TestGenerationSpecHasClips(t *testing.T) {
	tests := []struct {
		name     string
		spec     scriptjobs.GenerationSpec
		expected bool
	}{
		{"no clips", scriptjobs.GenerationSpec{}, false},
		{"clip IDs", scriptjobs.GenerationSpec{ClipIDs: []string{"a"}}, true},
		{"num clips", scriptjobs.GenerationSpec{NumClips: 5}, true},
		{"both", scriptjobs.GenerationSpec{ClipIDs: []string{"a"}, NumClips: 5}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.spec.HasClips(); got != tt.expected {
				t.Errorf("HasClips() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestGenerationSpecHasText(t *testing.T) {
	tests := []struct {
		name     string
		spec     scriptjobs.GenerationSpec
		expected bool
	}{
		{"empty", scriptjobs.GenerationSpec{}, false},
		{"topic only", scriptjobs.GenerationSpec{Topic: "AI"}, true},
		{"source only", scriptjobs.GenerationSpec{SourceText: "text"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.spec.HasText(); got != tt.expected {
				t.Errorf("HasText() = %v, want %v", got, tt.expected)
			}
		})
	}
}

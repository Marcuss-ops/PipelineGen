package script

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestGenerationEnvelopeV2_RoundTripPreservesContract(t *testing.T) {
	original := GenerationEnvelopeV2{
		Version:       2,
		Preset:        PresetCustom,
		CorrelationID: "corr-json-audit",
		ForceRefresh:  true,
		Items: []GenerationItemV2{{
			ID:       "item-1",
			Title:    "JSON contract audit",
			Language: "en",
			Tone:     "documentary",
			Style:    "cinematic",
			Model:    "llama3:8b",
			Source: SourceSpec{
				Type:       SourceText,
				Topic:      "Contract compatibility",
				SourceText: "Round-trip source text.",
			},
			ScriptParams: ScriptSpec{TargetWords: 240, PromptVersion: "v1"},
			Output: OutputSpec{
				ExtractEntities:     ToggleEnabled,
				GenerateMetadata:    ToggleDisabled,
				GenerateSceneImages: ToggleEnabled,
				StockEnabled:        ToggleDisabled,
				SaveToDB:            true,
				GenerateTimeline:    true,
				VoiceoverGroup:      "narration",
				DriveFolderID:       "folder-1",
				MaxChars:            12000,
				OutputFmt:           "json",
				Languages:           []string{"en", "it"},
				TranslateTo:         "it",
			},
			Docs: DocumentsSpec{
				Enabled:   true,
				Languages: []string{"en", "it"},
				FolderID:  "docs-folder",
			},
		}},
	}

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	wire := string(raw)
	for _, key := range []string{`"version"`, `"preset"`, `"correlation_id"`, `"force_refresh"`, `"items"`, `"source"`, `"script_params"`, `"output"`, `"docs"`} {
		if !strings.Contains(wire, key) {
			t.Fatalf("expected envelope JSON key %s: %s", key, wire)
		}
	}

	decoded, err := DecodeEnvelopeV2(raw)
	if err != nil {
		t.Fatalf("DecodeEnvelopeV2: %v\nJSON: %s", err, raw)
	}
	if !reflect.DeepEqual(original, *decoded) {
		t.Fatalf("round-trip mismatch:\noriginal=%#v\ndecoded=%#v\njson=%s", original, *decoded, raw)
	}
}

func TestGenerationEnvelopeV2_DecodeRejectsInvalidVersionWithoutChangingContract(t *testing.T) {
	_, err := DecodeEnvelopeV2(json.RawMessage(`{"version":1,"items":[]}`))
	if err == nil || !strings.Contains(err.Error(), "version must be 2") {
		t.Fatalf("expected version validation error, got %v", err)
	}
}

func TestOutputSpec_ToggleLegacyPayloadRoundTrip(t *testing.T) {
	var spec OutputSpec
	if err := json.Unmarshal([]byte(`{"extract_entities":true,"generate_metadata":false,"generate_scene_images":null}`), &spec); err != nil {
		t.Fatalf("legacy unmarshal: %v", err)
	}
	if spec.ExtractEntities != ToggleEnabled || spec.GenerateMetadata != ToggleDisabled || spec.GenerateSceneImages != ToggleDefault {
		t.Fatalf("legacy mapping mismatch: %#v", spec)
	}

	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("canonical marshal: %v", err)
	}
	wire := string(raw)
	if !strings.Contains(wire, `"extract_entities":"enabled"`) || !strings.Contains(wire, `"generate_metadata":"disabled"`) {
		t.Fatalf("canonical Toggle strings missing: %s", wire)
	}
	if strings.Contains(wire, `"generate_scene_images"`) {
		t.Fatalf("default Toggle should remain omitted: %s", wire)
	}

	var roundTrip OutputSpec
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatalf("canonical round-trip unmarshal: %v", err)
	}
	if !reflect.DeepEqual(spec, roundTrip) {
		t.Fatalf("Toggle round-trip mismatch: original=%#v roundTrip=%#v json=%s", spec, roundTrip, raw)
	}
}

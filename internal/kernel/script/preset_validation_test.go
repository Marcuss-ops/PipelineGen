package script

// preset_validation_test.go — `preset` used to be accepted without being
// validated, so the 61 real job payloads carrying "news" (a variant no build
// ever defined) looked applied while ApplyPreset silently fell through to the
// pass-through row.

import (
	"strings"
	"testing"
)

func TestEnvelopeRejectsUnknownPreset(t *testing.T) {
	env := &GenerationEnvelopeV2{
		Version: 2,
		Preset:  Preset("news"),
		Items:   []GenerationItemV2{{Source: SourceSpec{Type: "text", Topic: "t"}}},
	}
	err := env.Validate()
	if err == nil {
		t.Fatal("preset \"news\" is not a contract value and must fail closed")
	}
	if !strings.Contains(err.Error(), "preset \"news\" is unknown") {
		t.Fatalf("error must name the rejected preset, got %q", err.Error())
	}
}

func TestEnvelopeAcceptsCanonicalPresets(t *testing.T) {
	for _, preset := range []Preset{"", PresetCustom, PresetWithImages, PresetFullMedia, PresetCatalog, PresetSearch, PresetBatch} {
		env := &GenerationEnvelopeV2{
			Version: 2,
			Preset:  preset,
			Items: []GenerationItemV2{{
				Source:       SourceSpec{Type: "text", Topic: "t"},
				ScriptParams: ScriptSpec{TargetWords: 800},
			}},
		}
		if err := env.Validate(); err != nil {
			t.Fatalf("preset %q must validate: %v", preset, err)
		}
	}
}

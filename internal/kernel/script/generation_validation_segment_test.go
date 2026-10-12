package script

import (
	"strings"
	"testing"
)

func TestGenerationEnvelopeV2_RejectsInvalidRunEntityExtractionToggle(t *testing.T) {
	env := GenerationEnvelopeV2{Version: 2, Items: []GenerationItemV2{{
		Source: SourceSpec{Type: SourceText, Topic: "topic"},
		Output: OutputSpec{ExtractEntities: Toggle("sometimes")},
	}}}
	err := env.Validate()
	if err == nil || !strings.Contains(err.Error(), "output.extract_entities") {
		t.Fatalf("invalid run toggle error = %v", err)
	}
}

func TestGenerationEnvelopeV2_RejectsInvalidSegmentEntityExtractionToggle(t *testing.T) {
	env := GenerationEnvelopeV2{Version: 2, Items: []GenerationItemV2{{
		Source: SourceSpec{Type: SourceText, Topic: "topic"},
		ScriptParams: ScriptSpec{Segments: []ScriptSegment{{
			Topic: "scene", ExtractEntities: Toggle("sometimes"),
		}}},
	}}}
	err := env.Validate()
	if err == nil || !strings.Contains(err.Error(), "segments[0].extract_entities") {
		t.Fatalf("invalid segment toggle error = %v", err)
	}
}

func TestGenerationEnvelopeV2_AcceptsValidSegmentEntityExtractionToggles(t *testing.T) {
	for _, toggle := range []Toggle{"", ToggleDefault, ToggleEnabled, ToggleDisabled} {
		env := GenerationEnvelopeV2{Version: 2, Items: []GenerationItemV2{{
			Source: SourceSpec{Type: SourceText, Topic: "topic"},
			ScriptParams: ScriptSpec{Segments: []ScriptSegment{{
				Topic: "scene", ExtractEntities: toggle,
			}}},
		}}}
		if err := env.Validate(); err != nil {
			t.Fatalf("valid toggle %q rejected: %v", toggle, err)
		}
	}
}

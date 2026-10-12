package script_test

import (
	"testing"

	script "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestGenerationEnvelopeRejectsUnknownOverlayCategory(t *testing.T) {
	envelope := script.GenerationEnvelopeV2{Version: 2, Items: []script.GenerationItemV2{{
		ID: "item", Source: script.SourceSpec{Type: script.SourceText, Topic: "topic"},
		ScriptParams:      script.ScriptSpec{TargetWords: 100},
		OverlayCategories: map[script.OverlayCategory]script.Toggle{"unrecognized": script.ToggleDisabled},
	}}}
	if err := envelope.Validate(); err == nil {
		t.Fatal("unknown overlay category passed envelope validation")
	}
}

func TestGenerationEnvelopeRejectsInvalidOverlayCategoryToggle(t *testing.T) {
	envelope := script.GenerationEnvelopeV2{Version: 2, Items: []script.GenerationItemV2{{
		ID: "item", Source: script.SourceSpec{Type: script.SourceText, Topic: "topic"},
		ScriptParams: script.ScriptSpec{TargetWords: 100, Segments: []script.ScriptSegment{{Topic: "scene", OverlayCategories: map[script.OverlayCategory]script.Toggle{script.OverlayCategoryImages: "sometimes"}}}},
	}}}
	if err := envelope.Validate(); err == nil {
		t.Fatal("invalid segment category toggle passed envelope validation")
	}
}

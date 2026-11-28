package modelvalues

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
)

func TestDurableResultToDomainForLanguageUsesLocalizedSceneProjection(t *testing.T) {
	in := &contract.GenerateResult{
		Output: contract.GenerateOutput{Text: "English master text", WordCount: 3},
		Scenes: []contract.Scene{
			{ID: "one", Index: 0, Text: map[contract.Language]string{"en": "English first.", "it": "Prima scena italiana."}},
			{ID: "two", Index: 1, Text: map[contract.Language]string{"en": "English second.", "it": "Seconda scena italiana."}},
		},
	}

	got := DurableResultToDomainForLanguage(in, "it")
	if got == nil {
		t.Fatal("DurableResultToDomainForLanguage returned nil")
	}
	if got.Language != "it" {
		t.Fatalf("Language = %q, want it", got.Language)
	}
	if want := "Prima scena italiana.\n\nSeconda scena italiana."; got.Output.Text != want {
		t.Fatalf("Output.Text = %q, want localized projection %q", got.Output.Text, want)
	}
	if got.Output.WordCount != 6 {
		t.Fatalf("Output.WordCount = %d, want 6", got.Output.WordCount)
	}
	if got.Output.SpecScene.Scenes[0].Text != "Prima scena italiana." || got.Output.SpecScene.Scenes[1].Text != "Seconda scena italiana." {
		t.Fatalf("SpecScene was not localized: %+v", got.Output.SpecScene.Scenes)
	}
}

func TestDurableResultToDomainForLanguageDoesNotFallbackToMasterText(t *testing.T) {
	in := &contract.GenerateResult{
		Output: contract.GenerateOutput{Text: "English master text", WordCount: 3},
		Scenes: []contract.Scene{{ID: "one", Index: 0, Text: map[contract.Language]string{"en": "English scene."}}},
	}
	got := DurableResultToDomainForLanguage(in, "it")
	if got.Output.Text != "" || got.Output.WordCount != 0 || got.Output.SpecScene.Scenes[0].Text != "" {
		t.Fatalf("missing target-language text fell back to another language: %+v", got.Output)
	}
}

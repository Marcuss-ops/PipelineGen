package media

import (
	"encoding/json"
	"testing"
)

func TestMediaExtractionPolicyIncludes(t *testing.T) {
	legacy := MediaExtractionPolicy{}
	if !legacy.Includes(ExtractionIncludeEntities) || !legacy.Includes(ExtractionIncludeImportantPhrases) {
		t.Fatal("omitted include must preserve legacy unrestricted extraction")
	}

	selected := MediaExtractionPolicy{Include: []string{" entities ", "important_phrases"}}
	if !selected.Includes(ExtractionIncludeEntities) || !selected.Includes(ExtractionIncludeImportantPhrases) {
		t.Fatalf("selected surfaces were not recognized: %#v", selected.Include)
	}
	if selected.Includes("important_words") {
		t.Fatal("unselected surface must not be enabled")
	}
}

func TestMediaExtractionPolicyWireContract(t *testing.T) {
	payload := []byte(`{"media_plan":{"extraction":{"enabled":true,"include":["entities","important_phrases"],"max_entities_per_segment":4}}}`)
	var envelope struct {
		MediaPlan MediaPlanSpec `json:"media_plan"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatal(err)
	}
	got := envelope.MediaPlan.Extraction
	if len(got.Include) != 2 || got.Include[0] != ExtractionIncludeEntities || got.Include[1] != ExtractionIncludeImportantPhrases {
		t.Fatalf("include = %#v", got.Include)
	}
	if got.MaxEntitiesPerSegment != 4 {
		t.Fatalf("max entities = %d, want 4", got.MaxEntitiesPerSegment)
	}
}

func TestMediaPlanClonePreservesExtractionSelection(t *testing.T) {
	original := MediaPlanSpec{Extraction: MediaExtractionPolicy{
		Include:      []string{ExtractionIncludeEntities, ExtractionIncludeImportantPhrases},
		EntityImages: EntityImagePolicy{EntityTypes: []string{"PERSON"}},
	}}
	clone := original.Clone()
	if !clone.Extraction.Includes(ExtractionIncludeEntities) || !clone.Extraction.Includes(ExtractionIncludeImportantPhrases) {
		t.Fatalf("clone lost extraction include: %#v", clone.Extraction.Include)
	}
	clone.Extraction.Include[0] = "changed"
	clone.Extraction.EntityImages.EntityTypes[0] = "changed"
	if original.Extraction.Include[0] == "changed" || original.Extraction.EntityImages.EntityTypes[0] == "changed" {
		t.Fatal("clone must deep-copy extraction slices")
	}
}

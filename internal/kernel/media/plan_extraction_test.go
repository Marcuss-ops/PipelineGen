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
	if !selected.EntityImageSurfaceEnabled() {
		t.Fatal("explicit entities surface must enable identity images")
	}
	if !(MediaExtractionPolicy{Include: []string{ExtractionIncludeSpecialNames}}).EntityImageSurfaceEnabled() {
		t.Fatal("explicit special names surface must enable identity images")
	}
	if (MediaExtractionPolicy{Include: []string{ExtractionIncludeImportantPhrases}}).EntityImageSurfaceEnabled() {
		t.Fatal("important phrases alone must not enable identity images")
	}
}

func TestMediaExtractionPolicyCategorySelectors(t *testing.T) {
	for _, tc := range []struct {
		selector string
		kind     string
	}{
		{ExtractionIncludePersons, "PERSON"},
		{ExtractionIncludeBrands, "LOGO"},
		{ExtractionIncludeBrands, "BRAND"},
		{ExtractionIncludeMetrics, "NUMBER"},
		{ExtractionIncludeMetrics, "PERCENT"},
		{ExtractionIncludeMoney, "MONEY"},
		{ExtractionIncludeDates, "DATE"},
		{ExtractionIncludeLocations, "GPE"},
	} {
		policy := MediaExtractionPolicy{Include: []string{tc.selector}}
		if !policy.EntityExtractionRequested() || !policy.HasCategoryOnlyIncludes() || !policy.IncludesEntityType(tc.kind) {
			t.Errorf("selector %q should enable entity type %q", tc.selector, tc.kind)
		}
		for _, other := range []string{ExtractionIncludePersons, ExtractionIncludeBrands, ExtractionIncludeMetrics, ExtractionIncludeMoney, ExtractionIncludeDates, ExtractionIncludeLocations} {
			if other != tc.selector && policy.Includes(other) {
				t.Errorf("selector %q unexpectedly enabled %q", tc.selector, other)
			}
		}
	}
	if (MediaExtractionPolicy{Include: []string{ExtractionIncludeImportantPhrases}}).EntityExtractionRequested() {
		t.Fatal("phrase-only selection must not invoke entity extraction")
	}
	if !(MediaExtractionPolicy{Include: []string{ExtractionIncludeEntities}}).IncludesEntityType("BRAND") {
		t.Fatal("broad entities selection must retain legacy unrestricted types")
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

package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func TestParseTypedEntity(t *testing.T) {
	kind, value := parseTypedEntity("PERSON: Ada Lovelace")
	if kind != "PERSON" || value != "Ada Lovelace" {
		t.Fatalf("unexpected parsed entity: %s %s", kind, value)
	}

	kind, value = parseTypedEntity("[PLACE] Rome")
	if kind != "PLACE" || value != "Rome" {
		t.Fatalf("unexpected parsed place: %s %s", kind, value)
	}

	kind, value = parseTypedEntity("Apollo 11")
	if kind != "CONCEPT" || value != "Apollo 11" {
		t.Fatalf("unexpected legacy entity: %s %s", kind, value)
	}
}

func TestIsContainedEntityFragment_SuppressesModelTokenFragments(t *testing.T) {
	canonical := []string{
		"Genesis Mission",
		"United States",
		"White House Office of Science and Technology Policy",
	}
	for _, fragment := range []string{"Genesis", "United", "States", "White"} {
		if !isContainedEntityFragment(fragment, canonical) {
			t.Fatalf("fragment %q was not suppressed by canonical spans", fragment)
		}
	}
	if isContainedEntityFragment("NASA", canonical) {
		t.Fatal("unrelated entity NASA was suppressed")
	}
}

// TestEntityResultFromAnalysis_ConsolidatesEntitiesPhrasesWordsFromSingleResult
// certifies the SceneAnalysis single-pass boundary: one LLM response (one
// asset.EntityExtractionResult) carries named entities, important phrases, and
// important words together, and the adapter projects all three categories from
// that single result — never via three independent extraction requests.
func TestEntityResultFromAnalysis_ConsolidatesEntitiesPhrasesWordsFromSingleResult(t *testing.T) {
	analysis := &asset.EntityExtractionResult{
		FrasiImportanti:  []string{"the Apollo program"},
		ParoleImportanti: []string{"solar wind", "samples"},
		NomiSpeciali:     []string{"PERSON: Ada Lovelace", "PLACE: London"},
		EntitaSenzaTesto: map[string]string{"rocket": "launch pad"},
		ArtlistPhrases:   []string{"rocket launch"},
	}
	got := entityResultFromAnalysis(analysis)

	if len(got.Persons) != 1 || got.Persons[0].Value != "Ada Lovelace" {
		t.Fatalf("persons = %+v, want Ada Lovelace from the single result", got.Persons)
	}
	if len(got.Places) != 1 || got.Places[0].Value != "London" || got.Places[0].Type != "PLACE" {
		t.Fatalf("places = %+v, want PLACE London from the single result", got.Places)
	}
	if len(got.ImportantPhrases) != 1 || got.ImportantPhrases[0] != "the Apollo program" {
		t.Fatalf("important phrases = %+v, want phrase from the single result", got.ImportantPhrases)
	}
	if len(got.ImportantWords) != 2 {
		t.Fatalf("important words = %+v, want both words from the single result", got.ImportantWords)
	}
	if len(got.Concepts) != 1 || got.Concepts[0].Value != "rocket" || got.Concepts[0].Type != "VISUAL_SUBJECT" {
		t.Fatalf("concepts = %+v, want visual subject from the single result", got.Concepts)
	}
}

func TestAppendDeterministicEntities_PreservesTypedCanonicalSpans(t *testing.T) {
	result := &scriptpkg.EntityResult{}
	seen := map[string]struct{}{}
	appendDeterministicEntities(result, seen, &scriptpkg.EntityResult{
		Persons:  []scriptpkg.Entity{{Value: "Donald Trump", Type: "PERSON", Score: .9}},
		Places:   []scriptpkg.Entity{{Value: "United States", Type: "GPE", Score: .9}},
		Concepts: []scriptpkg.Entity{{Value: "Genesis Mission", Type: "CONCEPT", Score: .9}},
	})
	if len(result.Persons) != 1 || result.Persons[0].Value != "Donald Trump" {
		t.Fatalf("persons=%+v", result.Persons)
	}
	if len(result.Places) != 1 || result.Places[0].Value != "United States" || result.Places[0].Type != "PLACE" {
		t.Fatalf("places=%+v", result.Places)
	}
	if len(result.Concepts) != 1 || result.Concepts[0].Value != "Genesis Mission" {
		t.Fatalf("concepts=%+v", result.Concepts)
	}
}

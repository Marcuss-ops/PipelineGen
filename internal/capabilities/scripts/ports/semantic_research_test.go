package ports

import (
	"encoding/json"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestSemanticResearchResultKeepsResearchContextSeparate(t *testing.T) {
	result := SemanticResearchResult{
		Context: ResearchContext{
			Aliases:      []string{"John Froelich"},
			RelatedTerms: []string{"gasoline tractor"},
			Dates:        []string{"1892"},
			Locations:    []string{"Iowa"},
		},
		Reason: "historical segment",
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SemanticResearchResult
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Context.Aliases) != 1 || len(decoded.Context.RelatedTerms) != 1 {
		t.Fatalf("research context was not preserved: %+v", decoded)
	}
	if decoded.Context.Aliases[0] != "John Froelich" || decoded.Context.Dates[0] != "1892" {
		t.Fatalf("unexpected context: %+v", decoded.Context)
	}
}

func TestSemanticResearchRequestCarriesNLPEntitiesAsInputOnly(t *testing.T) {
	request := SemanticResearchRequest{
		SegmentID: "segment-1",
		Text:      "John Froelich built a tractor in Iowa",
		Entities:  []scriptpkg.ExtractedEntity{{Value: "John Froelich", Type: "PERSON"}},
	}
	if request.Entities[0].Type != "PERSON" {
		t.Fatalf("unexpected NLP entity: %+v", request.Entities)
	}
	// The contract exposes research output through Context, not Entities.
	result := SemanticResearchResult{Context: ResearchContext{Aliases: []string{"Froelich tractor"}}}
	if len(result.Context.Aliases) != 1 {
		t.Fatal("expected research alias")
	}
}

package nlp

import (
	"context"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type fixedBackend struct{ entities []scriptgen.VisualEntity }

func (f fixedBackend) Extract(context.Context, string, string, int) ([]scriptgen.VisualEntity, error) {
	return f.entities, nil
}

func TestEntityExtractorAdapterProjectsGroundedEntities(t *testing.T) {
	backend := fixedBackend{entities: []scriptgen.VisualEntity{{Text: "Tesla", Type: "ORG", Score: .8, Start: 0, End: 5, Evidence: "Tesla"}}}
	adapter, err := NewEntityExtractorAdapter(backend)
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.ExtractEntities(context.Background(), scriptpkg.EntityExtractionRequest{Text: "Tesla", Language: "en", EntityCount: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Concepts) != 1 || result.Concepts[0].Value != "Tesla" || result.Concepts[0].Type != "ORG" {
		t.Fatalf("result = %+v", result)
	}
}

func TestEntityExtractorAdapterRejectsBadEvidence(t *testing.T) {
	adapter, err := NewEntityExtractorAdapter(fixedBackend{entities: []scriptgen.VisualEntity{{Text: "Elon", Type: "PERSON", Start: 0, End: 4, Evidence: "Elon"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.ExtractEntities(context.Background(), scriptpkg.EntityExtractionRequest{Text: "Tesla", Language: "en"}); err == nil {
		t.Fatal("expected ungrounded entity rejection")
	}
}

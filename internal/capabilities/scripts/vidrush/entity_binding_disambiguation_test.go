package vidrush

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestEntityImageSearchQueryExpandsAmbiguousSingleTokenPerson(t *testing.T) {
	segment := scriptpkg.VidRushSegmentResult{Insights: scriptpkg.SegmentInsights{
		Entities: []scriptpkg.ExtractedEntity{
			{Value: "Natalia Augias", Type: "PERSON"},
			{Value: "Corrado Augias", Type: "PERSON"},
		},
	}}
	entity := scriptpkg.AnnotatedEntity{Text: "Natalia", CanonicalName: "Natalia", Type: "PERSON"}
	if got := entityImageSearchQuery(entity, segment); got != "Natalia Augias" {
		t.Fatalf("query = %q, want the full contextual person name", got)
	}
}

func TestEntityImageSearchQueryRefusesUnresolvedSingleTokenPerson(t *testing.T) {
	segment := scriptpkg.VidRushSegmentResult{Insights: scriptpkg.SegmentInsights{
		Entities: []scriptpkg.ExtractedEntity{{Value: "Corrado Augias", Type: "PERSON"}},
	}}
	entity := scriptpkg.AnnotatedEntity{Text: "Natalia", CanonicalName: "Natalia", Type: "PERSON"}
	if got := entityImageSearchQuery(entity, segment); got != "" {
		t.Fatalf("query = %q, want empty so an omonym is never bound", got)
	}
}

func TestEntityImageSearchQueryKeepsLinkedOrMultiTokenPerson(t *testing.T) {
	segment := scriptpkg.VidRushSegmentResult{}
	linked := scriptpkg.AnnotatedEntity{Text: "Natalia", CanonicalName: "Natalia", Type: "PERSON", CanonicalEntityID: "person-1"}
	if got := entityImageSearchQuery(linked, segment); got != "Natalia" {
		t.Fatalf("linked query = %q, want Natalia", got)
	}
	multi := scriptpkg.AnnotatedEntity{Text: "Corrado Augias", CanonicalName: "Corrado Augias", Type: "PERSON"}
	if got := entityImageSearchQuery(multi, segment); got != "Corrado Augias" {
		t.Fatalf("multi-token query = %q, want unchanged", got)
	}
}

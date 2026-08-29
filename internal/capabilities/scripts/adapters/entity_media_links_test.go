package adapters

import (
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"testing"
)

func TestAttachEntityMediaLinksDoesNotMutateNLP(t *testing.T) {
	s := scriptpkg.VidRushSegmentResult{SegmentID: "s", Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{{Value: "Michael Jordan", Type: "PERSON"}, {Value: "1941", Type: "DATE"}}}}
	out := AttachEntityMediaLinks(s, map[string][]string{"person:michael-jordan": {"asset-1"}})
	if len(s.Insights.EntityMediaLinks) != 0 {
		t.Fatal("input NLP was mutated")
	}
	if len(out.Insights.EntityMediaLinks) != 1 || out.Insights.EntityMediaLinks[0].CanonicalEntityID != "person:michael-jordan" || out.Insights.EntityMediaLinks[0].AssetIDs[0] != "asset-1" {
		t.Fatalf("links=%+v", out.Insights.EntityMediaLinks)
	}
	if len(out.Insights.Entities) != 2 {
		t.Fatal("entities were changed")
	}
}

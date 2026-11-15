package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestPersonCatalogIdentityExplicitlyExcludesNonPersonEntities(t *testing.T) {
	segment := scriptpkg.VidRushSegmentResult{Insights: scriptpkg.SegmentInsights{
		Entities: []scriptpkg.ExtractedEntity{
			{Value: "MGM Grand", Type: "ORG", Confidence: .9},
			{Value: "Las Vegas", Type: "GPE", Confidence: .9},
		},
		ImageEntityCanonicalIDs: map[string]string{
			"MGM Grand": "org:mgm-grand",
			"Las Vegas": "gpe:las-vegas",
		},
	}}
	for _, query := range []string{"MGM Grand", "Las Vegas"} {
		_, eligible, err := personCatalogIdentityForSegmentQuery(segment, query)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		if eligible {
			t.Fatalf("%s received a PERSON catalog identity; the durable catalog contract is PERSON-only", query)
		}
	}
}

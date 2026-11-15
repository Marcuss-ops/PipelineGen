package adapters

import (
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestPersonCatalogIdentityResolvesSegmentAliases(t *testing.T) {
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-1",
		Insights: scriptpkg.SegmentInsights{
			ImageEntityCanonicalIDs: map[string]string{
				"john gotti": "person:john-gotti",
				"teflon don": "person:john-gotti",
				"dapper don": "person:john-gotti",
			},
		},
	}
	for _, query := range []string{"Teflon Don", "Dapper Don", "John Gotti"} {
		identity, ok, err := personCatalogIdentityForSegmentQuery(segment, query)
		if err != nil || !ok {
			t.Fatalf("query %q: ok=%v err=%v", query, ok, err)
		}
		if identity.CanonicalEntityID != "person:john-gotti" {
			t.Fatalf("query %q resolved to %q, want person:john-gotti", query, identity.CanonicalEntityID)
		}
	}
}

func TestPersonCatalogIdentityRejectsAmbiguousSegmentAlias(t *testing.T) {
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-1",
		Insights: scriptpkg.SegmentInsights{
			ImageEntityCanonicalIDs: map[string]string{
				"john smith":   "person:john-smith--actor",
				"john  smith ": "person:john-smith--director",
			},
		},
	}
	identity, ok, err := personCatalogIdentityForSegmentQuery(segment, "John Smith")
	if !errors.Is(err, entitycatalog.ErrAmbiguousPersonName) || ok {
		t.Fatalf("ambiguous homonym was selectable: identity=%+v ok=%v err=%v", identity, ok, err)
	}
	if !identity.Unresolved || len(identity.Candidates) != 2 {
		t.Fatalf("ambiguous identity = %+v, want unresolved with two candidates", identity)
	}
}

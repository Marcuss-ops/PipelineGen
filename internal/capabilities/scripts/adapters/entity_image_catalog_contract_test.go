package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestPersonQueryIdentityFocusContract(t *testing.T) {
	identity := "Corrado Augias"
	cases := []struct {
		query string
		want  bool
	}{
		// The fanout lane always appends the portrait qualifier: that is the
		// only shape the catalog treats as an identity request.
		{"Corrado Augias portrait", true},
		{"Corrado Augias", true},
		// Contextual intents (program, book, place) belong to the scene
		// search lane; letting them through the PERSON catalog reproduced the
		// R22 provider-slot theft with bound=0.
		{"Corrado Augias La7", false},
		{"Corrado Augias La vita si impara", false},
	}
	for _, tc := range cases {
		if got := personQueryIsIdentityFocused(tc.query, identity); got != tc.want {
			t.Fatalf("personQueryIsIdentityFocused(%q) = %t, want %t", tc.query, got, tc.want)
		}
	}
}

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

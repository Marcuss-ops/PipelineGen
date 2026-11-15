package script

import (
	"testing"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
)

func TestImageSearchCacheSeparatesQueryClasses(t *testing.T) {
	person := imageSearchCacheKey(adapters.InternetImageSearchRequest{Entity: "Jordan", Query: "Jordan", QueryClass: "person", Language: "en", Limit: 10})
	brand := imageSearchCacheKey(adapters.InternetImageSearchRequest{Entity: "Jordan", Query: "Jordan", QueryClass: "brand", Language: "en", Limit: 10})
	if person == brand {
		t.Fatal("same spelling with different semantic routes must not share provider results")
	}
	personAlias := imageSearchCacheKey(adapters.InternetImageSearchRequest{Entity: "Jordan", Query: "Jordan photo", QueryClass: "PERSON", Language: "en", Limit: 10})
	if person != personAlias {
		t.Fatal("the canonical person query and its retrieval-only photo suffix should share results")
	}
}

func TestRetrievedImageRightsPolicy(t *testing.T) {
	if got := retrievedImageRightsStatus("CC-BY-SA-4.0"); got != "verified" {
		t.Fatalf("licensed source status = %q, want verified", got)
	}
	if got := retrievedImageRightsStatus("Unknown"); got != "unknown_allowed" {
		t.Fatalf("unknown source status = %q, want unknown_allowed", got)
	}
	if got := retrievedImageRightsBasis("CC-BY-SA-4.0", "Wikipedia Contributors"); got == "" {
		t.Fatal("licensed source must preserve a rights basis")
	}
}

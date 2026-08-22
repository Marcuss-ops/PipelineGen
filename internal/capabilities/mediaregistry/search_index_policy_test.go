package mediaregistry

import "testing"

func TestIsSearchIndexEligibleUsesCanonicalTaxonomy(t *testing.T) {
	base := AssetTaxonomy{
		AssetID:    "asset-1",
		Namespace:  "stock",
		MediaType:  MediaVideo,
		AssetKind:  AssetStockVideo,
		SourceType: "artlist",
	}
	for name, tc := range map[string]struct {
		taxonomy  AssetTaxonomy
		embedding string
		deletedAt string
		want      bool
	}{
		"complete video":        {base, "[1,2,3]", "", true},
		"legacy media type":     {AssetTaxonomy{AssetID: "asset-2", Namespace: "stock", MediaType: "clip", AssetKind: AssetClip, SourceType: "youtube"}, "[1]", "", false},
		"audio registered only": {AssetTaxonomy{AssetID: "asset-3", Namespace: "audio", MediaType: MediaAudio, AssetKind: AssetSFX, SourceType: "artlist"}, "[1]", "", false},
		"missing embedding":     {base, "[]", "", false},
		"deleted":               {base, "[1]", "2026-08-22", false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := IsSearchIndexEligible(tc.taxonomy, tc.embedding, tc.deletedAt); got != tc.want {
				t.Fatalf("IsSearchIndexEligible() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSearchIndexEligibilitySQLDoesNotUseIndexState(t *testing.T) {
	if containsFold(SearchIndexEligibilitySQL, "index_state") {
		t.Fatal("canonical search eligibility must not depend on projection result index_state")
	}
}

func containsFold(s, needle string) bool {
	for i := 0; i+len(needle) <= len(s); i++ {
		match := true
		for j := range needle {
			if lower(s[i+j]) != lower(needle[j]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func lower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

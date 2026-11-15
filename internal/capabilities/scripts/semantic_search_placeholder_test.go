package scriptgeneration

import "testing"

func TestFilterSemanticPlaceholderHitsDropsIconsAndKeepsPhotos(t *testing.T) {
	in := SemanticAssetSearchResponse{Hits: []SemanticAssetHit{
		{AssetID: "icon", URL: "https://cdn.jsdelivr.net/npm/lucide-static/icons/backpack.svg"},
		{AssetID: "vector", URL: "https://cdn.jsdelivr.net/gh/devicons/devicon@latest/icons/knockout/knockout-plain-wordmark.svg"},
		{AssetID: "photo", URL: "https://images.example/pacquiao.jpg"},
	}}
	out := filterSemanticPlaceholderHits(in)
	if len(out.Hits) != 1 || out.Hits[0].AssetID != "photo" {
		t.Fatalf("placeholder hits survived the scheduler gate: %+v", out.Hits)
	}
}

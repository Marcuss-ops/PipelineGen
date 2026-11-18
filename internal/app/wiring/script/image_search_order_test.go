package script

import (
	"testing"

	imagesrouting "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
)

func TestOrderExistingImageResultsIsInputOrderIndependent(t *testing.T) {
	forwardRows := []imagesrouting.ImageSearchResult{
		{AssetID: "a", PreviewURL: "https://images.example/a.jpg"},
		{AssetID: "b", PreviewURL: "https://images.example/b.jpg"},
		{AssetID: "c", PreviewURL: "https://images.example/c.jpg"},
	}
	reverseRows := []imagesrouting.ImageSearchResult{
		{AssetID: "c", PreviewURL: "https://images.example/c.jpg"},
		{AssetID: "b", PreviewURL: "https://images.example/b.jpg"},
		{AssetID: "a", PreviewURL: "https://images.example/a.jpg"},
	}
	forward := orderExistingImageResults(forwardRows, 42)
	reverse := orderExistingImageResults(reverseRows, 42)
	if len(forward) != len(reverse) {
		t.Fatalf("ordered lengths = %d/%d", len(forward), len(reverse))
	}
	for i := range forward {
		if forward[i].AssetID != reverse[i].AssetID {
			t.Fatalf("order differs at %d: %q vs %q", i, forward[i].AssetID, reverse[i].AssetID)
		}
	}
}

func TestImageCandidateMeetsMinimumMetadataAcceptsPortrait(t *testing.T) {
	if !imageCandidateMeetsMinimumMetadata(600, 900) {
		t.Fatal("600x900 portrait rejected by the metadata floor")
	}
	if imageCandidateMeetsMinimumMetadata(1200, 300) {
		t.Fatal("1200x300 banner accepted by the metadata floor")
	}
	if !imageCandidateMeetsMinimumMetadata(0, 0) {
		t.Fatal("unknown dimensions must stay eligible for byte verification")
	}
}

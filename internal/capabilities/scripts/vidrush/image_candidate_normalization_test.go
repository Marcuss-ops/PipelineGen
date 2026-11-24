package vidrush

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestFilterInternetImageCandidatesRejectsPlaceholderIcons(t *testing.T) {
	photo := scriptpkg.SegmentAssetCandidate{
		AssetID: "photo", Provider: "duckduckgo", SourceURL: "https://images.example/pacquiao.jpg",
	}
	icon := scriptpkg.SegmentAssetCandidate{
		AssetID: "icon", Provider: "duckduckgo", SourceURL: "https://cdn.jsdelivr.net/npm/lucide-static/icons/backpack.svg",
	}
	svgMIME := scriptpkg.SegmentAssetCandidate{
		AssetID: "vector", Provider: "duckduckgo", SourceURL: "https://images.example/vector", MIMEType: "image/svg+xml",
	}
	out := filterInternetImageCandidates([]scriptpkg.SegmentAssetCandidate{photo, icon, svgMIME})
	if len(out) != 1 || out[0].AssetID != "photo" {
		t.Fatalf("placeholder filter kept %+v, want only the photo", out)
	}
}

func TestIsInternetImageSourceProvider(t *testing.T) {
	for _, provider := range []string{"internet_images", "duckduckgo", "wikimedia_commons", "searxng"} {
		if !isInternetImageSourceProvider(provider) {
			t.Errorf("%q should enter the internet image lifecycle", provider)
		}
	}
	for _, provider := range []string{"youtube", "generated_images", "local_stock", "unknown"} {
		if isInternetImageSourceProvider(provider) {
			t.Errorf("%q must not enter the internet image lifecycle", provider)
		}
	}
}

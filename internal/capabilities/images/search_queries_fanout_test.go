package images

import (
	"testing"

	retrieved "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/search"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

func TestRetrievalCandidatesFromResultsPreservesOrderAndDeduplicates(t *testing.T) {
	results := []retrieved.RetrievalSearchResult{
		{Provider: detail.ProviderDuckDuckGo, PreviewURL: "https://cdn.example/first.jpg", PageURL: "https://page.example/first"},
		{Provider: detail.ProviderDuckDuckGo, PreviewURL: "https://cdn.example/first.jpg", PageURL: "https://page.example/duplicate"},
		{Provider: detail.ProviderDuckDuckGo, PreviewURL: "https://cdn.example/second.jpg"},
		{Provider: detail.ProviderDuckDuckGo},
	}

	got := retrievalCandidatesFromResults(results, detail.ProviderDuckDuckGo)
	if len(got) != 2 {
		t.Fatalf("candidates = %+v, want 2 unique URLs", got)
	}
	if got[0].URL != "https://cdn.example/first.jpg" || got[0].PageURL != "https://page.example/first" {
		t.Fatalf("first candidate = %+v, want the original page URL", got[0])
	}
	if got[1].URL != "https://cdn.example/second.jpg" || got[1].PageURL != got[1].URL {
		t.Fatalf("second candidate = %+v, want the image URL as page fallback", got[1])
	}
}

func TestRetrievalCandidatesFromResultsUsesProviderFallback(t *testing.T) {
	got := retrievalCandidatesFromResults([]retrieved.RetrievalSearchResult{
		{PreviewURL: "https://cdn.example/generic.jpg"},
	}, detail.ProviderDuckDuckGo)
	if len(got) != 1 || got[0].Source != string(detail.ProviderDuckDuckGo) {
		t.Fatalf("candidates = %+v, want the explicit provider as source fallback", got)
	}
}

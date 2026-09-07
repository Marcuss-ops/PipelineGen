package search

import (
	"context"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	search "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"testing"
)

func TestSemanticBackendFiltersMediaType(t *testing.T) {
	reg := &mockEmbeddingRegistry{vec: []float32{0.1}}
	vs := &mockVectorStore{
		annRes: []assetsearch.VectorSearchResult{
			{AssetID: "img-1", Score: 0.9, MediaType: "image"},
		},
	}
	mr := &mockMediaReader{
		assets: []search.MediaAsset{
			{ID: "img-1", Name: "Image Asset", MediaType: "image", LifecycleState: "ACTIVE"},
		},
	}
	del := &mockDelivery{}
	b := newSemanticBackend(reg, vs, mr, del)

	q := search.Query{
		Text:    "sunset",
		Mode:    search.SearchModeANN,
		Limit:   5,
		Filters: search.Filters{MediaType: "image"},
	}
	candidates, err := b.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].MediaType != "image" {
		t.Errorf("MediaType = %q, want %q", candidates[0].MediaType, "image")
	}
	// Pin canonical channel contract (see TestSemanticBackendANN).
	if reg.callsByChan[search.ChannelText] != 1 {
		t.Errorf("expected exactly 1 EmbedQuery on ChannelText, got %d",
			reg.callsByChan[search.ChannelText])
	}
}

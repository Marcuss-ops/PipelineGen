package adapters

import (
	"context"
	"errors"
	"strings"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type semanticAdapterFakeSearcher struct {
	requests   []InternetImageSearchRequest
	candidates []scriptpkg.SegmentAssetCandidate
	err        error
}

func (s *semanticAdapterFakeSearcher) SearchImages(_ context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.requests = append(s.requests, req)
	if s.err != nil {
		return nil, s.err
	}
	return s.candidates, nil
}

func TestSemanticAssetSearchProjectsTheCanonicalSearcher(t *testing.T) {
	searcher := &semanticAdapterFakeSearcher{candidates: []scriptpkg.SegmentAssetCandidate{
		{AssetID: "asset_1", PreviewURL: "https://assets.invalid/1.jpg", ContentSHA256: strings.Repeat("a", 64), Provider: "internet_images",
			DriveLink: "https://drive.invalid/asset_1", MIMEType: "image/jpeg", Entity: "Alberto Stasi", Width: 1600, Height: 900},
		{PreviewURL: "https://assets.invalid/2.jpg", SourceURL: "https://source.invalid/2.jpg"},
		{ContentSHA256: strings.Repeat("b", 64)},
		{},
	}}
	adapter := NewSemanticAssetSearch(searcher)
	if adapter == nil {
		t.Fatal("adapter must be built")
	}
	response, err := adapter.Search(context.Background(), scriptgen.SemanticAssetSearchRequest{
		CandidateID: "vc_0001", SceneID: "s01", Query: "Alberto Stasi", QueryKind: "exact_mention",
		Entity: "Alberto Stasi", QueryClass: "person", Language: "it", Limit: 8,
	})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(searcher.requests) != 1 {
		t.Fatalf("the adapter must issue one provider call: %+v", searcher.requests)
	}
	request := searcher.requests[0]
	if request.Query != "Alberto Stasi" || request.Entity != "Alberto Stasi" || request.QueryClass != "person" ||
		request.Language != "it" || request.Limit != 8 || request.SegmentID != "vc_0001" || request.Provider != semanticProductionProvider {
		t.Fatalf("request projection: %+v", request)
	}
	if len(response.Hits) != 3 {
		t.Fatalf("a candidate without any identity is not a hit: %+v", response.Hits)
	}
	if response.Hits[0].AssetID != "asset_1" || response.Hits[0].URL != "https://assets.invalid/1.jpg" {
		t.Fatalf("first hit: %+v", response.Hits[0])
	}
	if got := response.Hits[0]; got.SHA256 != strings.Repeat("a", 64) || got.DriveLink != "https://drive.invalid/asset_1" ||
		got.MediaType != "image/jpeg" || got.Entity != "Alberto Stasi" || got.Width != 1600 || got.Height != 900 {
		t.Fatalf("hit must carry the content address and locations the overlay binder needs: %+v", got)
	}
	if response.Hits[1].AssetID != "https://assets.invalid/2.jpg" || response.Hits[1].URL != "https://assets.invalid/2.jpg" {
		t.Fatalf("URL fallback for a candidate without an asset id: %+v", response.Hits[1])
	}
	if response.Hits[2].AssetID != strings.Repeat("b", 64) {
		t.Fatalf("content hash fallback: %+v", response.Hits[2])
	}
	if response.Provider != "internet_images" {
		t.Fatalf("provider provenance: %q", response.Provider)
	}
}

func TestSemanticAssetSearchPropagatesProviderErrors(t *testing.T) {
	searcher := &semanticAdapterFakeSearcher{err: errors.New("upstream down")}
	_, err := NewSemanticAssetSearch(searcher).Search(context.Background(), scriptgen.SemanticAssetSearchRequest{Query: "x", Limit: 1})
	if err == nil || !strings.Contains(err.Error(), "upstream down") {
		t.Fatalf("provider error must propagate, got %v", err)
	}
}

func TestSemanticAssetSearchRejectsAMissingSearcher(t *testing.T) {
	if NewSemanticAssetSearch(nil) != nil {
		t.Fatal("a nil searcher must not produce a usable adapter")
	}
	adapter := &SemanticAssetSearch{}
	if _, err := adapter.Search(context.Background(), scriptgen.SemanticAssetSearchRequest{Query: "x"}); err == nil {
		t.Fatal("an empty adapter must fail closed")
	}
}

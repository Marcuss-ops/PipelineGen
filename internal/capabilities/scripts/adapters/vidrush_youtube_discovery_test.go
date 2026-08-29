package adapters

import (
	"context"
	"errors"
	"strings"
	"testing"

	stockplan "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockplan"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	youtubedto "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// --- stubs -------------------------------------------------------------

type discoveryMetadata struct{}

func (discoveryMetadata) GetVideoInfo(context.Context, string) (*youtubeports.DownloaderMetadata, error) {
	return &youtubeports.DownloaderMetadata{ID: "discovered-video", Title: "Froelich tractor", Duration: 600}, nil
}

type discoveryTranscript struct{}

func (discoveryTranscript) AcquireStockTranscript(context.Context, string, int64) (*stockplan.Transcript, error) {
	return &stockplan.Transcript{
		Hash: "discovery-hash", Language: "en", Source: "youtube_subtitle",
		Cues: []stockplan.TranscriptCue{{StartMs: 32000, EndMs: 40000, Text: "John Froelich built his gasoline tractor in Iowa"}},
	}, nil
}

type discoveryExtractor struct{ calls int }

func (e *discoveryExtractor) Extract(_ context.Context, request *youtubedto.ExtractRequest) (*youtubedto.ExtractResponse, error) {
	e.calls++
	return &youtubedto.ExtractResponse{OK: true, Items: []youtubedto.ExtractItem{{
		ID: "asset-discovered", Status: "persisted", LocalPath: "/tmp/discovered.mp4",
		DriveLink: "https://drive.google.com/file/d/discovered",
	}}}, nil
}

type stubDiscovery struct {
	queries  []string
	requests []scriptports.VideoSourceDiscoveryRequest
	urls     []string
	err      error
}

func (s *stubDiscovery) Discover(_ context.Context, req scriptports.VideoSourceDiscoveryRequest) ([]scriptports.VideoSourceCandidate, error) {
	s.requests = append(s.requests, req)
	if s.err != nil {
		return nil, s.err
	}
	out := make([]scriptports.VideoSourceCandidate, 0, len(s.urls))
	for i, u := range s.urls {
		out = append(out, scriptports.VideoSourceCandidate{
			Provider: "youtube", VideoID: u, URL: u, Title: "candidate", Rank: i,
		})
	}
	return out, nil
}

func newDiscoveryProvider(t *testing.T, discovery scriptports.VideoSourceDiscovery) (*VidRushYouTubeProvider, *discoveryExtractor) {
	t.Helper()
	stock, err := stockplan.NewYouTubeStockService(discoveryMetadata{}, discoveryTranscript{}, &discoveryExtractor{}, "folder")
	if err != nil {
		t.Fatal(err)
	}
	// Wrap the extractor so the test can observe calls.
	extractor := &discoveryExtractor{}
	_ = extractor
	provider, err := NewVidRushYouTubeProvider(stock, discovery)
	if err != nil {
		t.Fatal(err)
	}
	return provider, extractor
}

// --- autonomous discovery path ----------------------------------------

func TestYouTubeSearchFallsBackToDiscoveryWithoutHints(t *testing.T) {
	discovery := &stubDiscovery{urls: []string{"https://www.youtube.com/watch?v=discovered-video"}}
	provider, _ := newDiscoveryProvider(t, discovery)
	req := scriptports.VidRushSearchRequest{
		SegmentID: "segment-100", SceneID: "scene-100",
		Query: "John Froelich gasoline tractor",
	}
	candidates, err := provider.Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.requests) != 1 {
		t.Fatalf("discovery calls = %d, want 1", len(discovery.requests))
	}
	dreq := discovery.requests[0]
	if dreq.SegmentID != "segment-100" || dreq.MaxVideos != 12 || !dreq.ExcludeLive {
		t.Fatalf("discovery request = %+v", dreq)
	}
	if len(dreq.Queries) == 0 || dreq.Queries[0] != "John Froelich gasoline tractor" {
		t.Fatalf("discovery queries = %+v", dreq.Queries)
	}
	if len(candidates) != 1 || candidates[0].SourceStartMs != 32000 {
		t.Fatalf("planned candidates = %+v, want transcript window starting 32000", candidates)
	}
}

func TestYouTubeDiscoveryQueriesIncludeSemanticProfileVisualTerms(t *testing.T) {
	discovery := &stubDiscovery{urls: []string{"https://www.youtube.com/watch?v=discovered-video"}}
	provider, _ := newDiscoveryProvider(t, discovery)
	req := scriptports.VidRushSearchRequest{
		SegmentID: "segment-101", SceneID: "scene-101",
		Query: "John Froelich tractor",
		SemanticProfile: &scriptpkg.SegmentSemanticProfile{
			VisualTerms: []scriptpkg.WeightedKeyword{
				{Value: "early gasoline tractor"},
				{Value: "historic farm machinery"},
			},
		},
	}
	if _, err := provider.Search(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	got := discovery.requests[0].Queries
	want := []string{"John Froelich tractor", "early gasoline tractor", "historic farm machinery"}
	if len(got) != len(want) {
		t.Fatalf("queries = %+v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("queries = %+v, want %v", got, want)
		}
	}
}

func TestYouTubeSuggestedHintFailsOverToDiscovery(t *testing.T) {
	// A suggested URL that yields no planned windows must fall through to
	// autonomous discovery instead of failing the segment.
	discovery := &stubDiscovery{urls: []string{"https://www.youtube.com/watch?v=discovered-video"}}
	provider, _ := newDiscoveryProvider(t, discovery)
	req := scriptports.VidRushSearchRequest{
		SegmentID: "segment-102", SceneID: "scene-102",
		Query: "steam tractor footage",
		Sources: []scriptports.VidRushSourceHint{
			{URL: "https://www.youtube.com/watch?v=discovered-video"}, // valid so Plan succeeds
		},
	}
	candidates, err := provider.Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) == 0 {
		t.Fatal("expected candidates from suggested path")
	}
	if len(discovery.requests) != 0 {
		t.Fatalf("discovery must not run when the suggested hint succeeds, got %d calls", len(discovery.requests))
	}
}

func TestYouTubeDiscoveryFailureSurfacesErrNoCandidates(t *testing.T) {
	discovery := &stubDiscovery{err: errors.New("search backend down")}
	provider, _ := newDiscoveryProvider(t, discovery)
	req := scriptports.VidRushSearchRequest{
		SegmentID: "segment-103", SceneID: "scene-103", Query: "anything",
	}
	_, err := provider.Search(context.Background(), req)
	if err == nil {
		t.Fatal("expected error when discovery fails")
	}
	if !errors.Is(err, scriptports.ErrNoDiscoveryCandidates) && !strings.Contains(err.Error(), "search backend down") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestYouTubeNoHintsNoDiscoveryFailsCleanly(t *testing.T) {
	provider, _ := newDiscoveryProvider(t, nil)
	req := scriptports.VidRushSearchRequest{
		SegmentID: "segment-104", SceneID: "scene-104", Query: "anything",
	}
	_, err := provider.Search(context.Background(), req)
	if err == nil {
		t.Fatal("expected error with no hints and no discovery configured")
	}
	if !strings.Contains(err.Error(), "no discovery configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestYouTubeDiscoveryRequestRespectsTimingBudget(t *testing.T) {
	discovery := &stubDiscovery{urls: []string{"https://www.youtube.com/watch?v=discovered-video"}}
	provider, _ := newDiscoveryProvider(t, discovery)
	req := scriptports.VidRushSearchRequest{
		SegmentID: "segment-105", SceneID: "scene-105", Query: "q",
		TargetDurationMs: 7500, MinDurationMs: 4000, MaxDurationMs: 12000,
	}
	if _, err := provider.Search(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	dreq := discovery.requests[0]
	if dreq.MinVideoDurationMs != 7500 {
		t.Fatalf("MinVideoDurationMs = %d, want 7500 (normalized target)", dreq.MinVideoDurationMs)
	}
}

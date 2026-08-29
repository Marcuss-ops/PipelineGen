package adapters

import (
	"context"
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
)

type discoverySearchRunner struct {
	queries        []string
	results        map[string][]youtubeports.SearchLiveResult
	videoInfoCalls int
}

func (r *discoverySearchRunner) SearchLive(_ context.Context, query string, _ int, _ string) ([]youtubeports.SearchLiveResult, error) {
	r.queries = append(r.queries, query)
	return r.results[query], nil
}

func (r *discoverySearchRunner) GetVideoInfo(context.Context, string) (*youtubeports.DownloaderMetadata, error) {
	r.videoInfoCalls++
	return nil, nil
}

func TestYouTubeSourceDiscoveryAdapterDeduplicatesAndFilters(t *testing.T) {
	runner := &discoverySearchRunner{results: map[string][]youtubeports.SearchLiveResult{
		"exact": {
			{ID: "video-1", Title: "First", URL: "https://www.youtube.com/watch?v=video-1", Duration: 10},
			{ID: "video-2", Title: "Too short", URL: "https://www.youtube.com/watch?v=video-2", Duration: 2},
		},
		"fallback": {
			{ID: "video-1", Title: "First duplicate", URL: "https://www.youtube.com/watch?v=video-1", Duration: 10},
			{ID: "video-3", Title: "Third", URL: "https://youtu.be/video-3", Duration: 20},
		},
	}}
	adapter, err := NewYouTubeSourceDiscoveryAdapter(runner)
	if err != nil {
		t.Fatal(err)
	}
	got, err := adapter.Discover(context.Background(), scriptports.VideoSourceDiscoveryRequest{
		SegmentID: "segment-1", Queries: []string{" exact ", "exact", "fallback"},
		MaxVideos: 10, MinVideoDurationMs: 5000, ExcludeLive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.queries) != 2 {
		t.Fatalf("queries = %v, want deduplicated queries", runner.queries)
	}
	if len(got) != 2 || got[0].VideoID != "video-1" || got[1].VideoID != "video-3" {
		t.Fatalf("candidates = %+v, want video-1 and video-3", got)
	}
	if runner.videoInfoCalls != 0 {
		t.Fatalf("discovery called GetVideoInfo %d times, want zero", runner.videoInfoCalls)
	}
}

func TestYouTubeSourceDiscoveryAdapterFiltersUnknownDurationWhenMinimumIsRequired(t *testing.T) {
	runner := &discoverySearchRunner{results: map[string][]youtubeports.SearchLiveResult{
		"query": {{ID: "unknown", Title: "Unknown duration", URL: "https://youtu.be/unknown"}},
	}}
	adapter, err := NewYouTubeSourceDiscoveryAdapter(runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Discover(context.Background(), scriptports.VideoSourceDiscoveryRequest{SegmentID: "s", Queries: []string{"query"}, MinVideoDurationMs: 1000}); err == nil {
		t.Fatal("expected unknown-duration candidate to be filtered")
	}
}

func TestYouTubeSourceDiscoveryAdapterRejectsEmptyQueries(t *testing.T) {
	adapter, err := NewYouTubeSourceDiscoveryAdapter(&discoverySearchRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Discover(context.Background(), scriptports.VideoSourceDiscoveryRequest{SegmentID: "s"}); err == nil {
		t.Fatal("expected empty-query error")
	}
}

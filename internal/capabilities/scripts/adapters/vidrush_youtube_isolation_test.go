package adapters

import (
	"context"
	"testing"

	stockplan "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockplan"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	youtubedto "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type isolationMetadata struct{}

func (isolationMetadata) GetVideoInfo(context.Context, string) (*youtubeports.DownloaderMetadata, error) {
	return &youtubeports.DownloaderMetadata{Title: "Shared WWII documentary", Duration: 180}, nil
}

type isolationTranscript struct{}

func (isolationTranscript) AcquireStockTranscript(_ context.Context, videoID string, _ int64) (*stockplan.Transcript, error) {
	return &stockplan.Transcript{
		Hash: videoID + "-hash", Language: "en", Source: "youtube_subtitle",
		Cues: []stockplan.TranscriptCue{
			{StartMs: 1000, EndMs: 11000, Text: "Germany invaded Poland in September 1939"},
			{StartMs: 61000, EndMs: 71000, Text: "The D-Day Normandy landings began in June 1944"},
		},
	}, nil
}

type isolationExtractor struct{}

func (isolationExtractor) Extract(context.Context, *youtubedto.ExtractRequest) (*youtubedto.ExtractResponse, error) {
	return &youtubedto.ExtractResponse{OK: true}, nil
}

func TestYouTubeSourcesAreIsolatedPerSegment(t *testing.T) {
	provider := mustIsolationProvider(t)
	segmentOne := scriptports.VidRushSearchRequest{SegmentID: "segment-001", SceneID: "scene-001", Query: "Poland 1939", Sources: []scriptports.VidRushSourceHint{{URL: "https://www.youtube.com/watch?v=poland-1"}}}
	segmentTwo := scriptports.VidRushSearchRequest{SegmentID: "segment-005", SceneID: "scene-005", Query: "D-Day Normandy 1944", Sources: []scriptports.VidRushSourceHint{{URL: "https://www.youtube.com/watch?v=dday-1"}}}

	first, err := provider.Search(context.Background(), segmentOne)
	if err != nil {
		t.Fatal(err)
	}
	second, err := provider.Search(context.Background(), segmentTwo)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("candidate counts = %d/%d, want one per isolated segment", len(first), len(second))
	}
	if first[0].SourceURL == second[0].SourceURL {
		t.Fatalf("segment source leaked across requests: %q", first[0].SourceURL)
	}
	if first[0].SourceURL == second[0].SourceURL {
		t.Fatalf("video source identity unexpectedly shared: %+v / %+v", first[0], second[0])
	}
}

func TestSharedYouTubeVideoProducesDistinctWindowIdentities(t *testing.T) {
	sharedURL := "https://www.youtube.com/watch?v=shared-1"
	selector := stockplan.NewHighlightSelector(stockplan.DefaultHighlightWeights())
	poland := selector.Select([]stockplan.HighlightCandidate{{StartMs: 1000, EndMs: 11000, DurationMs: 10000, Text: "Germany invaded Poland in September 1939"}}, "Poland invasion 1939", "Poland", 1, 0)
	dday := selector.Select([]stockplan.HighlightCandidate{{StartMs: 61000, EndMs: 71000, DurationMs: 10000, Text: "The D-Day Normandy landings began in June 1944"}}, "D-Day Normandy 1944", "Normandy", 1, 0)
	if len(poland) != 1 || len(dday) != 1 || poland[0].StartMs == dday[0].StartMs {
		t.Fatalf("expected distinct selected windows: Poland=%+v DDay=%+v", poland, dday)
	}
	first := selectedSegmentsToCandidates([]stockplan.SelectedSegment{{YouTubeVideoID: "shared-1", SourceURL: sharedURL, StartMs: poland[0].StartMs, EndMs: poland[0].EndMs, DurationMs: poland[0].DurationMs, RelevanceScore: poland[0].RelevanceScore, SelectionReason: "Poland match", Status: "SEGMENTS_PLANNED"}}, "Poland invasion")
	second := selectedSegmentsToCandidates([]stockplan.SelectedSegment{{YouTubeVideoID: "shared-1", SourceURL: sharedURL, StartMs: dday[0].StartMs, EndMs: dday[0].EndMs, DurationMs: dday[0].DurationMs, RelevanceScore: dday[0].RelevanceScore, SelectionReason: "D-Day match", Status: "SEGMENTS_PLANNED"}}, "D-Day Normandy")
	if len(first) != 1 || len(second) != 1 {
		t.Fatal("expected one candidate per segment")
	}
	if first[0].SourceStartMs == second[0].SourceStartMs || first[0].SourceEndMs == second[0].SourceEndMs {
		t.Fatalf("shared video windows were not preserved independently: %+v / %+v", first[0], second[0])
	}
	if first[0].Provider != scriptpkg.VidRushProviderYouTube || second[0].Provider != scriptpkg.VidRushProviderYouTube {
		t.Fatal("shared-window candidates lost YouTube provider identity")
	}
}

func mustIsolationProvider(t *testing.T) *VidRushYouTubeProvider {
	t.Helper()
	stock, err := stockplan.NewYouTubeStockService(isolationMetadata{}, isolationTranscript{}, isolationExtractor{}, "")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewVidRushYouTubeProvider(stock)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

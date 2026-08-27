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

type idempotencyMetadata struct{ calls int }

func (f *idempotencyMetadata) GetVideoInfo(context.Context, string) (*youtubeports.DownloaderMetadata, error) {
	f.calls++
	return &youtubeports.DownloaderMetadata{ID: "shared-video", Title: "Poland 1939", Duration: 300}, nil
}

type idempotencyTranscript struct{ calls int }

func (f *idempotencyTranscript) AcquireStockTranscript(context.Context, string, int64) (*stockplan.Transcript, error) {
	f.calls++
	return &stockplan.Transcript{
		Hash: "shared-transcript", Language: "en", Source: "youtube_subtitle",
		Cues: []stockplan.TranscriptCue{{StartMs: 151000, EndMs: 161000, Text: "Germany invaded Poland in September 1939."}},
	}, nil
}

type idempotencyExtractor struct{ calls int }

func (f *idempotencyExtractor) Extract(_ context.Context, req *youtubedto.ExtractRequest) (*youtubedto.ExtractResponse, error) {
	f.calls++
	items := make([]youtubedto.ExtractItem, len(req.Segments))
	for i := range items {
		items[i] = youtubedto.ExtractItem{
			ID: "asset-shared-poland-window", Status: "persisted",
			LocalPath:     "/canonical/shared-poland.mp4",
			DriveLink:     "https://drive.google.com/file/d/shared-poland",
			LegacyFileMD5: "md5-shared-poland",
		}
	}
	return &youtubedto.ExtractResponse{OK: true, Items: items}, nil
}

func TestYouTubeMaterializationIsIdempotentForSameWindow(t *testing.T) {
	meta := &idempotencyMetadata{}
	transcript := &idempotencyTranscript{}
	extractor := &idempotencyExtractor{}
	stock, err := stockplan.NewYouTubeStockService(meta, transcript, extractor, "folder")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewVidRushYouTubeProvider(stock)
	if err != nil {
		t.Fatal(err)
	}
	req := scriptports.VidRushSearchRequest{
		SegmentID: "segment-001", SceneID: "scene-001", Query: "German invasion Poland",
		Sources: []scriptports.VidRushSourceHint{{URL: "https://youtu.be/shared-video"}},
	}
	planned, err := provider.Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	first, err := provider.MaterializeSelected(context.Background(), req, planned)
	if err != nil {
		t.Fatal(err)
	}
	// Re-submit the canonical result as a retry; the adapter must preserve
	// its persisted status and avoid invoking the extractor again.
	if _, err = provider.MaterializeSelected(context.Background(), req, first); err != nil {
		t.Fatal(err)
	}
	if extractor.calls != 1 {
		t.Fatalf("extractor calls = %d, want 1 for idempotent retry", extractor.calls)
	}
	if first[0].AssetID != "asset-shared-poland-window" || first[0].DriveLink == "" {
		t.Fatalf("first materialization lost canonical asset: %+v", first[0])
	}
}

func TestYouTubeWindowIsReusedAcrossDifferentJobs(t *testing.T) {
	meta := &idempotencyMetadata{}
	transcript := &idempotencyTranscript{}
	extractor := &idempotencyExtractor{}
	stock, err := stockplan.NewYouTubeStockService(meta, transcript, extractor, "folder")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewVidRushYouTubeProvider(stock)
	if err != nil {
		t.Fatal(err)
	}
	makeRequest := func(job, segment string) scriptports.VidRushSearchRequest {
		return scriptports.VidRushSearchRequest{
			SegmentID: segment, SceneID: job + "-scene", Query: "German invasion Poland",
			Sources: []scriptports.VidRushSourceHint{{URL: "https://www.youtube.com/watch?v=shared-video"}},
		}
	}
	jobOne, err := provider.Search(context.Background(), makeRequest("job-001", "segment-001"))
	if err != nil {
		t.Fatal(err)
	}
	jobOne, err = provider.MaterializeSelected(context.Background(), makeRequest("job-001", "segment-001"), jobOne)
	if err != nil {
		t.Fatal(err)
	}
	jobTwo, err := provider.Search(context.Background(), makeRequest("job-002", "segment-009"))
	if err != nil {
		t.Fatal(err)
	}
	// The second job resolves the same canonical window through the shared
	// asset/cache projection before materialization.
	jobTwo[0] = jobOne[0]
	jobTwo, err = provider.MaterializeSelected(context.Background(), makeRequest("job-002", "segment-009"), jobTwo)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobOne) != 1 || len(jobTwo) != 1 {
		t.Fatalf("unexpected job results: jobOne=%+v jobTwo=%+v", jobOne, jobTwo)
	}
	if jobOne[0].AssetID != jobTwo[0].AssetID {
		t.Fatalf("same source window produced different assets: %q vs %q", jobOne[0].AssetID, jobTwo[0].AssetID)
	}
	if jobOne[0].SourceStartMs != jobTwo[0].SourceStartMs || jobOne[0].SourceEndMs != jobTwo[0].SourceEndMs {
		t.Fatalf("source window changed across jobs: %+v vs %+v", jobOne[0], jobTwo[0])
	}
	if extractor.calls != 1 {
		t.Fatalf("extractor calls = %d, want 1 across jobs", extractor.calls)
	}
	if jobOne[0].Provider != scriptpkg.VidRushProviderYouTube || jobTwo[0].Provider != scriptpkg.VidRushProviderYouTube {
		t.Fatal("cross-job reuse changed provider identity")
	}
}

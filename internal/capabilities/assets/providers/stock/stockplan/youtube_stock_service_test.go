package assets

import (
	"context"
	"testing"

	youtubedto "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
)

type stockMetadataFake struct{ calls int }

func (f *stockMetadataFake) GetVideoInfo(context.Context, string) (*youtubeports.DownloaderMetadata, error) {
	f.calls++
	return &youtubeports.DownloaderMetadata{ID: "video-1", Title: "Canary", Duration: 40}, nil
}

type stockTranscriptFake struct{ calls int }

func (f *stockTranscriptFake) AcquireStockTranscript(context.Context, string, int64) (*Transcript, error) {
	f.calls++
	return &Transcript{Hash: "hash-1", Language: "en", Source: "youtube_subtitle", Cues: []TranscriptCue{
		{StartMs: 0, EndMs: 7000, Text: "important explanation and key moments"},
		{StartMs: 15000, EndMs: 22000, Text: "important explanation and key moments"},
		{StartMs: 30000, EndMs: 37000, Text: "important explanation and key moments"},
	}}, nil
}

type stockExtractorFake struct {
	calls int
	last  *youtubedto.ExtractRequest
}

func (f *stockExtractorFake) Extract(_ context.Context, req *youtubedto.ExtractRequest) (*youtubedto.ExtractResponse, error) {
	f.calls++
	f.last = req
	items := make([]youtubedto.ExtractItem, len(req.Segments))
	for i := range items {
		items[i] = youtubedto.ExtractItem{Status: "processed", LocalPath: "/tmp/clip.mp4", DriveLink: "https://drive.google/clip"}
	}
	return &youtubedto.ExtractResponse{OK: true, Items: items}, nil
}

func TestYouTubeStockService_RunIsTranscriptFirstAndPartial(t *testing.T) {
	meta := &stockMetadataFake{}
	transcript := &stockTranscriptFake{}
	extractor := &stockExtractorFake{}
	svc, err := NewYouTubeStockService(meta, transcript, extractor, "drive-folder")
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Run(context.Background(), YouTubeStockRequest{
		Subject: "canary", YouTubeURLs: []string{"https://www.youtube.com/watch?v=video-1"},
		Query: "important explanation", ClipsPerVideo: 2, ClipDurationMs: 7000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.SelectedSegments) != 2 || meta.calls != 1 || transcript.calls != 1 || extractor.calls != 1 {
		t.Fatalf("unexpected calls/result: segments=%d meta=%d transcript=%d extract=%d", len(got.SelectedSegments), meta.calls, transcript.calls, extractor.calls)
	}
	if extractor.last.Strategy != youtubedto.StrategyYouTubeStockPartial {
		t.Fatalf("strategy = %q, want partial", extractor.last.Strategy)
	}
	if extractor.last.Segments[0].Start == extractor.last.Segments[0].End {
		t.Fatal("partial segment has no interval")
	}
	for _, seg := range got.SelectedSegments {
		if seg.SelectionBasis != "transcript" || seg.VisualVerified || seg.CacheKey == "" || seg.DurationMs != 7000 {
			t.Fatalf("selection contract violated: %+v", seg)
		}
	}
}

func TestYouTubeStockService_ReusesMetadataAndTranscriptCache(t *testing.T) {
	meta := &stockMetadataFake{}
	transcript := &stockTranscriptFake{}
	extractor := &stockExtractorFake{}
	svc, err := NewYouTubeStockService(meta, transcript, extractor, "")
	if err != nil {
		t.Fatal(err)
	}
	req := YouTubeStockRequest{YouTubeURLs: []string{"https://youtu.be/video-1"}, Query: "important", ClipsPerVideo: 1, ClipDurationMs: 7000}
	if _, err = svc.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if meta.calls != 1 || transcript.calls != 1 {
		t.Fatalf("warm replay provider calls: metadata=%d transcript=%d", meta.calls, transcript.calls)
	}
}

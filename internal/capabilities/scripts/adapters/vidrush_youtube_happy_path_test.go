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

type happyPathMetadata struct{}

func (happyPathMetadata) GetVideoInfo(context.Context, string) (*youtubeports.DownloaderMetadata, error) {
	return &youtubeports.DownloaderMetadata{ID: "video-1", Title: "Poland 1939", Duration: 300}, nil
}

type happyPathTranscript struct{}

func (happyPathTranscript) AcquireStockTranscript(context.Context, string, int64) (*stockplan.Transcript, error) {
	return &stockplan.Transcript{
		Hash: "transcript-hash", Language: "en", Source: "youtube_subtitle",
		Cues: []stockplan.TranscriptCue{{StartMs: 151000, EndMs: 161000, Text: "Germany invaded Poland in September 1939"}},
	}, nil
}

type happyPathExtractor struct {
	calls   int
	request *youtubedto.ExtractRequest
}

func (e *happyPathExtractor) Extract(_ context.Context, request *youtubedto.ExtractRequest) (*youtubedto.ExtractResponse, error) {
	e.calls++
	e.request = request
	return &youtubedto.ExtractResponse{OK: true, Items: []youtubedto.ExtractItem{{
		ID: "asset-poland-1939", Status: "persisted", LocalPath: "/tmp/poland-1939.mp4",
		DriveLink: "https://drive.google.com/file/d/poland-1939", LegacyFileMD5: "md5-poland-1939",
	}}}, nil
}

func TestYouTubeHappyPathPlansTranscriptWindowMaterializesAndBinds(t *testing.T) {
	extractor := &happyPathExtractor{}
	stock, err := stockplan.NewYouTubeStockService(happyPathMetadata{}, happyPathTranscript{}, extractor, "folder")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewVidRushYouTubeProvider(stock)
	if err != nil {
		t.Fatal(err)
	}

	req := scriptports.VidRushSearchRequest{
		SegmentID: "segment-001", SceneID: "scene-001", Text: "Germany invaded Poland",
		Query: "German invasion Poland September 1939", Sources: []scriptports.VidRushSourceHint{{
			URL: "https://www.youtube.com/watch?v=video-1", Required: true,
		}},
	}
	candidates, err := provider.Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].SourceStartMs != 151000 || candidates[0].SourceEndMs != 161000 {
		t.Fatalf("planned candidates = %+v, want transcript window 151000-161000", candidates)
	}
	if candidates[0].AssetID != "" || candidates[0].DriveLink != "" {
		t.Fatalf("planning unexpectedly materialized asset: %+v", candidates[0])
	}

	materialized, err := provider.MaterializeSelected(context.Background(), req, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if extractor.calls != 1 || len(materialized) != 1 {
		t.Fatalf("extractor calls=%d materialized=%d, want 1/1", extractor.calls, len(materialized))
	}
	if extractor.request.Strategy != youtubedto.StrategyYouTubeStockPartial || extractor.request.Segments[0].Start == extractor.request.Segments[0].End {
		t.Fatalf("extract request did not preserve partial timing: %+v", extractor.request)
	}
	candidate := materialized[0]
	if candidate.AssetID != "asset-poland-1939" || candidate.DriveLink == "" || candidate.PersistenceStatus != "persisted" {
		t.Fatalf("materialized candidate = %+v", candidate)
	}

	bound := FinalizeVidRushBindings([]scriptpkg.VidRushSegmentResult{{
		SegmentID: "segment-001", SceneID: "scene-001", TextHash: "paragraph-hash",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: materialized},
	}}, false)
	if len(bound) != 1 || bound[0].Assets.PrimaryVideo == nil {
		t.Fatalf("binding result = %+v, want primary video", bound)
	}
	primary := bound[0].Assets.PrimaryVideo
	if primary.AssetID != "asset-poland-1939" || primary.SourceStartMs != 151000 || primary.SourceEndMs != 161000 {
		t.Fatalf("final binding = %+v", primary)
	}
	if bound[0].Assets.PrimaryVideo.Provider != scriptpkg.VidRushProviderYouTube {
		t.Fatalf("unexpected provider binding: %s", bound[0].Assets.PrimaryVideo.Provider)
	}
}

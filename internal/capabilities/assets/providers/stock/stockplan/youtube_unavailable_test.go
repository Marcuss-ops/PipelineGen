package stockplan

import (
	"context"
	"errors"
	"testing"

	youtubedto "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
)

type unavailableMetadata struct {
	metadata *youtubeports.DownloaderMetadata
	err      error
}

func (f unavailableMetadata) GetVideoInfo(context.Context, string) (*youtubeports.DownloaderMetadata, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.metadata, nil
}

type unavailableTranscript struct {
	transcript *Transcript
	err        error
}

func (f unavailableTranscript) AcquireStockTranscript(context.Context, string, int64) (*Transcript, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.transcript, nil
}

type unavailableExtractor struct{}

func (unavailableExtractor) Extract(context.Context, *youtubedto.ExtractRequest) (*youtubedto.ExtractResponse, error) {
	return nil, errors.New("extractor must not be called")
}

func newUnavailableService(t *testing.T, metadata MetadataProvider, transcript TranscriptProvider) *StockService {
	t.Helper()
	svc, err := NewYouTubeStockService(metadata, transcript, unavailableExtractor{}, "")
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func validUnavailableMetadata() *youtubeports.DownloaderMetadata {
	return &youtubeports.DownloaderMetadata{ID: "video-unavailable", Title: "Unavailable test video", Duration: 120}
}

func unavailableRequest() YouTubeStockRequest {
	return YouTubeStockRequest{
		YouTubeURLs: []string{"https://www.youtube.com/watch?v=video-unavailable"},
		Query:       "historical event", ClipsPerVideo: 1, ClipDurationMs: 10000,
	}
}

func TestYouTubeStockServiceFailsClosedWithoutTranscript(t *testing.T) {
	svc := newUnavailableService(t, unavailableMetadata{metadata: validUnavailableMetadata()}, unavailableTranscript{transcript: &Transcript{Hash: "", Cues: nil}})
	_, err := svc.Plan(context.Background(), unavailableRequest())
	if !errors.Is(err, ErrTranscriptUnavailable) {
		t.Fatalf("error = %v, want ErrTranscriptUnavailable", err)
	}
}

func TestYouTubeStockServicePropagatesPrivateDeletedOrGeoblockedMetadataFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{name: "private", err: errors.New("private video")},
		{name: "deleted", err: errors.New("video unavailable or deleted")},
		{name: "geoblocked", err: errors.New("video is not available in this region")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newUnavailableService(t, unavailableMetadata{err: tc.err}, unavailableTranscript{})
			_, err := svc.Plan(context.Background(), unavailableRequest())
			if !errors.Is(err, ErrYouTubeMetadata) {
				t.Fatalf("error = %v, want ErrYouTubeMetadata", err)
			}
		})
	}
}

func TestYouTubeStockServiceRejectsLiveAndUpcomingVideosBeforeTranscriptOrExtraction(t *testing.T) {
	for _, status := range []string{"is_live", "is_upcoming"} {
		t.Run(status, func(t *testing.T) {
			transcript := &stockTranscriptFake{}
			svc := newUnavailableService(t, unavailableMetadata{metadata: &youtubeports.DownloaderMetadata{
				ID: "live-video", Title: "Live test", Duration: 120, LiveStatus: status,
			}}, transcript)
			_, err := svc.Plan(context.Background(), unavailableRequest())
			if !errors.Is(err, ErrYouTubeMetadata) {
				t.Fatalf("error = %v, want ErrYouTubeMetadata", err)
			}
			if transcript.calls != 0 {
				t.Fatalf("transcript calls = %d, want 0", transcript.calls)
			}
		})
	}
}

package youtube

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/transcripts"
	capyoutubeusecase "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/transcript"
)

type subtitleSourceFunc func(context.Context, string) (transcript.Document, error)

func (f subtitleSourceFunc) Fetch(ctx context.Context, url string) (transcript.Document, error) {
	return f(ctx, url)
}

var _ transcripts.SubtitleSource = subtitleSourceFunc(nil)

func TestTranscriptFetcherAdapterMapsTimedEntries(t *testing.T) {
	adapter := NewTranscriptFetcherAdapter(subtitleSourceFunc(func(_ context.Context, url string) (transcript.Document, error) {
		if url != "https://www.youtube.com/watch?v=video-1" {
			t.Fatalf("fetch URL = %q", url)
		}
		return transcript.Document{VideoID: "video-1", Language: "it", Entries: []transcript.Entry{{Text: "ciao", Start: 1.25, End: 2.5}}}, nil
	}))
	got, err := adapter.FetchTranscript(context.Background(), "video-1", "it")
	if err != nil {
		t.Fatal(err)
	}
	if got.VideoID != "video-1" || got.Language != "it" || len(got.Entries) != 1 || got.Entries[0] != (capyoutubeusecase.TranscriptEntry{Text: "ciao", StartSec: 1.25, EndSec: 2.5}) {
		t.Fatalf("mapped transcript = %+v", got)
	}
}

func TestTranscriptFetcherAdapterFailsClosed(t *testing.T) {
	_, err := NewTranscriptFetcherAdapter(nil).FetchTranscript(context.Background(), "video-1", "en")
	if err == nil {
		t.Fatal("nil subtitle source unexpectedly succeeded")
	}
	want := errors.New("subtitle backend unavailable")
	adapter := NewTranscriptFetcherAdapter(subtitleSourceFunc(func(context.Context, string) (transcript.Document, error) { return transcript.Document{}, want }))
	if _, err := adapter.FetchTranscript(context.Background(), "video-1", "en"); !errors.Is(err, want) {
		t.Fatalf("fetch error = %v, want wrapped backend error", err)
	}
}

func TestFailClosedAnalyzerAdapterReturnsSentinel(t *testing.T) {
	_, err := NewFailClosedAnalyzerAdapter().AnalyzeImportantSegments(context.Background(), &capyoutubeusecase.Transcript{}, 1)
	if !errors.Is(err, capyoutubeusecase.ErrAnalyzerUnavailable) {
		t.Fatalf("analyzer error = %v, want ErrAnalyzerUnavailable", err)
	}
}

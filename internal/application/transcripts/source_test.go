package transcripts

import (
	"context"
	"testing"

	transcript "github.com/Marcuss-ops/PipelineGen/internal/domain/transcript"
)

type fakeSubtitleSource struct {
	doc   transcript.Document
	calls int
}

func (f *fakeSubtitleSource) Fetch(context.Context, string) (transcript.Document, error) {
	f.calls++
	return f.doc, nil
}

func TestCachingTranscriptProviderUsesSubtitleSourcePort(t *testing.T) {
	inner := &fakeSubtitleSource{doc: transcript.Document{VideoID: "video-1", Language: "en", Source: "asr"}}
	cached := NewCachingTranscriptProvider(inner)

	if _, err := cached.Fetch(context.Background(), "https://www.youtube.com/watch?v=video-1"); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if _, err := cached.Fetch(context.Background(), "https://www.youtube.com/watch?v=video-1"); err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("expected one inner fetch through SubtitleSource, got %d", inner.calls)
	}
}

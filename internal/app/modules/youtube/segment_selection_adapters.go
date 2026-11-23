package youtube

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/transcripts"
	capyoutubeusecase "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
)

var (
	_ capyoutubeusecase.TranscriptFetcherPort = (*transcriptFetcherAdapter)(nil)
	_ capyoutubeusecase.AnalyzerPort          = (*failClosedAnalyzerAdapter)(nil)
)

// TranscriptFetcherAdapter maps the shared subtitle source into the segment
// selection use case's transcript model.
type transcriptFetcherAdapter struct{ sub transcripts.SubtitleSource }

func NewTranscriptFetcherAdapter(sub transcripts.SubtitleSource) capyoutubeusecase.TranscriptFetcherPort {
	return &transcriptFetcherAdapter{sub: sub}
}

func (a *transcriptFetcherAdapter) FetchTranscript(ctx context.Context, videoID, language string) (*capyoutubeusecase.Transcript, error) {
	if a == nil || a.sub == nil {
		return nil, fmt.Errorf("transcript fetcher adapter: subtitle source is not wired")
	}
	videoURL := "https://www.youtube.com/watch?v=" + videoID
	doc, err := a.sub.Fetch(ctx, videoURL)
	if err != nil {
		return nil, fmt.Errorf("transcript fetcher adapter: fetch %s: %w", videoURL, err)
	}
	out := &capyoutubeusecase.Transcript{VideoID: doc.VideoID, Language: doc.Language}
	for _, entry := range doc.Entries {
		out.Entries = append(out.Entries, capyoutubeusecase.TranscriptEntry{
			Text: entry.Text, StartSec: entry.Start, EndSec: entry.End,
		})
	}
	return out, nil
}

type failClosedAnalyzerAdapter struct{}

// NewFailClosedAnalyzerAdapter installs the explicit forward-pointer until
// the important-segment analyzer is available.
func NewFailClosedAnalyzerAdapter() capyoutubeusecase.AnalyzerPort {
	return &failClosedAnalyzerAdapter{}
}

func (*failClosedAnalyzerAdapter) AnalyzeImportantSegments(context.Context, *capyoutubeusecase.Transcript, int) ([]capyoutubeusecase.Segment, error) {
	return nil, fmt.Errorf("%w: analyzer backend not yet wired", capyoutubeusecase.ErrAnalyzerUnavailable)
}

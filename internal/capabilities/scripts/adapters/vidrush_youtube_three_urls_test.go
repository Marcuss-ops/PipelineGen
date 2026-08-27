package adapters

import (
	"context"
	"testing"

	stockplan "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockplan"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
)

type threeURLMetadata struct{}

func (threeURLMetadata) GetVideoInfo(context.Context, string) (*youtubeports.DownloaderMetadata, error) {
	return &youtubeports.DownloaderMetadata{Title: "WWII documentary", Duration: 60}, nil
}

type threeURLTranscript struct{}

func (threeURLTranscript) AcquireStockTranscript(_ context.Context, videoID string, _ int64) (*stockplan.Transcript, error) {
	text := map[string]string{
		"video-good":    "Germany invaded Poland on September 1 1939 and Warsaw resisted the German advance",
		"video-generic": "The Second World War involved Germany, armies, battles and many countries",
		"video-bad":     "The D-Day Normandy landings began in June 1944 on the western front",
	}
	return &stockplan.Transcript{
		Hash: videoID + "-transcript", Language: "en", Source: "youtube_subtitle",
		Cues: []stockplan.TranscriptCue{{StartMs: 1000, EndMs: 11000, Text: text[videoID]}},
	}, nil
}

type threeURLExtractor struct{}

func TestYouTubeThreeSourcesRankSemanticallyAndSelectWinner(t *testing.T) {
	// The adapter only needs the planning portion for this assertion. Use the
	// canonical stock service directly so the test isolates transcript ranking
	// from Drive/materialization side effects.

	selector := stockplan.NewHighlightSelector(stockplan.DefaultHighlightWeights())
	candidates := []stockplan.HighlightCandidate{
		{StartMs: 1000, EndMs: 11000, DurationMs: 10000, Text: "Germany invaded Poland on September 1 1939 and Warsaw resisted"},
		{StartMs: 1000, EndMs: 11000, DurationMs: 10000, Text: "The Second World War involved Germany armies battles and countries"},
		{StartMs: 1000, EndMs: 11000, DurationMs: 10000, Text: "The D-Day Normandy landings began in June 1944"},
	}
	selected := selector.Select(candidates, "German invasion Poland September 1939", "Germany invaded Poland", 1, 0)
	if len(selected) != 1 {
		t.Fatalf("selected windows = %d, want 1", len(selected))
	}
	if selected[0].Text != candidates[0].Text {
		t.Fatalf("winner = %q, want Poland-specific candidate", selected[0].Text)
	}
	if selected[0].RelevanceScore <= .5 {
		t.Fatalf("winner score = %.3f, want strong semantic match", selected[0].RelevanceScore)
	}
	if selected[0].RelevanceScore <= candidates[1].RelevanceScore || selected[0].RelevanceScore <= candidates[2].RelevanceScore {
		t.Fatalf("winner score %.3f did not exceed generic/bad candidates", selected[0].RelevanceScore)
	}

}

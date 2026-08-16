// Package app — adapters_extract_important.go (PR-GEMMA-EXTRACT-IMPORTANT
// follow-up, July 2026): concrete adapters for the SegmentSelectionResolver
// inline ports (TranscriptFetcherPort / AnalyzerPort).
//
// The former duplicate extract-important pipeline (SectionDownloader /
// DriveFolderCreator / DriveUploader / Hasher + per-clip commit loop) was
// RETIRED: "important" is now a selection strategy that flows through the
// SAME canonical extraction pipeline (ExtractionService → extractFanOut →
// ProcessYouTubeSegmentUseCase). Only the transcript fetcher and the
// nil-tolerant analyzer forward-pointer remain.
//
// godlike/06 one-canonical-owner-per-fact: each adapter is the SOLE owner
// of its port implementation; compile-time `var _` pins lock signature
// drift to build-failure (not runtime panic).
package app

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/transcripts"
	youtubeusecase "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
)

// ── Compile-time pins (godlike/06 lock signature drift to build-failure) ──

var (
	_ youtubeusecase.TranscriptFetcherPort = (*transcriptFetcherAdapter)(nil)
	_ youtubeusecase.AnalyzerPort          = (*failClosedAnalyzerAdapter)(nil)
)

// ── 1. TranscriptFetcherAdapter ─────────────────────────────────────

// transcriptFetcherAdapter wraps the canonical application-layer subtitle
// adapter (transcripts.SubtitleSource). Maps an in-process yt-dlp subtitle
// fetch (TranscriptDocument with Entries[].TimedEntry) to the resolver's
// Transcript shape.
type transcriptFetcherAdapter struct {
	sub transcripts.SubtitleSource
}

func (a *transcriptFetcherAdapter) FetchTranscript(ctx context.Context, videoID, language string) (*youtubeusecase.Transcript, error) {
	if a.sub == nil {
		return nil, fmt.Errorf("transcriptFetcherAdapter: subtitle adapter unwired")
	}
	videoURL := "https://www.youtube.com/watch?v=" + videoID
	doc, err := a.sub.Fetch(ctx, videoURL)
	if err != nil {
		return nil, fmt.Errorf("transcriptFetcherAdapter: fetch %s: %w", videoURL, err)
	}
	out := &youtubeusecase.Transcript{
		VideoID:  doc.VideoID,
		Language: doc.Language,
	}
	for _, e := range doc.Entries {
		out.Entries = append(out.Entries, youtubeusecase.TranscriptEntry{
			Text:     e.Text,
			StartSec: e.Start,
			EndSec:   e.End,
		})
	}
	return out, nil
}

// ── 2. FailClosedAnalyzerAdapter ─────────────────────────────────

// failClosedAnalyzerAdapter: until the LLM analyzer backend lands
// (forward-pointer), this adapter blocks the resolver's important mode
// with a typed ErrAnalyzerUnavailable sentinel. godlike/07 fail-closed
// means the resolver returns the typed error to the extraction pipeline;
// no silent 0-segments success.
type failClosedAnalyzerAdapter struct{}

func (a *failClosedAnalyzerAdapter) AnalyzeImportantSegments(ctx context.Context, transcript *youtubeusecase.Transcript, max int) ([]youtubeusecase.Segment, error) {
	return nil, fmt.Errorf("%w: analyzer backend not yet wired (forward-pointer)", youtubeusecase.ErrAnalyzerUnavailable)
}

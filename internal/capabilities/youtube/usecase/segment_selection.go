// Package usecase — segment_selection.go: canonical segment-selection
// strategy owner for the YouTube clip extraction pipeline.
//
// SegmentSelectionResolver is the SINGLE resolver behind
// ExtractRequest.Selection. It maps the two sanctioned selection modes
// onto the same canonical []dto.Segment shape:
//
//   - explicit (default / nil selection) → returns req.Segments verbatim
//   - important                            → fetches the timed transcript
//     (TranscriptFetcherPort) + invokes the LLM analyzer (AnalyzerPort)
//     to discover the important segments, then converts the discovered
//     second-bounded segments into dto.Segment timestamps.
//
// Both modes flow through ExtractionService.Extract → extractFanOut →
// ProcessYouTubeSegmentUseCase. The resolver has ZERO publishing
// responsibility (no download, no Drive, no commit): it only produces
// the canonical segment list. This retires the former duplicate
// extract-important pipeline (PR-GEMMA-EXTRACT-IMPORTANT), whose
// per-segment download/upload/hash/commit loop was a second ingest
// system parallel to ProcessYouTubeSegmentUseCase.
//
// godlike/06 inline ports (TranscriptFetcherPort / AnalyzerPort) are the
// forward-pointer FASE-X shapes previously declared by the retired
// extract-important pipeline; they stay here until the future mechanical
// port-move consolidates them into internal/capabilities/youtube/ports/ports.go.
package usecase

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// ── Typed sentinels (godlike/07) ──────────────────────────────────────

var (
	// ErrSubtitleUnavailable is surfaced when the timed-transcript fetch
	// fails or the subtitle source is unwired.
	ErrSubtitleUnavailable = errors.New("segment selection: subtitle fetcher unavailable")
	// ErrAnalyzerUnavailable is surfaced when the LLM analyzer is nil
	// (forward-pointer) or the analyzer call fails.
	ErrAnalyzerUnavailable = errors.New("segment selection: analyzer unavailable")
	// ErrNoSegments is surfaced when the analyzer returns zero segments.
	ErrNoSegments = errors.New("segment selection: no segments identified")
)

// ── Inline ports (godlike/06 FASE-X forward-pointer) ──────────────────

// TranscriptFetcherPort fetches the timed transcript for a video.
type TranscriptFetcherPort interface {
	FetchTranscript(ctx context.Context, videoID, language string) (*Transcript, error)
}

// AnalyzerPort discovers important segments from a transcript. It is
// NIL-TOLERANT at the resolver (analyzer is a forward-pointer; a nil
// analyzer fails closed with ErrAnalyzerUnavailable).
type AnalyzerPort interface {
	AnalyzeImportantSegments(ctx context.Context, transcript *Transcript, max int) ([]Segment, error)
}

// ── Domain DTOs (godlike/06 one canonical shape per type) ──────────────

// Transcript is the timed transcript handed to the analyzer.
type Transcript struct {
	VideoID  string
	Language string
	Entries  []TranscriptEntry
}

// TranscriptEntry is one timed transcript line.
type TranscriptEntry struct {
	Text     string
	StartSec float64
	EndSec   float64
}

// Segment is one LLM-discovered important segment (second-bounded).
type Segment struct {
	StartSec    float64
	EndSec      float64
	Description string
}

// SegmentSelectionResolver resolves the []dto.Segment for an
// ExtractRequest's selection mode. It owns NO publishing behaviour:
// the resolved segments are returned to the canonical extraction
// pipeline unchanged.
type SegmentSelectionResolver struct {
	log       *zap.Logger
	subtitles TranscriptFetcherPort
	analyzer  AnalyzerPort
}

// NewSegmentSelectionResolver constructs the resolver. subtitles is
// required (panic fail-closed at composition); analyzer is nil-tolerant
// (forward-pointer — a nil analyzer fails closed at resolve time with
// ErrAnalyzerUnavailable, never a silent empty result).
func NewSegmentSelectionResolver(log *zap.Logger, subtitles TranscriptFetcherPort, analyzer AnalyzerPort) *SegmentSelectionResolver {
	if subtitles == nil {
		panic("SegmentSelectionResolver.New: subtitle fetcher is required (composition must wire the timed-transcript source)")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &SegmentSelectionResolver{log: log, subtitles: subtitles, analyzer: analyzer}
}

// Resolve returns the []dto.Segment for the request's selection mode.
// Explicit mode (or a nil Selection) returns req.Segments verbatim.
// Unknown modes fail closed.
func (r *SegmentSelectionResolver) Resolve(ctx context.Context, req *youtubetypes.ExtractRequest) ([]youtubetypes.Segment, error) {
	if req == nil || req.Selection == nil || req.Selection.Mode == "" ||
		req.Selection.Mode == string(youtubetypes.SegmentSelectionModeExplicit) {
		return req.Segments, nil
	}
	if req.Selection.Mode != string(youtubetypes.SegmentSelectionModeImportant) {
		return nil, fmt.Errorf("segment selection: unknown mode %q (want %q or %q)",
			req.Selection.Mode,
			youtubetypes.SegmentSelectionModeExplicit,
			youtubetypes.SegmentSelectionModeImportant)
	}
	return r.ResolveImportant(ctx, req)
}

// ResolveImportant runs the transcript + LLM analyzer pipeline and
// returns the discovered segments as []dto.Segment (timestamps
// formatted via the canonical FormatSecondsToTimestamp helper).
func (r *SegmentSelectionResolver) ResolveImportant(ctx context.Context, req *youtubetypes.ExtractRequest) ([]youtubetypes.Segment, error) {
	if r.subtitles == nil {
		return nil, fmt.Errorf("%w: subtitle fetcher not wired", ErrSubtitleUnavailable)
	}
	videoID, err := urlutil.ExtractVideoID(req.URL)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid url: %v", ErrSubtitleUnavailable, err)
	}

	lang := req.Selection.Language
	if lang == "" {
		lang = "und"
	}
	transcript, err := r.subtitles.FetchTranscript(ctx, videoID, lang)
	if err != nil || transcript == nil {
		return nil, fmt.Errorf("%w: video_id=%s lang=%s: %v", ErrSubtitleUnavailable, videoID, lang, err)
	}

	if r.analyzer == nil {
		return nil, fmt.Errorf("%w: no LLM analyzer wired (analyzer is forward-pointer)", ErrAnalyzerUnavailable)
	}
	maxSegments := req.Selection.MaxSegments
	if maxSegments <= 0 {
		maxSegments = 5
	}
	segments, err := r.analyzer.AnalyzeImportantSegments(ctx, transcript, maxSegments)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAnalyzerUnavailable, err)
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("%w: video_id=%s max=%d", ErrNoSegments, videoID, maxSegments)
	}

	out := make([]youtubetypes.Segment, 0, len(segments))
	for _, s := range segments {
		out = append(out, youtubetypes.Segment{
			Start: textutil.FormatSecondsToTimestamp(int(s.StartSec)),
			End:   textutil.FormatSecondsToTimestamp(int(s.EndSec)),
			Name:  s.Description,
		})
	}
	return out, nil
}

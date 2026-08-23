// Package usecase — segment_selection_test.go: pins the canonical
// explicit|important segment-selection resolver contract behind
// POST /api/clips/process selection.mode.
//
// The resolver owns NO publishing behaviour: it only maps the request's
// selection mode onto the canonical []dto.Segment shape that then flows
// through the same extraction pipeline. These tests pin:
//   - explicit (nil / "explicit") returns req.Segments verbatim
//   - important runs transcript + analyzer and formats timestamps
//   - unknown modes fail closed
//   - nil analyzer fails closed with ErrAnalyzerUnavailable (godlike/07)
//   - zero analyzer segments fails closed with ErrNoSegments
package usecase

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

type stubSegmentTranscriptFetcher struct {
	transcript *Transcript
	err        error
	calls      int
}

func (f *stubSegmentTranscriptFetcher) FetchTranscript(_ context.Context, videoID, language string) (*Transcript, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.transcript, nil
}

type stubSegmentAnalyzer struct {
	segments []Segment
	err      error
	calls    int
}

func (a *stubSegmentAnalyzer) AnalyzeImportantSegments(_ context.Context, _ *Transcript, _ int) ([]Segment, error) {
	a.calls++
	if a.err != nil {
		return nil, a.err
	}
	return a.segments, nil
}

func TestSegmentSelectionResolver_ExplicitReturnsSegmentsVerbatim(t *testing.T) {
	resolver := NewSegmentSelectionResolver(zap.NewNop(), &stubSegmentTranscriptFetcher{}, nil)
	req := &youtubetypes.ExtractRequest{
		URL:      "https://www.youtube.com/watch?v=abc123",
		Segments: []youtubetypes.Segment{{Start: "00:00:10", End: "00:00:20", Name: "a"}},
	}
	got, err := resolver.Resolve(context.Background(), req)
	if err != nil {
		t.Fatalf("Resolve explicit: %v", err)
	}
	if len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("explicit must return req.Segments verbatim, got %+v", got)
	}
}

func TestSegmentSelectionResolver_ImportantResolvesSegments(t *testing.T) {
	sub := &stubSegmentTranscriptFetcher{transcript: &Transcript{VideoID: "abc123", Language: "und"}}
	an := &stubSegmentAnalyzer{segments: []Segment{{StartSec: 10, EndSec: 25, Description: "important moment"}}}
	resolver := NewSegmentSelectionResolver(zap.NewNop(), sub, an)
	req := &youtubetypes.ExtractRequest{
		URL:       "https://www.youtube.com/watch?v=abc123",
		Selection: &youtubetypes.SegmentSelection{Mode: "important"},
	}
	got, err := resolver.Resolve(context.Background(), req)
	if err != nil {
		t.Fatalf("Resolve important: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 segment, got %d", len(got))
	}
	if got[0].Start != "00:00:10" || got[0].End != "00:00:25" || got[0].Name != "important moment" {
		t.Fatalf("unexpected segment: %+v", got[0])
	}
	if sub.calls != 1 || an.calls != 1 {
		t.Fatalf("expected 1 fetch + 1 analyze, got %d/%d", sub.calls, an.calls)
	}
}

func TestSegmentSelectionResolver_UnknownModeFailsClosed(t *testing.T) {
	resolver := NewSegmentSelectionResolver(zap.NewNop(), &stubSegmentTranscriptFetcher{}, nil)
	req := &youtubetypes.ExtractRequest{
		URL:       "https://www.youtube.com/watch?v=abc123",
		Selection: &youtubetypes.SegmentSelection{Mode: "banana"},
	}
	if _, err := resolver.Resolve(context.Background(), req); err == nil {
		t.Fatal("unknown mode must fail closed")
	}
}

func TestSegmentSelectionResolver_NilAnalyzerFailsClosed(t *testing.T) {
	sub := &stubSegmentTranscriptFetcher{transcript: &Transcript{VideoID: "abc123", Language: "und"}}
	resolver := NewSegmentSelectionResolver(zap.NewNop(), sub, nil)
	req := &youtubetypes.ExtractRequest{
		URL:       "https://www.youtube.com/watch?v=abc123",
		Selection: &youtubetypes.SegmentSelection{Mode: "important"},
	}
	if _, err := resolver.Resolve(context.Background(), req); !errors.Is(err, ErrAnalyzerUnavailable) {
		t.Fatalf("want ErrAnalyzerUnavailable, got %v", err)
	}
}

func TestSegmentSelectionResolver_NoSegmentsFailsClosed(t *testing.T) {
	sub := &stubSegmentTranscriptFetcher{transcript: &Transcript{VideoID: "abc123", Language: "und"}}
	an := &stubSegmentAnalyzer{segments: nil}
	resolver := NewSegmentSelectionResolver(zap.NewNop(), sub, an)
	req := &youtubetypes.ExtractRequest{
		URL:       "https://www.youtube.com/watch?v=abc123",
		Selection: &youtubetypes.SegmentSelection{Mode: "important"},
	}
	if _, err := resolver.Resolve(context.Background(), req); !errors.Is(err, ErrNoSegments) {
		t.Fatalf("want ErrNoSegments, got %v", err)
	}
}

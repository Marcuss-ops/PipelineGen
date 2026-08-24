package monitor

import (
	"context"
	"errors"
	"strings"
	"testing"

	channels "github.com/Marcuss-ops/PipelineGen/internal/capabilities/channels"
	ytdomain "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	"go.uber.org/zap"
)

// monitor_analyzer_test.go — Step 6 (June 2026) one-shot AI-gate tests.
//
// analyzeVideo now uses the one-shot Fetch + AnalyzeFull flow (legacy
// GetTranscript + Score + Classify + FindSegments removed). The 5 cases
// below pin the new contract:
//   - transcript-error short-circuit (before AnalyzeFull)
//   - score-threshold soft skip (AnalyzeFull returns empty segments)
//   - score-above-threshold continuation (AnalyzeFull called, but
//     returns empty segments → soft skip)
//   - empty-segments soft skip
//   - successful segments return with category prefix
//
// Test shape: construct ChannelMonitor with stub ports, configure
// stubs, drive analyzeVideo, assert return values + call counts.

// channelFixture returns a minimal test channel.
func channelFixture() channels.Channel {
	return channels.Channel{
		ID:            "ch-1",
		ChannelURL:    "https://www.youtube.com/@Test",
		Category:      "test-cat",
		DriveFolderID: "test-folder",
	}
}

func videoFixture() VideoInfo {
	return VideoInfo{ID: "vid-1", Title: "Test Title"}
}

// TestAnalyzeVideo_TranscriptErrorReturnsFailure covers the
// "transcript failure" → empty Analysis + wrappedErr path. Fetch
// returns a non-nil err; analyzeVideo must short-circuit BEFORE
// calling AnalyzeFull.
func TestAnalyzeVideo_TranscriptErrorReturnsFailure(t *testing.T) {
	transcriptErr := errors.New("subtitles unavailable (yt-dlp returned 0 subtitles)")
	stubT := &stubTranscriptProvider{
		transcript: "",
		err:        transcriptErr,
	}
	stubA := &stubVideoAnalyzer{}
	m := &ChannelMonitor{
		log:        zap.NewNop(),
		transcript: stubT,
		analyzer:   stubA,
	}
	ch := channelFixture()
	ch.MinSemanticScore = 60
	info := videoFixture()

	a, err := m.analyzeVideo(context.Background(), info, ch, []string{"kw"})
	if err == nil {
		t.Fatal("expected non-nil err on transcript failure")
	}
	if !errors.Is(err, transcriptErr) {
		t.Errorf("err = %v, want wrap of %v", err, transcriptErr)
	}
	if got, want := err.Error(), "vid-1"; !strings.Contains(got, want) {
		t.Errorf("err should mention videoID=vid-1 for log triage; got %q", got)
	}
	if len(a.Segments) != 0 {
		t.Errorf("expected zero segments on transcript failure, got %d", len(a.Segments))
	}
	if stubA.analyzeFullCalls != 0 {
		t.Errorf("AnalyzeFull should not be called when Fetch fails (short-circuit), got %d calls", stubA.analyzeFullCalls)
	}
	if stubT.fetchCalls != 1 {
		t.Errorf("Fetch should be called once, got %d", stubT.fetchCalls)
	}
}

// TestAnalyzeVideo_ScoreBelowThresholdSoftSkips covers the
// "score below threshold" → soft skip path. Fetch succeeds;
// AnalyzeFull returns Analysis{Segments:nil} (one-shot determined
// no actionable segments).
func TestAnalyzeVideo_ScoreBelowThresholdSoftSkips(t *testing.T) {
	stubT := &stubTranscriptProvider{transcript: "valid transcript with some content"}
	stubA := &stubVideoAnalyzer{
		analysis: Analysis{Score: 40}, // low score, no segments
	}
	m := &ChannelMonitor{
		log:        zap.NewNop(),
		transcript: stubT,
		analyzer:   stubA,
	}
	ch := channelFixture()
	ch.MinSemanticScore = 70
	info := videoFixture()

	a, err := m.analyzeVideo(context.Background(), info, ch, []string{"kw"})
	if err != nil {
		t.Fatalf("expected nil err on soft skip (no segments), got %v", err)
	}
	if len(a.Segments) != 0 {
		t.Errorf("expected zero segments when AnalyzeFull returns empty, got %d", len(a.Segments))
	}
	// Soft-skip returns the zero-valued Analysis{}
	if a.Score != 0 {
		t.Errorf("Score = %d on soft-skip, want 0 (soft-skip returns Analysis{}, not populated)", a.Score)
	}
	if stubT.fetchCalls != 1 {
		t.Errorf("Fetch should be called once, got %d", stubT.fetchCalls)
	}
	if stubA.analyzeFullCalls != 1 {
		t.Errorf("AnalyzeFull should be called once, got %d", stubA.analyzeFullCalls)
	}
}

// TestAnalyzeVideo_ScoreAboveThresholdButNoSegments covers the
// "high score but empty segments" → soft skip path. AnalyzeFull
// is called (proving the Fetch gate passed) but returns no
// segments → soft skip.
func TestAnalyzeVideo_ScoreAboveThresholdButNoSegments(t *testing.T) {
	stubT := &stubTranscriptProvider{transcript: "valid transcript"}
	stubA := &stubVideoAnalyzer{
		analysis: Analysis{Score: 85, Category: "test-cat"}, // good score, no segments
	}
	m := &ChannelMonitor{
		log:        zap.NewNop(),
		transcript: stubT,
		analyzer:   stubA,
	}
	ch := channelFixture()
	ch.MinSemanticScore = 70
	info := videoFixture()

	a, err := m.analyzeVideo(context.Background(), info, ch, []string{"kw"})
	if err != nil {
		t.Fatalf("expected nil err on soft-skip (no segments), got %v", err)
	}
	if len(a.Segments) != 0 {
		t.Errorf("expected zero segments when AnalyzeFull returns empty, got %d", len(a.Segments))
	}
	if stubA.analyzeFullCalls != 1 {
		t.Errorf("AnalyzeFull should be called once, got %d", stubA.analyzeFullCalls)
	}
}

// TestAnalyzeVideo_SegmentsEmptySoftSkips covers the "no semantic
// keywords + empty segments" → soft skip path.
func TestAnalyzeVideo_SegmentsEmptySoftSkips(t *testing.T) {
	stubT := &stubTranscriptProvider{transcript: "valid transcript"}
	stubA := &stubVideoAnalyzer{
		analysis: Analysis{Category: "test-cat"}, // no segments
	}
	m := &ChannelMonitor{
		log:        zap.NewNop(),
		transcript: stubT,
		analyzer:   stubA,
	}
	ch := channelFixture()
	info := videoFixture()

	a, err := m.analyzeVideo(context.Background(), info, ch, nil) // no semanticKeywords
	if err != nil {
		t.Fatalf("expected nil err on segments-empty soft skip, got %v", err)
	}
	if len(a.Segments) != 0 {
		t.Errorf("expected zero segments when AnalyzeFull returns empty, got %d", len(a.Segments))
	}
	if stubA.analyzeFullCalls != 1 {
		t.Errorf("AnalyzeFull should be called once, got %d", stubA.analyzeFullCalls)
	}
}

// TestAnalyzeVideo_SegmentsReturned covers the spec's "segments
// returned" → success path. AnalyzeFull returns 2 segments →
// analyzeVideo returns a fully-populated Analysis with category
// prefix applied to segment names.
func TestAnalyzeVideo_SegmentsReturned(t *testing.T) {
	stubT := &stubTranscriptProvider{transcript: "valid transcript with rich content"}
	stubA := &stubVideoAnalyzer{
		analysis: Analysis{
			Category: "Comedy",
			Segments: []ytdomain.Segment{
				{Start: "00:01", End: "00:30", Name: "First Moment"},
				{Start: "00:30", End: "01:00", Name: "Second Moment"},
			},
		},
	}
	m := &ChannelMonitor{
		log:        zap.NewNop(),
		transcript: stubT,
		analyzer:   stubA,
	}
	ch := channelFixture()
	ch.Category = "Comedy"
	info := videoFixture()

	a, err := m.analyzeVideo(context.Background(), info, ch, nil)
	if err != nil {
		t.Fatalf("expected nil err on success, got %v", err)
	}
	if len(a.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(a.Segments))
	}
	if a.Category != "Comedy" {
		t.Errorf("Category = %q, want Comedy", a.Category)
	}
	if got, want := a.Segments[0].Name, "Comedy First Moment"; got != want {
		t.Errorf("Segment 0 Name = %q, want %q (Category prefix)", got, want)
	}
	if got, want := a.Segments[1].Name, "Comedy Second Moment"; got != want {
		t.Errorf("Segment 1 Name = %q, want %q (Category prefix)", got, want)
	}
	if stubT.fetchCalls != 1 {
		t.Errorf("Fetch should be called once, got %d", stubT.fetchCalls)
	}
	if stubA.analyzeFullCalls != 1 {
		t.Errorf("AnalyzeFull should be called once, got %d", stubA.analyzeFullCalls)
	}
}

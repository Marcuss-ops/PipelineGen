package monitor

import (
	"context"
	"errors"
	"strings"
	"testing"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	ytdomain "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"go.uber.org/zap"
)

// monitor_analyzer_test.go — Step 9 (June 2026) per-video AI-gate tests.
//
// analyzeVideo is the per-video orchestrator (analyzer.go). The 5 cases
// below pin the existing gate contract around TranscriptProvider +
// VideoAnalyzer ports: transcript-error short-circuit, score-threshold
// gate, score-above-threshold continuation, empty-segments soft skip,
// successful segments return.
//
// The shape of the tests is uniform:
//   1. Construct ChannelMonitor with stub ports.
//   2. Configure stubs to drive a single concrete gate decision.
//   3. Drive analyzeVideo.
//   4. Assert: (Analysis, error) return value + per-stub call counts.
//
// The per-stub call counters (scoreCalls, classifyCalls, findSegmentsCalls)
// pin the call-ordering invariant: a transcript failure must short-
// circuit BEFORE Score is called, a low Score must skip BEFORE
// Classify/FindSegments, an above-threshold Score must continue past
// the gate AT LEAST through FindSegments.

// channelFixture is a small helper to keep the channel-construction
// noise out of the per-test bodies. Pre-sets the channel shape that
// drives analyzeVideo (Category + DriveFolderID set → Classify is
// skipped; tests that need Classify to be called clear these fields
// explicitly).
func channelFixture() channels.Channel {
	return channels.Channel{
		ID:            "ch-1",
		ChannelURL:    "https://www.youtube.com/@Test",
		Category:      "test-cat",
		DriveFolderID: "test-folder",
	}
}

func videoFixture() downloader.VideoInfo {
	return downloader.VideoInfo{ID: "vid-1", Title: "Test Title"}
}

// TestAnalyzeVideo_TranscriptErrorReturnsFailure covers the spec's
// "transcript failure" → empty Analysis + wrappedErr path. The
// TranscriptProvider returns a non-nil err; analyzeVideo must
// short-circuit (Analysis{}, wrappedErr) BEFORE invoking Score +
// Classify + FindSegments.
//
// Implementation notes:
//   - semanticKeywords is non-empty so the test would otherwise
//     exercise the Score gate; the transcript failure must happen
//     BEFORE Score.
//   - The wrapped error must mention videoID for operator triage in
//     logs.
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
	// Channel with Threshold set so a misordering (Score after
	// transcript failure) would be detectable via scoreCalls.
	ch.MinSemanticScore = 60
	info := videoFixture()

	semanticKeywords := []string{"kw"}

	a, err := m.analyzeVideo(context.Background(), info, ch, semanticKeywords)
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
	if stubA.scoreCalls != 0 {
		t.Errorf("Score should not be called when transcript fails (short-circuit), got %d calls", stubA.scoreCalls)
	}
	if stubA.findSegmentsCalls != 0 {
		t.Errorf("FindSegments should not be called when transcript fails, got %d calls", stubA.findSegmentsCalls)
	}
	if stubT.getTranscriptCalls != 1 {
		t.Errorf("GetTranscript should be called once, got %d", stubT.getTranscriptCalls)
	}
}

// TestAnalyzeVideo_ScoreBelowThresholdSoftSkips covers the spec's
// "score below threshold" → soft skip path. Transcript succeeds, Score
// returns 40 against a 70-channel threshold → analyzeVideo returns
// (Analysis{Segments:nil}, nil) WITHOUT calling Classify or
// FindSegments.
//
// Per analyzer.go contract: the soft-skip path returns a zero-valued
// Analysis{} — Score + MatchedKeyword + Category are NOT propagated
// through (callers only inspect Segments). The asserted Score == 0
// pins the documented zero-value contract — a future change that
// populates Analysis on soft-skip would silently change the
// "no usable segments" downstream contract for processVideo and
// enqueueFromAnalysis.
func TestAnalyzeVideo_ScoreBelowThresholdSoftSkips(t *testing.T) {
	stubT := &stubTranscriptProvider{transcript: "valid transcript with some content"}
	stubA := &stubVideoAnalyzer{
		score:        40,
		scoreKeyword: "kw-noise",
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
		t.Fatalf("expected nil err on score-threshold soft skip, got %v", err)
	}
	if len(a.Segments) != 0 {
		t.Errorf("expected zero segments on score-threshold soft skip, got %d", len(a.Segments))
	}
	// Soft-skip returns the zero-valued Analysis{} — Score is NOT
	// echoed back from the stub on this path.
	if a.Score != 0 {
		t.Errorf("Score = %d on soft-skip, want 0 (soft-skip returns Analysis{}, not populated)", a.Score)
	}
	if stubT.getTranscriptCalls != 1 {
		t.Errorf("GetTranscript should be called once, got %d", stubT.getTranscriptCalls)
	}
	if stubA.scoreCalls != 1 {
		t.Errorf("Score should be called once, got %d", stubA.scoreCalls)
	}
	if stubA.classifyCalls != 0 {
		t.Errorf("Classify should NOT be called on score-threshold soft skip, got %d calls", stubA.classifyCalls)
	}
	if stubA.findSegmentsCalls != 0 {
		t.Errorf("FindSegments should NOT be called on score-threshold soft skip, got %d calls", stubA.findSegmentsCalls)
	}
}

// TestAnalyzeVideo_ScoreAboveThresholdContinuesToFindSegments covers
// the spec's "score above threshold" → continue downstream path. The
// score gate is passed (85 > 70); Classify is invoked (channel has no
// pre-bound Category + DriveFolderID, so the LLM-driven path is taken);
// FindSegments is invoked (and returns empty in this test). The final
// soft-skip comes from FindSegments returning empty, NOT from the
// score gate.
//
// The discriminating assertion is findSegmentsCalls == 1: this proves
// the score gate is NOT short-circuiting.
func TestAnalyzeVideo_ScoreAboveThresholdContinuesToFindSegments(t *testing.T) {
	stubT := &stubTranscriptProvider{transcript: "valid transcript"}
	stubA := &stubVideoAnalyzer{
		score:        85,
		scoreKeyword: "kw-strong",
		segments:     nil, // empty segments → soft skip at the end
	}
	m := &ChannelMonitor{
		log:        zap.NewNop(),
		transcript: stubT,
		analyzer:   stubA,
	}
	ch := channelFixture()
	ch.Category = ""      // empty Category → Classify will be invoked
	ch.DriveFolderID = "" // empty DriveFolderID → Classify will be invoked
	ch.MinSemanticScore = 70
	info := videoFixture()

	a, err := m.analyzeVideo(context.Background(), info, ch, []string{"kw"})
	if err != nil {
		t.Fatalf("expected nil err on soft-skip-after-FindSegments-empty, got %v", err)
	}
	if len(a.Segments) != 0 {
		t.Errorf("expected zero segments when FindSegments returns empty, got %d", len(a.Segments))
	}
	if stubA.scoreCalls != 1 {
		t.Errorf("Score should be called once, got %d", stubA.scoreCalls)
	}
	if stubA.classifyCalls != 1 {
		t.Errorf("Classify should be called once (score passed gate), got %d", stubA.classifyCalls)
	}
	if stubA.findSegmentsCalls != 1 {
		t.Errorf("FindSegments should be called once (score passed gate), got %d calls", stubA.findSegmentsCalls)
	}
}

// TestAnalyzeVideo_SegmentsEmptySoftSkips covers the spec's "segments
// empty" → soft skip path. No semantic keywords (Score gate is
// skipped), no pre-bound Category/DriveFolderID (Classify IS called
// because the LLM-driven path is taken), FindSegments returns nil.
//
// The final result is a (Analysis{Segments:nil}, nil) — soft skip at
// the END of analyzeVideo (after all gates but no usable segments).
func TestAnalyzeVideo_SegmentsEmptySoftSkips(t *testing.T) {
	stubT := &stubTranscriptProvider{transcript: "valid transcript"}
	stubA := &stubVideoAnalyzer{
		segments: nil, // FindSegments returns nil → soft skip
	}
	m := &ChannelMonitor{
		log:        zap.NewNop(),
		transcript: stubT,
		analyzer:   stubA,
	}
	ch := channelFixture()
	// Force Classify to be invoked by clearing pre-bound fields.
	ch.Category = ""
	ch.DriveFolderID = ""
	info := videoFixture()

	a, err := m.analyzeVideo(context.Background(), info, ch, nil) // no semanticKeywords
	if err != nil {
		t.Fatalf("expected nil err on segments-empty soft skip, got %v", err)
	}
	if len(a.Segments) != 0 {
		t.Errorf("expected zero segments when FindSegments returns nil, got %d", len(a.Segments))
	}
	if stubA.scoreCalls != 0 {
		t.Errorf("Score should NOT be called when no semanticKeywords are set, got %d calls", stubA.scoreCalls)
	}
	if stubA.findSegmentsCalls != 1 {
		t.Errorf("FindSegments should be called once, got %d", stubA.findSegmentsCalls)
	}
}

// TestAnalyzeVideo_SegmentsReturned covers the spec's "segments
// returned" → success path. FindSegments returns 2 segments →
// analyzeVideo returns a fully-populated Analysis.
//
// The Category prefix assertion is the canonical Step 9 behavior:
// analyzer.go iterates over the returned segments and prefixes the
// Category to the segment Name so downstream rendering shows
// "Comedy First Moment" instead of bare "First Moment".
func TestAnalyzeVideo_SegmentsReturned(t *testing.T) {
	stubT := &stubTranscriptProvider{transcript: "valid transcript with rich content"}
	segs := []ytdomain.Segment{
		{Start: "00:01", End: "00:30", Name: "First Moment"},
		{Start: "00:30", End: "01:00", Name: "Second Moment"},
	}
	stubA := &stubVideoAnalyzer{segments: segs}
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
	if stubT.getTranscriptCalls != 1 {
		t.Errorf("GetTranscript should be called once, got %d", stubT.getTranscriptCalls)
	}
	if stubA.findSegmentsCalls != 1 {
		t.Errorf("FindSegments should be called once, got %d", stubA.findSegmentsCalls)
	}
}

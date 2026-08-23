// Package scripts \u2014 clip_sampler_impl_test.go pins the FASE-7/8
// single defaultClipSampler contract. Five golden tests cover
// the move-only surface: dedup+limit, min_score floor, coverage
// pass, coverage fail with nil result (move-only parity), and
// limit-zero fail-closed (godlike/07 NO-FAKE-AVAILABILITY).
//
// FASE-8 enrichment: each candidate fixture satisfies all 10
// gates (richTranscript/VisualSummary/MediaType/DurationMs/
// AnchorCoverageRatio/DriveLink/Embedding). The Slot context is
// the same across tests (Slot.Topic="test-topic" token-overlaps
// with every candidate Transcript).
package usecase

import (
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// richSlot is the canonical Slot context used by every FASE-8
// test fixture below. Topic="test-topic" tokenises to a single
// 10-char token ">3-char" which exactly matches the substring
// "test-topic" embedded in every richCandidate's Transcript.
//
// TargetDurationMs=6000 keeps DurationMs=6000 inside
// [Floor=4800, Ceiling=12000] for the duration gate.
func richSlot() scriptpkg.ClipSearchSlot {
	return scriptpkg.ClipSearchSlot{
		Ref:              "slot-test",
		Topic:            "test-topic",
		TargetDurationMs: 6000,
	}
}

// richCandidate returns a candidate that satisfies all 10
// gates given richSlot() context. Each test may mutate
// individual fields (Score, Transcript, Embedding, ...) to
// exercise dedup / min-score / coverage paths.
//
// Defaults chosen to pass: Score=0.95 (>= MinQualityScore
// 0.50), Transcript has 12 words including the slot topic,
// VisualSummary has 14 words, MediaType="video", DurationMs
// = target, AnchorCoverageRatio = 0.80 (>= MinAnchorCoverage
// 0.50), DriveLink set, Embedding identity-different across
// distinct candidates to keep diversity trivially passing.
func richCandidate(id string, score float64) ports.ClipSamplerCandidate {
	return ports.ClipSamplerCandidate{
		ClipID:              id,
		Name:                id + "-name",
		Score:               score,
		Source:              "semantic",
		Transcript:          "test-topic content body for " + id + " with sufficient word count and references.",
		VisualSummary:       "The test-topic scene rendering for " + id + " with sufficient visual rune length.",
		MediaType:           "video",
		DurationMs:          6000,
		AnchorCoverageRatio: 0.80,
		DriveLink:           "https://drive.google.com/file/d/" + id + "/view",
		Embedding:           []float32{0.0, 1.0, 0.0},
	}
}

func TestSamplerImpl_BasicLimitAndDedup(t *testing.T) {
	sampler := NewDefaultClipSampler()
	req := ports.ClipSamplerRequest{
		SlotRef:       "slot-test",
		Slot:          richSlot(),
		Limit:         3,
		MinCoverage:   0,
		SourceType:    scriptpkg.SourceSearch,
		CallingSource: ClipSamplerCallerSearch,
	}
	// 5 distinct IDs + a duplicate of the first, so dedup + limit-3
	// yields exactly the first 3 in caller order.
	candidates := []ports.ClipSamplerCandidate{
		richCandidate("clip-1", 0.9),
		richCandidate("clip-2", 0.8),
		richCandidate("clip-1", 0.7), // dup
		richCandidate("clip-3", 0.6),
		richCandidate("clip-4", 0.5),
	}
	res, err := sampler.Select(req, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := res.ClipIDs, []string{"clip-1", "clip-2", "clip-3"}; !equalStrings(got, want) {
		t.Fatalf("ClipIDs: want %v, got %v", want, got)
	}
	if len(res.SearchItems) != 3 {
		t.Fatalf("SearchItems: want 3, got %d", len(res.SearchItems))
	}
	if res.SearchItems[0].ClipID != "clip-1" || res.SearchItems[0].Source != "semantic" {
		t.Errorf("first SearchItem: want {clip-1, semantic}, got %+v", res.SearchItems[0])
	}
}

func TestSamplerImpl_MinScoreFilters(t *testing.T) {
	sampler := NewDefaultClipSampler()
	req := ports.ClipSamplerRequest{
		SlotRef:       "slot-test",
		Slot:          richSlot(),
		Limit:         10,
		MinScore:      0.5,
		SourceType:    scriptpkg.SourceSearch,
		CallingSource: ClipSamplerCallerSearch,
	}
	high := richCandidate("high", 0.95)
	high.Embedding = []float32{1.0, 0.0, 0.0}
	mid := richCandidate("mid", 0.55)
	mid.Embedding = []float32{0.0, 1.0, 0.0}
	candidates := []ports.ClipSamplerCandidate{
		high,
		richCandidate("lo-1", 0.10), // dropped by MinScore pre-gate
		richCandidate("lo-2", 0.30), // dropped by MinScore pre-gate
		mid,
	}
	res, err := sampler.Select(req, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := res.ClipIDs, []string{"high", "mid"}; !equalStrings(got, want) {
		t.Fatalf("ClipIDs: want %v, got %v", want, got)
	}
}

func TestSamplerImpl_CoveragePass(t *testing.T) {
	sampler := NewDefaultClipSampler()
	req := ports.ClipSamplerRequest{
		SlotRef:       "slot-test",
		Slot:          richSlot(),
		Limit:         4,
		MinCoverage:   0.5,
		SourceType:    scriptpkg.SourceSearch,
		CallingSource: ClipSamplerCallerSearch,
	}
	candidates := []ports.ClipSamplerCandidate{
		richCandidate("a", 0.95),
		richCandidate("b", 0.90),
		richCandidate("c", 0.85),
	}
	res, err := sampler.Select(req, candidates)
	if err != nil {
		t.Fatalf("expected nil error (coverage 3/4=0.75 \u2265 0.5): %v", err)
	}
	if len(res.ClipIDs) != 3 {
		t.Errorf("expected 3 IDs, got %v", res.ClipIDs)
	}
}

func TestSamplerImpl_CoverageFailReturnsNilResult(t *testing.T) {
	// FASE-7 review-fix: coverage-fail returns (nil result, err) for
	// move-only parity with the original resolver behaviour.
	sampler := NewDefaultClipSampler()
	req := ports.ClipSamplerRequest{
		SlotRef:       "slot-test",
		Slot:          richSlot(),
		Limit:         10,
		MinCoverage:   0.5,
		SourceType:    scriptpkg.SourceSearch,
		CallingSource: ClipSamplerCallerSearch,
	}
	candidates := []ports.ClipSamplerCandidate{
		richCandidate("only-one", 0.95),
	}
	res, err := sampler.Select(req, candidates)
	if err == nil {
		t.Fatalf("expected coverage error (1/10=0.1 < 0.5)")
	}
	// Nil-result contract: callers see a Clipless result envelope
	// and decide what to do.
	if len(res.ClipIDs) != 0 || len(res.SearchItems) != 0 {
		t.Fatalf("expected nil result on coverage failure, got %+v", res)
	}
	var srcErr *scriptpkg.SourceResolutionError
	if !errors.As(err, &srcErr) {
		t.Fatalf("expected *scriptpkg.SourceResolutionError, got %T", err)
	}
	if srcErr.ResultCount != 1 {
		t.Errorf("ResultCount should reflect partial selection state (1), got %d", srcErr.ResultCount)
	}
	if !strings.Contains(srcErr.Inner.Error(), "coverage") {
		t.Errorf("Inner error should mention coverage, got %q", srcErr.Inner.Error())
	}
}

func TestSamplerImpl_LimitZeroFailsClosed(t *testing.T) {
	// godlike/07: Limit <= 0 returns a typed SourceResolutionError
	// rather than a degraded no-op.
	sampler := NewDefaultClipSampler()
	req := ports.ClipSamplerRequest{
		Limit:         0,
		SourceType:    scriptpkg.SourceSearch,
		CallingSource: ClipSamplerCallerSearch,
	}
	_, err := sampler.Select(req, []ports.ClipSamplerCandidate{{ClipID: "x"}})
	if err == nil {
		t.Fatal("expected typed error on Limit=0 (fail-closed; godlike/07)")
	}
	var srcErr *scriptpkg.SourceResolutionError
	if !errors.As(err, &srcErr) {
		t.Fatalf("expected *scriptpkg.SourceResolutionError, got %T: %v", err, err)
	}
	if !strings.Contains(srcErr.Inner.Error(), "limit must be > 0") {
		t.Errorf("Inner error should explain limit guard, got %q", srcErr.Inner.Error())
	}
}

// equalStrings is a small helper; the standard slices.Equal path
// would also work but this keeps the test dependency-light.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

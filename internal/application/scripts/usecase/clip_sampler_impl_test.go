// Package scripts \u2014 clip_sampler_impl_test.go pins the FASE-7
// single defaultClipSampler contract. Five golden tests cover
// the move-only surface: dedup+limit, min_score floor, coverage
// pass, coverage fail with nil result (move-only parity), and
// limit-zero fail-closed (godlike/07 NO-FAKE-AVAILABILITY).
package usecase

import (
	"errors"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
)

func TestSamplerImpl_BasicLimitAndDedup(t *testing.T) {
	sampler := NewDefaultClipSampler()
	req := ports.ClipSamplerRequest{
		Limit:         3,
		MinCoverage:   0,
		SourceType:    scriptpkg.SourceSearch,
		CallingSource: ClipSamplerCallerSearch,
	}
	// 5 distinct IDs + a duplicate of the first, so dedup + limit-3
	// yields exactly the first 3 in caller order.
	candidates := []ports.ClipSamplerCandidate{
		{ClipID: "clip-1", Name: "first", Score: 0.9, Source: "semantic"},
		{ClipID: "clip-2", Name: "second", Score: 0.8, Source: "semantic"},
		{ClipID: "clip-1", Name: "first-dup", Score: 0.7, Source: "semantic"},
		{ClipID: "clip-3", Name: "third", Score: 0.6, Source: "semantic"},
		{ClipID: "clip-4", Name: "fourth", Score: 0.5, Source: "semantic"},
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
		Limit:         10,
		MinScore:      0.5,
		SourceType:    scriptpkg.SourceSearch,
		CallingSource: ClipSamplerCallerSearch,
	}
	candidates := []ports.ClipSamplerCandidate{
		{ClipID: "high", Score: 0.95, Source: "semantic"},
		{ClipID: "lo-1", Score: 0.10, Source: "semantic"}, // dropped
		{ClipID: "lo-2", Score: 0.30, Source: "semantic"}, // dropped
		{ClipID: "mid", Score: 0.55, Source: "semantic"},
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
		Limit:         4,
		MinCoverage:   0.5,
		SourceType:    scriptpkg.SourceSearch,
		CallingSource: ClipSamplerCallerSearch,
	}
	candidates := []ports.ClipSamplerCandidate{
		{ClipID: "a", Source: "semantic"},
		{ClipID: "b", Source: "semantic"},
		{ClipID: "c", Source: "semantic"},
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
		Limit:         10,
		MinCoverage:   0.5,
		SourceType:    scriptpkg.SourceSearch,
		CallingSource: ClipSamplerCallerSearch,
	}
	candidates := []ports.ClipSamplerCandidate{
		{ClipID: "only-one", Source: "semantic"},
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

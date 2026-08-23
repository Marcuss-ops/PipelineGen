// Package jobs — classify_test.go: regression tests for
// ClassifyExtractionResult.
//
// Commit F (July 2026, P0-COMPL-2 follow-up): pins the cache-hit success
// contract. The previous full-success branch required Processed > 0,
// which flagged 100% cache-hit re-runs (Processed=0, Skipped=Requested)
// as terminal failure. The fix mirrors the usecase-level classifier
// (Correttezza #7, PR-C Commit 2/6): cache-hit re-run = full success.
//
// Each case asserts the contract table at the top of classify.go:
//
//	resp == nil | Requested == 0                     → ErrExtractionTerminal
//	Failed == 0 && (Processed+Skipped) == Requested  → nil
//	Processed > 0 && Failed > 0                      → *PartialSuccessError
//	Processed == 0 && Failed == Requested            → retryable/terminal scan
package jobs

import (
	"errors"
	"testing"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

// fullStats builds an ExtractStats envelope with the canonical fields
// for the test tables below. Counts default to zero unless overridden.
func fullStats(requested, processed, skipped, failed int) *youtubetypes.ExtractStats {
	return &youtubetypes.ExtractStats{
		Requested: requested,
		Processed: processed,
		Skipped:   skipped,
		Failed:    failed,
	}
}

// TestClassifyExtractionResult_AllSkippedCacheHit pins the cache-hit
// success contract: 100% skipped re-runs classify as full success (no
// error). Regression-prevention for the legacy Processed > 0 gate.
func TestClassifyExtractionResult_AllSkippedCacheHit(t *testing.T) {
	resp := &youtubetypes.ExtractResponse{
		Stats: fullStats(3, 0, 3, 0),
	}
	if err := ClassifyExtractionResult(resp); err != nil {
		t.Fatalf("all-skipped cache-hit should classify as full success, got %v", err)
	}
}

// TestClassifyExtractionResult_AllProcessedSuccess pins the regression-
// safe happy path: every segment processed, nothing skipped or failed.
func TestClassifyExtractionResult_AllProcessedSuccess(t *testing.T) {
	resp := &youtubetypes.ExtractResponse{
		Stats: fullStats(5, 5, 0, 0),
	}
	if err := ClassifyExtractionResult(resp); err != nil {
		t.Fatalf("all-processed run should classify as full success, got %v", err)
	}
}

// TestClassifyExtractionResult_MixedProcessedSkipped pins the mixed
// success contract: Processed + Skipped = Requested, zero failures.
func TestClassifyExtractionResult_MixedProcessedSkipped(t *testing.T) {
	resp := &youtubetypes.ExtractResponse{
		Stats: fullStats(5, 3, 2, 0),
	}
	if err := ClassifyExtractionResult(resp); err != nil {
		t.Fatalf("mixed processed+skipped should classify as full success, got %v", err)
	}
}

// TestClassifyExtractionResult_PartialSuccess pins the partial-success
// branch: at least one processed AND at least one failed.
// errors.As against the typed *PartialSuccessError must succeed.
func TestClassifyExtractionResult_PartialSuccess(t *testing.T) {
	resp := &youtubetypes.ExtractResponse{
		Stats: fullStats(5, 3, 0, 2),
	}
	err := ClassifyExtractionResult(resp)
	if err == nil {
		t.Fatal("partial-success should return error, got nil")
	}
	var p *PartialSuccessError
	if !errors.As(err, &p) {
		t.Fatalf("partial-success should produce *PartialSuccessError, got %T: %v", err, err)
	}
	if p.Processed != 3 || p.Failed != 2 {
		t.Fatalf("partial-success wrapper stats mismatch: processed=%d failed=%d want 3/2", p.Processed, p.Failed)
	}
	// Per classify.go contract, PartialSuccessError.Unwrap → ErrExtractionRetryable
	if !errors.Is(err, ErrExtractionRetryable) {
		t.Fatalf("PartialSuccessError should wrap ErrExtractionRetryable, got %v", err)
	}
}

// TestClassifyExtractionResult_AccountingDrift pins fail-closed on
// counter drift: when Processed+Skipped+Failed != Requested, the run
// is treated as terminal (no silent-success). godlike/07 no-fake-
// availability invariant.
func TestClassifyExtractionResult_AccountingDrift(t *testing.T) {
	resp := &youtubetypes.ExtractResponse{
		Stats: fullStats(5, 3, 0, 0), // 3+0 != 5, drift by 2
	}
	if err := ClassifyExtractionResult(resp); !errors.Is(err, ErrExtractionTerminal) {
		t.Fatalf("accounting drift should fail-closed as terminal, got %v", err)
	}
}

// TestClassifyExtractionResult_AllFailedRetryable pins the all-failed
// retryable path: any item.Error matches the transient substring
// taxonomy → ErrExtractionRetryable.
func TestClassifyExtractionResult_AllFailedRetryable(t *testing.T) {
	resp := &youtubetypes.ExtractResponse{
		Stats: fullStats(2, 0, 0, 2),
		Items: []youtubetypes.ExtractItem{
			{Status: "failed", Error: "HTTP 503 Service Unavailable"},
			{Status: "failed", Error: "network timeout"},
		},
	}
	if err := ClassifyExtractionResult(resp); !errors.Is(err, ErrExtractionRetryable) {
		t.Fatalf("all-failed-retryable should produce ErrExtractionRetryable, got %v", err)
	}
}

// TestClassifyExtractionResult_AllFailedTerminal pins the all-failed
// terminal path: no transient substring → ErrExtractionTerminal.
func TestClassifyExtractionResult_AllFailedTerminal(t *testing.T) {
	resp := &youtubetypes.ExtractResponse{
		Stats: fullStats(2, 0, 0, 2),
		Items: []youtubetypes.ExtractItem{
			{Status: "failed", Error: "Video unavailable"},
			{Status: "failed", Error: "Permission denied"},
		},
	}
	if err := ClassifyExtractionResult(resp); !errors.Is(err, ErrExtractionTerminal) {
		t.Fatalf("all-failed-terminal should produce ErrExtractionTerminal, got %v", err)
	}
}

// TestClassifyExtractionResult_NilResponse pins the nil/invalid
// payload guard: nil resp → ErrExtractionTerminal.
func TestClassifyExtractionResult_NilResponse(t *testing.T) {
	if err := ClassifyExtractionResult(nil); !errors.Is(err, ErrExtractionTerminal) {
		t.Fatalf("nil resp should yield terminal error, got %v", err)
	}
}

// TestClassifyExtractionResult_NilStats pins the Stats-missing guard.
func TestClassifyExtractionResult_NilStats(t *testing.T) {
	resp := &youtubetypes.ExtractResponse{Stats: nil}
	if err := ClassifyExtractionResult(resp); !errors.Is(err, ErrExtractionTerminal) {
		t.Fatalf("nil Stats should yield terminal error, got %v", err)
	}
}

// TestClassifyExtractionResult_ZeroRequested pins the trivial input
// guard: Requested == 0 → ErrExtractionTerminal (vacuously nothing
// to do, but explicit failure beats silent null-run).
func TestClassifyExtractionResult_ZeroRequested(t *testing.T) {
	resp := &youtubetypes.ExtractResponse{
		Stats: fullStats(0, 0, 0, 0),
	}
	if err := ClassifyExtractionResult(resp); !errors.Is(err, ErrExtractionTerminal) {
		t.Fatalf("Requested=0 should yield terminal error, got %v", err)
	}
}

// TestClassifyExtractionResult_AllFailedMixedWithSkipped pins the
// subtle drift case: Processed=0, Skipped=1, Failed=1 (Requested=2).
// Skipped + Failed = Requested but Processed=0 → falls through past
// the new success branch. Failed > 0 → not in partial (Processed=0).
// → reaches retryable substring scan → terminal (allowlisted errors).
func TestClassifyExtractionResult_AllFailedMixedWithSkipped(t *testing.T) {
	resp := &youtubetypes.ExtractResponse{
		Stats: fullStats(2, 0, 1, 1),
		Items: []youtubetypes.ExtractItem{
			{Status: "failed", Error: "Video unavailable"},
		},
	}
	if err := ClassifyExtractionResult(resp); !errors.Is(err, ErrExtractionTerminal) {
		t.Fatalf("all-failed-mixed-with-skipped (terminal error msg) should fail-closed, got %v", err)
	}
}

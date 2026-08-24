package assets

import (
	"strings"
	"testing"
)

// ----- PR-ARTLIST-OUTCOME-ACCOUNTING (P1, July 2026) -----------------------
//
// These tests pin the EvaluateRunOutcome policy matrix. The PR added two
// behaviors to EvaluateRunOutcome:
//
//  1. stagePersistResults now increments resp.Failed for three persistence
//     failure modes (drive_upload_failed, hash_missing, persist_failed).
//     These tests don't exercise stagePersistResults directly (that path
//     requires a live mainDB + assetFinalizer and is integration-territory);
//     they exercise EvaluateRunOutcome with the SAME integer Counters that
//     stagePersistResults would produce after the PR.
//
//  2. EvaluateRunOutcome gained Policy B: when no explicit failure was
//     recorded (Failed == 0) but Found - (Processed + Skipped) > 0, the run
//     is undercounted and must be flagged as failed.
//
// The 4 tests cover the matrix below:
//
//   case                            OK  Found  Proc  Skip  Fail  verdict
//   ------------------------------  --- -----  ----  ----  ----  -----------------
//   all persisted OK                T   10     10    0     0     false (legit pass)
//   all items failed                T   10     0     0     10    true  (policy A)
//   mix: real failures + persistence T   10     7     0     3     false (partial success, no silent loss)
//   silent loss with persistence    T   10     7     2     0     true  (policy B — undercount)
// --------------------------------------------------------------------------------

func TestEvaluateRunOutcome_AllPersistedOk(t *testing.T) {
	resp := &RunTagResponse{
		OK:        true,
		Found:     10,
		Processed: 10,
		Skipped:   0,
		Failed:    0,
	}
	failed, errMsg := EvaluateRunOutcome(resp)
	if failed {
		t.Fatalf("expected run to be healthy when every discovered clip persisted, got failed=%v errMsg=%q", failed, errMsg)
	}
	if errMsg != "" {
		t.Fatalf("expected empty error message on healthy run, got %q", errMsg)
	}
}

func TestEvaluateRunOutcome_AllItemsFailed(t *testing.T) {
	resp := &RunTagResponse{
		OK:        true,
		Found:     10,
		Processed: 0,
		Skipped:   0,
		Failed:    10,
	}
	failed, errMsg := EvaluateRunOutcome(resp)
	if !failed {
		t.Fatalf("expected all-items-failed run to be flagged, got failed=%v errMsg=%q", failed, errMsg)
	}
	if errMsg != "all artlist items failed" {
		t.Fatalf("expected canonical 'all artlist items failed' verdict, got %q", errMsg)
	}
}

func TestEvaluateRunOutcome_MixFailureAndPersistence_NoSilentLoss(t *testing.T) {
	// Found=10, Processed=7 (persisted), Failed=3 (drive_upload / hash /
	// persist failures after the PR's Failed bumps in stagePersistResults).
	// This is a legitimate partial-success: no silent gap, just real failures.
	resp := &RunTagResponse{
		OK:        true,
		Found:     10,
		Processed: 7,
		Skipped:   0,
		Failed:    3,
	}
	failed, errMsg := EvaluateRunOutcome(resp)
	if failed {
		t.Fatalf("expected mixed partial-success run to pass (no silent loss), got failed=%v errMsg=%q", failed, errMsg)
	}
	if errMsg != "" {
		t.Fatalf("expected empty error message on legitimate partial-success, got %q", errMsg)
	}
	if got, want := resp.Found-(resp.Processed+resp.Skipped), 3; got != want {
		// sanity-check the arithmetic the test relies on
		t.Fatalf("test arithmetic mismatch: Found-Processed-Skipped = %d, want %d", got, want)
	}
}

func TestEvaluateRunOutcome_SilentLossWithPartialProcessing(t *testing.T) {
	// Found=10, Processed=7, Skipped=2, Failed=0 → exactly 1 item silently
	// lost (Found-(Processed+Skipped) = 1 > 0). Policy B (added by
	// PR-ARTLIST-OUTCOME-ACCOUNTING) MUST flag this as failed even though
	// Processed > 0.
	resp := &RunTagResponse{
		OK:        true,
		Found:     10,
		Processed: 7,
		Skipped:   2,
		Failed:    0,
	}
	failed, errMsg := EvaluateRunOutcome(resp)
	if !failed {
		t.Fatalf("expected undercounted run to be flagged (Policy B), got failed=%v errMsg=%q", failed, errMsg)
	}
	if !strings.Contains(errMsg, "run undercounted") {
		t.Fatalf("expected undercounted diagnostic, got %q", errMsg)
	}
	// Diagnostic must quote the exact gap so operators can audit it.
	if !strings.Contains(errMsg, "1 items silently lost") {
		t.Fatalf("expected gap size '1' in diagnostic, got %q", errMsg)
	}
	if !strings.Contains(errMsg, "found=10") || !strings.Contains(errMsg, "processed=7") || !strings.Contains(errMsg, "skipped=2") {
		t.Fatalf("expected diagnostic to quote all 4 counters, got %q", errMsg)
	}
}

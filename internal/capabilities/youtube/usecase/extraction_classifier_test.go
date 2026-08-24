// Package usecase — extraction_classifier_test.go pins the
// Commit 2/6 Correttezza #7 + #8 helpers extracted from
// extraction_service.go:
//
//   - classifyExtractionRun: the canonical success/failure
//     classifier. Returns true on a 100% cache-hit re-run
//     (the "verify" strategy short-circuit), vacuously true
//     for requested==0, false on any failure or accounting
//     drift.
//   - resolveKeepAudio: the canonical nil-check for the
//     KeepAudio *bool DTO field. nil → true (canonical default);
//     non-nil → *req.KeepAudio.
//
// Extracting these as helpers keeps the test surface minimal
// (no need for the 11-field ExtractionService fixture).
package usecase

import (
	"testing"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

// ── Test 7: classifier ──────────────────────────────────────────────

// TestClassifyExtractionRun_VacuouslyTrueOnEmpty pins the
// requested==0 → true branch.
func TestClassifyExtractionRun_VacuouslyTrueOnEmpty(t *testing.T) {
	got := classifyExtractionRun(&youtubetypes.ExtractStats{Requested: 0})
	if !got {
		t.Errorf("requested=0 MUST be vacuously true (no work to fail)")
	}
}

// TestClassifyExtractionRun_AllProcessedSuccess pins the
// happy path: all requested segments were processed.
func TestClassifyExtractionRun_AllProcessedSuccess(t *testing.T) {
	got := classifyExtractionRun(&youtubetypes.ExtractStats{
		Requested: 5, Processed: 5, Skipped: 0, Failed: 0,
	})
	if !got {
		t.Errorf("all processed: want true got false")
	}
}

// TestClassifyExtractionRun_AllSkippedSuccess pins the
// Correttezza #7 contract: 100% cache-hit re-run (all
// segments skipped) classifies as success. The legacy
// classifier required processed > 0, which incorrectly
// flagged this as failure.
func TestClassifyExtractionRun_AllSkippedSuccess(t *testing.T) {
	got := classifyExtractionRun(&youtubetypes.ExtractStats{
		Requested: 3, Processed: 0, Skipped: 3, Failed: 0,
	})
	if !got {
		t.Errorf("all skipped (100%% cache-hit re-run): want true got false (Correttezza #7 — the 'verify' short-circuit must classify as success)")
	}
}

// TestClassifyExtractionRun_MixedProcessedAndSkipped pins the
// canonical mixed success: some processed, some skipped,
// total covers requested.
func TestClassifyExtractionRun_MixedProcessedAndSkipped(t *testing.T) {
	got := classifyExtractionRun(&youtubetypes.ExtractStats{
		Requested: 5, Processed: 3, Skipped: 2, Failed: 0,
	})
	if !got {
		t.Errorf("mixed processed+skipped covering requested: want true got false")
	}
}

// TestClassifyExtractionRun_AnyFailedIsFailure pins the
// negative path: any single failure fails the whole run.
func TestClassifyExtractionRun_AnyFailedIsFailure(t *testing.T) {
	got := classifyExtractionRun(&youtubetypes.ExtractStats{
		Requested: 5, Processed: 4, Skipped: 0, Failed: 1,
	})
	if got {
		t.Errorf("any failure: want false got true")
	}
}

// TestClassifyExtractionRun_AccountingDriftIsFailure pins the
// defensive branch: processed+skipped+failed must sum to
// requested. A counter that drifts is itself a fail-closed
// signal (caller-visible bug).
func TestClassifyExtractionRun_AccountingDriftIsFailure(t *testing.T) {
	got := classifyExtractionRun(&youtubetypes.ExtractStats{
		Requested: 5, Processed: 3, Skipped: 0, Failed: 0,
	})
	if got {
		t.Errorf("processed+skipped != requested (drift): want false got true (defensive: counter bug should fail-closed)")
	}
}

// ── Test 8: KeepAudio *bool nil-check ──────────────────────────────

// TestResolveKeepAudio_NilDefaultsToTrue pins the canonical
// default. PR-C flipped the legacy silent-default-false to
// silent-default-true; the typed-pointer + nil-check is the
// Commit 2/6 canonical form.
func TestResolveKeepAudio_NilDefaultsToTrue(t *testing.T) {
	if got := resolveKeepAudio(&youtubetypes.ExtractRequest{}); got != true {
		t.Errorf("nil KeepAudio MUST default to true (PR-C flip): got %v", got)
	}
	if got := resolveKeepAudio(&youtubetypes.ExtractRequest{KeepAudio: nil}); got != true {
		t.Errorf("explicit nil KeepAudio MUST default to true: got %v", got)
	}
	if got := resolveKeepAudio(nil); got != true {
		t.Errorf("nil request MUST default to true (defensive): got %v", got)
	}
}

// TestResolveKeepAudio_ExplicitTruePreserved pins the
// non-nil true case.
func TestResolveKeepAudio_ExplicitTruePreserved(t *testing.T) {
	tr := true
	if got := resolveKeepAudio(&youtubetypes.ExtractRequest{KeepAudio: &tr}); got != true {
		t.Errorf("explicit true: want true got %v", got)
	}
}

// TestResolveKeepAudio_ExplicitFalsePreserved pins the
// non-nil false case (caller wants to strip audio).
func TestResolveKeepAudio_ExplicitFalsePreserved(t *testing.T) {
	f := false
	if got := resolveKeepAudio(&youtubetypes.ExtractRequest{KeepAudio: &f}); got != false {
		t.Errorf("explicit false: want false got %v (Correttezza #8 — typed *bool must preserve explicit caller choice)", got)
	}
}

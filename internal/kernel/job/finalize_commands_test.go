// Package job — finalize_commands_test.go: hermetic validation for the
// canonical FinalizeAttempt typed surface (Fase 4(a)) and the
// LeaseState / RenewLeaseResult typed surface (Fase 4(b)).
//
// godlike/06 SSOT discipline: this file declares the canonical
// compile-time + runtime checks for the typed surface added by
// Push 4.1. A future push that introduces an interface change MUST
// extend this file in lockstep; drift between the interface and
// the typed surface is a build-failure on the adapter
// `var _ Store = (*Adapter)(nil)` assertion.
//
// godlike/07 fail-closed: every enum value MUST be reachable via
// IsValid() (no dead enum values); NewExpiry() MUST reject the
// non-Continue states even if NewLeaseExpiry is accidentally set
// (defensive against caller-side mutation after fence state read).
package job

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"encoding/json"
	"testing"
	"time"
)

// ── FinalizeAttemptOutcome enum-coverage tests ──────────────────────────

// TestFinalizeAttemptOutcome_AllDistinct verifies the canonical
// OutcomeSucceeded / OutcomeFailedPermanent / OutcomeScheduleRetry
// three values are pairwise distinct. A future push that adds a
// 4th outcome MUST update this test in lockstep; godlike/06 SSOT.
func TestFinalizeAttemptOutcome_AllDistinct(t *testing.T) {
	values := []FinalizeAttemptOutcome{
		OutcomeSucceeded,
		OutcomeFailedPermanent,
		OutcomeScheduleRetry,
	}
	seen := make(map[FinalizeAttemptOutcome]bool, len(values))
	for _, v := range values {
		if v == "" {
			t.Errorf("FinalizeAttemptOutcome has an empty value; canonical enum MUST be non-empty")
		}
		if seen[v] {
			t.Errorf("FinalizeAttemptOutcome value %q is duplicated; enum values MUST be distinct", v)
		}
		if !v.IsValid() {
			t.Errorf("FinalizeAttemptOutcome value %q is not Valid; canonical enum MUST be self-recognised", v)
		}
		seen[v] = true
	}
}

// TestFinalizeAttemptOutcome_IsValidRejectsUnknown is the gate for
// unknown wire values. SQL-layer fence rejects unknown outcomes via
// ErrFinalizeAttemptOutcomeInvalid; the Go-level IsValid is the
// first line of defence.
func TestFinalizeAttemptOutcome_IsValidRejectsUnknown(t *testing.T) {
	invalid := []FinalizeAttemptOutcome{
		"",
		FinalizeAttemptOutcome("UNKNOWN"),
		FinalizeAttemptOutcome("succeeded"), // lowercase is non-canonical
		FinalizeAttemptOutcome("SUCCEEDED_WITH_WARNINGS"),
	}
	for _, v := range invalid {
		if v.IsValid() {
			t.Errorf("FinalizeAttemptOutcome.IsValid returned true for invalid value %q", v)
		}
	}
}

// ── LeaseState enum-coverage tests ──────────────────────────────────────

// TestLeaseState_AllDistinct verifies the canonical
// LeaseStateContinue / LeaseStateCancelRequested / LeaseStateLeaseLost
// three values are pairwise distinct.
func TestLeaseState_AllDistinct(t *testing.T) {
	values := []LeaseState{
		LeaseStateContinue,
		LeaseStateCancelRequested,
		LeaseStateLeaseLost,
	}
	seen := make(map[LeaseState]bool, len(values))
	for _, v := range values {
		if v == "" {
			t.Errorf("LeaseState has an empty value; canonical enum MUST be non-empty")
		}
		if seen[v] {
			t.Errorf("LeaseState value %q is duplicated; enum values MUST be distinct", v)
		}
		if !v.IsValid() {
			t.Errorf("LeaseState value %q is not Valid; canonical enum MUST be self-recognised", v)
		}
		seen[v] = true
	}
}

// TestLeaseState_IsValidRejectsUnknown is the gate for unknown wire values.
func TestLeaseState_IsValidRejectsUnknown(t *testing.T) {
	invalid := []LeaseState{
		"",
		LeaseState("UNKNOWN"),
		LeaseState("continue "), // trailing space
		LeaseState("LEASE_LOST"),
	}
	for _, v := range invalid {
		if v.IsValid() {
			t.Errorf("LeaseState.IsValid returned true for invalid value %q", v)
		}
	}
}

// ── RenewLeaseResult.NewExpiry state-conditional accessor tests ─────────

// TestRenewLeaseResult_NewExpiry verifies that the
// state-conditional accessor correctly distinguishes LeaseStateContinue
// from the two failure modes. godlike/07 fail-closed: a worker that
// reads NewLeaseExpiry for LeaseStateLeaseLost would proceed with
// jobs owned by another worker — a SEV-1 blast-radius event.
// The accessor makes that pattern impossible to write by accident.
func TestRenewLeaseResult_NewExpiry(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	future := now.Add(30 * time.Second)

	cases := []struct {
		name     string
		result   RenewLeaseResult
		wantOK   bool
		wantTime time.Time
	}{
		{
			name:     "ContinueWithFutureExpiry",
			result:   RenewLeaseResult{State: LeaseStateContinue, NewLeaseExpiry: &future},
			wantOK:   true,
			wantTime: future,
		},
		{
			name:     "ContinueWithPastExpiry",
			result:   RenewLeaseResult{State: LeaseStateContinue, NewLeaseExpiry: &now},
			wantOK:   true,
			wantTime: now,
		},
		{
			name:   "ContinueWithNilExpiry",
			result: RenewLeaseResult{State: LeaseStateContinue, NewLeaseExpiry: nil},
			wantOK: false,
		},
		{
			name:   "CancelRequestedEvenWithExpirySet",
			result: RenewLeaseResult{State: LeaseStateCancelRequested, NewLeaseExpiry: &future},
			wantOK: false,
		},
		{
			name:   "LeaseLostEvenWithExpirySet",
			result: RenewLeaseResult{State: LeaseStateLeaseLost, NewLeaseExpiry: &future},
			wantOK: false,
		},
		{
			name:   "EmptyResult",
			result: RenewLeaseResult{},
			wantOK: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotTime, gotOK := c.result.NewExpiry()
			if gotOK != c.wantOK {
				t.Errorf("NewExpiry ok = %v, want %v", gotOK, c.wantOK)
			}
			if c.wantOK && !gotTime.Equal(c.wantTime) {
				t.Errorf("NewExpiry time = %v, want %v", gotTime, c.wantTime)
			}
			if !c.wantOK && !gotTime.IsZero() {
				t.Errorf("NewExpiry time = %v on non-Continue state, want zero", gotTime)
			}
		})
	}
}

// ── FinalizeAttemptCommand struct-shape tests ──────────────────────────

// TestFinalizeAttemptCommand_AllFieldsPopulated is a shape test
// verifying that the canonical typed surface enumerates the user's
// seven required fields (outcome, result/error, retry decision, next
// attempt, dlq payload, artifact state, outbox events) plus the
// lease-fence guards (WorkerID, LeaseID, ExpectedRevision).
//
// godlike/06 SSOT: a future push that drops one of these fields
// (or adds a non-canonical field) MUST update this test; a silent
// removal of a canonical field would be detected at the next
// database-write attempt rather than at compile-time.
func TestFinalizeAttemptCommand_AllFieldsPopulated(t *testing.T) {
	cmd := FinalizeAttemptCommand{
		JobID:            "job-1234",
		Outcome:          OutcomeSucceeded,
		WorkerID:         "worker-A",
		LeaseID:          "lease-5678",
		ExpectedRevision: 42,
		Result:           json.RawMessage(`{"ok":true}`),
		ErrorMessage:     "",
		Backoff:          0,
		DLQPayload:       nil,
		ArtifactState:    &ArtifactStatePatch{ArtifactID: "art-1", NewState: "PUBLISHED"},
		OutboxEvents: []OutboxEventSpec{
			{Type: "asset.index", EventKey: "k1", Payload: json.RawMessage(`{"asset_id":"art-1"}`)},
		},
		EventType: "job_completed",
		EventData: map[string]any{"foo": "bar"},
	}
	if cmd.JobID != "job-1234" {
		t.Errorf("JobID field not populated correctly")
	}
	if cmd.ArtifactState == nil || cmd.ArtifactState.ArtifactID != "art-1" {
		t.Errorf("ArtifactState field not populated correctly")
	}
	if len(cmd.OutboxEvents) != 1 {
		t.Errorf("OutboxEvents field not populated correctly; got %d", len(cmd.OutboxEvents))
	}
}

// TestFinalizeAttemptResult_AllFieldsPopulated verifies the post-commit
// projection surfaces JobID + FinalStatus + NewRevision + DLQRecorded
// + OutboxEventsWritten. godlike/06 SSOT: a future push that drops
// a field MUST update this test.
func TestFinalizeAttemptResult_AllFieldsPopulated(t *testing.T) {
	res := FinalizeAttemptResult{
		JobID:               "job-1234",
		FinalStatus:         StatusSucceeded,
		NewRevision:         43,
		DLQRecorded:         false,
		OutboxEventsWritten: []string{"evt_1_abc"},
	}
	if res.JobID != "job-1234" {
		t.Errorf("JobID field not populated correctly")
	}
	if res.NewRevision != 43 {
		t.Errorf("NewRevision field not populated correctly; got %d", res.NewRevision)
	}
	if len(res.OutboxEventsWritten) != 1 {
		t.Errorf("OutboxEventsWritten field not populated correctly; got %d", len(res.OutboxEventsWritten))
	}
}

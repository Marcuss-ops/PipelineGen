// repository_lifecycle_test.go — PR-F / ADR-0002 §D6.7 verification.
//
// Unit tests on validateOwnership's counter-bump contract:
//   - job.ErrTransitionConflict (revision mismatch) bumps
//     job_transition_conflict_total{method=<name>} by exactly 1
//   - job.ErrLeaseLost (worker OR lease mismatch) does NOT bump the counter
//     (different-worker-on-same-row is a distinct operator signal —
//     merging it under "transition_conflict" would corrupt dashboards)
//   - ErrInvalidState (status mismatch) does NOT bump the counter
//     (worker-called-wrong-transition is a distinct signal)
//   - All-match success does NOT bump the counter
//
// Each test uses a UNIQUE method label so cross-test pollution (the
// JobTransitionConflictTotal is a package-level CounterVec shared by
// every test that reads from the Prometheus global registry) does NOT
// mask deltas. The helper `readConflictCounter` reads counter values
// via the canonical prometheus dto Write path (mirrors the pattern
// from internal/platform/observability/progress_ratio_collector.go).
package jobs

import (
	"errors"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// readConflictCounter returns the current cumulative value of the
// job_transition_conflict_total{method=<method>} counter via the
// prometheus dto Write path. Same pattern as
// internal/platform/observability/progress_ratio_collector.go::counterValue.
func readConflictCounter(method string) float64 {
	var pb dto.Metric
	_ = observability.JobTransitionConflictTotal.WithLabelValues(method).Write(&pb)
	return pb.GetCounter().GetValue()
}

// TestValidateOwnership_RevisionMismatch_BumpsCounter locks the
// PR-F contract: validateOwnership returns job.ErrTransitionConflict
// exactly when the current row's revision != the worker's expected
// revision, and the counter
// job_transition_conflict_total{method=<name>} is incremented by 1.
func TestValidateOwnership_RevisionMismatch_BumpsCounter(t *testing.T) {
	method := "test_validate_rev_mismatch"
	before := readConflictCounter(method)

	err := validateOwnership("test-job", method,
		job.StatusRunning,
		"worker-A", "lease-X", 7, // current revision = 7
		"worker-A", "lease-X", 6, // expected revision = 6 -> mismatch
		job.StatusRunning)
	if err == nil {
		t.Fatalf("expected job.ErrTransitionConflict, got nil")
	}
	if !errors.Is(err, job.ErrTransitionConflict) {
		t.Fatalf("expected job.ErrTransitionConflict, got %v (type %T)", err, err)
	}

	after := readConflictCounter(method)
	if delta := after - before; delta != 1.0 {
		t.Errorf("counter delta: got %v, want 1.0 (one bump exactly)", delta)
	}
}

// TestValidateOwnership_WorkerMismatch_DoesNotBumpCounter locks
// the OPPOSITE contract for the worker-mismatch branch:
// job.ErrLeaseLost is a distinct signal (different worker on the same
// row, lease expired normally) and MUST NOT bump
// job_transition_conflict_total. Operators alert on the per-method
// totals to distinguish "lease-stolen" from
// "different-worker-on-same-row" — merging them would corrupt that
// signal.
func TestValidateOwnership_WorkerMismatch_DoesNotBumpCounter(t *testing.T) {
	method := "test_validate_worker_mismatch"
	before := readConflictCounter(method)

	err := validateOwnership("test-job", method,
		job.StatusRunning,
		"worker-A", "lease-X", 5, // current worker-A
		"worker-B", "lease-X", 5, // expected worker-B -> mismatch
		job.StatusRunning)
	if err == nil {
		t.Fatalf("expected job.ErrLeaseLost, got nil")
	}
	if !errors.Is(err, job.ErrLeaseLost) {
		t.Fatalf("expected job.ErrLeaseLost, got %v (type %T)", err, err)
	}

	after := readConflictCounter(method)
	if delta := after - before; delta != 0.0 {
		t.Errorf("counter MUST NOT bump on job.ErrLeaseLost (worker); got delta=%v", delta)
	}
}

// TestValidateOwnership_LeaseMismatch_DoesNotBumpCounter is the
// lease-mismatch twin of the worker-mismatch test. job.ErrLeaseLost is
// returned in both cases; the merged behaviour is "any
// lease-related mismatch surfaces as job.ErrLeaseLost, never as
// job.ErrTransitionConflict, and never bumps this counter".
func TestValidateOwnership_LeaseMismatch_DoesNotBumpCounter(t *testing.T) {
	method := "test_validate_lease_mismatch"
	before := readConflictCounter(method)

	err := validateOwnership("test-job", method,
		job.StatusRunning,
		"worker-A", "lease-X", 5, // current lease-X
		"worker-A", "lease-Y", 5, // expected lease-Y -> mismatch
		job.StatusRunning)
	if err == nil {
		t.Fatalf("expected job.ErrLeaseLost, got nil")
	}
	if !errors.Is(err, job.ErrLeaseLost) {
		t.Fatalf("expected job.ErrLeaseLost, got %v (type %T)", err, err)
	}

	after := readConflictCounter(method)
	if delta := after - before; delta != 0.0 {
		t.Errorf("counter MUST NOT bump on job.ErrLeaseLost (lease); got delta=%v", delta)
	}
}

// TestValidateOwnership_StatusMismatch_DoesNotBumpCounter locks the
// status-mismatch branch: ErrInvalidState is a
// "worker-called-wrong-transition" signal (e.g. Complete on a Queued
// row), distinct from the revision-mismatch transition_conflict
// signal. Operators alert on rate>0 of the conflict counter as a
// "lease-stolen / cross-writer race" signal — merging status errors
// there would falsely fire that alert on legitimate state-machine
// transitions gone wrong (which is ErrInvalidState, not
// job.ErrTransitionConflict).
func TestValidateOwnership_StatusMismatch_DoesNotBumpCounter(t *testing.T) {
	method := "test_validate_status_mismatch"
	before := readConflictCounter(method)

	err := validateOwnership("test-job", method,
		job.StatusQueued, // current = Queued
		"worker-A", "lease-X", 5,
		"worker-A", "lease-X", 5,
		job.StatusRunning) // expected = Running -> mismatch
	if err == nil {
		t.Fatalf("expected ErrInvalidState, got nil")
	}
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("expected ErrInvalidState, got %v (type %T)", err, err)
	}

	after := readConflictCounter(method)
	if delta := after - before; delta != 0.0 {
		t.Errorf("counter MUST NOT bump on ErrInvalidState; got delta=%v", delta)
	}
}

// TestValidateOwnership_AllMatch_DoesNotBumpCounter locks the
// happy-path contract: when all 4 gates pass, validateOwnership
// returns nil and zero counter delta. Sanity check that the success
// path does not accidentally bump the counter (e.g. via a misplaced
// Inc call).
func TestValidateOwnership_AllMatch_DoesNotBumpCounter(t *testing.T) {
	method := "test_validate_all_match"
	before := readConflictCounter(method)

	err := validateOwnership("test-job", method,
		job.StatusRunning,
		"worker-A", "lease-X", 5,
		"worker-A", "lease-X", 5,
		job.StatusRunning)
	if err != nil {
		t.Fatalf("validateOwnership all-match: expected nil, got %v", err)
	}

	after := readConflictCounter(method)
	if delta := after - before; delta != 0.0 {
		t.Errorf("counter MUST NOT bump on success; got delta=%v", delta)
	}
}

// TestValidateOwnership_AcceptsFinalizing verifies the FASE 2b contract:
// validateOwnership accepts FINALIZING as a valid current status for
// completion/failure. A job in FINALIZING is still recoverable — the
// worker that holds the lease can still Complete or Fail it.
func TestValidateOwnership_AcceptsFinalizing(t *testing.T) {
	method := "test_validate_finalizing_accept"

	// Case 1: current=FINALIZING, expected={RUNNING, FINALIZING} → success.
	err := validateOwnership("test-job", method,
		job.StatusFinalizing,
		"worker-A", "lease-X", 5,
		"worker-A", "lease-X", 5,
		job.StatusRunning, job.StatusFinalizing)
	if err != nil {
		t.Fatalf("validateOwnership with FINALIZING current, expected {RUNNING,FINALIZING}: got %v, want nil", err)
	}

	// Case 2: current=RUNNING, expected={RUNNING, FINALIZING} → success
	// (backwards-compat: the variadic signature accepts single status too).
	err = validateOwnership("test-job", method,
		job.StatusRunning,
		"worker-A", "lease-X", 5,
		"worker-A", "lease-X", 5,
		job.StatusRunning, job.StatusFinalizing)
	if err != nil {
		t.Fatalf("validateOwnership with RUNNING current, expected {RUNNING,FINALIZING}: got %v, want nil", err)
	}

	// Case 3: current=QUEUED, expected={RUNNING, FINALIZING} → ErrInvalidState
	// (a queued job has not been leased yet — cannot be finalised).
	err = validateOwnership("test-job", method,
		job.StatusQueued,
		"worker-A", "lease-X", 5,
		"worker-A", "lease-X", 5,
		job.StatusRunning, job.StatusFinalizing)
	if err == nil {
		t.Fatalf("validateOwnership with QUEUED current, expected {RUNNING,FINALIZING}: expected ErrInvalidState, got nil")
	}
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("validateOwnership with QUEUED current: expected ErrInvalidState, got %v", err)
	}

	// Case 4: backwards-compat — single expectedStatus still works.
	err = validateOwnership("test-job", method,
		job.StatusRunning,
		"worker-A", "lease-X", 5,
		"worker-A", "lease-X", 5,
		job.StatusRunning) // single arg → variadic of one
	if err != nil {
		t.Fatalf("validateOwnership backwards-compat single status: got %v, want nil", err)
	}
}

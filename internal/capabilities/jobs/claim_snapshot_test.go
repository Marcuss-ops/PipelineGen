package jobs

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// recordingClaimSnapshotter captures every SnapshotPreparationClaim input so
// tests can assert the claim-time KPI identity (job, attempt, revision).
type recordingClaimSnapshotter struct {
	calls int
	last  job.PreparationClaimInput
	err   error
}

func (r *recordingClaimSnapshotter) SnapshotPreparationClaim(_ context.Context, input job.PreparationClaimInput) (*job.PreparationClaimSnapshot, error) {
	r.calls++
	r.last = input
	if r.err != nil {
		return nil, r.err
	}
	return &job.PreparationClaimSnapshot{JobID: input.JobID, AttemptID: input.AttemptID}, nil
}

// TestRunner_AttachesClaimSnapshotterToWorkers — Runner.WithClaimSnapshotter
// propagates the snapshotter onto every Worker built by buildWorkers (mirrors
// the TestRunner_AttachesObserverToWorkers pin). Uses buildWorkers directly
// (not Start) so the attachment contract is asserted deterministically.
func TestRunner_AttachesClaimSnapshotterToWorkers(t *testing.T) {
	snapshotter := &recordingClaimSnapshotter{}
	const poolSize = 2
	runner := NewRunner(
		nil,
		nil,
		zap.NewNop(),
		RunnerConfig{
			Workers:   poolSize,
			PollEvery: 2 * time.Second,
			LeaseTTL:  5 * time.Minute,
			Backoff:   BackoffConfig{},
		},
	).WithClaimSnapshotter(snapshotter)

	workers := runner.buildWorkers()
	if len(workers) != poolSize {
		t.Fatalf("buildWorkers: got %d workers, want %d", len(workers), poolSize)
	}
	for i, w := range workers {
		if w.claimSnapshot != snapshotter {
			t.Errorf("worker[%d] claimSnapshot: not the snapshotter attached to Runner", i)
		}
	}
}

// TestWorker_CapturesClaimSnapshotOnClaim — the production claim path: the
// instant a job is claimed (ClaimNext returns), captureClaimSnapshot records
// the durable KPI with the real attempt identity jobID:revision.
func TestWorker_CapturesClaimSnapshotOnClaim(t *testing.T) {
	snapshotter := &recordingClaimSnapshotter{}
	w := NewWorker(WorkerDeps{ID: "w-1", Log: zap.NewNop()}).WithClaimSnapshotter(snapshotter)

	claimed := &job.Job{ID: "job-77", Type: "script.generate", Revision: 3}
	w.captureClaimSnapshot(context.Background(), claimed)

	if snapshotter.calls != 1 {
		t.Fatalf("capture calls = %d, want 1", snapshotter.calls)
	}
	if snapshotter.last.JobID != "job-77" {
		t.Errorf("JobID = %q, want job-77", snapshotter.last.JobID)
	}
	if snapshotter.last.AttemptID != "job-77:3" {
		t.Errorf("AttemptID = %q, want job-77:3 (real attempt identity)", snapshotter.last.AttemptID)
	}
	if snapshotter.last.JobRevision != 3 {
		t.Errorf("JobRevision = %d, want 3", snapshotter.last.JobRevision)
	}
	if snapshotter.last.ClaimedAt.IsZero() {
		t.Error("ClaimedAt must be set (the claim instant)")
	}
}

// TestWorker_CaptureClaimSnapshot_NilSnapshotterIsTolerant — an unwired
// snapshotter keeps the legacy un-instrumented path (no panic, no call).
func TestWorker_CaptureClaimSnapshot_NilSnapshotterIsTolerant(t *testing.T) {
	w := NewWorker(WorkerDeps{ID: "w-1", Log: zap.NewNop()}) // no snapshotter
	w.captureClaimSnapshot(context.Background(), &job.Job{ID: "job-1", Type: "probe", Revision: 1})
	w.captureClaimSnapshot(context.Background(), nil) // nil job guard
}

// TestWorker_CaptureClaimSnapshot_ErrorIsNonFatal — a snapshot failure is a
// control-plane side effect: it must be swallowed (logged), never returned,
// so the claim path is never delayed or failed by KPI capture.
func TestWorker_CaptureClaimSnapshot_ErrorIsNonFatal(t *testing.T) {
	snapshotter := &recordingClaimSnapshotter{err: context.DeadlineExceeded}
	w := NewWorker(WorkerDeps{ID: "w-1", Log: zap.NewNop()}).WithClaimSnapshotter(snapshotter)
	w.captureClaimSnapshot(context.Background(), &job.Job{ID: "job-1", Type: "probe", Revision: 1})
	if snapshotter.calls != 1 {
		t.Fatalf("capture calls = %d, want 1 (attempted despite expected failure)", snapshotter.calls)
	}
}

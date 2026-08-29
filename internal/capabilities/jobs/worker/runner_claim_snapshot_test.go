package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	jobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// claimSnapshotLeaseBroker returns exactly one lease from Claim (the real
// claim) and then nil so the Run loop idles — the test cancels after the
// snapshot fires.
type claimSnapshotLeaseBroker struct {
	mu       sync.Mutex
	claimed  bool
	complete int
}

func (b *claimSnapshotLeaseBroker) RegisterWorker(context.Context, jobs.RegisterWorkerCommand) (*jobs.WorkerSession, error) {
	return nil, nil
}
func (b *claimSnapshotLeaseBroker) Heartbeat(context.Context, jobs.HeartbeatCommand) error {
	return nil
}
func (b *claimSnapshotLeaseBroker) Claim(context.Context, jobs.ClaimCommand) (*jobs.Lease, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.claimed {
		return nil, nil
	}
	b.claimed = true
	return &jobs.Lease{
		Job: &job.Job{
			ID:       "claim-snap-job",
			Type:     "snap.test",
			Revision: 2,
			LeaseID:  "lease-1",
		},
		LeaseID: "lease-1",
	}, nil
}
func (b *claimSnapshotLeaseBroker) Renew(context.Context, jobs.RenewCommand) (*jobs.Lease, error) {
	return nil, nil
}
func (b *claimSnapshotLeaseBroker) Progress(context.Context, jobs.ProgressCommand) error { return nil }
func (b *claimSnapshotLeaseBroker) Complete(context.Context, jobs.CompleteCommand) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.complete++
	return nil
}
func (b *claimSnapshotLeaseBroker) CompleteWithArtifacts(context.Context, jobs.CompleteWithArtifactsCommand) ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.complete++
	return nil, nil
}
func (b *claimSnapshotLeaseBroker) Fail(context.Context, jobs.FailCommand) error { return nil }
func (b *claimSnapshotLeaseBroker) IsCancelled(context.Context, string, string) (bool, error) {
	return false, nil
}

var _ jobs.Broker = (*claimSnapshotLeaseBroker)(nil)

// workerClaimSnapshotter records the durable claim-time KPI input as seen by
// the worker's claim path.
type workerClaimSnapshotter struct {
	mu    sync.Mutex
	calls int
	last  job.PreparationClaimInput
}

func (r *workerClaimSnapshotter) SnapshotPreparationClaim(_ context.Context, input job.PreparationClaimInput) (*job.PreparationClaimSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.last = input
	return &job.PreparationClaimSnapshot{JobID: input.JobID, AttemptID: input.AttemptID}, nil
}

func (r *workerClaimSnapshotter) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func (r *workerClaimSnapshotter) latest() job.PreparationClaimInput {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

// TestRunner_CapturesClaimSnapshotOnRealClaim pins the ownership handoff: the
// durable prepared_at_claim_ratio KPI fires on the REAL broker.Claim() — not
// on the coordinator's queue peek. The snapshot must be captured with the real
// attempt identity (jobID:revision) before any unit executes.
func TestRunner_CapturesClaimSnapshotOnRealClaim(t *testing.T) {
	broker := &claimSnapshotLeaseBroker{}
	reg := NewRegistry()
	if err := reg.Register("snap.test", job.Handler(func(context.Context, *job.Job, *job.JobExecutionTools) (job.Result, error) {
		return map[string]any{}, nil
	})); err != nil {
		t.Fatalf("Register: %v", err)
	}
	workspace, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	snapshotter := &workerClaimSnapshotter{}

	runner := NewRunner(broker, reg, workspace, nil, zap.NewNop(), "worker-1", "session-1", []string{"snap.test"})
	runner.SetRenewInterval(minRenewInterval)
	runner.WithClaimSnapshotter(snapshotter)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = runner.Run(ctx)
	}()

	deadline := time.After(3 * time.Second)
	for snapshotter.count() == 0 {
		select {
		case <-deadline:
			t.Fatal("runner did not capture the claim snapshot after the real Claim()")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	<-done

	input := snapshotter.latest()
	if input.JobID != "claim-snap-job" {
		t.Errorf("JobID = %q, want claim-snap-job", input.JobID)
	}
	if input.AttemptID != "claim-snap-job:2" {
		t.Errorf("AttemptID = %q, want claim-snap-job:2 (real attempt identity)", input.AttemptID)
	}
	if input.JobRevision != 2 {
		t.Errorf("JobRevision = %d, want 2", input.JobRevision)
	}
	if input.ClaimedAt.IsZero() {
		t.Error("ClaimedAt must be set (the claim instant)")
	}
}

// TestRunner_ClaimSnapshot_NilIsTolerant — an unwired snapshotter keeps the
// legacy un-instrumented claim path working (no panic, no capture).
func TestRunner_ClaimSnapshot_NilIsTolerant(t *testing.T) {
	broker := &claimSnapshotLeaseBroker{}
	reg := NewRegistry()
	if err := reg.Register("snap.test", job.Handler(func(context.Context, *job.Job, *job.JobExecutionTools) (job.Result, error) {
		return map[string]any{}, nil
	})); err != nil {
		t.Fatalf("Register: %v", err)
	}
	workspace, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	runner := NewRunner(broker, reg, workspace, nil, zap.NewNop(), "worker-1", "session-1", []string{"snap.test"})
	runner.SetRenewInterval(minRenewInterval)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = runner.Run(ctx)
	}()
	deadline := time.After(3 * time.Second)
	for broker.completed() == 0 {
		select {
		case <-deadline:
			t.Fatal("worker did not complete the claimed job")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	<-done
}

func (b *claimSnapshotLeaseBroker) completed() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.complete
}

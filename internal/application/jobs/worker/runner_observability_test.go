// Package worker — runner_observability_test.go (FASE 2, August 2026).
//
// Pins the kernel-observability instrumentation of the remote worker
// runtime (runLease): every claimed lease produces one Run with
// queue_wait_ms, wall_time_ms, status and attempts, and the terminal
// classification is correct even when the broker ACCEPTS a failure
// report (tools.Fail returning nil must still close the run as
// FAILED — the return value alone cannot distinguish that from a
// successful Complete).
package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// obsCaptureRecorder is a thread-safe in-memory kernobs.Recorder.
type obsCaptureRecorder struct {
	mu      sync.Mutex
	reports []*kernobs.RunReport
}

func (c *obsCaptureRecorder) SaveReport(_ context.Context, rep *kernobs.RunReport) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reports = append(c.reports, rep)
	return nil
}

func (c *obsCaptureRecorder) last() *kernobs.RunReport {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.reports) == 0 {
		return nil
	}
	return c.reports[len(c.reports)-1]
}

// obsLeaseBroker is a minimal appjobs.Broker recording the terminal
// action taken by runLease (Fail vs Complete vs CompleteWithArtifacts).
type obsLeaseBroker struct {
	mu                     sync.Mutex
	failCalls              int
	completeCalls          int
	completeWithArtifacts  int
	failReturnErr          error
	completeReturnErr      error
	completeWithArtifactsE error
}

func (b *obsLeaseBroker) RegisterWorker(context.Context, appjobs.RegisterWorkerCommand) (*appjobs.WorkerSession, error) {
	return nil, nil
}
func (b *obsLeaseBroker) Heartbeat(context.Context, appjobs.HeartbeatCommand) error { return nil }
func (b *obsLeaseBroker) Claim(context.Context, appjobs.ClaimCommand) (*appjobs.Lease, error) {
	return nil, nil
}
func (b *obsLeaseBroker) Renew(_ context.Context, cmd appjobs.RenewCommand) (*appjobs.Lease, error) {
	return &appjobs.Lease{Job: &job.Job{ID: cmd.JobID, Type: "obs.test", Revision: 1, LeaseID: cmd.LeaseID}, LeaseID: cmd.LeaseID}, nil
}
func (b *obsLeaseBroker) Progress(context.Context, appjobs.ProgressCommand) error { return nil }
func (b *obsLeaseBroker) Complete(_ context.Context, _ appjobs.CompleteCommand) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.completeCalls++
	return b.completeReturnErr
}
func (b *obsLeaseBroker) CompleteWithArtifacts(_ context.Context, _ appjobs.CompleteWithArtifactsCommand) ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.completeWithArtifacts++
	return nil, b.completeWithArtifactsE
}
func (b *obsLeaseBroker) Fail(_ context.Context, _ appjobs.FailCommand) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failCalls++
	return b.failReturnErr
}
func (b *obsLeaseBroker) IsCancelled(context.Context, string, string) (bool, error) {
	return false, nil
}

func (b *obsLeaseBroker) failCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failCalls
}

// newObservedRunLeaseRunner builds a Runner with a registered handler,
// workspace and an attached RunObserver.
func newObservedRunLeaseRunner(t *testing.T, rec *obsCaptureRecorder, handler Handler) (*Runner, *obsLeaseBroker) {
	t.Helper()
	broker := &obsLeaseBroker{}
	reg := NewRegistry()
	if err := reg.Register("obs.test", handler); err != nil {
		t.Fatalf("Register: %v", err)
	}

	workspace, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}

	runner := NewRunner(broker, reg, workspace, nil, zap.NewNop(), "worker-1", "session-1", []string{"obs.test"})
	runner.SetRenewInterval(minRenewInterval)
	runner.WithObserver(kernobs.NewRunObserver(rec))
	return runner, broker
}

func obsTestLease(createdAt time.Time, startedAt time.Time, retryCount int) *appjobs.Lease {
	return &appjobs.Lease{
		Job: &job.Job{
			ID:         "obs-remote-job",
			Type:       "obs.test",
			Revision:   1,
			LeaseID:    "lease-obs-remote",
			RetryCount: retryCount,
			CreatedAt:  createdAt,
			StartedAt:  &startedAt,
		},
		LeaseID: "lease-obs-remote",
	}
}

// TestRunLease_ProducesRunReport — success path: runLease closes the
// run as SUCCEEDED with queue_wait_ms (created_at → started_at),
// wall_time_ms > 0, AttemptID is distinct from the lease fence, Counters.Retries = RetryCount.
func TestRunLease_ProducesRunReport(t *testing.T) {
	rec := &obsCaptureRecorder{}
	handler := func(_ context.Context, _ *job.Job, _ *appjobs.JobExecutionTools) (appjobs.Result, error) {
		return appjobs.Result{}, nil
	}
	runner, broker := newObservedRunLeaseRunner(t, rec, handler)

	createdAt := time.Now().Add(-8 * time.Second)
	startedAt := time.Now().Add(-3 * time.Second)
	err := runner.runLease(context.Background(), obsTestLease(createdAt, startedAt, 2))
	if err != nil {
		t.Fatalf("runLease: %v", err)
	}
	if broker.failCount() != 0 {
		t.Fatalf("fail calls = %d, want 0", broker.failCount())
	}

	rep := rec.last()
	if rep == nil {
		t.Fatal("no run report persisted")
	}
	if rep.Status != kernobs.StatusSucceeded {
		t.Fatalf("status = %q, want SUCCEEDED", rep.Status)
	}
	if rep.JobID != "obs-remote-job" || rep.AttemptID == "" || rep.AttemptID == "lease-obs-remote" {
		t.Fatalf("identity/attempt = %q/%q", rep.JobID, rep.AttemptID)
	}
	if rep.QueueWaitMs != 5000 {
		t.Fatalf("queue_wait_ms = %d, want 5000", rep.QueueWaitMs)
	}
	if rep.WallTimeMs <= 0 {
		t.Fatalf("wall_time_ms = %d, want > 0", rep.WallTimeMs)
	}
	if rep.Counters.Retries != 2 {
		t.Fatalf("counters.retries = %d, want 2", rep.Counters.Retries)
	}
}

// TestRunLease_FailedAttempt_FinishesFailed — when the handler fails and
// the broker ACCEPTS the failure report (tools.Fail returns nil, so
// runLease returns nil), the run must STILL close as FAILED (the
// terminalErr marker, not the return value, classifies the attempt).
func TestRunLease_FailedAttempt_FinishesFailed(t *testing.T) {
	rec := &obsCaptureRecorder{}
	handler := func(_ context.Context, _ *job.Job, _ *appjobs.JobExecutionTools) (appjobs.Result, error) {
		return nil, errors.New("handler exploded")
	}
	runner, broker := newObservedRunLeaseRunner(t, rec, handler)

	createdAt := time.Now().Add(-8 * time.Second)
	startedAt := time.Now().Add(-3 * time.Second)
	err := runner.runLease(context.Background(), obsTestLease(createdAt, startedAt, 0))
	if err != nil {
		t.Fatalf("runLease: %v", err)
	}
	if broker.failCount() != 1 {
		t.Fatalf("fail calls = %d, want 1", broker.failCount())
	}

	rep := rec.last()
	if rep == nil {
		t.Fatal("no run report persisted")
	}
	if rep.Status != kernobs.StatusFailed {
		t.Fatalf("status = %q, want FAILED (accepted failure report must not mask the attempt outcome)", rep.Status)
	}
	if rep.Error == "" {
		t.Fatal("failed run must carry the error message")
	}
}

// TestRunLease_NilObserver_Tolerant — a nil observer keeps the legacy
// un-instrumented runLease path (test fixtures without an observer
// keep working).
func TestRunLease_NilObserver_Tolerant(t *testing.T) {
	handler := func(_ context.Context, _ *job.Job, _ *appjobs.JobExecutionTools) (appjobs.Result, error) {
		return appjobs.Result{}, nil
	}
	runner, _ := newObservedRunLeaseRunner(t, nil, handler)
	runner.WithObserver(nil) // explicitly clear — legacy path

	createdAt := time.Now().Add(-8 * time.Second)
	startedAt := time.Now().Add(-3 * time.Second)
	if err := runner.runLease(context.Background(), obsTestLease(createdAt, startedAt, 0)); err != nil {
		t.Fatalf("runLease: %v", err)
	}
}

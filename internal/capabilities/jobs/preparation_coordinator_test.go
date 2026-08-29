package jobs

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type coordinatorNotifier struct{ ch chan struct{} }

func (n *coordinatorNotifier) Subscribe() <-chan struct{} { return n.ch }
func (n *coordinatorNotifier) Broadcast()                 { close(n.ch); n.ch = make(chan struct{}) }

type coordinatorReader struct {
	jobs  []job.Job
	calls atomic.Int32
}

func (r *coordinatorReader) PeekQueued(_ context.Context, _ int) ([]job.Job, error) {
	r.calls.Add(1)
	return r.jobs, nil
}

// fakeClaimSnapshotter records every SnapshotPreparationClaim call so the
// coordinator's KPI wiring is observable without a real store.
type fakeClaimSnapshotter struct {
	calls atomic.Int32
}

func (f *fakeClaimSnapshotter) SnapshotPreparationClaim(_ context.Context, _ job.PreparationClaimInput) (*job.PreparationClaimSnapshot, error) {
	f.calls.Add(1)
	return &job.PreparationClaimSnapshot{
		RequiredUnits:        2,
		ReadyUnits:           2,
		RunningUnits:         0,
		MissingUnits:         0,
		PreparedAtClaimRatio: 1.0,
		EstimatedSavedMS:     8500,
	}, nil
}

// TestPreparationCoordinator_DoesNotCaptureClaimSnapshots pins the ownership
// handoff: the claim-time KPI (prepared_at_claim_ratio) is captured by the
// WORKER claim path on the real ClaimNext/Claim() instant, NOT by the
// coordinator's queue peek (which is not a claim and would photograph a
// stale/guessed readiness state). The coordinator must never invoke the
// snapshotter during inspection.
func TestPreparationCoordinator_DoesNotCaptureClaimSnapshots(t *testing.T) {
	notifier := &coordinatorNotifier{ch: make(chan struct{})}
	reader := &coordinatorReader{jobs: []job.Job{{ID: "job-snap", Type: job.TypeScriptGenerate}}}
	registry, err := ComposeJobPreparationRegistry()
	if err != nil {
		t.Fatal(err)
	}
	gate := ActiveWorkFunc(func() bool { return true })
	scheduler := NewSpeculationScheduler(DefaultSpeculationConfig(), gate)
	snapshotter := &fakeClaimSnapshotter{}
	coordinator, err := NewPreparationCoordinator(reader, notifier, registry, scheduler, 3, func(context.Context, SpeculationCandidate) error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- coordinator.Start(ctx) }()
	deadline := time.After(time.Second)
	for reader.calls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("coordinator did not inspect initial queue")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	<-done
	if snapshotter.calls.Load() != 0 {
		t.Fatalf("coordinator captured %d claim snapshots during inspection; the worker claim path owns the snapshot", snapshotter.calls.Load())
	}
}

func TestPreparationCoordinator_UsesNotifierInsteadOfPolling(t *testing.T) {
	notifier := &coordinatorNotifier{ch: make(chan struct{})}
	reader := &coordinatorReader{jobs: []job.Job{{ID: "job-1", Type: job.TypeScriptGenerate}}}
	registry, err := ComposeJobPreparationRegistry()
	if err != nil {
		t.Fatal(err)
	}
	gate := ActiveWorkFunc(func() bool { return true })
	scheduler := NewSpeculationScheduler(DefaultSpeculationConfig(), gate)
	var executed atomic.Int32
	coordinator, err := NewPreparationCoordinator(reader, notifier, registry, scheduler, 3, func(context.Context, SpeculationCandidate) error { executed.Add(1); return nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- coordinator.Start(ctx) }()
	deadline := time.After(time.Second)
	for reader.calls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("coordinator did not inspect initial queue")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if reader.calls.Load() != 1 {
		t.Fatalf("initial peek calls=%d, want 1", reader.calls.Load())
	}
	notifier.Broadcast()
	deadline = time.After(time.Second)
	for reader.calls.Load() < 2 {
		select {
		case <-deadline:
			t.Fatal("notifier did not trigger second inspection")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("coordinator did not stop")
	}
	if executed.Load() == 0 {
		t.Fatal("coordinator did not execute admitted preparation units")
	}
}

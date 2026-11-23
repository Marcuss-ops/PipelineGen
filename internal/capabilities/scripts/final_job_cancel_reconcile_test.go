package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// cancelRaceAttacher models a Master job across the three local-cancel
// outcomes: already SUCCEEDED, still active, and finished while the cancel was
// in flight.
type cancelRaceAttacher struct {
	statuses  []string
	cancelErr error
	polls     int
	cancelled bool
	adopted   bool
}

func (f *cancelRaceAttacher) AttachFinalJob(_ context.Context, jobID string, _ bool) (RemoteFinalJobResult, error) {
	f.adopted = true
	return RemoteFinalJobResult{JobID: jobID, Status: "SUCCEEDED", ArtifactPath: "/tmp/final.mp4"}, nil
}

func (f *cancelRaceAttacher) FinalJobStatus(_ context.Context, jobID string) (RemoteFinalJobResult, error) {
	status := "RUNNING"
	if f.polls < len(f.statuses) {
		status = f.statuses[f.polls]
	}
	f.polls++
	if status == "SUCCEEDED" {
		return RemoteFinalJobResult{JobID: jobID, Status: status}, nil
	}
	return RemoteFinalJobResult{JobID: jobID, Status: status},
		fmt.Errorf("%w: %s", ErrFinalJobPending, status)
}

func (f *cancelRaceAttacher) CancelFinalJob(_ context.Context, _ string) error {
	f.cancelled = true
	return f.cancelErr
}

func TestReconcileInterruptedFinalJobAdoptsCompletedRender(t *testing.T) {
	fake := &cancelRaceAttacher{statuses: []string{"SUCCEEDED"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the local job is already cancelled

	got, err := reconcileInterruptedFinalJob(ctx, fake, "job_1")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got.Status != "SUCCEEDED" || !fake.adopted {
		t.Fatalf("receipt = %+v adopted=%v, want adopted SUCCEEDED", got, fake.adopted)
	}
	if fake.cancelled {
		t.Fatal("a completed render must not be cancelled remotely")
	}
}

func TestReconcileInterruptedFinalJobStopsActiveRender(t *testing.T) {
	fake := &cancelRaceAttacher{statuses: []string{"RUNNING"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := reconcileInterruptedFinalJob(ctx, fake, "job_2")
	if err == nil {
		t.Fatal("an active render must keep the run cancelled")
	}
	if !fake.cancelled {
		t.Fatal("an active render must be cancelled remotely")
	}
	if fake.adopted {
		t.Fatal("an active render has no artifact to adopt")
	}
}

func TestReconcileInterruptedFinalJobAdoptsAfterCancelRace(t *testing.T) {
	fake := &cancelRaceAttacher{
		statuses:  []string{"RUNNING", "SUCCEEDED"},
		cancelErr: ErrFinalJobCancelRejected,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := reconcileInterruptedFinalJob(ctx, fake, "job_3")
	if err != nil {
		t.Fatalf("reconcile after 409: %v", err)
	}
	if got.Status != "SUCCEEDED" || !fake.adopted {
		t.Fatalf("receipt = %+v adopted=%v, want adopted SUCCEEDED", got, fake.adopted)
	}
	if !errors.Is(fake.cancelErr, ErrFinalJobCancelRejected) {
		t.Fatalf("fake cancel error lost: %v", fake.cancelErr)
	}
}

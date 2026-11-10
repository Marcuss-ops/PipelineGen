package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// TestLeaseGenerationFencesStaleTerminalReport is the PostgreSQL parity of the
// memory test: the same worker re-claims after expiry, and the first attempt's
// generation can no longer transition the row.
func TestLeaseGenerationFencesStaleTransitions(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t, 10*time.Millisecond, 3)
	job := model.Job{ID: "fence-postgres", RenderPlan: []byte(`{}`)}
	if err := r.Submit(ctx, job); err != nil {
		t.Fatalf("submit: %v", err)
	}

	first, _, err := r.Claim(ctx, "w1")
	if err != nil || first == nil {
		t.Fatalf("first claim: job=%v err=%v", first, err)
	}
	if first.LeaseGeneration == 0 {
		t.Fatal("first claim did not allocate a lease generation")
	}
	firstGeneration := first.LeaseGeneration

	time.Sleep(25 * time.Millisecond)
	if n, err := r.RequeueExpired(ctx, time.Now()); err != nil || n != 1 {
		t.Fatalf("requeue expired: n=%d err=%v", n, err)
	}
	second, _, err := r.Claim(ctx, "w1")
	if err != nil || second == nil {
		t.Fatalf("second claim: job=%v err=%v", second, err)
	}
	if second.LeaseGeneration <= firstGeneration {
		t.Fatalf("generation did not advance: first=%d second=%d", firstGeneration, second.LeaseGeneration)
	}

	stale := model.Ownership{JobID: job.ID, WorkerID: "w1", Generation: firstGeneration}
	if err := r.Fail(ctx, stale.JobID, stale.WorkerID, stale.Generation, "late failure from expired attempt"); err == nil {
		t.Fatal("stale attempt was allowed to fail the re-claimed job")
	}
	got, err := r.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("get after stale report: %v", err)
	}
	if got.State != model.StateRunning || got.Worker != "w1" {
		t.Fatalf("stale report changed the job: state=%s worker=%s", got.State, got.Worker)
	}

	if err := r.Renew(ctx, job.ID, "w1", firstGeneration); err == nil {
		t.Fatal("stale attempt was allowed to renew the re-claimed job")
	}
	if err := r.SetProgress(ctx, job.ID, "w1", firstGeneration, model.Progress{FramesDone: 1, TotalFrames: 10}); err == nil {
		t.Fatal("stale attempt was allowed to write progress for the re-claimed job")
	}
	if err := r.Renew(ctx, job.ID, "w1", second.LeaseGeneration); err != nil {
		t.Fatalf("current renewal rejected: %v", err)
	}
	if err := r.SetProgress(ctx, job.ID, "w1", second.LeaseGeneration, model.Progress{FramesDone: 2, TotalFrames: 10}); err != nil {
		t.Fatalf("current progress rejected: %v", err)
	}

	current := model.Ownership{JobID: job.ID, WorkerID: "w1", Generation: second.LeaseGeneration}
	if err := r.Fail(ctx, current.JobID, current.WorkerID, current.Generation, "failure of the current attempt"); err != nil {
		t.Fatalf("current attempt report was rejected: %v", err)
	}
	got, err = r.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("get after current report: %v", err)
	}
	if got.State != model.StatePending {
		t.Fatalf("current attempt did not requeue the job: state=%s", got.State)
	}
}

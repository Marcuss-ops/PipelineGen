package memory

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// TestClaimSkipsJobsUntilNotBefore pins the deferred-scheduling contract of
// the in-memory backend: a job whose not_before is in the future must not be
// claimable, while a due (past) job behind it in FIFO order must be. The
// postgres claim query enforces the same predicate, so a divergence here would
// mean the two backends disagree about when a job is available.
func TestClaimSkipsJobsUntilNotBefore(t *testing.T) {
	repo := New(time.Minute, 3)

	future := time.Now().Add(time.Hour)
	if err := repo.Submit(context.Background(), model.Job{ID: "deferred", NotBefore: &future}); err != nil {
		t.Fatalf("Submit(deferred): %v", err)
	}

	// The only pending job is not due: a claim must return nothing and must
	// leave the job pending (still claimable later).
	job, _, err := repo.Claim(context.Background(), "worker-1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if job != nil {
		t.Fatalf("deferred job claimed before its not_before: %+v", job)
	}
	stored, err := repo.Get(context.Background(), "deferred")
	if err != nil {
		t.Fatalf("Get(deferred): %v", err)
	}
	if stored.State != model.StatePending {
		t.Fatalf("deferred job state = %s, want pending", stored.State)
	}

	// A due job queued behind it is claimed first (FIFO over the claimable set).
	past := time.Now().Add(-time.Minute)
	if err := repo.Submit(context.Background(), model.Job{ID: "due", NotBefore: &past}); err != nil {
		t.Fatalf("Submit(due): %v", err)
	}
	job, _, err = repo.Claim(context.Background(), "worker-1")
	if err != nil {
		t.Fatalf("Claim(due): %v", err)
	}
	if job == nil || job.ID != "due" {
		t.Fatalf("claim = %+v, want the due job", job)
	}
}

// TestJobWithoutNotBeforeIsImmediatelyClaimable pins backward compatibility:
// an unset not_before (every pre-existing producer) is claimable at once.
func TestJobWithoutNotBeforeIsImmediatelyClaimable(t *testing.T) {
	repo := New(time.Minute, 3)
	if err := repo.Submit(context.Background(), model.Job{ID: "immediate"}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	job, _, err := repo.Claim(context.Background(), "worker-1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if job == nil || job.ID != "immediate" {
		t.Fatalf("claim = %+v, want the immediate job", job)
	}
}

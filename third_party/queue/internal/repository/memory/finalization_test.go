package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

func TestClaimFinalizationHasSingleOwner(t *testing.T) {
	r := New(time.Minute, 3)
	if err := r.Submit(context.Background(), model.Job{ID: "parent", RenderPlan: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	// A finalization target is an assembly anchor: it must own at least one
	// chunk child. Without this the anchor invariant would reject the claim.
	if err := r.Submit(context.Background(), model.Job{
		ID: "parent-chunk-0", ParentJobID: "parent", ChunkIndex: 0,
		FrameRange: &model.FrameRange{Start: 0, End: 120}, RenderPlan: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	const claimers = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	owners := 0
	for i := 0; i < claimers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, claimed, err := r.ClaimFinalization(context.Background(), "parent", "worker")
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			if claimed {
				mu.Lock()
				owners++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if owners != 1 {
		t.Fatalf("owners = %d, want 1", owners)
	}
	job, err := r.Get(context.Background(), "parent")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.StateFinalizing {
		t.Fatalf("state = %q", job.State)
	}
}

// TestClaimFinalizationRequiresChunkChildren pins the repository-side anchor
// invariant. A finalizer claim rewrites Worker and LeaseUntil, so accepting an
// ordinary job would take it away from the render worker that still owns it.
// The check must live in the repository, not rely on every caller remembering
// to ask "is this an anchor?" first.
func TestClaimFinalizationRequiresChunkChildren(t *testing.T) {
	ctx := context.Background()
	r := New(time.Minute, 3)
	if err := r.Submit(ctx, model.Job{ID: "ordinary", RenderPlan: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}

	// Pending ordinary job: rejected, and nothing about it changes.
	if _, _, err := r.ClaimFinalization(ctx, "ordinary", "finalizer"); err == nil {
		t.Fatal("ClaimFinalization accepted a job with no chunk children")
	}
	job, err := r.Get(ctx, "ordinary")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.StatePending || job.Worker != "" || !job.LeaseUntil.IsZero() {
		t.Fatalf("rejected claim changed the job: state=%q worker=%q lease=%v", job.State, job.Worker, job.LeaseUntil)
	}

	// Running ordinary job: the dangerous case. The finalizer must not be able
	// to take the row (state, owner, lease) from its render worker.
	claimed, _, err := r.Claim(ctx, "renderer")
	if err != nil || claimed == nil {
		t.Fatalf("claim ordinary job: job=%+v err=%v", claimed, err)
	}
	lease := claimed.LeaseUntil
	if _, _, err := r.ClaimFinalization(ctx, "ordinary", "finalizer"); err == nil {
		t.Fatal("ClaimFinalization stole a running job with no chunk children")
	}
	job, err = r.Get(ctx, "ordinary")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.StateRunning || job.Worker != "renderer" {
		t.Fatalf("state/owner changed by rejected claim: state=%q worker=%q", job.State, job.Worker)
	}
	if !job.LeaseUntil.Equal(lease) {
		t.Fatalf("lease changed by rejected claim: %v -> %v", lease, job.LeaseUntil)
	}
}

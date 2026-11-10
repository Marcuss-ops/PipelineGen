package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// TestClaimFinalizationSQLRequiresChildren is the database-independent half of
// the anchor invariant: the UPDATE itself must restrict the transition to jobs
// that own chunk children. A caller-side check alone is not enough — the
// repository is what rewrites current_worker_id and lease_until.
func TestClaimFinalizationSQLRequiresChildren(t *testing.T) {
	if !strings.Contains(claimFinalizationSQL, "EXISTS") {
		t.Fatal("ClaimFinalization SQL has no EXISTS child predicate")
	}
	if !strings.Contains(claimFinalizationSQL, "child.parent_job_id = parent.id") {
		t.Fatal("ClaimFinalization SQL does not correlate children to the parent row")
	}
}

// TestClaimFinalizationRejectsNonAnchorRunningJob proves the transition leaves
// an ordinary claimed job untouched. Before the invariant, this call could
// change the owner and lease of a job another worker was still rendering.
func TestClaimFinalizationRejectsNonAnchorRunningJob(t *testing.T) {
	r := newRepo(t, 30*time.Second, 3)
	ctx := context.Background()
	if err := r.Submit(ctx, model.Job{ID: "ordinary", RenderPlan: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	claimed, _, err := r.Claim(ctx, "renderer")
	if err != nil || claimed == nil {
		t.Fatalf("claim: job=%+v err=%v", claimed, err)
	}
	before, err := r.Get(ctx, "ordinary")
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := r.ClaimFinalization(ctx, "ordinary", "finalizer"); err == nil {
		t.Fatal("ClaimFinalization accepted a job with no chunk children")
	}

	after, err := r.Get(ctx, "ordinary")
	if err != nil {
		t.Fatal(err)
	}
	if after.State != before.State || after.Worker != before.Worker {
		t.Fatalf("rejected claim changed state/owner: %q/%q -> %q/%q", before.State, before.Worker, after.State, after.Worker)
	}
	if !after.LeaseUntil.Equal(before.LeaseUntil) {
		t.Fatalf("rejected claim changed lease: %v -> %v", before.LeaseUntil, after.LeaseUntil)
	}
	if after.Attempts != before.Attempts {
		t.Fatalf("rejected claim changed attempt count: %d -> %d", before.Attempts, after.Attempts)
	}
}

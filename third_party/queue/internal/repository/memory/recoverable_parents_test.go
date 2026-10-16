package memory

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// TestRecoverableParents pins the parent-recovery scan that parent_completion
// consumes on every restart. The scan's answer defines which render families a
// restart can still drive to completion, so a wrong predicate either loses a
// crashed family forever (an unrecoverable parent skipped) or replays a
// finished one (a terminal parent returned).
func TestRecoverableParents(t *testing.T) {
	r := New(time.Minute, 3)
	ctx := context.Background()
	var plan = json.RawMessage(`{"n":1}`)

	submitFamily := func(id string) {
		if err := r.Submit(ctx, model.Job{ID: id, RenderPlan: plan}); err != nil {
			t.Fatalf("parent %s: %v", id, err)
		}
		child := model.Job{
			ID:          id + "-chunk-0",
			ParentJobID: id,
			ChunkIndex:  0,
			FrameRange:  &model.FrameRange{Start: 0, End: 120},
			RenderPlan:  plan,
		}
		if err := r.Submit(ctx, child); err != nil {
			t.Fatalf("child of %s: %v", id, err)
		}
	}

	// Recoverable: anchor pending, awaiting its chunks.
	submitFamily("parent-pending")

	// Recoverable: anchor finalizing — its finalizer crashed mid-drive and the
	// lease-recovery path requeued it; the scan must still list it so a
	// restart can finalize the family.
	submitFamily("parent-finalizing")
	if _, ok, err := r.ClaimFinalization(ctx, "parent-finalizing", "w1"); err != nil || !ok {
		t.Fatalf("claim finalization: ok=%t err=%v", ok, err)
	}

	// NOT recoverable: a terminal family. Built white-box because the anchor
	// rule makes a parent with children unreachable through the rendering
	// claim path (that is the rule the anchor exists for).
	submitFamily("parent-completed")
	r.mu.Lock()
	r.jobs["parent-completed"].State = model.StateCompleted
	r.mu.Unlock()

	// NOT recoverable: a childless pending job — no family to finalize.
	if err := r.Submit(ctx, model.Job{ID: "childless", RenderPlan: plan}); err != nil {
		t.Fatal(err)
	}

	ids, err := r.RecoverableParents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"parent-finalizing", "parent-pending"}
	if len(ids) != len(want) {
		t.Fatalf("recoverable parents = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("recoverable parents = %v, want %v", ids, want)
		}
	}
}

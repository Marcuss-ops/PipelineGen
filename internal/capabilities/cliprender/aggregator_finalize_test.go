package cliprender

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// clipParentFixture builds a parent job that passed through the submit phase:
// it carries the settle child id in its result and is waiting on aggregation.
func clipParentFixture(t *testing.T, parentJobID, parentType, childID string) *job.Job {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"parent_state": ParentStateWaitingChildren,
		"child_job_id": childID,
	})
	if err != nil {
		t.Fatalf("marshal parent result: %v", err)
	}
	return &job.Job{ID: parentJobID, Type: parentType, Result: raw, Revision: 1}
}

// TestParentAggregator_FinalizeParentFlipsTerminalParent is the acceptance
// criterion for event-driven completion: a single parent named by its terminal
// child turns terminal without a Tick.
func TestParentAggregator_FinalizeParentFlipsTerminalParent(t *testing.T) {
	jobs := newBenchJobsService()
	childID := "settle-child-1"
	jobs.Register(clipParentFixture(t, "parent-1", TypeClipRender, childID), childID)
	jobs.CompleteChild(childID, job.StatusSucceeded)

	agg := NewParentAggregator(jobs, zap.NewNop(), time.Second)
	if err := agg.FinalizeParent(context.Background(), "parent-1"); err != nil {
		t.Fatalf("finalize parent: %v", err)
	}
	if got, ok := jobs.finalized["parent-1"]; !ok || got != job.StatusSucceeded {
		t.Fatalf("parent status = %v (recorded=%v), want SUCCEEDED recorded", got, ok)
	}
	// Replay must be a harmless no-op: the notification can race the recovery
	// sweep, and every retry path re-enters here.
	if err := agg.FinalizeParent(context.Background(), "parent-1"); err != nil {
		t.Fatalf("replayed finalize must be a no-op, got %v", err)
	}
}

func TestParentAggregator_FinalizeParentFlipsFailedChildToFailedParent(t *testing.T) {
	jobs := newBenchJobsService()
	childID := "settle-child-2"
	jobs.Register(clipParentFixture(t, "parent-2", TypeClipRender, childID), childID)
	jobs.CompleteChild(childID, job.StatusFailed)

	agg := NewParentAggregator(jobs, zap.NewNop(), time.Second)
	if err := agg.FinalizeParent(context.Background(), "parent-2"); err != nil {
		t.Fatalf("finalize parent: %v", err)
	}
	if got := jobs.finalized["parent-2"]; got != job.StatusFailed {
		t.Fatalf("parent status = %v, want FAILED", got)
	}
}

// TestParentAggregator_FinalizeParentWaitsForRunningChild pins that the
// notification must not finalise a parent whose child is still running — the
// event fires on the child's terminal commit, and a mid-flight child means the
// parent is not eligible yet.
func TestParentAggregator_FinalizeParentWaitsForRunningChild(t *testing.T) {
	jobs := newBenchJobsService()
	childID := "settle-child-3"
	jobs.Register(clipParentFixture(t, "parent-3", TypeClipRender, childID), childID)

	agg := NewParentAggregator(jobs, zap.NewNop(), time.Second)
	if err := agg.FinalizeParent(context.Background(), "parent-3"); err != nil {
		t.Fatalf("finalize parent: %v", err)
	}
	if _, ok := jobs.finalized["parent-3"]; ok {
		t.Fatal("a running settle child must not finalise its parent")
	}
}

// TestParentAggregator_FinalizeParentIgnoresForeignParentTypes pins that the
// notifier's aggregator owns exactly one child type: other capabilities have
// their own (multi-child) aggregation semantics.
func TestParentAggregator_FinalizeParentIgnoresForeignParentTypes(t *testing.T) {
	jobs := newBenchJobsService()
	childID := "script-child"
	jobs.Register(clipParentFixture(t, "parent-4", "script.generate", childID), childID)
	jobs.CompleteChild(childID, job.StatusSucceeded)

	agg := NewParentAggregator(jobs, zap.NewNop(), time.Second)
	if err := agg.FinalizeParent(context.Background(), "parent-4"); err != nil {
		t.Fatalf("foreign parent type must be a silent no-op, got %v", err)
	}
	if _, ok := jobs.finalized["parent-4"]; ok {
		t.Fatal("a non-clip.render parent must not be finalised by the clip aggregator")
	}
}

// TestParentAggregator_FinalizeParentIgnoresEmptyID pins that a child without a
// parent link cannot cause work or an error.
func TestParentAggregator_FinalizeParentIgnoresEmptyID(t *testing.T) {
	agg := NewParentAggregator(newBenchJobsService(), zap.NewNop(), time.Second)
	if err := agg.FinalizeParent(context.Background(), ""); err != nil {
		t.Fatalf("empty parent id must be a no-op, got %v", err)
	}
	var nilAgg *ParentAggregator
	if err := nilAgg.FinalizeParent(context.Background(), "parent-x"); err != nil {
		t.Fatalf("nil aggregator must be a no-op, got %v", err)
	}
}

// TestParentAggregator_RecoveryTickStillFinalisesStrandedParent pins the
// durability net: the tick must still finalise a parent whose notification was
// lost (process died between the child commit and the notification).
func TestParentAggregator_RecoveryTickStillFinalisesStrandedParent(t *testing.T) {
	jobs := newBenchJobsService()
	childID := "settle-child-5"
	jobs.Register(clipParentFixture(t, "parent-5", TypeClipRender, childID), childID)
	jobs.CompleteChild(childID, job.StatusSucceeded)

	// No notification: only the sweeper runs.
	NewParentAggregator(jobs, zap.NewNop(), time.Second).Tick(context.Background())
	if got := jobs.finalized["parent-5"]; got != job.StatusSucceeded {
		t.Fatalf("recovery tick must finalise a stranded parent, got %v", got)
	}
}

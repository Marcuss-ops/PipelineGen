package batch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// aggregatorJobsStub is an ID-addressed jobs service: the aggregator resolves
// both the parent (by the id under finalisation) and the settle child (by the
// id recorded in the parent result).
type aggregatorJobsStub struct {
	parent        job.Job
	child         *job.Job
	finalized     bool
	finalizeCalls int
	status        job.Status
	result        map[string]any
	errMsg        string
}

func (s *aggregatorJobsStub) Get(_ context.Context, id string) (*job.Job, error) {
	if s.child != nil && id == s.child.ID {
		return s.child, nil
	}
	if id == s.parent.ID {
		parent := s.parent
		return &parent, nil
	}
	return nil, fmt.Errorf("aggregator stub: unknown job %s", id)
}

func (s *aggregatorJobsStub) ListAwaitingAggregation(context.Context, string, int) ([]job.Job, error) {
	if s.finalized {
		return nil, nil
	}
	return []job.Job{s.parent}, nil
}

func (s *aggregatorJobsStub) FinalizeAggregateParent(_ context.Context, _ string, status job.Status, result map[string]any, errMsg string, _ int) error {
	s.finalized, s.status, s.result, s.errMsg = true, status, result, errMsg
	s.finalizeCalls++
	return nil
}

// settlingStub builds a stub whose parent has fanned out to one settle child
// that is already terminal.
func settlingStub(t *testing.T, childStatus job.Status) *aggregatorJobsStub {
	t.Helper()
	parentResult, err := json.Marshal(map[string]any{
		"phase": "submitted", "parent_state": ParentStateWaitingChildren, "child_job_id": "settle-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return &aggregatorJobsStub{
		parent: job.Job{ID: "parent-1", Type: job.TypeClipRender, Result: parentResult, Revision: 7},
		child:  &job.Job{ID: "settle-1", Status: childStatus},
	}
}

// TestParentAggregatorDefaultIntervalIsRecoveryCadence pins the completion
// contract after the move to event-driven finalisation.
//
// The interval used to BE the completion mechanism — the parent only flipped on
// a tick, so the interval was added latency on every clip the caller waits on,
// and the default had to stay tiny. Now the settle child reports its parent the
// instant it commits, so the tick is the crash-recovery bound only; a generous
// interval is correct, and finalisation must not wait for it.
func TestParentAggregatorDefaultIntervalIsRecoveryCadence(t *testing.T) {
	agg := NewParentAggregator(&aggregatorJobsStub{}, zap.NewNop(), 0)
	if agg.interval != DefaultParentAggregationInterval {
		t.Fatalf("default interval = %v, want %v", agg.interval, DefaultParentAggregationInterval)
	}

	// A deliberately absurd interval: if finalisation still needed the tick,
	// this would not complete inside any reasonable test timeout.
	stub := settlingStub(t, job.StatusSucceeded)
	eventDriven := NewParentAggregator(stub, zap.NewNop(), time.Hour)
	started := time.Now()
	if err := eventDriven.FinalizeParent(context.Background(), stub.parent.ID); err != nil {
		t.Fatalf("FinalizeParent: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("event-driven finalisation took %v: it must not wait for the recovery interval", elapsed)
	}
	if !stub.finalized || stub.status != job.StatusSucceeded {
		t.Fatalf("finalized=%v status=%q, want the parent terminal immediately", stub.finalized, stub.status)
	}
}

// TestParentAggregatorFinalizesOrphanParentOnce pins CR-19-11: a parent whose
// submit died before EnqueueContinuation has no settle child and therefore no
// future terminal state. It must be finalised FAILED with an explicit reason on
// the first sweep, and the sweeper must not warn about it on every later tick.
func TestParentAggregatorFinalizesOrphanParentOnce(t *testing.T) {
	raw, err := json.Marshal(map[string]any{"parent_state": ParentStateWaitingChildren})
	if err != nil {
		t.Fatal(err)
	}
	stub := &aggregatorJobsStub{
		parent: job.Job{ID: "parent-orphan", Type: job.TypeClipRender, Result: raw, Revision: 3},
	}
	agg := NewParentAggregator(stub, zap.NewNop(), 0)

	agg.Tick(context.Background())
	if !stub.finalized || stub.status != job.StatusFailed {
		t.Fatalf("orphan parent finalized=%v status=%q, want FAILED", stub.finalized, stub.status)
	}
	if !strings.Contains(stub.errMsg, "child_job_id") {
		t.Fatalf("failure reason must name the missing settle child, got %q", stub.errMsg)
	}
	if stub.result["parent_state"] != "failed" {
		t.Fatalf("result parent_state = %v, want failed", stub.result["parent_state"])
	}

	// A second sweep must see no awaiting parent, so the terminal parent is not
	// re-finalised (and no warning loop can form).
	agg.Tick(context.Background())
	if stub.finalizeCalls != 1 {
		t.Fatalf("finalize calls = %d, want exactly 1", stub.finalizeCalls)
	}
}

func TestParentAggregatorFinalizesAfterSettleChild(t *testing.T) {
	stub := settlingStub(t, job.StatusSucceeded)
	agg := NewParentAggregator(stub, zap.NewNop(), 0)
	agg.Tick(context.Background())
	if !stub.finalized || stub.status != job.StatusSucceeded {
		t.Fatalf("finalized=%v status=%q result=%v", stub.finalized, stub.status, stub.result)
	}
	if stub.result["parent_state"] != "completed" || stub.result["settle_child_id"] != "settle-1" {
		t.Fatalf("aggregate result = %v", stub.result)
	}
}

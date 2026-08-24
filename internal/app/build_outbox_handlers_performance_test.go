package app

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

type stubProjectionService struct {
	projected []string
	err       error
}

func (s *stubProjectionService) ProjectCompletedJob(_ context.Context, jobID string) error {
	s.projected = append(s.projected, jobID)
	return s.err
}

func TestJobCompletedPerformanceAdapter_EventType(t *testing.T) {
	a := jobCompletedPerformanceAdapter{projection: &stubProjectionService{}, log: zap.NewNop()}
	if got := a.EventType(); got != outboxevents.EventJobCompleted {
		t.Fatalf("event type = %q, want %q", got, outboxevents.EventJobCompleted)
	}
	if got := a.IdempotencyKey(); got == "" {
		t.Fatal("idempotency key must be non-empty")
	}
}

func TestJobCompletedPerformanceAdapter_ProjectsAggregateID(t *testing.T) {
	stub := &stubProjectionService{}
	a := jobCompletedPerformanceAdapter{projection: stub, log: zap.NewNop()}
	if err := a.Handle(context.Background(), outboxevents.Event{AggregateID: "job-1", PayloadJSON: `{"job_id":"ignored"}`}); err != nil {
		t.Fatal(err)
	}
	if len(stub.projected) != 1 || stub.projected[0] != "job-1" {
		t.Fatalf("projected = %v, want [job-1]", stub.projected)
	}
}

func TestJobCompletedPerformanceAdapter_FallsBackToPayloadJobID(t *testing.T) {
	stub := &stubProjectionService{}
	a := jobCompletedPerformanceAdapter{projection: stub, log: zap.NewNop()}
	if err := a.Handle(context.Background(), outboxevents.Event{AggregateID: "", PayloadJSON: `{"job_id":"job-payload"}`}); err != nil {
		t.Fatal(err)
	}
	if len(stub.projected) != 1 || stub.projected[0] != "job-payload" {
		t.Fatalf("projected = %v, want [job-payload]", stub.projected)
	}
}

func TestJobCompletedPerformanceAdapter_MissingJobID(t *testing.T) {
	a := jobCompletedPerformanceAdapter{projection: &stubProjectionService{}, log: zap.NewNop()}
	if err := a.Handle(context.Background(), outboxevents.Event{}); err == nil {
		t.Fatal("expected missing job id error")
	}
}

func TestJobCompletedPerformanceAdapter_PropagatesProjectionError(t *testing.T) {
	stub := &stubProjectionService{err: errors.New("run not finalized yet")}
	a := jobCompletedPerformanceAdapter{projection: stub, log: zap.NewNop()}
	if err := a.Handle(context.Background(), outboxevents.Event{AggregateID: "job-1"}); err == nil {
		t.Fatal("expected projection error to propagate (retryable)")
	}
}

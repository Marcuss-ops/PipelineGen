package outbox

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type scriptQueuedJobLookup struct{ found bool }

func (s scriptQueuedJobLookup) Get(context.Context, string) (*job.Job, error) {
	if !s.found {
		return nil, nil
	}
	return &job.Job{ID: "job-1"}, nil
}

func TestScriptGenerateQueuedHandlerValidatesDurableJob(t *testing.T) {
	h, err := NewScriptGenerateQueuedHandler(scriptQueuedJobLookup{found: true})
	if err != nil {
		t.Fatal(err)
	}
	if h.EventType() != outboxevents.EventScriptGenerateQueued {
		t.Fatalf("event type = %q", h.EventType())
	}
	err = h.Handle(context.Background(), outboxevents.Event{PayloadJSON: `{"operation_id":"op-1","job_id":"job-1"}`})
	if err != nil {
		t.Fatal(err)
	}
}

func TestScriptGenerateQueuedHandlerRejectsMissingJob(t *testing.T) {
	h, err := NewScriptGenerateQueuedHandler(scriptQueuedJobLookup{})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Handle(context.Background(), outboxevents.Event{PayloadJSON: `{"operation_id":"op-1","job_id":"job-1"}`}); err == nil {
		t.Fatal("expected missing job error")
	}
}

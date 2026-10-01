package worker

import (
	"context"
	"encoding/json"
	"testing"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type progressCaptureBroker struct {
	appjobs.Broker
	command   appjobs.ProgressCommand
	event     map[string]any
	eventType string
}

func (b *progressCaptureBroker) AddEvent(_ context.Context, _ string, eventType, _ string, data map[string]any) error {
	b.eventType = eventType
	b.event = data
	return nil
}

func (b *progressCaptureBroker) Progress(_ context.Context, command appjobs.ProgressCommand) error {
	b.command = command
	return nil
}

func TestToolsProgressIncludesStructuredActivityData(t *testing.T) {
	broker := &progressCaptureBroker{}
	tools := NewTools(broker, nil, "worker-1", "session-1", &job.Job{
		ID: "job-1", Type: "script.generate", LeaseID: "lease-1", Revision: 7,
	}, "", nil)

	if err := tools.Progress(context.Background(), 42, "Resolving source material"); err != nil {
		t.Fatalf("Progress: %v", err)
	}
	if broker.command.Progress != 42 || broker.command.Message != "Resolving source material" {
		t.Fatalf("progress command = %+v, want progress/message preserved", broker.command)
	}
	if broker.command.ExpectedRevision != 7 || broker.command.JobID != "job-1" {
		t.Fatalf("progress command identity = %+v, want job-1 revision 7", broker.command)
	}

	var data map[string]any
	if err := json.Unmarshal(broker.command.Data, &data); err != nil {
		t.Fatalf("decode progress data: %v", err)
	}
	if data["kind"] != "script.generate" || data["status"] != "running" || data["detail"] != "Resolving source material" {
		t.Fatalf("activity envelope = %#v", data)
	}
	if data["sub_kind"] == "" {
		t.Fatalf("activity envelope has no sub_kind: %#v", data)
	}
	payload, ok := data["payload"].(map[string]any)
	if !ok || payload["progress"] != float64(42) || payload["message"] != "Resolving source material" {
		t.Fatalf("activity payload = %#v, want progress/message details", data["payload"])
	}
}

func TestToolsEventIncludesTraceEnvelope(t *testing.T) {
	broker := &progressCaptureBroker{}
	tools := NewTools(broker, broker, "worker-1", "session-1", &job.Job{
		ID: "job-1", Type: "script.generate", LeaseID: "lease-1", Revision: 7,
	}, "", nil)
	tools.traceCtx = job.WithActivityTrace(context.Background(), job.ActivityTrace{
		RunID: "run-1", AttemptID: "attempt-1", ParentRunID: "parent-run", CorrelationID: "corr-1",
	})
	if err := tools.Event(context.Background(), "stage_progress", "Stage progress updated", map[string]any{"stage": "translation", "item_id": "item-1"}); err != nil {
		t.Fatalf("Event: %v", err)
	}
	if broker.eventType != "stage_progress" || broker.event["kind"] != "script.generate" || broker.event["micro_kind"] != "translation" {
		t.Fatalf("event type/activity = %q %#v", broker.eventType, broker.event)
	}
	for key, want := range map[string]any{"run_id": "run-1", "attempt_id": "attempt-1", "parent_run_id": "parent-run", "correlation_id": "corr-1", "sequence": uint64(1)} {
		if broker.event[key] != want {
			t.Errorf("%s = %#v, want %#v", key, broker.event[key], want)
		}
	}
	if _, exists := broker.event["sequence"]; !exists {
		t.Fatalf("event does not expose sequence at top level: %#v", broker.event)
	}
}

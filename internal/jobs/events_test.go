package jobs

import (
	"context"
	"testing"
)

func TestCreateAndListEvents(t *testing.T) {
	_, eventsStore, cleanup := setupTestStoreWithEvents(t)
	defer cleanup()

	ctx := context.Background()

	event := &JobEvent{
		ID:      "event-001",
		JobID:   "job-001",
		Type:    EventTypeCreated,
		Message: "Job was created",
		DataJSON: `{}`,
	}

	err := eventsStore.CreateEvent(ctx, event)
	if err != nil {
		t.Fatalf("CreateEvent failed: %v", err)
	}

	events, err := eventsStore.ListEvents(ctx, "job-001")
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Type != EventTypeCreated {
		t.Errorf("expected event type %s, got %s", EventTypeCreated, events[0].Type)
	}
}

func TestListEventsMultiple(t *testing.T) {
	_, eventsStore, cleanup := setupTestStoreWithEvents(t)
	defer cleanup()

	ctx := context.Background()

	events := []*JobEvent{
		{ID: "event-001", JobID: "job-001", Type: EventTypeCreated, Message: "Created"},
		{ID: "event-002", JobID: "job-001", Type: EventTypeQueued, Message: "Queued"},
		{ID: "event-003", JobID: "job-001", Type: EventTypeRunning, Message: "Running"},
		{ID: "event-004", JobID: "job-002", Type: EventTypeCreated, Message: "Other job"},
	}

	for _, e := range events {
		e.DataJSON = `{}`
		if err := eventsStore.CreateEvent(ctx, e); err != nil {
			t.Fatalf("CreateEvent failed: %v", err)
		}
	}

	job1Events, err := eventsStore.ListEvents(ctx, "job-001")
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}

	if len(job1Events) != 3 {
		t.Errorf("expected 3 events for job-001, got %d", len(job1Events))
	}

	job2Events, err := eventsStore.ListEvents(ctx, "job-002")
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}

	if len(job2Events) != 1 {
		t.Errorf("expected 1 event for job-002, got %d", len(job2Events))
	}
}

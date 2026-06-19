package outboxhandlers_test

import (
	"context"
	"sync"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/outboxevents"
)

func TestWorkflowStepCompletedHandler_EventType(t *testing.T) {
	h := outboxhandlers.NewWorkflowStepCompletedHandler(zaptest.NewLogger(t))
	if got := h.EventType(); got != outboxevents.EventWorkflowStepCompleted {
		t.Errorf("expected %q got %q", outboxevents.EventWorkflowStepCompleted, got)
	}
}

func TestWorkflowStepCompletedHandler_ValidPayload_NoError(t *testing.T) {
	h := outboxhandlers.NewWorkflowStepCompletedHandler(zaptest.NewLogger(t))

	evt := outboxevents.Event{
		ID:           42,
		EventType:    outboxevents.EventWorkflowStepCompleted,
		AggregateID:  "workflow-123",
		PayloadJSON:  `{"workflow_id":"workflow-123","step_id":"step-4","status":"completed","correlation_id":"req-abc","duration_ms":1234,"actor_worker_id":"worker-1"}`,
		AttemptCount: 1,
	}

	if err := h.Handle(context.Background(), evt); err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
}

func TestWorkflowStepCompletedHandler_MinimalPayload_NoError(t *testing.T) {
	h := outboxhandlers.NewWorkflowStepCompletedHandler(zaptest.NewLogger(t))
	evt := outboxevents.Event{
		EventType:   outboxevents.EventWorkflowStepCompleted,
		AggregateID: "workflow-min",
		PayloadJSON: `{}`,
	}
	if err := h.Handle(context.Background(), evt); err != nil {
		t.Fatalf("expected nil err for empty payload, got %v", err)
	}
}

func TestWorkflowStepCompletedHandler_MalformedPayload_ReturnsError(t *testing.T) {
	h := outboxhandlers.NewWorkflowStepCompletedHandler(zaptest.NewLogger(t))
	evt := outboxevents.Event{
		EventType:   outboxevents.EventWorkflowStepCompleted,
		AggregateID: "workflow-bad",
		PayloadJSON: `{ not json`,
	}
	if err := h.Handle(context.Background(), evt); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestWorkflowStepFailedHandler_EventType(t *testing.T) {
	h := outboxhandlers.NewWorkflowStepFailedHandler(zaptest.NewLogger(t), nil)
	if got := h.EventType(); got != outboxevents.EventWorkflowStepFailed {
		t.Errorf("expected %q got %q", outboxevents.EventWorkflowStepFailed, got)
	}
}

func TestWorkflowStepFailedHandler_ValidPayload_HookFnCalled(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	lastWF := ""
	lastStep := ""
	lastErr := ""
	h := outboxhandlers.NewWorkflowStepFailedHandler(zaptest.NewLogger(t), func(wf, step, errMsg string) {
		mu.Lock()
		defer mu.Unlock()
		hits++
		lastWF = wf
		lastStep = step
		lastErr = errMsg
	})
	evt := outboxevents.Event{
		EventType:   outboxevents.EventWorkflowStepFailed,
		AggregateID: "workflow-1",
		PayloadJSON: `{"workflow_id":"workflow-1","step_id":"step-3","status":"failed","result_summary":"step timeout"}`,
	}
	if err := h.Handle(context.Background(), evt); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Fatalf("expected hookFn called once, got %d", hits)
	}
	if lastWF != "workflow-1" || lastStep != "step-3" || lastErr != "step timeout" {
		t.Errorf("hookFn params wrong: (%q, %q, %q)", lastWF, lastStep, lastErr)
	}
}

func TestWorkflowStepFailedHandler_NoHook_StillSucceeds(t *testing.T) {
	h := outboxhandlers.NewWorkflowStepFailedHandler(zap.NewNop(), nil)
	evt := outboxevents.Event{
		EventType:   outboxevents.EventWorkflowStepFailed,
		AggregateID: "workflow-2",
		PayloadJSON: `{"workflow_id":"workflow-2","step_id":"step-1","status":"failed"}`,
	}
	if err := h.Handle(context.Background(), evt); err != nil {
		t.Fatalf("expected nil (failure log is not a handler error), got %v", err)
	}
}

func TestStubHandlers_EventType(t *testing.T) {
	cases := []struct {
		name string
		h    outboxevents.Handler
		want string
	}{
		{"delivery", outboxhandlers.NewDeliveryHandlerStub(zap.NewNop()), outboxevents.EventDeliveryRequested},
		{"metadata_export", outboxhandlers.NewMetadataExportHandlerStub(zap.NewNop()), outboxevents.EventAssetMetadataExportRequested},
		{"provider_sync", outboxhandlers.NewProviderSyncHandlerStub(zap.NewNop()), outboxevents.EventProviderSyncRequested},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.h.EventType(); got != tc.want {
				t.Errorf("EventType: want %q got %q", tc.want, got)
			}
		})
	}
}

func TestStubHandlers_ReturnError(t *testing.T) {
	cases := []struct {
		name string
		h    outboxevents.Handler
	}{
		{"delivery", outboxhandlers.NewDeliveryHandlerStub(zap.NewNop())},
		{"metadata_export", outboxhandlers.NewMetadataExportHandlerStub(zap.NewNop())},
		{"provider_sync", outboxhandlers.NewProviderSyncHandlerStub(zap.NewNop())},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evt := outboxevents.Event{
				ID:            1,
				EventType:     tc.h.EventType(),
				AggregateID:   "agg-stub",
				AggregateType: "stub",
				PayloadJSON:   `{"k":"v"}`,
			}
			if err := tc.h.Handle(context.Background(), evt); err == nil {
				t.Errorf("stub handler should return error (not nil) so events go to dead_letter for operator visibility")
			}
		})
	}
}

func TestIndexingHandler_EventType(t *testing.T) {
	// nil indexer is OK for EventType test — Handle would panic but we only test the type
	h := &outboxhandlers.IndexingHandler{}
	if got := h.EventType(); got != outboxevents.EventAssetIndexRequested {
		t.Errorf("expected %q got %q", outboxevents.EventAssetIndexRequested, got)
	}
}

func TestIndexingHandler_EmptyPayload_ReturnsError(t *testing.T) {
	h := &outboxhandlers.IndexingHandler{}
	evt := outboxevents.Event{
		EventType:   outboxevents.EventAssetIndexRequested,
		AggregateID: "asset-123",
		PayloadJSON: `{}`,
	}
	if err := h.Handle(context.Background(), evt); err == nil {
		t.Fatal("expected error for empty asset_id")
	}
}

func TestIndexingHandler_MalformedPayload_ReturnsError(t *testing.T) {
	h := &outboxhandlers.IndexingHandler{}
	evt := outboxevents.Event{
		EventType:   outboxevents.EventAssetIndexRequested,
		AggregateID: "asset-bad",
		PayloadJSON: `not json`,
	}
	if err := h.Handle(context.Background(), evt); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

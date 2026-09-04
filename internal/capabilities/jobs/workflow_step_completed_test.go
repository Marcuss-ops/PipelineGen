package jobs

import (
	"context"
	"sync"
	"testing"

	finalize "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/finalize"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

func TestWorkflowStepCompletedHandler_EventType(t *testing.T) {
	h := finalize.NewWorkflowStepCompletedHandler(zaptest.NewLogger(t))
	if got := h.EventType(); got != outboxevents.EventWorkflowStepCompleted {
		t.Errorf("expected %q got %q", outboxevents.EventWorkflowStepCompleted, got)
	}
}

func TestWorkflowStepCompletedHandler_ValidPayload_NoError(t *testing.T) {
	h := finalize.NewWorkflowStepCompletedHandler(zaptest.NewLogger(t))

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
	h := finalize.NewWorkflowStepCompletedHandler(zaptest.NewLogger(t))
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
	h := finalize.NewWorkflowStepCompletedHandler(zaptest.NewLogger(t))
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
	h := finalize.NewWorkflowStepFailedHandler(zaptest.NewLogger(t), nil)
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
	h := finalize.NewWorkflowStepFailedHandler(zaptest.NewLogger(t), func(wf, step, errMsg string) {
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
	h := finalize.NewWorkflowStepFailedHandler(zap.NewNop(), nil)
	evt := outboxevents.Event{
		EventType:   outboxevents.EventWorkflowStepFailed,
		AggregateID: "workflow-2",
		PayloadJSON: `{"workflow_id":"workflow-2","step_id":"step-1","status":"failed"}`,
	}
	if err := h.Handle(context.Background(), evt); err != nil {
		t.Fatalf("expected nil (failure log is not a handler error), got %v", err)
	}
}

// RealHandlers_EventType asserts the EventType of the three real
// handlers (delivery / metadata_export / provider_sync) matches the
// canonical outboxevents constants. Replaces the legacy TestStubHandlers_*
// that referenced DeliveryHandlerStub / MetadataExportHandlerStub /
// ProviderSyncHandlerStub — those types were removed in the
// Operational Readiness PR alongside stubs.go.
//
// The regression intent ("the canonical event types must be wired")
// is preserved by the new no_stubs_test.go::TestRealHandlersRegistered_NotStubs
// and by this assertion.
func TestStubHandlers_EventType(t *testing.T) {
	cases := []struct {
		name string
		h    outboxevents.Handler
		want string
	}{
		{"delivery", NewDeliveryHandler(zap.NewNop(), nil, nil, nil, false), outboxevents.EventDeliveryRequested},
		{"metadata_export", NewMetadataExportHandler(MetadataExportHandlerDeps{Log: zap.NewNop(), OutputDir: t.TempDir()}), outboxevents.EventAssetMetadataExportRequested},
		{"provider_sync", NewProviderSyncHandler(zap.NewNop(), nil), outboxevents.EventProviderSyncRequested},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.h.EventType(); got != tc.want {
				t.Errorf("EventType: want %q got %q", tc.want, tc.want)
			}
		})
	}
}

// RealHandlers_InvalidPayload_ReturnsError exercises the real
// handlers' input-validation contract on a deliberately empty payload.
// The legacy stub tests asserted "stubs always error" — that was the
// legacy behaviour (always dead_letter, never do useful work). Now the
// real handlers accept valid payloads and only error on malformed ones,
// which is what we want. Validating this contract keeps a regression
// guard against a future PR accidentally removing the input-validation
// (which would silently route events to operations with empty payloads).
func TestStubHandlers_ReturnError(t *testing.T) {
	cases := []struct {
		name string
		h    outboxevents.Handler
	}{
		{"delivery", NewDeliveryHandler(zap.NewNop(), nil, nil, nil, false)},
		{"metadata_export", NewMetadataExportHandler(MetadataExportHandlerDeps{Log: zap.NewNop(), OutputDir: t.TempDir()})},
		{"provider_sync", NewProviderSyncHandler(zap.NewNop(), nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evt := outboxevents.Event{
				ID:          1,
				EventType:   tc.h.EventType(),
				AggregateID: "agg-real",
				PayloadJSON: `{}`, // empty payload — handlers must reject with error
			}
			if err := tc.h.Handle(context.Background(), evt); err == nil {
				t.Errorf("handler must reject empty payload (got nil error → would silently route to operations with empty data)")
			}
		})
	}
}

func TestIndexingHandler_EventType(t *testing.T) {
	// nil indexer is OK for EventType test — Handle would panic but we only test the type
	h := &IndexingHandler{}
	if got := h.EventType(); got != outboxevents.EventAssetIndexRequested {
		t.Errorf("expected %q got %q", outboxevents.EventAssetIndexRequested, got)
	}
}

func TestIndexingHandler_EmptyPayload_ReturnsError(t *testing.T) {
	h := &IndexingHandler{}
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
	h := &IndexingHandler{}
	evt := outboxevents.Event{
		EventType:   outboxevents.EventAssetIndexRequested,
		AggregateID: "asset-bad",
		PayloadJSON: `not json`,
	}
	if err := h.Handle(context.Background(), evt); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

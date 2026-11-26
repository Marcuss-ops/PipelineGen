package finalize

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	outbox "github.com/Marcuss-ops/PipelineGen/internal/kernel/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/pkg/sqlport"
)

// The concrete SQLite repository must keep satisfying the capability-owned
// port without a wrapper: the composition root passes it unchanged.
var _ OutboxEnqueuer = (*outboxevents.Repository)(nil)

type enqueuedEvent struct {
	eventType     string
	aggregateID   string
	aggregateType string
	payloadJSON   string
	eventKey      string
}

type fakeEnqueuer struct {
	events []enqueuedEvent
	err    error
}

func (f *fakeEnqueuer) Enqueue(
	_ context.Context,
	_ sqlport.Tx,
	eventType, aggregateID, aggregateType, payloadJSON, eventKey string,
) (*outbox.EnqueueResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.events = append(f.events, enqueuedEvent{
		eventType:     eventType,
		aggregateID:   aggregateID,
		aggregateType: aggregateType,
		payloadJSON:   payloadJSON,
		eventKey:      eventKey,
	})
	return &outbox.EnqueueResult{Inserted: true}, nil
}

func newTestFinalizer(outboxPort OutboxEnqueuer) *Finalizer {
	return &Finalizer{outbox: outboxPort, log: zap.NewNop()}
}

func TestWriteOutboxEventsUsesThePortWithEmptyPayloadFallback(t *testing.T) {
	fake := &fakeEnqueuer{}
	f := newTestFinalizer(fake)

	err := f.writeOutboxEvents(context.Background(), nil, []finalization.OutboxEvent{
		{EventType: "asset.index.requested.v1", AggregateID: "asset-1", EventKey: "k1"},
		{EventType: "asset.published.v1", AggregateID: "asset-2", EventKey: "k2", Payload: []byte("null")},
	})
	if err != nil {
		t.Fatalf("writeOutboxEvents: %v", err)
	}
	if len(fake.events) != 2 {
		t.Fatalf("enqueued %d events, want 2", len(fake.events))
	}
	for i, evt := range fake.events {
		if evt.payloadJSON != "{}" {
			t.Fatalf("event[%d] payload = %q, want the empty-object fallback", i, evt.payloadJSON)
		}
		if evt.aggregateType != "" {
			t.Fatalf("event[%d] aggregateType = %q, want empty (asset events)", i, evt.aggregateType)
		}
	}
	if fake.events[0].eventType != "asset.index.requested.v1" || fake.events[0].eventKey != "k1" {
		t.Fatalf("event[0] = %+v, want the input event verbatim", fake.events[0])
	}
}

func TestWriteOutboxEventsPropagatesPortErrors(t *testing.T) {
	boom := errors.New("outbox unavailable")
	f := newTestFinalizer(&fakeEnqueuer{err: boom})

	err := f.writeOutboxEvents(context.Background(), nil, []finalization.OutboxEvent{
		{EventType: "asset.index.requested.v1", AggregateID: "asset-1"},
	})
	if !errors.Is(err, boom) {
		t.Fatalf("writeOutboxEvents error = %v, want wrapped %v", err, boom)
	}
}

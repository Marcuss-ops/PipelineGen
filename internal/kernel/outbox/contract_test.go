package outbox

import (
	"context"
	"testing"
)

type fakeHandler struct {
	eventType string
	idemKey   string
	handled   []Event
	err       error
}

func (f *fakeHandler) EventType() string { return f.eventType }

func (f *fakeHandler) IdempotencyKey() string { return f.idemKey }

func (f *fakeHandler) Handle(_ context.Context, evt Event) error {
	f.handled = append(f.handled, evt)
	return f.err
}

// The registry is the capability-facing half of the outbox contract: a
// consumer must be able to register and dispatch a handler without the SQL
// adapter in its import graph.
func TestHandlerRegistryRegistersAndDispatchesThroughTheFake(t *testing.T) {
	registry := NewHandlerRegistry()
	h := &fakeHandler{eventType: EventAssetPublished, idemKey: "asset.published.v1"}
	if err := registry.Register(h); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if registry.Len() != 1 {
		t.Fatalf("Len = %d, want 1", registry.Len())
	}
	got, ok := registry.Get(EventAssetPublished)
	if !ok || got != h {
		t.Fatalf("Get(%q) = (%v, %v), want the registered fake", EventAssetPublished, got, ok)
	}
	if err := got.Handle(context.Background(), Event{ID: 7, AggregateID: "asset-1"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(h.handled) != 1 || h.handled[0].ID != 7 {
		t.Fatalf("fake did not receive the event: %+v", h.handled)
	}
}

func TestHandlerRegistryRejectsInvalidRegistrations(t *testing.T) {
	registry := NewHandlerRegistry()
	if err := registry.Register(nil); err == nil {
		t.Fatal("Register(nil) = nil error, want rejection")
	}
	if err := registry.Register(&fakeHandler{idemKey: "x"}); err == nil {
		t.Fatal("Register(empty event type) = nil error, want rejection")
	}
	first := &fakeHandler{eventType: EventDeliveryRequested, idemKey: "delivery.v1"}
	if err := registry.Register(first); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := registry.Register(&fakeHandler{eventType: EventDeliveryRequested, idemKey: "delivery.v1"}); err == nil {
		t.Fatal("duplicate Register = nil error, want rejection")
	}
}

func TestHandlerRegistryPanicsOnEmptyIdempotencyKey(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Register with empty IdempotencyKey did not panic (fail-closed at init)")
		}
	}()
	_ = NewHandlerRegistry().Register(&fakeHandler{eventType: EventDeliveryRequested})
}

func TestDecodeV1MakesEnvelopeViolationsTerminal(t *testing.T) {
	err := DecodeV1(DecodePolicy{
		EventName:         "asset.published",
		WantSchemaVersion: "asset.published.v1",
	}, Event{PayloadJSON: `{"schema_version":"asset.published.v0","event_id":"e","idempotency_key":"k"}`}, nil)
	if err == nil {
		t.Fatal("DecodeV1 accepted a schema mismatch")
	}
	if !IsTerminal(err) {
		t.Fatalf("DecodeV1 violation is not terminal: %v", err)
	}
}

func TestJobCompletedEventKeyIsTheJobIDAlone(t *testing.T) {
	if got, want := JobCompletedEventKey("job-1"), "job.completed:job-1"; got != want {
		t.Fatalf("JobCompletedEventKey = %q, want %q", got, want)
	}
}

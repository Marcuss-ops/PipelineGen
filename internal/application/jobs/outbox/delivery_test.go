package outbox_test

import (
	"context"
	"errors"
	"testing"

	assetdelivery "github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	outboxhandlers "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"go.uber.org/zap"
)

type deliveryRemoteRegistrarStub struct {
	called bool
	req    assetdelivery.RemoteReferenceRequest
	err    error
}

func (s *deliveryRemoteRegistrarStub) RegisterRemoteReference(_ context.Context, req assetdelivery.RemoteReferenceRequest) error {
	s.called = true
	s.req = req
	return s.err
}

type deliveryMaterializerStub struct {
	called bool
	req    assetdelivery.MaterializationRequest
	err    error
}

func (s *deliveryMaterializerStub) MaterializeLocal(_ context.Context, req assetdelivery.MaterializationRequest) error {
	s.called = true
	s.req = req
	return s.err
}

func deliveryEvent(operation string) outboxevents.Event {
	return outboxevents.Event{
		ID:          7,
		EventType:   outboxevents.EventDeliveryRequested,
		AggregateID: "asset-1",
		PayloadJSON: `{"schema_version":"delivery.requested.v1","event_id":"evt-1","occurred_at":"2026-08-06T12:00:00Z","job_id":"job-1","artifact":{"artifact_id":"asset-1","remote_url":"https://example.test/asset.mp4","filename":"asset.mp4","storage_key":"asset-1/asset.mp4","sha256":"abc","size_bytes":42,"content_type":"video/mp4"},"destination":{"provider":"drive","destination_id":"folder-1","account_id":"account-1"},"idempotency_key":"delivery-1","operation":"` + operation + `"}`,
	}
}

func TestDeliveryHandler_RegisterRemoteReferenceUsesDedicatedPort(t *testing.T) {
	registrar := &deliveryRemoteRegistrarStub{}
	h := outboxhandlers.NewDeliveryHandlerWithOperations(zap.NewNop(), nil, nil, nil, false, outboxhandlers.DeliveryOperation{
		RemoteRegistrar: registrar,
	})

	if err := h.Handle(context.Background(), deliveryEvent(string(assetdelivery.OperationRegisterRemoteReference))); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if !registrar.called {
		t.Fatal("remote registrar was not called")
	}
	if registrar.req.RemoteURL != "https://example.test/asset.mp4" || registrar.req.AssetID != "asset-1" || registrar.req.IdempotencyKey != "delivery-1" {
		t.Fatalf("remote reference request = %#v", registrar.req)
	}
}

func TestDeliveryHandler_MaterializeLocalUsesDedicatedPort(t *testing.T) {
	materializer := &deliveryMaterializerStub{}
	h := outboxhandlers.NewDeliveryHandlerWithOperations(zap.NewNop(), nil, nil, nil, false, outboxhandlers.DeliveryOperation{
		Materializer: materializer,
	})

	if err := h.Handle(context.Background(), deliveryEvent(string(assetdelivery.OperationMaterializeLocal))); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if !materializer.called {
		t.Fatal("materializer was not called")
	}
	if materializer.req.RemoteURL != "https://example.test/asset.mp4" || materializer.req.StorageKey != "asset-1/asset.mp4" || materializer.req.IdempotencyKey != "delivery-1" {
		t.Fatalf("materialization request = %#v", materializer.req)
	}
}

func TestDeliveryHandler_RemoteRegistrarErrorPropagatesRetryable(t *testing.T) {
	wantErr := errors.New("repository busy")
	registrar := &deliveryRemoteRegistrarStub{err: wantErr}
	h := outboxhandlers.NewDeliveryHandlerWithOperations(zap.NewNop(), nil, nil, nil, false, outboxhandlers.DeliveryOperation{RemoteRegistrar: registrar})

	err := h.Handle(context.Background(), deliveryEvent(string(assetdelivery.OperationRegisterRemoteReference)))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Handle() error = %v, want %v", err, wantErr)
	}
	if outboxevents.IsTerminal(err) {
		t.Fatalf("registrar error must remain retryable: %v", err)
	}
}

func TestDeliveryHandler_MaterializerErrorPropagatesRetryable(t *testing.T) {
	wantErr := errors.New("download temporarily unavailable")
	materializer := &deliveryMaterializerStub{err: wantErr}
	h := outboxhandlers.NewDeliveryHandlerWithOperations(zap.NewNop(), nil, nil, nil, false, outboxhandlers.DeliveryOperation{Materializer: materializer})

	err := h.Handle(context.Background(), deliveryEvent(string(assetdelivery.OperationMaterializeLocal)))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Handle() error = %v, want %v", err, wantErr)
	}
	if outboxevents.IsTerminal(err) {
		t.Fatalf("materializer error must remain retryable: %v", err)
	}
}

func TestDeliveryHandler_ExplicitOperationWithoutPortFailsClosed(t *testing.T) {
	h := outboxhandlers.NewDeliveryHandler(zap.NewNop(), nil, nil, nil, false)

	err := h.Handle(context.Background(), deliveryEvent(string(assetdelivery.OperationMaterializeLocal)))
	if !errors.Is(err, outboxhandlers.ErrOperationUnavailable) {
		t.Fatalf("Handle() error = %v, want ErrOperationUnavailable", err)
	}
	if !outboxevents.IsTerminal(err) {
		t.Fatalf("operation-unavailable error must be terminal: %v", err)
	}
}

func TestDeliveryHandler_RemoteURLWithoutOperationDoesNotUseLegacyAck(t *testing.T) {
	h := outboxhandlers.NewDeliveryHandler(zap.NewNop(), nil, nil, nil, false)
	evt := deliveryEvent("")
	if err := h.Handle(context.Background(), evt); !errors.Is(err, outboxhandlers.ErrRemoteURLRequired) {
		t.Fatalf("Handle() error = %v, want ErrRemoteURLRequired", err)
	}
}

func TestDeliveryHandler_LegacyDriveEnvelopeKeepsCompatibilityAck(t *testing.T) {
	h := outboxhandlers.NewDeliveryHandler(zap.NewNop(), nil, nil, nil, false)
	evt := deliveryEvent("")
	// The legacy envelope omits operation entirely rather than encoding an
	// empty operation value.
	evt.PayloadJSON = `{"schema_version":"delivery.requested.v1","event_id":"evt-1","occurred_at":"2026-08-06T12:00:00Z","job_id":"job-1","artifact":{"artifact_id":"asset-1","sha256":"abc"},"destination":{"provider":"drive","destination_id":"folder-1"},"idempotency_key":"delivery-legacy"}`

	if err := h.Handle(context.Background(), evt); err != nil {
		t.Fatalf("legacy Handle() error = %v", err)
	}
}

func TestDeliveryHandler_OperationOnWebhookFailsClosed(t *testing.T) {
	h := outboxhandlers.NewDeliveryHandler(zap.NewNop(), nil, nil, nil, false)
	evt := deliveryEvent(string(assetdelivery.OperationMaterializeLocal))
	evt.PayloadJSON = `{"schema_version":"delivery.requested.v1","event_id":"evt-1","occurred_at":"2026-08-06T12:00:00Z","job_id":"job-1","artifact":{"artifact_id":"asset-1","remote_url":"https://example.test/asset.mp4","sha256":"abc"},"destination":{"provider":"webhook","destination_id":"https://example.test/hook"},"idempotency_key":"delivery-webhook","operation":"materialize_local"}`
	if err := h.Handle(context.Background(), evt); !errors.Is(err, outboxhandlers.ErrOperationProviderMismatch) {
		t.Fatalf("Handle() error = %v, want ErrOperationProviderMismatch", err)
	}
}

func TestDeliveryHandler_UnknownOperationFailsClosed(t *testing.T) {
	h := outboxhandlers.NewDeliveryHandler(zap.NewNop(), nil, nil, nil, false)

	err := h.Handle(context.Background(), deliveryEvent("fetch_and_publish"))
	if !errors.Is(err, outboxhandlers.ErrUnsupportedOperation) {
		t.Fatalf("Handle() error = %v, want ErrUnsupportedOperation", err)
	}
}

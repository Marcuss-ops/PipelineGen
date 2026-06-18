package outboxhandlers

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"velox/go-master/internal/repository/outboxevents"
)

// DeliveryHandlerStub is a placeholder for "delivery.requested". The
// real implementation will dispatch delivery to Drive/YouTube/voiceover
// etc. based on the payload. For now it returns an error so the outbox
// pool retries and eventually dead-letters the event — operators can
// inspect dead_letter rows instead of silently draining delivery events.
type DeliveryHandlerStub struct {
	log *zap.Logger
}

// NewDeliveryHandlerStub creates a stub handler with the supplied logger.
func NewDeliveryHandlerStub(log *zap.Logger) *DeliveryHandlerStub {
	return &DeliveryHandlerStub{log: log}
}

func (h *DeliveryHandlerStub) EventType() string {
	return outboxevents.EventDeliveryRequested
}

func (h *DeliveryHandlerStub) Handle(ctx context.Context, evt outboxevents.Event) error {
	h.log.Warn("delivery.requested handler is a stub — retrying until dead_letter",
		zap.Int64("event_id", evt.ID),
		zap.String("aggregate_id", evt.AggregateID),
		zap.String("aggregate_type", evt.AggregateType),
		zap.Int("attempt", evt.AttemptCount),
		zap.String("payload_preview", previewPayload(evt.PayloadJSON)),
	)
	return fmt.Errorf("delivery.requested: stub handler (not yet implemented)")
}

// MetadataExportHandlerStub is a placeholder for "asset.metadata.export.requested".
type MetadataExportHandlerStub struct {
	log *zap.Logger
}

// NewMetadataExportHandlerStub creates a stub handler with the supplied logger.
func NewMetadataExportHandlerStub(log *zap.Logger) *MetadataExportHandlerStub {
	return &MetadataExportHandlerStub{log: log}
}

func (h *MetadataExportHandlerStub) EventType() string {
	return outboxevents.EventAssetMetadataExportRequested
}

func (h *MetadataExportHandlerStub) Handle(ctx context.Context, evt outboxevents.Event) error {
	h.log.Warn("asset.metadata.export.requested handler is a stub — retrying until dead_letter",
		zap.Int64("event_id", evt.ID),
		zap.String("aggregate_id", evt.AggregateID),
		zap.String("aggregate_type", evt.AggregateType),
		zap.Int("attempt", evt.AttemptCount),
		zap.String("payload_preview", previewPayload(evt.PayloadJSON)),
	)
	return fmt.Errorf("asset.metadata.export.requested: stub handler (not yet implemented)")
}

// ProviderSyncHandlerStub is a placeholder for "provider.sync.requested"
// (Artlist/YouTube/Drive provider sync events).
type ProviderSyncHandlerStub struct {
	log *zap.Logger
}

// NewProviderSyncHandlerStub creates a stub handler with the supplied logger.
func NewProviderSyncHandlerStub(log *zap.Logger) *ProviderSyncHandlerStub {
	return &ProviderSyncHandlerStub{log: log}
}

func (h *ProviderSyncHandlerStub) EventType() string {
	return outboxevents.EventProviderSyncRequested
}

func (h *ProviderSyncHandlerStub) Handle(ctx context.Context, evt outboxevents.Event) error {
	h.log.Warn("provider.sync.requested handler is a stub — retrying until dead_letter",
		zap.Int64("event_id", evt.ID),
		zap.String("aggregate_id", evt.AggregateID),
		zap.String("aggregate_type", evt.AggregateType),
		zap.Int("attempt", evt.AttemptCount),
		zap.String("payload_preview", previewPayload(evt.PayloadJSON)),
	)
	return fmt.Errorf("provider.sync.requested: stub handler (not yet implemented)")
}

// previewPayload returns the first min(len, 200) chars of a payload so
// stub logs are useful without flooding on long metadata exports.
func previewPayload(payload string) string {
	const max = 200
	if len(payload) <= max {
		return payload
	}
	return payload[:max] + "…"
}

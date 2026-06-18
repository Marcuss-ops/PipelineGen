package outboxevents

import (
	"context"
	"fmt"
	"sync"
)

const (
	EventAssetIndexRequested         = "asset.index.requested"
	EventDeliveryRequested           = "delivery.requested"
	EventAssetMetadataExportRequested = "asset.metadata_export.requested"
	EventProviderSyncRequested       = "provider.sync.requested"
	EventWorkflowStepCompleted       = "workflow.step.completed"
	EventWorkflowStepFailed          = "workflow.step.failed"
)

type Handler interface {
	EventType() string
	Handle(ctx context.Context, evt Event) error
}

type HandlerRegistry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{handlers: make(map[string]Handler)}
}

func (r *HandlerRegistry) Register(h Handler) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h == nil {
		return fmt.Errorf("handler is nil")
	}
	key := h.EventType()
	if key == "" {
		return fmt.Errorf("handler event type is empty")
	}
	if _, exists := r.handlers[key]; exists {
		return fmt.Errorf("handler already registered for %s", key)
	}
	r.handlers[key] = h
	return nil
}

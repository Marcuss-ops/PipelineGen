package outboxevents

import (
	"context"
	"fmt"
	"sync"
)

// event-type constants are shared between producers (Dispatcher.Enqueue,
// EnqueueDeleteAndDelete) and consumers (Handler.EventType). The ticket
// docs use "media.index.*" in prose; the codebase standard adopted here
// is "asset.index.*" because that prefix matches the sibling Indexing
// event (asset.index.requested) and keeps greppability symmetric across
// the outbox-events column. The QDRANT-002 ticket's "media.index.*"
// naming maps to these constants 1:1.
const (
	EventAssetIndexRequested          = "asset.index.requested"
	EventAssetIndexDeleteRequested    = "asset.index.delete_requested"
	EventDeliveryRequested            = "delivery.requested"
	EventAssetMetadataExportRequested = "asset.metadata_export.requested"
	EventProviderSyncRequested        = "provider.sync.requested"
	EventWorkflowStepCompleted        = "workflow.step.completed"
	EventWorkflowStepFailed           = "workflow.step.failed"
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

func (r *HandlerRegistry) Get(eventType string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[eventType]
	return h, ok
}

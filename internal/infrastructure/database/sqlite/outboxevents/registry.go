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
	EventAssetIndexRequested       = "asset.index.requested"
	EventAssetIndexDeleteRequested = "asset.index.delete_requested"
	// EventAssetIndexRestoreRequested is the canonical event-type
	// emitted by mutations.AssetMutationDispatcher.EnqueueAndRestore.
	// Handler (deferred to task 3 of 5, currently
	// mutations.AssetMutationDispatcher is foundation-only) consumes
	// this event and completes the picture with Qdrant re-upsert +
	// lifecycle_state flip back to 'ready'.
	//
	// Naming follows the established asset.index.* family so a single
	// substring search finds the producer + consumer + tests on the
	// same grep pass.
	EventAssetIndexRestoreRequested   = "asset.index.restore_requested"
	EventDeliveryRequested            = "delivery.requested"
	EventAssetMetadataExportRequested = "asset.metadata_export.requested"
	EventProviderSyncRequested        = "provider.sync.requested"
	EventWorkflowStepCompleted        = "workflow.step.completed"
	EventWorkflowStepFailed           = "workflow.step.failed"

	// EventVoiceoverCleanupRequested (P0.7 Wave 21, Step 10/12, June 2026).
	// Replaces the pre-fix fire-and-forget `cleanupOrphanVoiceover`
	// goroutine (detached via context.Background) with a durable
	// outbox event that survives handler cancel + server restart.
	// Producer (voiceover.finalizeStage) enqueues this event INSIDE
	// the same SQL tx as the voiceovers UPSERT + media_assets
	// projection UPSERT, so all four writes commit atomically; a
	// rollback discards all four. The consumer
	// (voiceover.outbox.VoiceoverCleanupHandler) deletes OLD Drive
	// files ONLY when `old_drive_file_id != new_drive_file_id`,
	// removes old local files, and returns retryable errors on
	// transient Drive failures so the pool's exponential backoff
	// retries per its config.
	EventVoiceoverCleanupRequested = "voiceover.cleanup.requested"
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

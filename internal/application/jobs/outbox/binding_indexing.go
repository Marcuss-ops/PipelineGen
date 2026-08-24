// Package outbox — binding_indexing.go carries the consumer handler for
// `binding.index.requested` events.
//
// The handler is the async half of the canonical binding mutation
// dispatcher. Every SQLite write to media_bindings emits this event in
// the same transaction; this handler consumes it and reindexes the
// parent concept in Qdrant so the semantic projection stays
// consistent with the authoritative SQLite state.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// BindingIndexRequestSchemaVersion is the only schema version this
// handler accepts.
const BindingIndexRequestSchemaVersion = "binding.index.requested.v1"

// BindingIndexer is the narrow surface the handler needs to update
// the Qdrant projection for a concept. Production wiring passes the
// canonical mediamemory.EmbeddingIndexer adapter.
type BindingIndexer interface {
	IndexConcept(ctx context.Context, c mediamemory.MediaConcept) error
}

// BindingConceptRepository is the narrow read surface the handler
// needs to resolve the concept_id carried in the event.
type BindingConceptRepository interface {
	FindByID(ctx context.Context, id string) (mediamemory.MediaConcept, error)
}

// bindingIndexRequestV1 is the canonical v1 envelope for
// binding.index.requested events.
type bindingIndexRequestV1 struct {
	SchemaVersion string `json:"schema_version"`
	EventID       string `json:"event_id"`
	BindingID     string `json:"binding_id"`
	ConceptID     string `json:"concept_id"`
	RequestedAt   string `json:"requested_at"`
}

// BindingIndexingHandler is the canonical handler for
// binding.index.requested.v1 events.
type BindingIndexingHandler struct {
	indexer  BindingIndexer
	concepts BindingConceptRepository
	log      *zap.Logger
}

// NewBindingIndexingHandler wires the handler. nil logger is replaced
// with a nop logger.
func NewBindingIndexingHandler(indexer BindingIndexer, concepts BindingConceptRepository, log *zap.Logger) *BindingIndexingHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &BindingIndexingHandler{
		indexer:  indexer,
		concepts: concepts,
		log:      log.Named("binding_index"),
	}
}

// EventType returns the canonical outboxevents constant.
func (h *BindingIndexingHandler) EventType() string {
	return outboxevents.EventBindingIndexRequested
}

// IdempotencyKey returns the static idempotency key for this handler.
func (h *BindingIndexingHandler) IdempotencyKey() string {
	return outboxevents.EventBindingIndexRequested + "." + BindingIndexRequestSchemaVersion
}

// Handle parses the v1 envelope, resolves the concept, and reindexes it
// in Qdrant. A missing concept is terminal (retrying will not help).
// Transient Qdrant/embedding failures are returned as retryable
// errors so the outbox pool retries them.
func (h *BindingIndexingHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	var p bindingIndexRequestV1
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &p); err != nil {
		return outboxevents.NewTerminalError(fmt.Errorf("binding.index.requested: unmarshal payload: %w", err))
	}
	if p.SchemaVersion != BindingIndexRequestSchemaVersion {
		return outboxevents.NewTerminalError(
			fmt.Errorf("binding.index.requested: unsupported schema version %q (want %q)", p.SchemaVersion, BindingIndexRequestSchemaVersion),
		)
	}
	if p.BindingID == "" {
		return outboxevents.NewTerminalError(fmt.Errorf("binding.index.requested: missing binding_id"))
	}

	log := h.log.With(
		zap.String("binding_id", p.BindingID),
		zap.String("concept_id", p.ConceptID),
		zap.Int64("outbox_event_id", evt.ID),
		zap.String("event_id", p.EventID),
	)

	if p.ConceptID == "" {
		return outboxevents.NewTerminalError(fmt.Errorf("binding.index.requested: missing concept_id"))
	}

	if h.indexer == nil {
		return fmt.Errorf("binding.index.requested: BindingIndexer not wired (retryable)")
	}
	if h.concepts == nil {
		return fmt.Errorf("binding.index.requested: ConceptRepository not wired (retryable)")
	}

	concept, err := h.concepts.FindByID(ctx, p.ConceptID)
	if err != nil {
		if errors.Is(err, mediamemory.ErrConceptNotFound) {
			return outboxevents.NewTerminalError(fmt.Errorf("binding.index.requested: concept %q not found", p.ConceptID))
		}
		return fmt.Errorf("binding.index.requested: find concept %q: %w", p.ConceptID, err)
	}

	if err := h.indexer.IndexConcept(ctx, concept); err != nil {
		return fmt.Errorf("binding.index.requested: IndexConcept(%s): %w", p.ConceptID, err)
	}

	log.Info("binding.index.requested: concept reindexed")
	return nil
}

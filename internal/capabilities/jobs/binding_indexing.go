// Package outbox — binding_indexing.go carries the consumer handler for
// `binding.index.requested` events.
//
// The handler is the async half of the canonical binding mutation
// dispatcher. Every SQLite write to media_bindings emits this event in
// the same transaction; this handler consumes it and reindexes the
// parent concept in pgvector so the semantic projection stays
// consistent with the authoritative SQLite state.
package jobs

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// BindingIndexRequestSchemaVersion is the only schema version this
// handler accepts. OWNED by internal/kernel/event (godlike/06);
// compile-time re-export.
const BindingIndexRequestSchemaVersion = event.BindingIndexRequestedV1Schema

// BindingIndexer is the narrow surface the handler needs to update
// the pgvector projection for a concept. Production wiring passes the
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
// in pgvector. A missing concept is terminal (retrying will not help).
// Transient pgvector/embedding failures are returned as retryable
// errors so the outbox pool retries them.
func (h *BindingIndexingHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	var p bindingIndexRequestV1
	// SkipIdempotencyKey: the PRODUCER of binding.index.requested
	// (platform/sqlite/mediamemory.BindingDispatcher) does not emit an
	// idempotency_key — its dedupe key is the outbox event_key. Requiring a
	// field the producer never writes would dead-letter every real event.
	if err := outboxevents.DecodeV1(outboxevents.DecodePolicy{
		EventName:          "binding.index.requested",
		WantSchemaVersion:  BindingIndexRequestSchemaVersion,
		SkipIdempotencyKey: true,
		Log:                h.log,
		OnViolation:        bindingIndexViolation,
	}, evt, &p); err != nil {
		return err
	}
	// Resource-level validation (the aggregate is a binding, not an asset).
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

// bindingIndexViolation preserves this handler's diagnostics while the envelope
// CHECKS live in outboxevents.DecodeV1.
func bindingIndexViolation(v outboxevents.Violation) error {
	switch v.Kind {
	case outboxevents.ViolationParse:
		return outboxevents.NewTerminalError(fmt.Errorf("binding.index.requested: unmarshal payload: %w", v.Err))
	case outboxevents.ViolationSchemaVersion:
		return outboxevents.NewTerminalError(fmt.Errorf("binding.index.requested: unsupported schema version %q (want %q)", v.Got, v.Want))
	case outboxevents.ViolationEventID:
		return outboxevents.NewTerminalError(fmt.Errorf("binding.index.requested: missing event_id"))
	default:
		return outboxevents.NewTerminalError(fmt.Errorf("binding.index.requested: %s envelope violation", v.Kind))
	}
}

// Package mediamemory (sqlite infrastructure) — binding_dispatcher.go
// is the canonical BindingMutationDispatcher concrete implementation.
//
// Every binding mutation (Create/Update/Approve/Reject/Delete) is
// persisted in media_bindings and a binding.index.requested outbox
// event is emitted in the same SQLite transaction. A background worker
// consumes the event and updates the Qdrant projection asynchronously.
package mediamemory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// BindingMutationPrimitives is the narrow repository surface the
// dispatcher needs inside a transaction. The canonical SQLite
// bindingsRepository satisfies this interface via UpsertTx/DeleteTx.
type BindingMutationPrimitives interface {
	UpsertBindingTx(ctx context.Context, tx *sql.Tx, b mediamemory.MediaBinding) (mediamemory.MediaBinding, error)
	DeleteBindingTx(ctx context.Context, tx *sql.Tx, id string) error
}

// BindingDispatcher is the canonical concrete
// mediamemory.BindingMutationDispatcher.
type BindingDispatcher struct {
	primitives BindingMutationPrimitives
	outbox     *outboxevents.Repository
	txmgr      outbox.TxManager
}

// NewBindingDispatcher constructs a dispatcher. primitives is typically
// *bindingsRepository, outbox is the canonical outbox_events repository,
// and txmgr is the canonical outbox transaction manager.
func NewBindingDispatcher(primitives BindingMutationPrimitives, outbox *outboxevents.Repository, txmgr outbox.TxManager) *BindingDispatcher {
	return &BindingDispatcher{
		primitives: primitives,
		outbox:     outbox,
		txmgr:      txmgr,
	}
}

// UpsertBinding atomically persists the binding and emits a
// binding.index.requested outbox event inside the same transaction.
func (d *BindingDispatcher) UpsertBinding(ctx context.Context, b mediamemory.MediaBinding) (mediamemory.MediaBinding, error) {
	if d == nil {
		return mediamemory.MediaBinding{}, fmt.Errorf("BindingDispatcher is nil")
	}
	if d.primitives == nil {
		return mediamemory.MediaBinding{}, fmt.Errorf("BindingDispatcher: primitives not configured")
	}
	if d.outbox == nil {
		return mediamemory.MediaBinding{}, fmt.Errorf("BindingDispatcher: outbox events repo not configured")
	}
	if d.txmgr == nil {
		return mediamemory.MediaBinding{}, fmt.Errorf("BindingDispatcher: txmgr not configured")
	}

	var result mediamemory.MediaBinding
	err := d.txmgr.InTransaction(ctx, func(tx *sql.Tx) error {
		out, err := d.primitives.UpsertBindingTx(ctx, tx, b)
		if err != nil {
			return err
		}
		result = out
		return d.enqueueBindingIndexEvent(ctx, tx, result.ID, b.ConceptID)
	})
	if err != nil {
		return mediamemory.MediaBinding{}, err
	}
	return result, nil
}

// DeleteBinding atomically removes the binding and emits a
// binding.index.requested outbox event inside the same transaction.
func (d *BindingDispatcher) DeleteBinding(ctx context.Context, id, conceptID string) error {
	if d == nil {
		return fmt.Errorf("BindingDispatcher is nil")
	}
	if d.primitives == nil {
		return fmt.Errorf("BindingDispatcher: primitives not configured")
	}
	if d.outbox == nil {
		return fmt.Errorf("BindingDispatcher: outbox events repo not configured")
	}
	if d.txmgr == nil {
		return fmt.Errorf("BindingDispatcher: txmgr not configured")
	}
	if id == "" {
		return fmt.Errorf("BindingDispatcher.DeleteBinding: id is required")
	}
	if conceptID == "" {
		return fmt.Errorf("BindingDispatcher.DeleteBinding: concept_id is required")
	}

	return d.txmgr.InTransaction(ctx, func(tx *sql.Tx) error {
		if err := d.primitives.DeleteBindingTx(ctx, tx, id); err != nil {
			return err
		}
		return d.enqueueBindingIndexEvent(ctx, tx, id, conceptID)
	})
}

// bindingIndexRequestV1 is the canonical envelope for
// binding.index.requested events.
type bindingIndexRequestV1 struct {
	SchemaVersion string `json:"schema_version"`
	EventID       string `json:"event_id"`
	BindingID     string `json:"binding_id"`
	ConceptID     string `json:"concept_id"`
	RequestedAt   string `json:"requested_at"`
}

const bindingIndexEventType = "binding.index.requested"

func (d *BindingDispatcher) enqueueBindingIndexEvent(ctx context.Context, tx *sql.Tx, bindingID, conceptID string) error {
	eventID := uuid.NewString()
	payload := bindingIndexRequestV1{
		SchemaVersion: "binding.index.requested.v1",
		EventID:       eventID,
		BindingID:     bindingID,
		ConceptID:     conceptID,
		RequestedAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("BindingDispatcher: marshal binding index payload %s: %w", bindingID, err)
	}
	eventKey := fmt.Sprintf("binding:index:%s", bindingID)
	if _, err := d.outbox.Enqueue(ctx, tx, bindingIndexEventType, bindingID, "media_binding", string(payloadJSON), eventKey); err != nil {
		return fmt.Errorf("BindingDispatcher: enqueue binding index event %s: %w", bindingID, err)
	}
	return nil
}

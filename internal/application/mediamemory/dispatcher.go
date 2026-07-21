package mediamemory

import (
	"context"
	"errors"
)

// ErrBindingMutationDispatcherUnavailable is the canonical sentinel
// returned when the BindingService detects that the dispatcher was not
// wired at composition time. A nil dispatcher must never be treated as
// a silent no-op.
var ErrBindingMutationDispatcherUnavailable = errors.New("mediamemory: BindingMutationDispatcher unavailable")

// BindingMutationDispatcher is the canonical single surface for all
// media_bindings mutations. The concrete implementation writes the
// binding row in SQLite and emits a durable outbox event inside the
// same transaction; a background worker consumes the event and updates
// the Qdrant projection asynchronously.
type BindingMutationDispatcher interface {
	// UpsertBinding atomically persists the binding row in
	// media_bindings AND emits a binding.index.requested outbox event
	// in a SINGLE transaction. Create and Update both route here.
	UpsertBinding(ctx context.Context, b MediaBinding) (MediaBinding, error)

	// DeleteBinding atomically removes the binding row from
	// media_bindings AND emits a binding.index.requested outbox event
	// in a SINGLE transaction. conceptID is the canonical
	// media_concepts.id that owns the binding; it is carried in the
	// event so the async worker can reindex the concept even after
	// the binding row is gone.
	DeleteBinding(ctx context.Context, id, conceptID string) error
}

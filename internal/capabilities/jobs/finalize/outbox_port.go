package finalize

import (
	"context"

	outbox "github.com/Marcuss-ops/PipelineGen/internal/kernel/outbox"
	"github.com/Marcuss-ops/PipelineGen/pkg/sqlport"
)

// OutboxEnqueuer is the finalizer's capability-owned port for inserting one
// outbox event inside the caller's transaction.
//
// Before this port existed the Finalizer held *outboxevents.Repository, which
// made the SQLite adapter a compile-time dependency of the finalization
// capability: the isolated test needed the adapter, and a storage change
// crossed the capability core. The port is the exact method set the finalizer
// uses (Enqueue only); *outboxevents.Repository satisfies it structurally, so
// the composition root passes the concrete repository unchanged.
//
// The port belongs to the consumer, not to the adapter: a test supplies a fake
// that records the enqueued events without opening a database.
type OutboxEnqueuer interface {
	Enqueue(
		ctx context.Context,
		tx sqlport.Tx,
		eventType, aggregateID, aggregateType, payloadJSON, eventKey string,
	) (*outbox.EnqueueResult, error)
}

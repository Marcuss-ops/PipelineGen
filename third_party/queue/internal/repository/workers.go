package repository

import (
	"context"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// WorkerRepository is the storage contract for the rendering-worker registry
// and its heartbeat ledger. It is deliberately separate from JobRepository so
// the queue logic and the worker liveness tracking stay independent.
//
// It is STORAGE only: there is deliberately no Health method here. A health
// report is a classification of the rows ("is this worker alive?"), and asking
// each backend to compute it meant an in-memory Go loop and a PostgreSQL FILTER
// count answering the same question independently — which is how /workers and
// /workers/health came to disagree about a frozen worker. The classification has
// exactly one authority (model.ClassifyWorkerLiveness /
// model.SummarizeWorkerHealth) and the service applies it to List()'s rows.
// A future backend therefore cannot invent a second answer: it implements four
// methods and inherits the classification.
//
// Like JobRepository, every method takes the caller's context first so
// cancellation and tracing reach the storage driver.
type WorkerRepository interface {
	// Register upserts a worker's identity and records an initial heartbeat.
	Register(ctx context.Context, worker model.Worker) error

	// Heartbeat records a heartbeat for a registered worker, updating its
	// last_heartbeat_at and appending to the heartbeat ledger.
	Heartbeat(ctx context.Context, workerID string) error

	// List returns all registered workers. The returned rows carry the stored
	// status and the last heartbeat; the DERIVED liveness is not a column and
	// is applied by the caller (see the contract note above).
	List(ctx context.Context) ([]model.Worker, error)
}

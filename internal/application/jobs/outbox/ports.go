// Package outbox — typed application-layer ports for the outbox subsystem.
//
// Two ports live here:
//
//   - MonitorPort — Wave 14 (June 2026) addition; narrow read-only
//     surface consumed by the outbox-events HTTP handler
//     (internal/api/outbox/handler.go). Production adapter:
//     outboxMonitorAdapter (internal/app/outbox_monitor_adapter.go)
//     wraps *outboxevents.Repository. Pre-existing before PR 4.
//
//   - VectorPointDeleter — PR 4 (June 2026, refactor/single-qdrant-runtime)
//     addition; narrow single-method port consumed by IndexDeleteHandler
//     for the Qdrant side of the asset.index.delete_requested.v1 flow.
//     Production concrete: *qdrant.IndexWriter via the compile-time
//     assertion at internal/infrastructure/qdrant/index_writer.go.
//     Replaces the previous pair of duplicated `QdrantDeleter`
//     interfaces (one in infra/qdrant/types.go, one local to
//     outbox/index_delete.go) — there is now ONE VectorPointDeleter
//     per the PR 4 acceptance criterion.
//
// AGENTS.md Pattern 0: ports live in internal/application/.../ports.go
// — never in internal/infrastructure/.
package outbox

import (
	"context"
	"time"
)

// MonitorPort is the canonical narrow surface for the outbox events
// monitoring HTTP endpoint. The two methods exposed are exactly the
// ones exercised by internal/api/outbox/handler.go (handleStatus +
// handleEvents). Other *outboxevents.Repository methods (Enqueue,
// ClaimNext, MarkCompleted, MarkFailed, MarkDeadLetter, MarkSuperseded,
// RequeueExpiredLeases, RenewLease) are intentionally NOT on the port
// — the API is read-only by design.
type MonitorPort interface {
	// CountByStatus returns the count of outbox events in a given
	// status bucket (e.g. "pending", "processing", "completed",
	// "dead_letter", "superseded").
	CountByStatus(ctx context.Context, status string) (int64, error)
	// ListPending returns the events currently in pending or
	// processing state, ordered by created_at ASC. Used by the
	// operator dashboard feed.
	ListPending(ctx context.Context) ([]EventDTO, error)
}

// EventDTO mirrors the JSON-relevant subset of outboxevents.Event.
// Field names preserve the same JSON keys the handler emitted when it
// directly returned []outboxevents.Event to gin.H (Go's default
// JSON encoding uses the exported field names verbatim). Time fields
// keep their original string-RFC3339 representation because the
// underlying outbox_events schema stores them as TEXT, not TIMESTAMP.
// Pointer-to-time fields (LeaseExpiry, CompletedAt) are kept as
// pointer-to-time so the JSON output preserves the null-vs-string
// distinction the operator dashboard relies on.
type EventDTO struct {
	ID            int64      `json:"ID"`
	EventType     string     `json:"EventType"`
	AggregateID   string     `json:"AggregateID"`
	AggregateType string     `json:"AggregateType"`
	PayloadJSON   string     `json:"PayloadJSON"`
	Status        string     `json:"Status"`
	AttemptCount  int        `json:"AttemptCount"`
	MaxAttempts   int        `json:"MaxAttempts"`
	LastError     string     `json:"LastError"`
	EventKey      string     `json:"EventKey"`
	WorkerID      string     `json:"WorkerID"`
	LeaseID       string     `json:"LeaseID"`
	LeaseExpiry   *time.Time `json:"LeaseExpiry"`
	CompletedAt   *time.Time `json:"CompletedAt"`
	CreatedAt     string     `json:"CreatedAt"`
	UpdatedAt     string     `json:"UpdatedAt"`
}

// VectorPointDeleter is the canonical application-layer port consumed
// by the outbox IndexDeleteHandler (asset.index.delete_requested.v1
// events). The production concrete is *qdrant.IndexWriter — see
// internal/infrastructure/qdrant/index_writer.go for the compile-time
// assertion `var _ outbox.VectorPointDeleter = (*qdrant.IndexWriter)(nil)`.
//
// PR 4 (June 2026, refactor/single-qdrant-runtime) — section #6 of the
// verdict Qdrant consolidates the previous pair of duplicated
// `QdrantDeleter` interfaces into this single port:
//
//   - Removed: internal/infrastructure/qdrant/types.go::QdrantDeleter
//     (was used by infra-side writers and tests, but the application
//     layer never imports infra — duplication was unidirectional drift).
//
//   - Removed: internal/application/jobs/outbox/index_delete.go::QdrantDeleter
//     (was a private replica; merging into VectorPointDeleter keeps
//     the application-layer surface canonical).
//
//   - This interface (`VectorPointDeleter`): the new home. Renamed
//     to make the application-layer intent explicit
//     (vector-store-point deletion) rather than the previous
//     infra-sounding name.
//
// The wiring at composition.go::NewComposition is now:
//   outboxDeps.VectorPointDeleter = qd.Runtime.Writer
// — direct field assignment, no runtime type assertion: the
// compile-time `var _` above guarantees QdrantRuntime.Writer fits.
type VectorPointDeleter interface {
	DeletePoints(ctx context.Context, assetIDs []string) error
}

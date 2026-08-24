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
//     assertion at internal/platform/qdrant/index_writer.go.
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

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
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
	// ListByStatus returns the events in a given status bucket,
	// ordered by created_at DESC. Used by the operator dashboard
	// to inspect failed / dead-letter / completed events.
	ListByStatus(ctx context.Context, status string) ([]EventDTO, error)
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
// internal/platform/qdrant/index_writer.go for the compile-time
// assertion `var _ outbox.VectorPointDeleter = (*qdrant.IndexWriter)(nil)`.
//
// PR 4 (June 2026, refactor/single-qdrant-runtime) — section #6 of the
// verdict Qdrant consolidates the previous pair of duplicated
// `QdrantDeleter` interfaces into this single port:
//
//   - Removed: internal/platform/qdrant/types.go::QdrantDeleter
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
//
//	outboxDeps.VectorPointDeleter = qd.Runtime.Writer
//
// — direct field assignment, no runtime type assertion: the
// compile-time `var _` above guarantees QdrantRuntime.Writer fits.
type VectorPointDeleter interface {
	DeleteAssetPoints(ctx context.Context, assetIDs []string) error
}

// DriveDeleter is the canonical Blocco 3.1 (June 2026)
// application-layer port for the Drive side of the deletion state
// machine. DriveDeleteHandler (asset.drive.delete_requested.v1) routes
// a successful Trash or permanent Delete through this interface —
// the production concrete is *drive.FileLifecycleAdapter (slotted
// under drive.FileLifecycle).
//
// Pattern 0 (AGENTS.md): the application layer never imports
// google.golang.org/api/drive/v3 — this narrow port keeps
// DriveDeleteHandler testable with an in-memory stub. The
// interface is declared NARROW on purpose: only Trash + Delete are
// consumed by the outbox chain. AddParent / Rename / Cleanup stay
// off the port so a future regression that grows the surface is a
// build failure here rather than a silent expansion of the outbox
// handler's authority.
type DriveDeleter interface {
	// Trash moves a fileID to Drive's trash (idempotent at the
	// Drive API level: re-trashing a trashed file succeeds).
	Trash(ctx context.Context, fileID string) error

	// Delete permanently removes a fileID from Drive (NOT
	// idempotent: a re-delete returns 404 from the Drive SDK).
	// DriveDeleteHandler MUST tolerate the 404 and treat it as a
	// successful idempotent skip.
	Delete(ctx context.Context, fileID string) error
}

// StateAdvancer is the Blocco 3.1 application-layer port for the
// tx-bound state-machine-advance + next-event-emit primitive.
// DriveDeleteHandler uses this method to atomically flip
// lifecycle_state from DRIVE_DELETE_PENDING to INDEX_DELETE_PENDING
// AND emit EventAssetIndexDeleteRequested in a single tx so a
// worker crash mid-flow is recoverable (a re-enqueue from the
// outbox pool's lease-fence is a no-op at the state-machine
// layer).
//
// Production concrete: *outbox.Dispatcher via the AdvanceAndEmit
// method (added in Blocco 3.1 commit 2/3, dispatcher_advance.go).
//
// Pattern 0 narrowness: ONLY this single state-machine advance
// + emit primitive lives on the port. The full Dispatcher
// surface (EnqueueAndIndex / EnqueueDriveDelete / EnqueueAndRestore
// / EnqueueAndDelete) stays off the port so DriveDeleteHandler
// cannot redirect an event into a different chain by accident.
type StateAdvancer interface {
	AdvanceAndEmit(
		ctx context.Context,
		assetID string,
		expectedState, newState asset.LifecycleState,
		eventType string,
		payloadJSON []byte,
		eventKey string,
	) error
}

// LifecycleStateReader is the Blocco 3.1 application-layer port for
// the pre-flight check DriveDeleteHandler performs before the
// side-effect chain begins. The handler reads the current
// lifecycle_state of the asset, accepts (and continues) if the row
// is in {DELETE_REQUESTED, DELETE_PENDING, DRIVE_DELETE_PENDING},
// and treats {INDEX_DELETE_PENDING, DELETED} as already-completed
// (idempotent re-enqueue skip).
//
// Production concrete: *assets.ClipsRepository :: GetClip(ctx,
// id) which returns *asset.Asset including LifecycleState.
//
// Pattern 0 narrowness: only GetClip — not the full ClipsRepository
// surface — so DriveDeleteHandler cannot accidentally mutate
// other domain columns through this port. SetLifecycleState uses a
// separate port (ClipsLifecycleStateWriter below).
type LifecycleStateReader interface {
	GetClip(ctx context.Context, id string) (*asset.Asset, error)
}

// ClipsLifecycleStateWriter is the Blocco 3.1 application-layer
// port for the BEFORE-Drive visibility stamp
// (lifecycle_state=DRIVE_DELETE_PENDING). The handler writes this
// state BEFORE the Drive API call so an operator dashboard sees
// the row "in flight on Drive" rather than "stuck" between the
// dispatcher's DELETE_REQUESTED stamp and the Drive round-trip.
//
// Production concrete: *assets.ClipsRepository :: SetLifecycleState
// (added in Blocco 3.1 commit 2/3, clips_lifecycle_state.go).
//
// Pattern 0 narrowness: only SetLifecycleState — not the broader
// CRUD port — so DriveDeleteHandler cannot accidentally UPSERT
// other columns.
type ClipsLifecycleStateWriter interface {
	SetLifecycleState(ctx context.Context, id string, state asset.LifecycleState) error
}

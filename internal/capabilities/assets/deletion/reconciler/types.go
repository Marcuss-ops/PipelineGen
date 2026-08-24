// Package reconciler — types.go (Blocco 3.2 commit 2/2, June 2026)
//
// Lifecycle:
//
//	ACTIVE → DELETE_REQUESTED → DRIVE_DELETE_PENDING → INDEX_DELETE_PENDING → DELETED
//
// Blocco 3.1 (June 2026) shipped the producer side + DriveDeleteHandler
// + IndexDeleteHandler to advance the chain hop-by-hop. A worker
// crash BETWEEN hops leaves the row in {DELETE_REQUESTED,
// DRIVE_DELETE_PENDING, INDEX_DELETE_PENDING} indefinitely — the
// outbox-lease-fence re-issues the EVENT but the row state-machine
// gates the UPDATE so a re-issued event collapses to a no-op.
//
// This package (DeletionReconciler) closes that gap with a periodic
// ticker that re-emits the correct outbox event for any media_assets
// row stuck in a deletion-chain state past a configurable threshold:
//   - DELETE_REQUESTED      → re-emit EventAssetDriveDeleteRequested
//     (Dispatcher.EnqueueDriveDelete(
//     assetID, permanently=false))
//     The Trash route is safer because the
//     original permanently=true intent is
//     not recoverable from the row state;
//     operators can re-trigger permanently
//     manually if needed.
//   - DRIVE_DELETE_PENDING  → re-emit EventAssetIndexDeleteRequested
//     (Dispatcher.EnqueueIndexDelete(assetID))
//     The IndexDelete handler's pre-flight
//     accepts INDEX_DELETE_PENDING|DELETE_PENDING
//     and idempotently processes.
//   - INDEX_DELETE_PENDING  → re-emit EventAssetIndexDeleteRequested
//     (same as above)
//
// The outbox event_key shape
//
//	drive_delete:<permanently?>:<assetID>          (drive delete)
//	delete:<assetID>                                (index delete)
//
// absorbs repeat dispatches via ON CONFLICT(event_key) DO NOTHING —
// each stuck-row replay results in at most ONE pending outbox row.
// Since both IndexDelete and the gap between Drive and Index are
// lease-fenced at the outbox layer, the reconciler can safely re-
// invoke without coordinating with a running worker.
package reconciler

import "time"

// RepairAction is the canonical action the reconciler takes for a
// stuck row. The reconcileOnce pipeline classifies each row into
// exactly one RepairAction via Classify (see scanner.go).
type RepairAction string

const (
	// ActionRequeueDrive — re-emit
	// EventAssetDriveDeleteRequested for a row stuck in
	// DELETE_REQUESTED past the threshold. Routes through
	// Dispatcher.EnqueueDriveDelete(assetID, permanently=false).
	ActionRequeueDrive RepairAction = "requeue_drive"

	// ActionRequeueIndex — re-emit
	// EventAssetIndexDeleteRequested for a row stuck in
	// DRIVE_DELETE_PENDING or INDEX_DELETE_PENDING past the threshold.
	// Routes through Dispatcher.EnqueueIndexDelete(assetID). The
	// IndexDelete handler's idempotent pre-flight skips if the
	// row has already advanced past INDEX_DELETE_PENDING.
	ActionRequeueIndex RepairAction = "requeue_index"
)

// Scanner is the application-layer port for reading stuck rows
// from the media_assets table. The production concrete adapter is
// at internal/platform/sqlite/deletion/stuck_row_scanner.go.
type Scanner interface {
	// ListStuckRows returns media_assets rows in any of the 3 deletion-
	// chain states {DELETE_REQUESTED, DRIVE_DELETE_PENDING,
	// INDEX_DELETE_PENDING} whose updated_at is strictly before
	// thresholdSortedAt. The result is sorted by updated_at ASC so
	// the oldest stuck row is reconciled first (operator-dashboard
	// visible skew).
	ListStuckRows(now time.Time, threshold time.Duration) ([]StuckRow, error)
}

// StuckRow is the minimal media_assets projection consumed by the
// reconciler. Production concrete adapter hydrates these from
// media_assets; tests use hand-built literals. The set of fields
// is intentionally narrow: state + updated_at only, since
// reconciliation is a state-machine recovery path and doesn't need
// the asset's metadata (Drive file IDs, etc) — those live in the
// consumers (DriveDeleteHandler reads them on the next event).
type StuckRow struct {
	AssetID   string
	State     string
	UpdatedAt time.Time
}

// ClassifyResult is the per-row output of repair classification.
// Stuck rows discarded from the action dispatch (e.g. deleted_at
// non-empty — the row is already cancelled) are still reported in
// RunReport.Skipped for operator visibility.
type ClassifyResult struct {
	Row    StuckRow
	Action RepairAction // "" → skipped
	Skip   string       // human-readable reason when Action == ""
}

// RunOptions is the per-tick configuration for Service.ReconcileOnce.
// Production caller passes:
//
//	svc.ReconcileOnce(ctx, repos.RunOptions{
//	    Now:             time.Now,
//	    DefaultInterval: 15 * time.Minute,
//	    DefaultThreshold: 30 * time.Minute,
//	})
//
// Mirrors the cfg.Jobs shape (DeletionReconcilerInterval +
// DeletionReconcilerStuckThreshold) so the composition root can
// plumb the config through without depending on internal/config.
type RunOptions struct {
	Now       func() time.Time
	Interval  time.Duration // logged on every tick
	Threshold time.Duration // rows with updated_at < Now()-Threshold are stuck
}

// RunReport is the per-tick observability surface. Counts drive the
// deletion_reconciler_actions_total counter; Skipped rows surface in
// operator dashboards per deletion-reconciler audit query.
type RunReport struct {
	StartedAt        time.Time
	CompletedAt      time.Time
	DurationMs       int64
	SInterval        time.Duration
	SThreshold       time.Duration
	RowsScanned      int
	RowsRequeueDrive int
	RowsRequeueIndex int
	RowsSkipped      int
	RowsErrored      int
	Errors           []string
}

package asset

// LifecycleState is the SINGLE canonical enum for the asset lifecycle
// (PR 1 — Lifecycle state SSOT, June 2026). Six values, all UPPERCASE.
// The previous lowercase compat values (`ready`, `pending`) and the
// dual-enum AssetStatus (lifecycle_core.go) have been retired.
//
// Migration history:
//   - Legacy compat (pre-PR1): lowercase values mixed with canonical
//     uppercase values in media_assets.lifecycle_state, plus a
//     parallel `status` column with its own lowercase enum (`active`,
//     `archived`, `deleted`, `processing`, `failed`). Reads consulted
//     the COALESCE fallback (NULLIF(lifecycle_state), NULLIF(status)).
//   - PR1: AssetStatus enum deleted; `status` column dropped by
//     migration 101; writers use only these 6 constants; readers
//     read the column directly without fallback.
type LifecycleState string

const (
	// StatePreparing — asset row created, local file being validated/hashed,
	// not yet published to remote storage. Added FASE 3b (July 2026).
	StatePreparing LifecycleState = "PREPARING"
	// StatePublished — artifact published to remote storage (Drive, S3)
	// but not yet indexed. Transitions to ACTIVE after Qdrant indexing
	// completes. Added FASE 3b (July 2026).
	StatePublished LifecycleState = "PUBLISHED"
	// StateStaging — asset row created but not yet indexable.
	StateStaging LifecycleState = "STAGING"
	// StateProcessing — vectorisation currently in flight.
	StateProcessing LifecycleState = "PROCESSING"
	// StateActive — terminal-and-searchable default for indexable rows.
	// This is the canonical payload value at Qdrant and the only state
	// returned by the /internal/v1/media/search endpoint.
	StateActive LifecycleState = "ACTIVE"
	// StateDeletePending — LEGACY broad intent state (pre-Blocco 3.1,
	// June 2026). Reads that previously matched DELETE_PENDING as a
	// single "soft-delete initiated" intent must now distinguish
	// between the three explicit deletion steps; new producers MUST
	// write StateDeleteRequested and follow the chain. Kept here so
	// in-flight rows that predate the state-machine migration stay
	// visible to operators + the reconciler can rewrite them on its
	// next pass.
	StateDeletePending LifecycleState = "DELETE_PENDING"
	// StateDeleteRequested (Blocco 3.1, June 2026) — first hop of
	// the chain. Set by Dispatcher.EnqueueDriveDelete in the same tx
	// that emits the asset.drive.delete_requested.v1 outbox event.
	// The DriveDeleteHandler pre-flight accepts {DELETE_REQUESTED,
	// DRIVE_DELETE_PENDING} so a re-enqueue on the same asset
	// doesn't reset the chain.
	StateDeleteRequested LifecycleState = "DELETE_REQUESTED"
	// StateDriveDeletePending — Drive Trash (or Delete for hard-
	// deletion) is in flight or retrying. DriveDeleteHandler stamps
	// this BEFORE the Drive API call and leaves the row in this
	// state on transient failure. The reconciler picks the row up
	// if the state has been stuck > reconciliationThreshold.
	StateDriveDeletePending LifecycleState = "DRIVE_DELETE_PENDING"
	// StateDriveDeleted (Blocco 3.1 commit 2/3, July 2026) — Drive
	// side-effect CONFIRMED (Drive.Trash or Drive.Delete returned
	// success + 404 folds to success). DriveDeleteHandler stamps this
	// AFTER the Drive API call via StateAdvancer.AdvanceAndEmit
	// (atomic flip DRIVE_DELETE_PENDING → DRIVE_DELETED + emits the
	// next outbox event asset.index.delete_requested.v1 in one tx).
	// IndexDeleteHandler's Drive-block guard rejects rows still at
	// DRIVE_DELETE_PENDING (file still alive) with a typed error
	// mentioning the guard + retry guidance. Reads that previously
	// resolved "the file is gone" by observing SoftDeleteFilter now
	// observe lifecycle_state=DRIVE_DELETED as the explicit post-Drive
	// confirmation hop.
	StateDriveDeleted LifecycleState = "DRIVE_DELETED"
	// StateLifecycleIndexDeletePending — Drive delete succeeded; the
	// Qdrant DeletePoints + media_assets SoftDelete chain is in
	// flight or retrying. IndexDeleteHandler pre-flights on this
	// state and stamps DELETED on success.
	StateLifecycleIndexDeletePending LifecycleState = "INDEX_DELETE_PENDING"
	// StateIndexDeleted (Blocco 3.1 commit 2/3, July 2026) — Qdrant
	// projection removal CONFIRMED (Qdrant.DeletePoints returned
	// success; idempotent on already-absent point via
	// deleted_count:0 at the API layer) AND SQLite SoftDelete
	// applied. IndexDeleteHandler stamps this AFTER Qdrant +
	// SoftDelete + index_state=DELETED as the intermediate
	// intermediate confirmation hop, BEFORE the terminal
	// lifecycle_state=DELETED flip. Two-TX with compensation: the
	// intermediate flip + terminal flip are both idempotent same-
	// state writes; transient SQLite failures on either flip
	// surface as retryable and the next pool attempt's pre-flight
	// (catches INDEX_DELETED or DELETED) re-runs the write path.
	StateIndexDeleted LifecycleState = "INDEX_DELETED"
	// StateDeleted — terminal tombstone. The SoftDeleteFilter and
	// the Qdrant lifecycle waterfall exclude this state from all
	// reads.
	StateDeleted LifecycleState = "DELETED"
	// StateError — indexer failed and could not recover; surfaced
	// to operators via dashboards and the reaper diagnostic.
	StateError LifecycleState = "ERROR"
)

// CanonicalLifecycleStateValues returns the closed enumeration of
// canonical lifecycle_state strings. Callers use this as the
// single-source-of-truth list for migrations, dashboards, and
// qdrant payload validation. StateDeletePending is the legacy
// broad-intent value kept for in-flight migration; new writes use
// the 6 explicit deletion states added in Blocco 3.1 (with commit
// 2/3 adding StateDriveDeleted + StateIndexDeleted as the post-Drive
// + post-Qdrant confirmation hops).
func CanonicalLifecycleStateValues() []LifecycleState {
	return []LifecycleState{
		StatePreparing,
		StatePublished,
		StateStaging,
		StateProcessing,
		StateActive,
		StateDeletePending,
		StateDeleteRequested,
		StateDriveDeletePending,
		StateDriveDeleted,
		StateLifecycleIndexDeletePending,
		StateIndexDeleted,
		StateDeleted,
		StateError,
	}
}

// Valid returns true if s is a known canonical lifecycle state
// (Blocco 3.1: includes the 6 explicit deletion states; commit 2/3
// added StateDriveDeleted + StateIndexDeleted).
func (s LifecycleState) Valid() bool {
	switch s {
	case StatePreparing, StatePublished,
		StateStaging, StateProcessing, StateActive,
		StateDeletePending, StateDeleteRequested,
		StateDriveDeletePending, StateDriveDeleted,
		StateLifecycleIndexDeletePending, StateIndexDeleted,
		StateDeleted, StateError:
		return true
	}
	return false
}

// IsValidTransition reports whether moving from `from` to `to` is
// one of the allowed edges of the deletion state machine (Blocco 3.1,
// June 2026; extended in Blocco 3.1 commit 2/3, July 2026).
//
// Strict-machine contract (Blocco 3.1 commit 2/3 expanded to 6 explicit
// deletion hops with the post-Drive + post-Qdrant confirmation hops):
//
//	ACTIVE              → DELETE_REQUESTED        (user-initiated delete)
//	DELETE_REQUESTED    → DRIVE_DELETE_PENDING    (DriveDeleteHandler pre-flip)
//	DRIVE_DELETE_PENDING→ DRIVE_DELETED           (DriveDeleteHandler post-success flip + emit index-delete event)
//	DRIVE_DELETED       → INDEX_DELETE_PENDING    (IndexDeleteHandler pre-flip; legacy forward-compat accepts
//	                                               pre-commit 2/3 rows already at INDEX_DELETE_PENDING)
//	INDEX_DELETE_PENDING→ INDEX_DELETED           (IndexDeleteHandler post-Qdrant+SoftDelete flip, intermediate)
//	INDEX_DELETED       → DELETED                 (IndexDeleteHandler post-success terminal flip)
//	*                   → ACTIVE                  (restore path is symmetric; see
//	                                               Restore handler + IsValidRestoreTransition)
//
// The DRIVE_DELETED + INDEX_DELETED confirmation hops are the
// audit-pinning surface for the closed deletion chain: an asset
// dashboard distinguishes "Drive side-effect confirmed" (DRIVE_DELETED)
// from "Qdrant projection removed confirmed" (INDEX_DELETED) and
// from "fully retired" (DELETED), so a stuck row at any of the
// intermediate hops is observable per the canonical-state-machine
// invariant.
//
// The PREPARING and PUBLISHED states (FASE 3b, July 2026) represent the
// early lifecycle before indexing:
//
//	PREPARING           → PUBLISHED              (AssetTxFinalizer writes PUBLISHED)
//	PUBLISHED           → ACTIVE                 (post-indexing activation)
//	*                   → PREPARING              (creation path)
//
// FASE 3b explicit forward edges:
//
//	PREPARING           → PUBLISHED
//	PUBLISHED           → ACTIVE
//
// Self-loops are allowed and idempotent (writing the same state
// twice in a row is harmless; the chain-defensive SetLifecycleState
// callers use this for retry safety). StateDeletePending is the
// LEGACY broad-intent value; transitions FROM it are allowed into
// the new chain so the legacy migration path stays valid:
//
//	DELETE_PENDING      → DRIVE_DELETE_PENDING    (legacy rewrite path)
//
// All other transitions (including the terminal DELETED state) are
// rejected. Callers using SetLifecycleStateTx + an explicit
// transition check get a typed error rather than a silent row flip;
// the write itself is gated by IsValidTransition so a programmer
// error becomes a build-time constraint rather than a runtime
// tombstone of an ACTIVE row.
func (s LifecycleState) IsValidTransition(to LifecycleState) bool {
	if s == to {
		return true // idempotent self-loop
	}
	switch s {
	case StatePreparing:
		// FASE 3b: preparing → published (finalizer writes PUBLISHED).
		return to == StatePublished
	case StatePublished:
		// FASE 3b: published → active (post-indexing activation)
		// or published → delete (asset deletable even before indexing).
		return to == StateActive || to == StateError ||
			to == StateDeleteRequested || to == StateDeletePending
	case StateActive:
		// A reconciled asset whose only publishable location is gone
		// remains in SQLite for diagnosis/republication but leaves the
		// searchable projection. ERROR is the explicit non-searchable
		// state for that fail-closed transition.
		return to == StateDeleteRequested || to == StateDeletePending || to == StateError
	case StateDeleteRequested:
		return to == StateDriveDeletePending
	case StateDeletePending:
		// Legacy broad-intent → Drive-pending rewrite path. Drives
		// the reconciler's rewrite of pre-Blocco 3.1 rows.
		return to == StateDriveDeletePending || to == StateDeleteRequested
	case StateDriveDeletePending:
		// Blocco 3.1 commit 2/3 (July 2026): DriveDeleteHandler
		// post-success flip. Atomic via StateAdvancer.AdvanceAndEmit
		// (DRIVE_DELETE_PENDING → DRIVE_DELETED same tx as the emit).
		return to == StateDriveDeleted
	case StateDriveDeleted:
		// IndexDeleteHandler pre-flip stamp. Daily state-machine
		// advancement beyond the Drive hop; IndexDeleteHandler
		// idempotently rewrites this as part of its work path
		// (SetLifecycleState(INDEX_DELETE_PENDING)).
		return to == StateLifecycleIndexDeletePending
	case StateLifecycleIndexDeletePending:
		// Blocco 3.1 commit 2/3 (July 2026): IndexDeleteHandler
		// post-Qdrant+SoftDelete confirmation flip — the closed-chain
		// audit-pinning intermediate between "index work in flight"
		// and "fully retired".
		return to == StateIndexDeleted
	case StateIndexDeleted:
		// IndexDeleteHandler post-success terminal flip.
		return to == StateDeleted
	}
	return false
}

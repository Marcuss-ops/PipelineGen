// Package asset — index_state.go is the canonical 7-state enum that
// drives the media_assets.index_state column (QDRANT-002 PR6, ships
// with migration 094).
//
// Lifecycle (companion, not replacement): LifecycleState in
// asset_types.go tracks the high-level asset lifecycle
// (STAGING/PROCESSING/ACTIVE/DELETED/ready/pending). IndexState tracks
// the per-asset indexing progress narrowly. They are orthogonal:
//   - LifecycleState = "ACTIVE" + IndexState = "INDEXED"
//     → real, searchable asset.
//   - LifecycleState = "DELETED" + IndexState = "DELETED"
//     → canonical tombstone; Qdrant point gone AND row retired.
//   - LifecycleState = "ACTIVE" + IndexState = "INDEX_FAILED"
//     → unexpectedly broken — Qdrant search will silently miss this
//     asset until an operator re-indexes it (QDRANT-005 followup
//     adds an alert).
//   - LifecycleState = "ACTIVE" + IndexState = "DELETE_PENDING"
//     → race window: Qdrant delete acknowledged, SQLite
//     soft-delete pending next outbox pass.
//
// Do NOT add a sub-state "RETRYING" — the canonical 7 reflect stable
// state, not transient worker activity. QDRANT-002 PR4's supersede
// gate already short-circuits stale events before the worker hits a
// retry loop; if a future PR needs the distinction, model it as a
// separate `last_index_attempt_count` column, not a sub-state.
package asset

// IndexState is the canonical per-asset indexing progress. Stored on
// the media_assets.index_state first-class column (migration 094).
//
// Mirrors the lowercase ad-hoc alphabet that previously lived in
// metadata_json.$.index_state ("embedding", "upserting", "indexed",
// "failed", "retrying"); Valid() returns false on those legacy values
// so a worker reading a pre-PR6 row that has not yet been
// re-touched correctly reports "unknown" rather than silently
// accepting a deprecated spelling.
type IndexState string

const (
	// StateDiscovered — initial sentinel for a row that has never
	// been touched by a worker. Migration 094's ALTER TABLE DEFAULT
	// writes this on every existing row, so a row in DISCOVERED
	// means "either newly inserted or pre-PR6 untouched".
	StateDiscovered IndexState = "DISCOVERED"

	// StateIndexPending — worker hit a retryable error; row is in
	// the queue waiting for the next claim slot. Maps to the legacy
	// "retrying" JSON value during the migration backfill.
	StateIndexPending IndexState = "INDEX_PENDING"

	// StateIndexing — worker actively holding the lease, performing
	// embedding generation + Qdrant upsert. Maps to the legacy
	// "embedding" + "upserting" JSON values (both indicate
	// in-flight work).
	StateIndexing IndexState = "INDEXING"

	// StateIndexed — terminal success. Qdrant point is current AND
	// media_assets.embedded_at / indexed_content_hash are
	// populated. No further automatic transitions; the next state
	// change is to DELETE_PENDING if a delete event arrives.
	StateIndexed IndexState = "INDEXED"

	// StateIndexFailed — terminal failure. Worker has exhausted
	// max_attempts on a retryable error, or hit a typed terminal
	// classification (and the outbox record was MarkedDeadLetter).
	// Maps to the legacy "failed" JSON value during backfill.
	StateIndexFailed IndexState = "INDEX_FAILED"

	// StateDeletePending — IndexDeleteHandler has acknowledged the
	// Qdrant DeletePoints call but the SQLite SoftDelete has not yet
	// committed (retryable window). Same lease-fence guarantees as
	// INDEXING: only one worker per event, but a restart between
	// Qdrant and SQLite leaves the row here.
	StateDeletePending IndexState = "DELETE_PENDING"

	// StateDELETED — canonical tombstone. Qdrant point gone AND
	// media_assets.lifecycle_state set to "deleted" (lowercase, per
	// ClipsRepository.SoftDeleteFilter) OR "DELETED" (canonical,
	// post-PR6 long-term). NOTE: uppercase matches LifecycleState
	// canonical form so a single SELECT reports a consistent
	// tombstone across both columns; the legacy lowercase
	// "deleted" remains accepted on read because SoftDeleteFilter
	// matches both casings.
	StateDELETED IndexState = "DELETED"

	// Legacy values that lived in metadata_json.$.index_state
	// pre-PR6. Valid() returns false for these — workers reading
	// them should map via readSourceVersion-style priority walker
	// rather than treat them as canonical. Defining them here as
	// named constants keeps the source-of-truth without inviting
	// accidental use.
	legacyStateEmbedding IndexState = "embedding"
	legacyStateUpserting IndexState = "upserting"
	legacyStateRetrying  IndexState = "retrying"
	legacyStateIndexed   IndexState = "indexed"
	legacyStateFailed    IndexState = "failed"
)

// Valid returns true if s is one of the 7 canonical IndexState values.
// Legacy lowercase values are intentionally rejected so a worker can
// spot pre-PR6 rows whose metadata_json carried the deprecated
// alphabet — those rows are recoverable via the reindex admin
// endpoint but should NOT participate in state-machine transitions
// until first re-touch (which normalises them to the canonical enum).
func (s IndexState) Valid() bool {
	switch s {
	case StateDiscovered, StateIndexPending, StateIndexing, StateIndexed,
		StateIndexFailed, StateDeletePending, StateDELETED:
		return true
	}
	return false
}

// IsTerminal returns true if the state is one of the four terminal
// values (no further automatic transitions expected from the worker
// unless a new event is enqueued). Used by outbox worker pre-flight
// to short-circuit gates: an already-terminal state skips the
// supersede gate (the event is a no-op).
func (s IndexState) IsTerminal() bool {
	switch s {
	case StateIndexed, StateIndexFailed, StateDELETED:
		return true
	}
	return false
}

// IsFailedTerminal distinguishes the "operator must intervene" terminal
// from the "successful terminal". StateIndexFailed is the only
// failure terminal — StateDELETED and StateIndexed are intentional
// outcomes of successful flows.
func (s IndexState) IsFailedTerminal() bool {
	return s == StateIndexFailed
}

// IsDeletedCanonical returns true if the row is in a tombstone state
// from any of the two deletion-side terminals (DELETE_PENDING during
// the in-flight window, DELETED after commit). IndexDeleteHandler's
// idempotency pre-flight consults this in addition to
// LifecycleState — see index_delete.go for the contract.
func (s IndexState) IsDeletedCanonical() bool {
	return s == StateDeletePending || s == StateDELETED
}

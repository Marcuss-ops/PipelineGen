// Package asset — index_state.go is the canonical IndexState enum that
// drives the media_assets.index_state column (migration 094).
//
// Lifecycle (companion, not replacement): LifecycleState in
// asset_types.go tracks the high-level asset lifecycle
// (STAGING/PROCESSING/ACTIVE/DELETED). IndexState tracks the
// per-asset indexing progress narrowly. They are orthogonal:
//   - LifecycleState = "ACTIVE" + IndexState = "INDEXED" → real, searchable asset.
//   - LifecycleState = "ACTIVE" + IndexState = "INDEXING_FAILED" → Qdrant upsert failed.
//   - LifecycleState = "ACTIVE" + IndexState = "EMBEDDING_FAILED" → embedding generation failed.
//   - LifecycleState = "DELETED" + IndexState = "DELETED" → canonical tombstone.
//
// Core principle: embedding saved != asset indexed.
//   - EMBEDDED = embeddings in SQLite, Qdrant NOT yet updated.
//   - INDEXED  = Qdrant upsert succeeded AND vectors validated.
package asset

// IndexState is the canonical per-asset indexing progress. Stored on
// the media_assets.index_state first-class column (migration 094).
//
// Valid() returns false on the legacy lowercase values ("embedding",
// "upserting", "indexed", "failed", "retrying") that previously
// lived in metadata_json.$.index_state, so a worker reading a
// pre-migration row correctly reports "unknown".
type IndexState string

const (
	// StateNotIndexable — asset is not eligible for indexing (e.g.
	// voiceover, sound effect, or assets with no vectorizable content).
	// Terminal for indexing purposes; the asset can still be ACTIVE.
	StateNotIndexable IndexState = "NOT_INDEXABLE"

	// StateDiscovered — initial sentinel for a row that has never
	// been touched by a worker. Migration 094's ALTER TABLE DEFAULT
	// writes this on every existing row.
	StateDiscovered IndexState = "DISCOVERED"

	// StateEmbedding — worker is actively generating embeddings
	// (text model, visual SigLIP, audio CLAP). On success the
	// worker transitions to EMBEDDED.
	StateEmbedding IndexState = "EMBEDDING"

	// StateEmbedded — embeddings have been generated and saved to
	// SQLite. Qdrant has NOT yet been updated. The next step is INDEXING.
	StateEmbedded IndexState = "EMBEDDED"

	// StateIndexing — worker is actively performing Qdrant upsert.
	// Embeddings MUST already be in SQLite (row must have passed
	// through EMBEDDED).
	StateIndexing IndexState = "INDEXING"

	// StateIndexed — terminal success. Qdrant point is current AND
	// media_assets.embedded_at / indexed_content_hash are populated.
	StateIndexed IndexState = "INDEXED"

	// StateEmbeddingFailed — terminal failure at the embedding-
	// generation stage. SQLite carries no vectors. The asset CANNOT
	// be found in Qdrant. Operator must re-index from scratch.
	StateEmbeddingFailed IndexState = "EMBEDDING_FAILED"

	// StateIndexingFailed — terminal failure at the Qdrant-upsert
	// stage. SQLite carries VALID embeddings but the Qdrant point
	// is missing or invalid. Operator can re-index from EMBEDDED.
	StateIndexingFailed IndexState = "INDEXING_FAILED"

	// StateIndexingSkippedNoIndexer — transient retry-pending state
	// stamped by IndexingHandler when indexer.IndexClip returns
	// ErrIndexClipDisabledButEventRequested: "we tried, indexer was
	// offline, do not retry as a fresh success; the outbox event
	// stays pending and IndexClip re-runs when the indexer comes
	// back online". Non-terminal: IsTerminal() intentionally does
	// NOT include this state. Use IsRetryPending() to branch on it.
	StateIndexingSkippedNoIndexer IndexState = "INDEXING_SKIPPED_NO_INDEXER"

	// StateIndexDeletePending — IndexDeleteHandler has acknowledged the
	// Qdrant DeletePoints call but the SQLite SoftDelete has not yet
	// committed (retryable window).
	StateIndexDeletePending IndexState = "DELETE_PENDING"

	// StateDELETED — canonical tombstone. Qdrant point gone AND
	// media_assets.lifecycle_state set to "deleted" or "DELETED".
	StateDELETED IndexState = "DELETED"
)

// Valid returns true if s is one of the canonical IndexState values.
// Only the canonical states are accepted. Migration 189 backfills and
// rejects the retired INDEX_PENDING and INDEX_FAILED values before this
// alphabet is enforced by SQLite.
func (s IndexState) Valid() bool {
	switch s {
	case StateNotIndexable,
		StateDiscovered,
		StateEmbedding, StateEmbedded,
		StateIndexing, StateIndexed,
		StateEmbeddingFailed, StateIndexingFailed,
		StateIndexingSkippedNoIndexer,
		StateIndexDeletePending, StateDELETED:
		return true
	}
	return false
}

// IsTerminal returns true if the state is a terminal value (no further
// automatic transitions expected unless a new event is enqueued).
// Used by outbox worker pre-flight to short-circuit gates.
func (s IndexState) IsTerminal() bool {
	switch s {
	case StateNotIndexable, StateIndexed, StateDELETED,
		StateEmbeddingFailed, StateIndexingFailed:
		return true
	}
	return false
}

// IsFailedTerminal returns true if the state is a failure terminal
// (operator-must-intervene): EMBEDDING_FAILED or INDEXING_FAILED.
func (s IndexState) IsFailedTerminal() bool {
	return s == StateEmbeddingFailed || s == StateIndexingFailed
}

// IsDeletedCanonical returns true if the row is in a tombstone state
// from any of the two deletion-side terminals (DELETE_PENDING during
// the in-flight window, DELETED after commit).
func (s IndexState) IsDeletedCanonical() bool {
	return s == StateIndexDeletePending || s == StateDELETED
}

// IsRetryPending returns true if the state is a transient
// retry-pending marker (StateIndexingSkippedNoIndexer). The event
// stays in the outbox pool's pending bucket and is re-emitted when
// the operator re-enables the indexer. Distinct from IsTerminal
// (done) and IsFailedTerminal (stuck) — a retry-pending row is
// mid-loop.
func (s IndexState) IsRetryPending() bool {
	return s == StateIndexingSkippedNoIndexer
}

// ── Asset-state factory ───────────────────────────────────────────────

// NewIndexableAssetState returns the canonical initial LifecycleState
// and IndexState for a newly created, indexable asset. This is the
// single factory that every asset writer must call instead of
// hardcoding LifecycleState/IndexState literals.
//
// LifecycleState = StateStaging (asset row created, not yet published).
// IndexState     = StateDiscovered (initial sentinel, never touched by
// a worker).
func NewIndexableAssetState() (LifecycleState, IndexState) {
	return StateStaging, StateDiscovered
}

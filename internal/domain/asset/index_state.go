// Package asset — index_state.go is the canonical 12-state enum that
// drives the media_assets.index_state column (QDRANT-002 PR6, ships
// with migration 094; Task 2 July 2026 adds EMBEDDING/EMBEDDED/
// EMBEDDING_FAILED/INDEXING_FAILED).
//
// Lifecycle (companion, not replacement): LifecycleState in
// asset_types.go tracks the high-level asset lifecycle
// (STAGING/PROCESSING/ACTIVE/DELETED/ready/pending). IndexState tracks
// the per-asset indexing progress narrowly. They are orthogonal:
//   - LifecycleState = "ACTIVE" + IndexState = "INDEXED"
//     → real, searchable asset.
//   - LifecycleState = "ACTIVE" + IndexState = "INDEXING_FAILED"
//     → Qdrant upsert failed but embeddings exist in SQLite;
//     operator re-indexes to recover.
//   - LifecycleState = "ACTIVE" + IndexState = "EMBEDDING_FAILED"
//     → embedding generation failed; SQLite carries no vectors.
//   - LifecycleState = "DELETED" + IndexState = "DELETED"
//     → canonical tombstone; Qdrant point gone AND row retired.
//   - LifecycleState = "ACTIVE" + IndexState = "DELETE_PENDING"
//     → race window: Qdrant delete acknowledged, SQLite
//     soft-delete pending next outbox pass.
//
// Task 2 (July 2026): split the conflated INDEXING state into
// EMBEDDING → EMBEDDED → INDEXING, and split INDEX_FAILED into
// EMBEDDING_FAILED + INDEXING_FAILED. The old states (INDEX_PENDING,
// INDEX_FAILED) remain valid for backward-compatibility with existing
// DB rows but are deprecated in new writer paths.
//
// Core principle: embedding saved != asset indexed.
//   - EMBEDDED = embeddings in SQLite, Qdrant NOT yet updated.
//   - INDEXED  = Qdrant upsert succeeded AND point verified AND
//     vectors validated AND payload verified.
//
// Do NOT add a sub-state "RETRYING" — the canonical states reflect
// stable state, not transient worker activity. QDRANT-002 PR4's supersede
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
	// StateNotIndexable — asset is not eligible for indexing (e.g.
	// voiceover, sound effect, or assets with no vectorizable content).
	// Terminal for indexing purposes; the asset can still be ACTIVE.
	StateNotIndexable IndexState = "NOT_INDEXABLE"

	// StateDiscovered — initial sentinel for a row that has never
	// been touched by a worker. Migration 094's ALTER TABLE DEFAULT
	// writes this on every existing row, so a row in DISCOVERED
	// means "either newly inserted or pre-PR6 untouched".
	StateDiscovered IndexState = "DISCOVERED"

	// StateEmbedding (Task 2, July 2026) — worker is actively
	// generating embeddings (text model, visual SigLIP, audio CLAP).
	// This is the FIRST step of what was previously conflated into
	// INDEXING. On success the worker transitions to EMBEDDED.
	StateEmbedding IndexState = "EMBEDDING"

	// StateEmbedded (Task 2, July 2026) — embeddings have been
	// generated and saved to SQLite (embedding_json, visual_embedding,
	// audio_embedding columns populated). Qdrant has NOT yet been
	// updated. This is the key state that encodes "embedding saved
	// != asset indexed". The next step is INDEXING.
	StateEmbedded IndexState = "EMBEDDED"

	// StateIndexing — worker is actively performing Qdrant upsert.
	// Post-Task 2 this means "Qdrant write ONLY" — embeddings MUST
	// already be in SQLite (the row must have passed through EMBEDDED).
	StateIndexing IndexState = "INDEXING"

	// StateIndexed — terminal success. Qdrant point is current AND
	// media_assets.embedded_at / indexed_content_hash are populated.
	// Post-Task 2 this ALSO means: point ID was verified, vectors
	// were validated for correct dimensions, payload was verified
	// (no missing required keys). No further automatic transitions.
	StateIndexed IndexState = "INDEXED"

	// StateEmbeddingFailed (Task 2, July 2026) — terminal failure
	// at the embedding-generation stage. SQLite carries no vectors.
	// The asset CANNOT be found in Qdrant (no upsert was attempted).
	// Operator must re-index from scratch.
	StateEmbeddingFailed IndexState = "EMBEDDING_FAILED"

	// StateIndexingFailed (Task 2, July 2026) — terminal failure
	// at the Qdrant-upsert stage. SQLite carries VALID embeddings
	// (the row passed through EMBEDDED), but the Qdrant point is
	// missing or invalid. Operator can re-index from EMBEDDED
	// (skipping embedding generation) — the backfill command's
	// --only-missing flag recognises this state and re-upserts.
	StateIndexingFailed IndexState = "INDEXING_FAILED"

	// StateIndexingSkippedNoIndexer (PR-QDRANT-INDEXCLIP-GUARD,
	// July 2026) — transient retry-pending state stamped by
	// IndexingHandler when indexer.IndexClip returns
	// ErrIndexClipDisabledButEventRequested: "we tried, indexer
	// was offline, do not retry as a fresh success; the outbox
	// event stays pending and IndexClip re-runs when the
	// indexer comes back online".
	//
	// godlike/06 SSOT — mirrors the supersede gate's
	// "noise-suppressor" pattern: the recorded state stops
	// downstream consumers from treating the absence of a
	// Qdrant embedding as a failure. Distinct from
	// StateIndexingFailed (operator must intervene to recover)
	// and from the deprecated StateIndexPending (legacy
	// retryable terminology from a pre-Task-2 model).
	//
	// Non-terminal: IsTerminal() intentionally does NOT
	// include this state — a row in INDEXING_SKIPPED_NO_INDEXER
	// is pending retry, NOT done. The handler returns a
	// non-nil retryable error so the outbox pool re-emits
	// the event. Use IsRetryPending() to branch on this state.
	StateIndexingSkippedNoIndexer IndexState = "INDEXING_SKIPPED_NO_INDEXER"

	// StateIndexDeletePending — IndexDeleteHandler has acknowledged the
	// Qdrant DeletePoints call but the SQLite SoftDelete has not yet
	// committed (retryable window). Same lease-fence guarantees as
	// INDEXING: only one worker per event, but a restart between
	// Qdrant and SQLite leaves the row here.
	StateIndexDeletePending IndexState = "DELETE_PENDING"

	// StateDELETED — canonical tombstone. Qdrant point gone AND
	// media_assets.lifecycle_state set to "deleted" (lowercase, per
	// ClipsRepository.SoftDeleteFilter) OR "DELETED" (canonical,
	// post-PR6 long-term). NOTE: uppercase matches LifecycleState
	// canonical form so a single SELECT reports a consistent
	// tombstone across both columns; the legacy lowercase
	// "deleted" remains accepted on read because SoftDeleteFilter
	// matches both casings.
	StateDELETED IndexState = "DELETED"

	// ── Deprecated (pre-Task 2) — kept for DB backward-compat ──────

	// StateIndexPending — DEPRECATED. Worker hit a retryable error;
	// row is waiting for the next claim slot. Post-Task 2 this is
	// subsumed: embedding retries use StateEmbeddingFailed → re-enqueue;
	// indexing retries use StateIndexingFailed → re-enqueue. The
	// state remains valid so existing rows with INDEX_PENDING are
	// not orphaned; new writers should use the granular states.
	StateIndexPending IndexState = "INDEX_PENDING"

	// StateIndexFailed — DEPRECATED. Catch-all failure terminal.
	// Post-Task 2 this is split into EMBEDDING_FAILED (no vectors)
	// and INDEXING_FAILED (vectors exist, Qdrant missing). The state
	// remains valid for existing rows; new writers use the granular
	// states.
	StateIndexFailed IndexState = "INDEX_FAILED"

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

// Valid returns true if s is one of the canonical IndexState values.
// Both old (pre-Task 2) and new (Task 2) states are accepted so
// existing DB rows are not orphaned. Legacy lowercase values are
// intentionally rejected.
func (s IndexState) Valid() bool {
	switch s {
	case StateNotIndexable,
		StateDiscovered,
		// Task 2 granular states
		StateEmbedding, StateEmbedded,
		StateIndexing, StateIndexed,
		StateEmbeddingFailed, StateIndexingFailed,
		// PR-QDRANT-INDEXCLIP-GUARD: transient retry-pending
		// state stamped when the indexer was offline at the
		// moment a new event arrived.
		StateIndexingSkippedNoIndexer,
		// Deletion states
		StateIndexDeletePending, StateDELETED,
		// Deprecated pre-Task 2 states (backward-compat)
		StateIndexPending, StateIndexFailed:
		return true
	}
	return false
}

// IsTerminal returns true if the state is a terminal value (no further
// automatic transitions expected unless a new event is enqueued).
// Used by outbox worker pre-flight to short-circuit gates.
//
// Task 2: EMBEDDING_FAILED and INDEXING_FAILED are both terminal —
// the worker will not re-touch them unless an operator re-enqueues.
func (s IndexState) IsTerminal() bool {
	switch s {
	case StateNotIndexable, StateIndexed, StateDELETED,
		StateEmbeddingFailed, StateIndexingFailed,
		StateIndexFailed: // deprecated, treated as terminal
		return true
	}
	return false
}

// IsFailedTerminal returns true if the state is a failure terminal
// (operator-must-intervene). Post-Task 2 this includes the granular
// EMBEDDING_FAILED and INDEXING_FAILED, plus the legacy INDEX_FAILED.
func (s IndexState) IsFailedTerminal() bool {
	return s == StateIndexFailed || s == StateEmbeddingFailed || s == StateIndexingFailed
}

// IsDeletedCanonical returns true if the row is in a tombstone state
// from any of the two deletion-side terminals (DELETE_PENDING during
// the in-flight window, DELETED after commit). IndexDeleteHandler's
// idempotency pre-flight consults this in addition to
// LifecycleState — see index_delete.go for the contract.
func (s IndexState) IsDeletedCanonical() bool {
	return s == StateIndexDeletePending || s == StateDELETED
}

// IsRetryPending returns true if the state is a transient
// retry-pending marker. PR-QDRANT-INDEXCLIP-GUARD (July 2026):
// StateIndexingSkippedNoIndexer is the canonical "we tried, the
// indexer was offline" state — the event stays in the outbox
// pool's pending bucket and is re-emitted when the operator
// re-enables the indexer.
//
// Distinct from IsTerminal (which is the "no further automatic
// transitions expected" predicate) and from IsFailedTerminal
// (which is the "operator must intervene to recover" predicate):
// a retry-pending row is neither done nor stuck — it is
// mid-loop. godlike/07 fail-closed: a row in this state should
// NOT be promoted to terminal until the next retry attempt
// returns its real outcome.
func (s IndexState) IsRetryPending() bool {
	return s == StateIndexingSkippedNoIndexer
}

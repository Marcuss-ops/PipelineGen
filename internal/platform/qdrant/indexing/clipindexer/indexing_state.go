package clipindexer

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
	indexing "github.com/Marcuss-ops/PipelineGen/internal/kernel/indexing"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

// ErrIndexSuperseded is retained as a provider compatibility alias. The
// canonical typed error is owned by kernel/indexing so application consumers
// can classify it without importing the Qdrant provider.
type ErrIndexSuperseded = indexing.ErrIndexSuperseded

// setIndexState atomically writes the canonical index_state column
// (media_assets.index_state, QDRANT-002 PR6 / migration 094) + the
// matching index_state_updated_at stamp. lastError is mirrored into
// metadata_json.$.last_index_error for operator audit — keep that
// sidecar because operators grep on
// `metadata_json LIKE '%last_index_error%'` today and a change
// there is out of scope for PR6.
//
// The previous implementation used a `json_set` chain inside
// metadata_json.$.index_state. That worked but: (a) it was
// un-indexable by SQLite (json_extract cost on every scan), (b) the
// values were an ad-hoc lowercase alphabet unrelated to the
// canonical state machine, (c) IndexedAt/databases couldn't
// CHECK-constrain a JSON-set value. After PR6 the column is the
// single source of truth and a CHECK constraint is the natural
// follow-up (in a separate migration — keeping PR6 to "promote
// without re-shaping").
//
// Defense in depth: setIndexState refuses to write
// asset.StateIndexed. INDEXED is the success terminal — write it
// via setIndexedAt, which folds the sidecar metadata (indexed_at,
// indexed_content_hash, embedding_model, embedding_model_version)
// into a SINGLE atomic UPDATE (the thinker's question G answer).
// A future refactor that accidentally passes StateIndexed to
// setIndexState panics loudly rather than silently double-counting
// the MediaIndexSuccessTotal metric.
func (s *Service) setIndexState(ctx context.Context, clipID string, state asset.IndexState, lastError string) error {
	if state == asset.StateIndexed {
		panic("clipindexer.setIndexState must NOT write INDEXED — use setIndexedAt (writes the indexed_at / indexed_content_hash sidecars in a single atomic UPDATE)")
	}
	// Defense in depth (PR6 invariant): refuse empty IndexState
	// explicitly. The pre-PR6 "empty as no-op marker" pattern is
	// retired — a worker that misconfigures the state enum and
	// passes `IndexState("")` would otherwise flip the column to
	// empty and the row would re-read as the column's DEFAULT
	// (DISCOVERED), silently losing any INDEXED / INDEX_FAILED /
	// DELETED pre-state. Today the only legitimate "did nothing"
	// signal is "didn't call setIndexState at all"; if a future
	// caller needs an explicit no-op, add a typed setter (e.g.
	// setIndexStateNoop) rather than conflating empty with no-op.
	if state == "" {
		return fmt.Errorf("setIndexState: refusing empty state for %s (silent garbage write would lose pre-state)", clipID)
	}
	if !state.Valid() {
		// Defense in depth against an enum drift: refuse unknown
		// state values instead of writing a garbage value. Catches
		// the legacy lowercase alphabet (legacyStateEmbedding etc.)
		// that pre-PR6 callers may still pass if their callers
		// haven't been migrated to the canonical enum yet.
		return fmt.Errorf("setIndexState: refusing unknown state %q for %s", state, clipID)
	}

	source := s.sourceLabel(ctx, clipID)
	if s == nil || s.assetMutator == nil {
		return fmt.Errorf("setIndexState: canonical asset mutation committer is not wired for %s", clipID)
	}
	if err := s.assetMutator.SetIndexState(ctx, clipID, state, lastError); err != nil {
		return fmt.Errorf("setIndexState for %s (state=%s): %w", clipID, state, err)
	}

	// Metric increments: only transient / failure states here.
	// Terminal success (INDEXED) is incremented by setIndexedAt —
	// the success-path writer. This split prevents a future
	// refactor from double-counting MediaIndexSuccessTotal when it
	// mistakenly passes StateIndexed to setIndexState.
	switch state {
	case asset.StateIndexing:
		// No metric: in-flight Qdrant work is not directly observable.
	case asset.StateEmbedding:
		// No metric: in-flight embedding work.
	case asset.StateEmbedded:
		// No metric: intermediate state; embeddings saved, awaiting upsert.
	case asset.StateEmbeddingFailed:
		metrics.MediaIndexFailureTotal.WithLabelValues(source).Inc()
		metrics.StaleAssets.WithLabelValues(source, "embedding_failed").Inc()
	case asset.StateIndexingFailed:
		metrics.MediaIndexFailureTotal.WithLabelValues(source).Inc()
		metrics.StaleAssets.WithLabelValues(source, "indexing_failed").Inc()
	case asset.StateDiscovered:
		// Initial state; no metric. Stale-Assets gauge remains at 0.
	}

	s.log.Debug("index state transition",
		zap.String("clip_id", clipID),
		zap.String("state", string(state)))
	return nil
}

// sourceLabel reads provenance from the canonical SQLite asset row. Asset IDs
// are opaque identifiers and must never be parsed to infer source provenance.
// Metrics remain available for legacy rows whose source is not populated by
// using the explicit unknown bucket.
func (s *Service) sourceLabel(ctx context.Context, assetID string) string {
	if s == nil || s.db == nil || assetID == "" {
		return "other"
	}
	var source string
	if err := s.db.QueryRowContext(ctx,
		"SELECT source FROM media_assets WHERE id = ?", assetID).Scan(&source); err != nil {
		return "other"
	}
	if source == "" {
		return "other"
	}
	return source
}

// setIndexedAt persists the canonical INDEXED state (column flip)
// PLUS the indexed-completion sidecars in ONE atomic UPDATE. Splitting
// the column write and the sidecar write is incorrect — a timeout
// between the two would leave media_assets.index_state = "INDEXED"
// while metadata_json.$.indexed_at is empty, and the fast-path check
// would race on whatever value the reader sees first.
//
// Mirrors the pre-PR6 json_set chain on metadata_json
// ($.index_state='indexed', $.indexed_at, $.indexed_content_hash,
// $.embedding_model, $.embedding_model_version) PLUS the
// column-level flip (index_state = 'INDEXED', index_state_updated_at).
// Migration 094's backfill brush-handles pre-PR6 rows: they start
// at column 'DISCOVERED' (the sentinel) and the worker eventually
// re-touches them via the next outbox event, normalising to INDEXED.
//
// The order of fields in the UPDATE statement is metadata_json LAST
// so a reader that observes the row mid-write via SQLite's read
// snapshot sees the column flip first; the sidecars are observable
// "next". Both are inside the same atomic write so observers can't
// catch a between-the-two state, but the column-first write
// ordering is defensive in case a future refactor splits the
// UPDATE in error.
func (s *Service) setIndexedAt(ctx context.Context, clipID, contentHash, sourceVersion string) error {
	if s == nil || s.assetMutator == nil {
		return fmt.Errorf("setIndexedAt: canonical asset mutation committer is not wired for %s", clipID)
	}
	ok, err := s.assetMutator.SetIndexed(ctx, clipID, contentHash, sourceVersion,
		embeddingModel, embeddingModelVersion, coreembedding.CanonicalText.Hash())
	if err != nil {
		return fmt.Errorf("set indexed_at for %s: %w", clipID, err)
	}
	if !ok {
		return &ErrIndexSuperseded{ClipID: clipID, SourceVersion: sourceVersion}
	}
	metrics.MediaIndexSuccessTotal.WithLabelValues(s.sourceLabel(ctx, clipID)).Inc()
	return nil
}

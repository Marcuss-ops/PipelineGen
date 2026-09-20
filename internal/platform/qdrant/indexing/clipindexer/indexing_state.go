package clipindexer

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

// setIndexState atomically writes a NON-terminal index_state transition on the
// canonical media SSOT through the asset-mutation committer (PostgreSQL).
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-20): the historical
// setIndexedAt helper (the SQLite UPDATE that flipped the terminal INDEXED
// state) was DELETED. PostgreSQL
// PostgresIndexWorker is now the ONLY writer of the INDEXED terminal state, so
// this file no longer performs any media_assets SQL read or write. The only
// remaining caller is MarkIndexingSkippedNoIndexer (the IndexerStateUpdater
// port), which writes the transient INDEXING_SKIPPED_NO_INDEXER state.
//
// Defense in depth: setIndexState refuses to write asset.StateIndexed. INDEXED
// is the success terminal owned by PostgresIndexWorker; a future refactor that
// accidentally passes StateIndexed here panics loudly rather than
// double-writing the terminal state.
func (s *Service) setIndexState(ctx context.Context, clipID string, state asset.IndexState, lastError string) error {
	if state == asset.StateIndexed {
		panic("clipindexer.setIndexState must NOT write INDEXED — the terminal state is owned by PostgresIndexWorker on the media SSOT")
	}
	// Defense in depth (PR6 invariant): refuse empty IndexState explicitly.
	// The pre-PR6 "empty as no-op marker" pattern is retired — a worker that
	// misconfigures the state enum and passes `IndexState("")` would otherwise
	// flip the column to empty and the row would re-read as the column's
	// DEFAULT (DISCOVERED), silently losing any INDEXED / INDEX_FAILED /
	// DELETED pre-state.
	if state == "" {
		return fmt.Errorf("setIndexState: refusing empty state for %s (silent garbage write would lose pre-state)", clipID)
	}
	if !state.Valid() {
		// Defense in depth against an enum drift: refuse unknown state values
		// instead of writing a garbage value.
		return fmt.Errorf("setIndexState: refusing unknown state %q for %s", state, clipID)
	}

	if s == nil || s.assetMutator == nil {
		return fmt.Errorf("setIndexState: canonical asset mutation committer is not wired for %s", clipID)
	}
	if err := s.assetMutator.SetIndexState(ctx, clipID, state, lastError); err != nil {
		return fmt.Errorf("setIndexState for %s (state=%s): %w", clipID, state, err)
	}

	// Metric increments: only transient / failure states here. Terminal
	// success (INDEXED) is owned by PostgresIndexWorker on the media SSOT.
	//
	// The historical per-source label was derived from a SQLite read of
	// media_assets.source; that read is retired with the legacy read plane, so
	// the metric uses the explicit unknown bucket rather than grading a second
	// engine.
	const source = "other"
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

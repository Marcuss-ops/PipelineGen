// Package clipindexer — state_writer.go declares the canonical
// IndexerStateUpdater port + the *Service concrete that satisfies
// it. The port lets out-of-package consumers (notably the
// IndexingHandler in internal/capabilities/jobs/outbox) record
// transient retry-pending side-effects on media_assets.index_state
// without reaching back into SQLite directly.
//
// godlike/06 SSOT: *Service is the canonical SOLE writer of
// media_assets.index_state in production (per the FASE 3.7
// monitor-infra-import ban — clipindexer owns the schema column
// and the typed-state machine, no other package writes it
// directly). The port exists so callers get the typed surface
// without coupling to *Service or to internal SQL helpers.
package clipindexer

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// IndexerStateUpdater is the per-asset index_state writer the
// IndexingHandler uses to stamp retry-pending side-effects on
// media_assets without coupling to internal SQL.
//
// godlike/06 SSOT: typed-port so callers (e.g. outbox.IndexingHandler)
// depend on the interface, not the concrete. Production wiring
// (BuildOutboxBundle) hands the SAME *clipindexer.Service
// concrete to BOTH the IndexClipper port and the
// IndexingStateUpdater port — single source-of-truth for "any
// caller that wants to change media_assets.index_state".
//
// PR-QDRANT-INDEXCLIP-GUARD (July 2026): today the only side-effect
// flush the handler needs is MarkIndexingSkippedNoIndexer (the
// sentinel-driven INDEXING_SKIPPED_NO_INDEXER transition). Future
// transient states (e.g. INDEXING_PENDING_RETRY after another
// enumeration) should be added here so the package boundary
// stays stable and godlike/06 SSOT remains intact.
type IndexerStateUpdater interface {
	// MarkIndexingSkippedNoIndexer transitions the asset's
	// index_state to StateIndexingSkippedNoIndexer.
	//
	// Fired by the IndexingHandler after detecting
	// ErrIndexClipDisabledButEventRequested so the canonical
	// transient retry-pending state is observable on
	// dashboards even while the outbox event stays pending in
	// the retry pool.
	//
	// godlike/07 fail-closed: a non-nil error from this call
	// MUST NOT abort the retry path — the handler logs the
	// error at Warn level and continues to return the
	// retryable error so the outbox event is not silently
	// lost.
	MarkIndexingSkippedNoIndexer(ctx context.Context, assetID string) error
}

// Compile-time assertion: *Service implements the port so the
// production BuildOutboxBundle path can wire the indexer into
// both the IndexingHandler.IndexClipper port and its
// IndexingStateUpdater port from a single concrete (no second
// adapter layer needed). godlike/06 SSOT: one concrete per
// port-binding; if a future refactor splits the concrete, this
// assertion catches the drift at build time, not runtime.
var _ IndexerStateUpdater = (*Service)(nil)

// MarkIndexingSkippedNoIndexer is the *Service concrete of the
// IndexerStateUpdater port.
//
// Routes through the private setIndexState helper which refuses
// to write StateIndexed (the canonical panic guard enforces the
// SSOT that ONLY setIndexedAt may write INDEXED — see
// indexing_state.go for the rationale).
// StateIndexingSkippedNoIndexer is NOT in the panic list so
// this write is permitted without touching the terminal-state
// fence.
//
// godlike/06 SSOT: this method is the SOLE caller of
// `setIndexState(ctx, clipID, asset.StateIndexingSkippedNoIndexer, "")`
// in production code. Test files may use setIndexState directly.
func (s *Service) MarkIndexingSkippedNoIndexer(ctx context.Context, assetID string) error {
	if s == nil {
		// godlike/07 minimum-blast-radius: a nil-receiver Service
		// (rare — only constructors in weird wiring paths) must
		// produce a typed failure rather than panic the worker.
		// Re-use the canonical disabled-event sentinel because it
		// carries the same semantic ("indexer not operational")
		// as the underlying nil-state — the consumer sees the
		// signal via errors.Is and routes to retry accordingly.
		return ErrIndexClipDisabledButEventRequested
	}
	return s.setIndexState(ctx, assetID, asset.StateIndexingSkippedNoIndexer, "")
}

// Package outbox — Dispatcher is the canonical ingestion entry point.
//
// PR1 invariant: every code path that mutates media_assets and triggers
// vector indexing MUST route through Dispatcher.EnqueueAndIndex. Doing so
// guarantees that the metadata write (media_assets) and the indexing job
// (outbox_events insert) are committed atomically — no orphan jobs, no
// orphan embeddings.
//
// All callers must use Dispatcher (EnqueueAndIndex for inserts,
// EnqueueAndDelete for deletes). No production path mutates media_assets
// and indexes outside this dispatcher.
package outbox

import (
	"context"
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

type DiscoveryCommitter interface {
	CommitDiscoveredAsset(context.Context, *sql.Tx, *asset.Asset, asset.LifecycleState, asset.IndexState) error
}

// ── Dispatcher — canonical ingestion entry point ───────────────────────

// Dispatcher is the ingestion entry point for the canonical
// UPSERT + INSERT-IN-OUTBOX pattern AND the canonical DEL +
// INSERT-IN-OUTBOX pattern (QDRANT-002 PR7 EnqueueAndDelete).
//
// Every ingestion path (catalogsync, YouTube clip registration, Artlist
// clip processing, stock pipeline, manual upload, transcript updates, …)
// MUST funnel through Dispatcher.EnqueueAndIndex. Every deletion path
// MUST funnel through Dispatcher.EnqueueAndDelete.
//
// By colocating the metadata write and the outbox_events insert in a
// single transaction we either commit both or neither.
type Dispatcher struct {
	clips              ClipsUpserter
	stateWriter        ClipsStateWriter
	outboxEventsRepo   outboxEnqueuer
	txmgr              TxManager
	log                *zap.Logger
	discoveryCommitter DiscoveryCommitter
	canonicalCommitter persistence.AssetCommitter
	canonicalWriter    persistence.CanonicalAssetWriter
}

// NewDispatcher wires a Dispatcher against the canonical dependencies.
// clips is typically *assets.ClipsRepository (which implements ClipsUpserter).
// stateWriter is typically the same *assets.ClipsRepository (which
// implements ClipsStateWriter from PR7).
// outboxEventsRepo is the canonical outbox_events repository for
// asset.index.requested + asset.index.delete_requested event enqueue.
func NewDispatcher(
	clips ClipsUpserter,
	stateWriter ClipsStateWriter,
	outboxEventsRepo outboxEnqueuer,
	txmgr TxManager,
	log *zap.Logger,
	extra ...any,
) *Dispatcher {
	var discoveryCommitter DiscoveryCommitter
	var canonicalCommitter persistence.AssetCommitter
	var canonicalWriter persistence.CanonicalAssetWriter
	for _, value := range extra {
		if discoveryCommitter == nil {
			if candidate, ok := value.(DiscoveryCommitter); ok {
				discoveryCommitter = candidate
			}
		}
		if canonicalWriter == nil {
			if candidate, ok := value.(persistence.CanonicalAssetWriter); ok {
				canonicalWriter = candidate
			}
		}
		if canonicalCommitter == nil {
			if candidate, ok := value.(persistence.AssetCommitter); ok {
				canonicalCommitter = candidate
			}
		}
	}
	if canonicalCommitter == nil && canonicalWriter != nil {
		canonicalCommitter = canonicalWriter
	}
	return &Dispatcher{
		clips:              clips,
		stateWriter:        stateWriter,
		outboxEventsRepo:   outboxEventsRepo,
		txmgr:              txmgr,
		log:                log,
		discoveryCommitter: discoveryCommitter,
		canonicalCommitter: canonicalCommitter,
		canonicalWriter:    canonicalWriter,
	}
}

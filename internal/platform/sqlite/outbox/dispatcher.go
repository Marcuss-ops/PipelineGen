// Package outbox — Dispatcher is the canonical ingestion entry point.
//
// PR1 invariant: every code path that mutates media_assets and triggers
// vector indexing MUST route through Dispatcher.EnqueueAndIndex. Doing so
// guarantees that the media write and the indexing job are committed
// atomically — no orphan jobs, no orphan embeddings.
//
// All callers must use Dispatcher (EnqueueAndIndex for inserts,
// EnqueueAndDelete for deletes).
//
// MEDIA-SSOT (September 2026): the media domain authority is PostgreSQL.
// The dispatcher therefore owns the SQLite OPERATIONAL outbox only
// (Drive/webhook/async side effects) and delegates every media commit to
// the canonical, self-owned `persistence.AssetCommitter`
// (`*pgmedia.PostgresMediaCommitter`). It no longer exposes — and cannot be
// wired with — a SQLite transaction handed to a media writer: that
// cross-engine seam was the single most dangerous construct in the old
// composition, because a SQLite `*sql.Tx` reaching PostgreSQL SQL fails
// only at first runtime statement. Removing it from the type system makes
// the bug unrepresentable.
package outbox

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// DiscoveryCommitAndIndexCommitter is the self-owned-transaction discovery
// port. The canonical media committer lives on a DIFFERENT engine than the
// dispatcher's SQLite TxManager, so it opens its own transaction and commits
// the discovered asset atomically on that engine, deliberately emitting no
// indexing request at discovery time.
//
// It is the ONLY discovery contract the dispatcher accepts: there is no
// tx-bound `DiscoveryCommitter` counterpart, because handing a SQLite
// `*sql.Tx` to a PostgreSQL writer is precisely the defect this package no
// longer permits.
type DiscoveryCommitAndIndexCommitter interface {
	CommitDiscoveredAssetAndIndex(ctx context.Context, clip *asset.Asset, lifecycle asset.LifecycleState, idx asset.IndexState) error
}

// ── Dispatcher — canonical ingestion entry point ───────────────────────

// Dispatcher is the ingestion entry point for the canonical
// UPSERT + INSERT-IN-OUTBOX pattern AND the canonical DEL +
// INSERT-IN-OUTBOX pattern.
//
// Every ingestion path (catalogsync, YouTube clip registration, Artlist
// clip processing, stock pipeline, manual upload, transcript updates, …)
// MUST funnel through Dispatcher.EnqueueAndIndex. Every deletion path
// MUST funnel through Dispatcher.EnqueueAndDelete.
type Dispatcher struct {
	outboxEventsRepo outboxEnqueuer
	txmgr            TxManager
	log              *zap.Logger
	// committer is the canonical media writer. It is the SINGLE media
	// mutation surface the dispatcher may invoke, and every method it
	// exposes is self-owned-transaction: nothing the dispatcher holds can
	// place a SQLite transaction into a media write.
	committer persistence.AssetCommitter
}

// NewDispatcher wires a Dispatcher against the canonical dependencies.
//
// outboxEventsRepo is the canonical SQLite outbox_events repository, used
// only for the OPERATIONAL (non-media) delete/restore envelopes this
// dispatcher still owns.
//
// txmgr is the SQLite transaction manager for those operational envelopes.
//
// committer is the canonical media AssetCommitter
// (`*pgmedia.PostgresMediaCommitter`). The dispatcher only ever calls its
// self-owned entry points (CommitAndIndex / CommitAsset /
// CommitDiscoveredAssetAndIndex), so no SQLite transaction can cross into
// the media SSOT.
//
// There is deliberately NO variadic `extra ...any` parameter: a compile-time
// type is the only way to prevent the composition root from assembling an
// incoherent (tx-bound cross-engine) dispatcher that the compiler would
// otherwise accept.
func NewDispatcher(
	outboxEventsRepo outboxEnqueuer,
	txmgr TxManager,
	log *zap.Logger,
	committer persistence.AssetCommitter,
) *Dispatcher {
	if log == nil {
		log = zap.NewNop()
	}
	return &Dispatcher{
		outboxEventsRepo: outboxEventsRepo,
		txmgr:            txmgr,
		log:              log,
		committer:        committer,
	}
}

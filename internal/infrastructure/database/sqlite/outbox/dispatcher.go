// Package outbox — Dispatcher is the canonical ingestion entry point.
//
// PR1 invariant: every code path that mutates media_assets and triggers
// vector indexing MUST route through Dispatcher.EnqueueAndIndex. Doing so
// guarantees that the metadata write (media_assets) and the indexing job
// (outbox_events insert) are committed atomically — no orphan jobs, no
// orphan embeddings.
//
// The ONLY legitimate way to bypass the outbox is the DirectIndexer, which
// is restricted to admin reindex endpoints (see direct_indexer.go for
// the rule). All other callers must use Dispatcher.
package outbox

import (
	"go.uber.org/zap"
)

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
	clips            ClipsUpserter
	stateWriter      ClipsStateWriter
	outboxEventsRepo outboxEnqueuer
	txmgr            TxManager
	log              *zap.Logger
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
) *Dispatcher {
	return &Dispatcher{
		clips:            clips,
		stateWriter:      stateWriter,
		outboxEventsRepo: outboxEventsRepo,
		txmgr:            txmgr,
		log:              log,
	}
}

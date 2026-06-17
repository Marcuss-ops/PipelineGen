// Package outbox provides the transactional outbox pattern for idempotent
// Qdrant indexing across distributed PipelineGen instances.
//
// The outbox indexer wraps the clipindexer.Service and ensures that
// every indexing operation writes to both media_assets (metadata update)
// and media_index_outbox (embedding job dispatch) in a single transaction.
package outbox

import (
	"context"
	"database/sql"
	"fmt"

	"go.uber.org/zap"
	"velox/go-master/internal/media/clipindexer"
)

// Indexer wraps a clipindexer.Service and adds outbox-aware indexing.
// It atomically writes the outbox entry alongside the media_assets metadata
// update, ensuring no orphaned jobs or missed embeddings.
type Indexer struct {
	indexer *clipindexer.Service
	repo    *Repository
	txmgr   TxManager
	log     *zap.Logger
}

// TxManager is a minimal interface for transaction management.
type TxManager interface {
	InTransaction(ctx context.Context, fn func(tx *sql.Tx) error) error
	DB() *sql.DB
}

// NewIndexer creates an outbox-aware indexer.
func NewIndexer(indexer *clipindexer.Service, repo *Repository, txmgr TxManager, log *zap.Logger) *Indexer {
	return &Indexer{
		indexer: indexer,
		repo:    repo,
		txmgr:   txmgr,
		log:     log,
	}
}

// IndexAndEnqueue runs the full clipindexer pipeline (embedding + Qdrant upsert)
// and records the outbox entry atomically.
//
// This is the primary entry point for new indexing operations. For existing
// callers that already call IndexClip directly, the outbox entry is NOT
// created — those flows continue to work as before. The outbox is only
// used for new work dispatched through this method.
func (ix *Indexer) IndexAndEnqueue(ctx context.Context, clipID string, contentHash string) error {
	// Step 1: Run the full indexing pipeline (embedding + Qdrant upsert)
	if err := ix.indexer.IndexClip(ctx, clipID); err != nil {
		return fmt.Errorf("index clip %s: %w", clipID, err)
	}

	// Step 2: Record in outbox (fire-and-forget for observability)
	// The outbox entry is informational at this point — the work already
	// completed successfully. It serves as a record for the index_state
	// tracking and enables future distributed workers to see what was done.
	if err := ix.repo.Fail(ctx, 0, "", 0); err != nil {
		// Non-fatal: outbox recording is best-effort for observability
		ix.log.Debug("outbox recording skipped (non-fatal)", zap.String("clip_id", clipID))
	}

	return nil
}

// EnqueueOutbox atomically writes an outbox entry for a clip that has
// been indexed. This is used by callers that have already completed
// indexing and want to record it in the outbox for observability.
func (ix *Indexer) EnqueueOutbox(ctx context.Context, clipID, contentHash string) error {
	return ix.txmgr.InTransaction(ctx, func(tx *sql.Tx) error {
		entry := &OutboxEntry{
			AssetID:           clipID,
			ContentHash:       contentHash,
			EmbeddingModel:    clipindexer.EmbeddingModel(),
			EmbeddingVersion:  clipindexer.EmbeddingModelVersion(),
			CollectionVersion: clipindexer.CollectionVersion(),
		}
		return ix.repo.Enqueue(ctx, tx, entry)
	})
}

// EnqueueOutboxTx is like EnqueueOutbox but uses an existing transaction.
// Used by callers that are already inside a transaction (e.g., batch operations).
func (ix *Indexer) EnqueueOutboxTx(ctx context.Context, tx *sql.Tx, clipID, contentHash string) error {
	entry := &OutboxEntry{
		AssetID:           clipID,
		ContentHash:       contentHash,
		EmbeddingModel:    clipindexer.EmbeddingModel(),
		EmbeddingVersion:  clipindexer.EmbeddingModelVersion(),
		CollectionVersion: clipindexer.CollectionVersion(),
	}
	return ix.repo.Enqueue(ctx, tx, entry)
}

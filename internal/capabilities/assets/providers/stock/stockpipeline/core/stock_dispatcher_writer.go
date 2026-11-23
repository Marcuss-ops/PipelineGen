package core

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// StockDispatcherWriter adapts the canonical asset/outbox dispatcher to the
// resilient orchestrator's transactional writer port. The dispatcher owns the
// single canonical commit: PostgreSQL media_assets + the media index outbox
// event. Search text travels with that commit (`clip.SearchText` →
// `media_assets.search_text`), so no second index is maintained here.
type StockDispatcherWriter struct {
	dispatcher StockChunkDispatcher
}

func (w StockDispatcherWriter) WriteAndEnqueue(ctx context.Context, clip *asset.Asset, fileHash string) error {
	return w.dispatcher.EnqueueAndIndex(ctx, clip, fileHash)
}

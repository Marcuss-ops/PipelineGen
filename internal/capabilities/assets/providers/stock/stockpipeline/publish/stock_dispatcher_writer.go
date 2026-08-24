package publish

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// stockDispatcherWriter adapts the canonical SQLite asset/outbox
// dispatcher to the resilient orchestrator's transactional writer port.
// The dispatcher owns the single transaction for media_assets and the
// asset.index.requested outbox event.
type stockDispatcherWriter struct {
	dispatcher  stockChunkDispatcher
	termUpdater stockClipsSearchTermUpdater
}

func (w stockDispatcherWriter) WriteAndEnqueue(ctx context.Context, clip *asset.Asset, fileHash string) error {
	if err := w.dispatcher.EnqueueAndIndex(ctx, clip, fileHash); err != nil {
		return err
	}
	if w.termUpdater != nil {
		if err := w.termUpdater.UpdateSearchTerms(ctx, clip.ID, string(clip.Source), clip.Name, clip.Tags, clip.SearchText); err != nil {
			return err
		}
	}
	return nil
}

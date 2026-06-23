// Package artlist — dispatch_bridge.go
//
// DispatchBridge is the single entry point for clip persistence and indexing
// in the artlist pipeline. It routes through the canonical media_index_outbox
// dispatcher (atomic upsert + outbox enqueue) when wired, otherwise it
// preserves the legacy UpsertClip + IndexClip fallback.
//
// PR2.5: clipsRepo → assetStore (AssetStore port), clipIndexer → indexer
// (Indexer port). Both ports declare exactly the methods this bridge
// uses (UpsertClip + IndexClip + IsEnabled) so the swap is mechanical
// with no behavior change.
package artlist

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// dispatchBridge consolidates the canonical write/index decision into one
// helper. It pulls its building blocks from the surrounding Service so
// callers don't have to plumb them through.
//
// The dispatcher is preferred in production. Legacy fallback remains so
// partial-wiring deployments and tests keep working.
type dispatchBridge struct {
	dispatcher Dispatcher
	assetStore AssetStore
	indexer    Indexer
	log        *zap.Logger
}

// newDispatchBridge returns a bridge wired to the Service's current
// upstream dependencies. A nil dispatcher is allowed.
//
// PR2.5: pulls ports (assetStore, indexer, dispatcher) from the
// surrounding Service. The legacy concrete fields are gone; this is
// the only path the rest of the package uses.
func (s *Service) newDispatchBridge() *dispatchBridge {
	return &dispatchBridge{
		dispatcher: s.dispatcher,
		assetStore: s.assetStore,
		indexer:    s.indexer,
		log:        s.log,
	}
}

// EnqueueOrFallback routes clip persistence and indexing through the
// canonical media_index_outbox dispatcher when wired; otherwise it falls
// back to UpsertClip + IndexClip (the legacy pair), with a WARN log on
// every fallback so SREs see it in production.
func (b *dispatchBridge) EnqueueOrFallback(ctx context.Context, clip *asset.Asset, hash string) {
	if clip == nil || clip.ID == "" {
		return
	}
	if b.dispatcher != nil {
		if err := b.dispatcher.EnqueueAndIndex(ctx, clip, hash); err != nil {
			b.log.Warn("dispatch_bridge: dispatcher.EnqueueAndIndex failed",
				zap.String("clip_id", clip.ID), zap.Error(err))
		}
		return
	}
	b.log.Warn("dispatch_bridge: legacy fallback (UpsertClip + IndexClip) — canonical dispatcher should be wired in production",
		zap.String("clip_id", clip.ID))
	if b.assetStore != nil {
		if err := b.assetStore.UpsertClip(ctx, clip); err != nil {
			b.log.Warn("dispatch_bridge: legacy UpsertClip failed",
				zap.String("clip_id", clip.ID), zap.Error(err))
		}
	}
	if b.indexer != nil && b.indexer.IsEnabled() {
		if err := b.indexer.IndexClip(ctx, clip.ID); err != nil {
			b.log.Warn("dispatch_bridge: legacy IndexClip failed",
				zap.String("clip_id", clip.ID), zap.Error(err))
		}
	}
}

// Dispatch keeps the older call sites compiling while the pipeline
// transitions to EnqueueOrFallback.
func (b *dispatchBridge) Dispatch(ctx context.Context, clip *asset.Asset, hash string) error {
	b.EnqueueOrFallback(ctx, clip, hash)
	return nil
}

// IsCanonical reports whether the canonical dispatcher is wired.
func (b *dispatchBridge) IsCanonical() bool {
	return b != nil && b.dispatcher != nil
}

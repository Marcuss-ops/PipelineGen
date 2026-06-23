// Package artlist — dispatch_bridge.go
//
// DispatchBridge is the single entry point for clip persistence and indexing
// in the artlist pipeline. It routes through the canonical media_index_outbox
// dispatcher (atomic upsert + outbox enqueue). The dispatcher is required —
// construction fails if it is nil.
//
// PR2.5: clipsRepo → assetStore (AssetStore port), clipIndexer → indexer
// (Indexer port). Both ports declare exactly the methods this bridge
// uses (UpsertClip + IndexClip + IsEnabled) so the swap is mechanical
// with no behavior change.
package artlist

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// dispatchBridge consolidates the canonical write/index decision into one
// helper. It pulls its building blocks from the surrounding Service so
// callers don't have to plumb them through.
//
// The dispatcher is REQUIRED — production wiring must provide it.
// Calling dispatchBridge.Dispatch routes clip persistence and indexing
// through the canonical outbox dispatcher (atomic upsert + outbox enqueue).
type dispatchBridge struct {
	dispatcher Dispatcher
	assetStore AssetStore
	indexer    Indexer
	log        *zap.Logger
}

// newDispatchBridge returns a bridge wired to the Service's current
// upstream dependencies. Returns an error if the dispatcher is nil.
//
// PR2.5: pulls ports (assetStore, indexer, dispatcher) from the
// surrounding Service. The legacy concrete fields are gone; this is
// the only path the rest of the package uses.
func (s *Service) newDispatchBridge() (*dispatchBridge, error) {
	if s.dispatcher == nil {
		return nil, fmt.Errorf("artlist: dispatcher is required — production wiring must provide it")
	}
	return &dispatchBridge{
		dispatcher: s.dispatcher,
		assetStore: s.assetStore,
		indexer:    s.indexer,
		log:        s.log,
	}, nil
}

// Dispatch routes clip persistence and indexing through the canonical
// media_index_outbox dispatcher (atomic upsert + outbox enqueue).
//
// The dispatcher is required — this method returns an error if the
// bridge was constructed without one.
func (b *dispatchBridge) Dispatch(ctx context.Context, clip *asset.Asset, hash string) error {
	if clip == nil || clip.ID == "" {
		return nil
	}
	if b.dispatcher == nil {
		return fmt.Errorf("dispatch_bridge: dispatcher is nil")
	}
	if err := b.dispatcher.EnqueueAndIndex(ctx, clip, hash); err != nil {
		return fmt.Errorf("dispatch_bridge: dispatcher.EnqueueAndIndex: %w", err)
	}
	return nil
}

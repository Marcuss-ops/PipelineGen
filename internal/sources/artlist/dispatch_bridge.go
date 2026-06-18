// Package artlist — dispatch_bridge.go
//
// The artlist pipeline has historically had a `dispatcher != nil` branch
// at three different call sites (SemanticEnricher.Enrich, stagePersistResults,
// stageIndexAsync). Each branch made the *same* legacy/canonical decision
// inline, spread across files, with subtly different error handling. The
// canonical path (outbox.Dispatcher.EnqueueAndIndex) was therefore easy
// to silently fall off, because the fallback lived next to the production
// path in three spots.
//
// DispatchBridge is the single source of truth for that decision.
// Construct it once per Service, then use its helpers. New sites MUST
// route through the bridge — direct checks of `e.dispatcher != nil` are
// forbidden by this package's lint rules.
//
// The bridge logs WARN when the legacy fallback is taken. In production,
// when the dispatcher is properly wired, those warnings should never
// appear, so a CI grep for them is a cheap integration check.
package artlist

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/media/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/outbox"
)

// dispatchBridge consolidates the canonical-vs-legacy write/index
// decision into one helper. It pulls its building blocks from the
// surrounding Service so callers don't have to plumb them through.
//
// Calling dispatchBridge.EnqueueOrFallback replaces the historical
// pattern:
//
//	if dispatcher != nil {
//	    dispatcher.EnqueueAndIndex(ctx, clip, hash)
//	} else if clipIndexer != nil {
//	    clipIndexer.IndexClip(ctx, clip.ID)
//	}
type dispatchBridge struct {
	dispatcher  *outbox.Dispatcher
	clipsRepo   *clips.Repository
	clipIndexer *clipindexer.Service
	log         *zap.Logger
}

// newDispatchBridge returns a bridge wired to the Service's current
// upstream dependencies. Cheap to construct — does not capture state.
func (s *Service) newDispatchBridge() *dispatchBridge {
	return &dispatchBridge{
		dispatcher:  s.dispatcher,
		clipsRepo:   s.artlistRepo,
		clipIndexer: s.clipIndexer,
		log:         s.log,
	}
}

// EnqueueOrFallback routes clip persistence and indexing through the
// canonical media_index_outbox dispatcher when wired; otherwise it
// falls back to UpsertClip + IndexClip (the legacy pair), with a WARN
// log on every fallback so SREs see it in production.
//
// Replaces the inline `dispatcher != nil` check that used to live at
// each call site. After this consolidation runs in production for one
// release, the legacy path is expected to be removed and this helper
// will become a no-op pass-through to dispatcher.EnqueueAndIndex.
func (b *dispatchBridge) EnqueueOrFallback(ctx context.Context, clip *models.MediaAsset, hash string) {
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
	if b.clipsRepo != nil {
		if err := b.clipsRepo.UpsertClip(ctx, clip); err != nil {
			b.log.Warn("dispatch_bridge: legacy UpsertClip failed",
				zap.String("clip_id", clip.ID), zap.Error(err))
		}
	}
	if b.clipIndexer != nil {
		if err := b.clipIndexer.IndexClip(ctx, clip.ID); err != nil {
			b.log.Warn("dispatch_bridge: legacy IndexClip failed",
				zap.String("clip_id", clip.ID), zap.Error(err))
		}
	}
}

// IsCanonical returns true when the bridge has a dispatcher wired
// AND would route through the canonical path. Use this only in rare
// branches (e.g., stageIndexAsync's no-op-with-canonical case); prefer
// always calling EnqueueOrFallback.
func (b *dispatchBridge) IsCanonical() bool {
	return b.dispatcher != nil
}

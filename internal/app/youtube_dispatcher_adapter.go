// Package app — YouTube index dispatcher adapter extracted from
// assets_register_adapters.go (PR-GODOBJ-8, July 2026).
package app

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	assetsrepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
)

// youtubeIndexDispatcherAdapter implements youtube.IndexDispatcherPort by
// composing the legacy outbox.Dispatcher with the asset-tree service.
//
// Behaviour (per thinker audit June 2026 / P0-1 commit 1 correction):
//   - Dispatcher upsert failures BUBBLE to the caller (fail-closed;
//     preserves QDRANT-asset-mutation isolation discipline)
//   - Asset-tree upsert failures are SWALLOWED at the adapter boundary
//     (fail-open; mirrors the historical `_ = s.assetTree.UpsertNode(...)`
//     warn-only behaviour of the god-method. Returns nil).
type youtubeIndexDispatcherAdapter struct {
	disp *outbox.Dispatcher
	tree *assettree.Service
}

func (a *youtubeIndexDispatcherAdapter) EnqueueAndIndex(ctx context.Context, clip *sourcing.ExistingClip, contentHash string) error {
	if a.disp == nil {
		return fmt.Errorf("youtubeIndexDispatcherAdapter: dispatcher is nil (compose-time bug — wire outbox.Dispatcher in newAssetRegisterService)")
	}
	if clip == nil {
		return fmt.Errorf("youtubeIndexDispatcherAdapter: clip is nil")
	}
	domainAsset := fromExistingClip(clip)
	if err := a.disp.EnqueueAndIndex(ctx, domainAsset, contentHash); err != nil {
		return fmt.Errorf("dispatcher upsert+outbox: %w", err)
	}
	// Asset-tree upsert is best-effort post-dispatcher. The historical
	// god-method called `_ = s.assetTree.UpsertNode(...)` ignoring
	// errors and discarding the warn log; we mirror that exact
	// behaviour in the adapter. Tree drift is a separate concern
	// tracked by PR-ASSETS-MONITOR-CONTRACT-AUDIT-2026-06-28, not the
	// YouTubeRegistrar flow.
	if a.tree != nil {
		now := time.Now().UTC()
		node := &assetsrepo.AssetNode{
			ID:        domainAsset.ID,
			Source:    string(domainAsset.Source),
			AssetID:   domainAsset.ID,
			Name:      domainAsset.Name,
			Type:      "file",
			Path:      domainAsset.Name,
			IsFolder:  false,
			DriveLink: domainAsset.DriveLink(),
			Metadata:  "{}",
			CreatedAt: now,
			UpdatedAt: now,
		}
		_ = a.tree.UpsertNode(ctx, node) // matches historical warn-only behaviour
	}
	return nil
}

// Package app — YouTube indexer + Ollama adapters
// split from youtube_adapters.go (PR-GODOBJ-Azione-4, July 2026).
//
// 3 adapters: ClipIndexerAdapter, ollamaClientAdapter, YoutubeIndexDispatcherAdapter.
package adapters

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	assetsrepo "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	clipindexer "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing/clipindexer"
)

// ── ClipIndexerAdapter ────────────────────────────────────────────────

type ClipIndexerAdapter struct {
	inner *clipindexer.Service
}

func (a *ClipIndexerAdapter) IsEnabled() bool { return a.inner.IsEnabled() }
func (a *ClipIndexerAdapter) IndexClip(ctx context.Context, id string) error {
	return a.inner.IndexClip(ctx, id)
}

// ── YoutubeIndexDispatcherAdapter ────────────────────────────────────
// Merged from youtube_dispatcher_adapter.go (PR-GODOBJ-Azione-4, July 2026).

type YoutubeIndexDispatcherAdapter struct {
	disp *outbox.Dispatcher
	tree *assettree.Service
}

func (a *YoutubeIndexDispatcherAdapter) EnqueueAndIndex(ctx context.Context, clip *sourcing.ExistingClip, contentHash string) error {
	if a.disp == nil {
		return fmt.Errorf("YoutubeIndexDispatcherAdapter: dispatcher is nil")
	}
	if clip == nil {
		return fmt.Errorf("YoutubeIndexDispatcherAdapter: clip is nil")
	}
	domainAsset := fromExistingClip(clip)
	if err := a.disp.EnqueueAndIndex(ctx, domainAsset, contentHash); err != nil {
		return fmt.Errorf("dispatcher upsert+outbox: %w", err)
	}
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
		_ = a.tree.UpsertNode(ctx, node)
	}
	return nil
}

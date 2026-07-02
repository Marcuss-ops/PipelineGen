// Package app — adapters_media_search.go bridges the infrastructure-layer
// ClipsRepository to the application-layer mediasearch.MediaReadRepository
// port consumed by the Fase 6 semanticSearchBackend.
//
// The adapter batch-fetches assets by ID from SQLite and maps the
// canonical *asset.Asset shape into mediasearch.MediaAsset. It filters
// by lifecycle_state (allowStates) so the semanticSearchBackend hydrates
// only searchable rows.
package app

import (
	"context"
	"fmt"

	mediasearch "github.com/Marcuss-ops/PipelineGen/internal/application/mediasearch"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	assets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

// mediaSearchReadAdapter wraps *assets.ClipsRepository to satisfy
// mediasearch.MediaReadRepository. The adapter batch-fetches by ID
// using the canonical repo.Get(ctx, id) path and maps each row
// into a mediasearch.MediaAsset.
type mediaSearchReadAdapter struct {
	repo *assets.ClipsRepository
}

// Compile-time assertion: mediaSearchReadAdapter satisfies
// mediasearch.MediaReadRepository.
var _ mediasearch.MediaReadRepository = (*mediaSearchReadAdapter)(nil)

// newMediaSearchReadAdapter creates the canonical composition-root
// adapter. Returns nil when repo is nil so the wiring site
// preserves the != nil discipline.
func newMediaSearchReadAdapter(repo *assets.ClipsRepository) mediasearch.MediaReadRepository {
	if repo == nil {
		return nil
	}
	return &mediaSearchReadAdapter{repo: repo}
}

// GetMany fetches assets by ID from SQLite. Each asset is mapped
// into a mediasearch.MediaAsset. Rows whose LifecycleState is not
// in allowStates are silently dropped (the Qdrant adapter already
// applies this filter upstream; the post-hydration guard is
// defence-in-depth per godlike/07).
//
// The workspace context is accepted for forward-compat with
// QDRANT-001 multi-tenancy but is currently unused (the
// media_assets.workspace_id column doesn't exist yet).
func (a *mediaSearchReadAdapter) GetMany(
	ctx context.Context,
	_ mediasearch.WorkspaceContext,
	assetIDs []string,
	allowStates []string,
) ([]mediasearch.MediaAsset, error) {
	if a == nil || a.repo == nil {
		return nil, fmt.Errorf("mediaSearchReadAdapter: not wired")
	}
	if len(assetIDs) == 0 {
		return nil, nil
	}

	// Build the allow-set for O(1) lookup.
	allowSet := make(map[string]bool, len(allowStates))
	for _, s := range allowStates {
		allowSet[s] = true
	}

	out := make([]mediasearch.MediaAsset, 0, len(assetIDs))
	for _, id := range assetIDs {
		row, err := a.repo.Get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("mediaSearchReadAdapter: repo.Get(%s): %w", id, err)
		}
		if row == nil {
			continue // not found — skip
		}
		// Defence-in-depth: filter by lifecycle_state allowlist.
		// The primary enforcement lives in the Qdrant adapter;
		// this guard catches SQL drift or mock adapters that
		// ignore allowStates.
		if len(allowSet) > 0 && !allowSet[string(row.LifecycleState)] {
			continue
		}
		out = append(out, mediaAssetFromDomain(row))
	}
	return out, nil
}

// mediaAssetFromDomain maps *asset.Asset → mediasearch.MediaAsset.
// Optional fields (Language, Width, Height) are left zero when the
// domain asset doesn't carry them; the semanticSearchBackend
// doesn't consume these today.
func mediaAssetFromDomain(a *asset.Asset) mediasearch.MediaAsset {
	if a == nil {
		return mediasearch.MediaAsset{}
	}
	durMs := 0
	if a.Duration > 0 {
		durMs = int(a.Duration.Milliseconds())
	}
	return mediasearch.MediaAsset{
		ID:             a.ID,
		Name:           a.Name,
		Source:         string(a.Source),
		MediaType:      string(a.MediaType),
		Category:       a.Category,
		Tags:           append([]string(nil), a.Tags...),
		SearchText:     a.SearchText,
		DurationMs:     durMs,
		LifecycleState: string(a.LifecycleState),
	}
}

// Package app — adapters_media_search.go bridges the infrastructure-layer
// ClipsRepository to the application-layer search.MediaReadRepository
// port consumed by the Fase 6 semanticSearchBackend.
//
// The adapter batch-fetches assets by ID from SQLite and maps the
// canonical *asset.Asset shape into search.MediaAsset. It filters
// by lifecycle_state (allowStates) so the semanticSearchBackend hydrates
// only searchable rows.
package app

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	assets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

// searchReadAdapter wraps *assets.ClipsRepository to satisfy the
// canonical search.MediaReadRepository (Commit 3-A/3-B promotion;
// the search package is the sole canonical owner per godlike/06
// one-owner-per-fact + QDRANT-004 invariant). The adapter
// batch-fetches by ID using the canonical repo.Get(ctx, id)
// path and maps each row into a search.MediaAsset.
type searchReadAdapter struct {
	repo *assets.ClipsRepository
}

// Compile-time assertion: searchReadAdapter satisfies
// search.MediaReadRepository. Drift is a build failure.
var _ search.MediaReadRepository = (*searchReadAdapter)(nil)

// newSearchReadAdapter creates the canonical composition-root
// adapter. Returns nil when repo is nil so the wiring site
// preserves the != nil discipline.
func newSearchReadAdapter(repo *assets.ClipsRepository) search.MediaReadRepository {
	if repo == nil {
		return nil
	}
	return &searchReadAdapter{repo: repo}
}

// GetMany fetches assets by ID from SQLite. Each asset is mapped
// into a search.MediaAsset. Rows whose LifecycleState is not
// in allowStates are silently dropped (the Qdrant adapter already
// applies this filter upstream; the post-hydration guard is
// defence-in-depth per godlike/07).
//
// The workspace context is accepted for forward-compat with
// QDRANT-001 multi-tenancy but is currently unused (the
// media_assets.workspace_id column doesn't exist yet).
//
// SEARCH-T07-001 fail-closed default (2026-07-04, Phase 9 closure):
// when the caller passes an empty `allowStates`, we default to
// `search.SearchableLifecycleStates` (the canonical search-owned
// allowlist = []string{"ACTIVE"}) instead of bypassing the filter.
// This closes the fail-open path where `len(allowSet) > 0` would
// short-circuit the guard and let deleted/archived/pending rows
// reach the semantic backend. godlike/07 no-fake-availability:
// the canonical allowlist is the single source of truth — callers
// that want to override MUST pass the explicit allowlist slice.
func (a *searchReadAdapter) GetMany(
	ctx context.Context,
	_ search.Actor,
	assetIDs []string,
	allowStates []string,
) ([]search.MediaAsset, error) {
	if a == nil || a.repo == nil {
		return nil, fmt.Errorf("searchReadAdapter: not wired")
	}
	if len(assetIDs) == 0 {
		return nil, nil
	}

	// SEARCH-T07-001 fail-closed default: empty allowStates → canonical
	// SearchableLifecycleStates. Closes the pre-PR fail-open path
	// where `len(allowSet) > 0` short-circuited the lifecycle filter.
	if len(allowStates) == 0 {
		allowStates = search.SearchableLifecycleStates
	}

	// Build the allow-set for O(1) lookup.
	allowSet := make(map[string]bool, len(allowStates))
	for _, s := range allowStates {
		allowSet[s] = true
	}

	out := make([]search.MediaAsset, 0, len(assetIDs))
	for _, id := range assetIDs {
		row, err := a.repo.Get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("searchReadAdapter: repo.Get(%s): %w", id, err)
		}
		if row == nil {
			continue // not found — skip
		}
		// Defence-in-depth: filter by lifecycle_state allowlist.
		// The primary enforcement lives in the Qdrant adapter;
		// this guard catches SQL drift or mock adapters that
		// ignore allowStates.
		if !allowSet[string(row.LifecycleState)] {
			continue
		}
		out = append(out, assetToMediaAsset(row))
	}
	return out, nil
}

// assetToMediaAsset maps *asset.Asset → search.MediaAsset.
// Optional fields (Language, Width, Height) are left zero when the
// domain asset doesn't carry them; the semanticSearchBackend doesn't
// consume these today. The canonical target (search.MediaAsset)
// deliberately carries NO server-internal locator (no LocalPath,
// no DriveLink, no RawDriveFileID) per the QDRANT-004 invariant;
// operator/admin surfaces needing those fields consume
// duplicates.DuplicateMatch from
// internal/application/assets/duplicates/types.go (godlike/06 SSOT).
func assetToMediaAsset(a *asset.Asset) search.MediaAsset {
	if a == nil {
		return search.MediaAsset{}
	}
	durMs := 0
	if a.Duration > 0 {
		durMs = int(a.Duration.Milliseconds())
	}
	return search.MediaAsset{
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

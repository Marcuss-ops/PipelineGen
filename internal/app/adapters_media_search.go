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
	assets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
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
// in search.SearchableLifecycleStates (the canonical ACTIVE-only
// allowlist) are silently dropped (the Qdrant adapter already
// applies this filter upstream; the post-hydration guard is
// defence-in-depth per godlike/07).
//
// The workspace context is accepted for forward-compat with
// QDRANT-001 multi-tenancy but is currently unused (the
// media_assets.workspace_id column doesn't exist yet).
//
// SEARCH-T07-LIFECYCLE-DEL (P0, 2026-07-15): the `allowStates`
// parameter is REMOVED. The canonical ACTIVE-only filter is
// hardcoded at the call site (this implementation). The interface
// boundary is intentionally drift-free per godlike/06
// one-canonical-owner-per-fact — the search capability owns
// the searchable-projection semantics and the interface exposes
// no knob to override the canonical ACTIVE-only filter.
//
// SEARCH-T07-001 fail-closed default (2026-07-04, Phase 9 closure,
// SUPERSEDED by SEARCH-T07-LIFECYCLE-DEL): the fail-open path
// where `len(allowSet) > 0` short-circuited the lifecycle filter
// is now impossible at the interface level — callers cannot pass
// an empty allowStates because there is no allowStates parameter.
func (a *searchReadAdapter) GetMany(
	ctx context.Context,
	_ search.Actor,
	assetIDs []string,
) ([]search.MediaAsset, error) {
	if a == nil || a.repo == nil {
		return nil, fmt.Errorf("searchReadAdapter: not wired")
	}
	if len(assetIDs) == 0 {
		return nil, nil
	}

	// SEARCH-T07-LIFECYCLE-DEL (P0, 2026-07-15): the canonical
	// ACTIVE-only filter is pinned at the call site. This is the
	// SOLE lifecycle_state filter surface for the search capability
	// per godlike/06 one-canonical-owner-per-fact. Implementations
	// MUST NOT introduce a different allowlist; the constant
	// `search.SearchableLifecycleStates` is the SSOT and is
	// updated atomically with the lifecycle_state enum
	// (internal/kernel/asset/asset_types.go::LifecycleState).
	allowSet := make(map[string]bool, len(search.SearchableLifecycleStates))
	for _, s := range search.SearchableLifecycleStates {
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
		// ignore SearchableLifecycleStates.
		if !allowSet[string(row.LifecycleState)] {
			continue
		}
		out = append(out, assetToMediaAsset(row))
	}
	return out, nil
}

// assetToMediaAsset maps *asset.Asset → search.MediaAsset.
// Optional fields (Language, Width, Height) are left zero when the
// domain asset doesn't carry them. PR-SEARCH-DRIVELINK (July 2026):
// DriveLink is populated from the domain asset's metadata_json for
// in-memory enrichment of search.Candidate in the semantic backend.
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
		DriveLink:      a.DriveLink(),
		LifecycleState: string(a.LifecycleState),
	}
}

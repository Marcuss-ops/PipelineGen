// Package app — canonical_identity_adapter.go bridges the canonical
// search.CanonicalIdentityResolver port to the existing asset_index
// resolver (infrastructure/database/assetindex). This is the composition
// root's single bridge between the search capability's identity port and
// the SQLite-backed source→asset lookup (Pattern 0).
//
// PR-SEARCH-UNIVERSE (August 2026): the provider backend delegates
// source_type|source_ref → canonical-asset resolution here. Until the
// CanonicalIdentityResolver backfill (media_asset_sources as the canonical
// lookup) lands, this adapter forwards to the existing asset_index table,
// which already maps (source, source_id) → asset_id.
package app

import (
	"context"

	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
)

// assetIndexCanonicalResolver implements search.CanonicalIdentityResolver
// over *assetindex.Resolver.
type assetIndexCanonicalResolver struct {
	inner *assetindex.Resolver
}

var _ search.CanonicalIdentityResolver = (*assetIndexCanonicalResolver)(nil)

// newAssetIndexCanonicalResolver wraps the asset_index resolver as the
// canonical identity resolver. nil degrades to the noop resolver
// (identity unknown) so the provider backend never fabricates an AssetID.
func newAssetIndexCanonicalResolver(r *assetindex.Resolver) search.CanonicalIdentityResolver {
	if r == nil {
		return search.NewNoopCanonicalIdentityResolver()
	}
	return &assetIndexCanonicalResolver{inner: r}
}

func (r *assetIndexCanonicalResolver) ResolveSource(ctx context.Context, sourceType, sourceRef string) (search.CanonicalIdentity, error) {
	if r == nil || r.inner == nil {
		return search.CanonicalIdentity{SourceType: sourceType, SourceRef: sourceRef}, nil
	}
	rec, err := r.inner.ResolveBySource(ctx, sourceType, sourceRef)
	if err != nil {
		return search.CanonicalIdentity{}, err
	}
	if rec == nil || rec.AssetID == "" {
		return search.CanonicalIdentity{SourceType: sourceType, SourceRef: sourceRef}, nil
	}
	return search.CanonicalIdentity{
		AssetID:    rec.AssetID,
		SourceType: sourceType,
		SourceRef:  sourceRef,
		Resolved:   true,
	}, nil
}

func (r *assetIndexCanonicalResolver) ResolveContent(ctx context.Context, contentSHA256 string) (search.CanonicalIdentity, error) {
	if r == nil || r.inner == nil {
		return search.CanonicalIdentity{}, nil
	}
	rec, err := r.inner.ResolveByContentHash(ctx, contentSHA256)
	if err != nil {
		return search.CanonicalIdentity{}, err
	}
	if rec == nil || rec.AssetID == "" {
		return search.CanonicalIdentity{}, nil
	}
	return search.CanonicalIdentity{
		AssetID:  rec.AssetID,
		Resolved: true,
	}, nil
}

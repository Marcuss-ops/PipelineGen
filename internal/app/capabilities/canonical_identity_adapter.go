// Package app — canonical_identity_adapter.go bridges the canonical
// mediaregistry port to the search capability's request-side port.
//
// Provider candidates are allowed to become canonical assets only after this
// SQLite-backed registry lookup. The historical asset_index fallback is not a
// provenance source: keeping it here would allow discovery to silently
// bypass media_asset_sources during the cutover.
package capabilities

import (
	"context"

	"database/sql"
	search "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	sqlitemediaregistry "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/mediaregistry"
)

type canonicalIdentityAdapter struct {
	inner capregistry.CanonicalIdentityResolver
}

var _ search.CanonicalIdentityResolver = (*canonicalIdentityAdapter)(nil)

// newCanonicalIdentityResolver wires the search capability to the durable
// media registry. A missing DB is fail-closed: the provider candidate keeps
// its external identity and is never assigned a fabricated AssetID.
func newCanonicalIdentityResolver(db *sql.DB) search.CanonicalIdentityResolver {
	if db == nil {
		return search.NewNoopCanonicalIdentityResolver()
	}
	inner, err := sqlitemediaregistry.NewCanonicalIdentityResolver(db)
	if err != nil {
		return search.NewNoopCanonicalIdentityResolver()
	}
	return &canonicalIdentityAdapter{inner: inner}
}

func (r *canonicalIdentityAdapter) ResolveSource(ctx context.Context, sourceType, sourceRef string) (search.CanonicalIdentity, error) {
	identity, err := r.inner.ResolveSource(ctx, sourceType, sourceRef)
	if err != nil {
		return search.CanonicalIdentity{}, err
	}
	return search.CanonicalIdentity{
		AssetID: identity.AssetID, SourceType: identity.SourceType,
		SourceRef: identity.SourceRef, Resolved: identity.AssetID != "",
	}, nil
}

func (r *canonicalIdentityAdapter) ResolveContent(ctx context.Context, contentSHA256 string) (search.CanonicalIdentity, error) {
	identity, err := r.inner.ResolveContent(ctx, contentSHA256)
	if err != nil {
		return search.CanonicalIdentity{}, err
	}
	return search.CanonicalIdentity{AssetID: identity.AssetID, Resolved: identity.AssetID != ""}, nil
}

// Package app — canonical_identity_adapter.go bridges the canonical
// mediaregistry port to the search capability's request-side port.
//
// Provider candidates are allowed to become canonical assets only after this
// durable registry lookup. The historical asset_index fallback is not a
// provenance source: keeping it here would allow discovery to silently
// bypass media_asset_sources.
//
// MEDIA-SSOT (September 2026): identity is a media fact, so the resolver reads
// the PostgreSQL media SSOT (media_asset_sources + media_assets.content_sha256)
// — the same database the canonical committer writes. Resolving against the
// operational SQLite registry canonicalised provider discoveries against a
// different database than the assets they were deduplicated against.
package wiring

import (
	"context"

	"database/sql"

	search "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	sqlitemediaregistry "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/mediaregistry"
)

type canonicalIdentityAdapter struct {
	inner capregistry.CanonicalIdentityResolver
}

var _ search.CanonicalIdentityResolver = (*canonicalIdentityAdapter)(nil)

// newCanonicalIdentityResolver wires the search capability to the durable
// media registry. PostgreSQL is the media SSOT, so every media-enabled
// deployment resolves identity there; the legacy SQLite registry is retained
// ONLY for the documented graceful-degrade path where the media plane is
// intentionally disabled (mediaDB == nil).
//
// A missing handle is fail-closed: the provider candidate keeps its external
// identity and is never assigned a fabricated AssetID.
func newCanonicalIdentityResolver(mediaDB, legacyDB *sql.DB) search.CanonicalIdentityResolver {
	if mediaDB != nil {
		if inner, err := pgmedia.NewPostgresCanonicalIdentityResolver(mediaDB); err == nil {
			return &canonicalIdentityAdapter{inner: inner}
		}
	}
	if legacyDB == nil {
		return search.NewNoopCanonicalIdentityResolver()
	}
	inner, err := sqlitemediaregistry.NewCanonicalIdentityResolver(legacyDB)
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

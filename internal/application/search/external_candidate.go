// Package search — external_candidate.go is the canonical discovery-side
// identity surface (PR-SEARCH-UNIVERSE, August 2026).
//
// A discovery-universe search hits live providers (artlist, youtube,
// stock, images). Its result is NOT yet a canonical MediaAsset — it is an
// ExternalCandidate: a provider-native reference that MAY already be
// registered in the canonical registry. The CanonicalIdentityResolver is
// the single owner of the mapping
//
//	source_type | source_ref  →  canonical asset id
//
// The provider adapter MUST NOT invent a canonical AssetID from the
// provider-native ID (the historical `AssetID = providerID` mismatch
// source). It builds an ExternalCandidate and asks the resolver whether
// that source is already known; the returned AssetID (KnownAssetID) is the
// only value ever placed in Candidate.AssetID.
//
// This file stays stdlib-only (Wave 19 invariant): it imports context
// and nothing else.
package search

import "context"

// ExternalCandidate is the canonical discovery-universe hit: a provider
// result BEFORE it is canonicalized. It is deliberately NOT a
// search.Candidate — a Candidate carries catalog-side hydration fields
// (Score, ThumbnailURL, PreviewURL, ...) that a live provider result does
// not yet own. The fields here are exactly the identity + display surface
// the blended merge needs to dedup a discovery hit against the catalog.
type ExternalCandidate struct {
	// SourceType is the canonical source identifier (e.g. "artlist",
	// "youtube", "stock", "image").
	SourceType string

	// SourceRef is the provider-native reference (YouTube VideoID,
	// artlist item id, stock asset id, ...). Never empty for a valid hit.
	SourceRef string

	// Title is the human-readable title as reported by the provider.
	Title string

	// URL is the provider's human-facing asset page (never a temporary
	// download URL).
	URL string

	// KnownAssetID is the canonical asset id when the source is already
	// registered in the canonical registry; empty when unknown. It is
	// populated by CanonicalIdentityResolver.ResolveSource, never by the
	// provider adapter itself.
	KnownAssetID string
}

// CanonicalIdentity is the resolved identity of a source or content
// reference. Resolved=false means "not known in the canonical registry"
// (the reference is a genuinely new candidate); Resolved=true carries the
// canonical AssetID.
type CanonicalIdentity struct {
	AssetID    string
	SourceType string
	SourceRef  string
	Resolved   bool
}

// CanonicalIdentityResolver is the single owner of the source→asset and
// content→asset mappings. It is the application-layer port that closes the
// "provider ID used as canonical AssetID" drift: the provider backend
// delegates identity resolution here instead of inventing one inline.
//
// godlike/07 fail-closed contract:
//   - "not found" is returned as Resolved=false with a nil error (NOT an
//     error and NOT a fabricated identity).
//   - errors are reserved for genuine failures (empty inputs, database
//     unavailable) and callers MUST treat them as "identity unknown"
//     rather than substituting a provider ID.
type CanonicalIdentityResolver interface {
	// ResolveSource maps a (sourceType, sourceRef) pair to the canonical
	// asset id, if that source is already registered. Resolved=false
	// when the source is unknown.
	ResolveSource(ctx context.Context, sourceType, sourceRef string) (CanonicalIdentity, error)

	// ResolveContent maps a content SHA-256 to the canonical asset id,
	// if the bytes are already registered. Resolved=false when unknown.
	ResolveContent(ctx context.Context, contentSHA256 string) (CanonicalIdentity, error)
}

// noopCanonicalIdentityResolver returns Resolved=false for every lookup.
// It is the fail-safe default when no resolver is wired: a provider hit
// is treated as "identity unknown" (empty AssetID) rather than having a
// provider ID fabricated into the canonical identity field.
type noopCanonicalIdentityResolver struct{}

// NewNoopCanonicalIdentityResolver returns a resolver that never resolves
// anything. Use it as the composition-time fallback so the provider
// backend always has a non-nil resolver (no nil-guard branches in the
// hot path).
func NewNoopCanonicalIdentityResolver() CanonicalIdentityResolver {
	return noopCanonicalIdentityResolver{}
}

func (noopCanonicalIdentityResolver) ResolveSource(_ context.Context, sourceType, sourceRef string) (CanonicalIdentity, error) {
	return CanonicalIdentity{SourceType: sourceType, SourceRef: sourceRef}, nil
}

func (noopCanonicalIdentityResolver) ResolveContent(_ context.Context, _ string) (CanonicalIdentity, error) {
	return CanonicalIdentity{}, nil
}

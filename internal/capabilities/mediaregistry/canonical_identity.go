// Package mediaregistry — canonical_identity.go: canonical asset identity
// resolution contract.
//
// The canonical asset identity is NOT encoded in the AssetID. The historical
// heuristics (yt_ → youtube, drive_ → drive, planner: → stock) are drift:
// they deduce identity from a string prefix instead of from durable registry
// facts. This contract replaces them with two explicit resolutions:
//
//   - source identity: (source_type, source_ref) → asset_id, stored durably
//     in media_asset_sources. The same bytes discovered on YouTube, Drive and
//     Artlist carry three provenance sources but share ONE content object.
//   - content identity: content_sha256 → content object → the assets that
//     reference those bytes (media_assets.content_sha256).
//
// godlike/06 SSOT: any code that needs to know "which asset is this source /
// these bytes" MUST resolve through this interface, never by inspecting the
// AssetID prefix.
package mediaregistry

import (
	"context"
	"errors"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"strings"
)

// CanonicalIdentity is the resolved identity of an asset. Exactly one of the
// two identity dimensions is populated by a given resolution: ResolveSource
// populates SourceType + SourceRef, ResolveContent populates ContentSHA256.
type CanonicalIdentity struct {
	AssetID       string
	SourceType    string
	SourceRef     string
	ContentSHA256 string
}

var (
	// ErrCanonicalIdentityNotFound is returned when no durable mapping exists
	// for the requested source or content. Callers must NOT fall back to the
	// AssetID prefix heuristic — an unresolvable identity is UNKNOWN.
	ErrCanonicalIdentityNotFound = errors.New("mediaregistry: canonical identity not found")

	// ErrCanonicalIdentityAmbiguous is returned when a source resolves to more
	// than one distinct canonical asset. The identity audit (duplicate source
	// identity) must be zero; any ambiguity is fail-closed, never guessed.
	ErrCanonicalIdentityAmbiguous = errors.New("mediaregistry: canonical identity ambiguous")
)

// CanonicalIdentityResolver resolves canonical asset identity from durable
// registry facts.
type CanonicalIdentityResolver interface {
	// ResolveSource returns the canonical asset for (sourceType, sourceRef).
	// sourceRef is the provider external reference (YouTube video id, Drive
	// file id, Artlist asset id, …) — it maps to media_asset_sources.source_uri.
	ResolveSource(ctx context.Context, sourceType, sourceRef string) (CanonicalIdentity, error)

	// ResolveContent returns the canonical identity for contentSHA256. When
	// exactly one asset references the bytes, AssetID is populated; when the
	// bytes are multi-provenance (several assets, one content object), AssetID
	// is left empty and the caller treats the identity as a content object.
	ResolveContent(ctx context.Context, contentSHA256 string) (CanonicalIdentity, error)
}

// DeriveAssetSourceID derives the historical asset-dependent source identity
// sha256(asset_id | source_type | source_uri | source_version). It remains
// available only for compatibility with legacy rows and migration tests.
func DeriveAssetSourceID(assetID, sourceType, sourceURI, sourceVersion string) string {
	sum := digest.SHA256Bytes([]byte(strings.Join([]string{assetID, sourceType, sourceURI, sourceVersion}, "|")))
	return sum
}

// DeriveCanonicalSourceID derives the source identity from the source tuple
// itself. AssetID is deliberately absent: one provider source must resolve to
// one canonical asset even when legacy callers disagree about the asset ID.
func DeriveCanonicalSourceID(sourceType, sourceURI, sourceVersion string) string {
	sum := digest.SHA256Bytes([]byte(strings.Join([]string{sourceType, sourceURI, sourceVersion}, "|")))
	return sum
}

// BackfillReport is the expand/backfill/cutover report produced by the
// identity backfill. Every counter is fail-closed: ambiguous sources are
// counted UNKNOWN, never guessed.
type BackfillReport struct {
	AssetsTotal             int `json:"assets_total"`
	SourcesBackfilled       int `json:"sources_backfilled"`
	SourcesAlreadyKnown     int `json:"sources_already_known"`
	SourcesUnknown          int `json:"sources_unknown"`
	SourcesAmbiguous        int `json:"sources_ambiguous"`
	ContentSHAKnown         int `json:"content_sha_known"`
	ContentSHAUnknown       int `json:"content_sha_unknown"`
	DuplicateSourceIdentity int `json:"duplicate_source_identity"`
	DuplicateContentSHA     int `json:"duplicate_content_sha"`
}

// TaxonomyBackfillReport describes the explicit legacy-column migration. It
// is separate from runtime taxonomy resolution: only the admin backfill may
// use historical source/media_type columns as input.
type TaxonomyBackfillReport struct {
	AssetsConsidered   int `json:"assets_considered"`
	TaxonomyBackfilled int `json:"taxonomy_backfilled"`
	TaxonomyUnknown    int `json:"taxonomy_unknown"`
}

// CanonicalIdentityBackfiller is the expand/backfill/cutover tooling surface.
// Backfill reconstructs media_asset_sources rows from the existing
// media_assets columns (source_video_id, drive_file_id, …) WITHOUT inventing
// identity from the AssetID heuristic.
type CanonicalIdentityBackfiller interface {
	Backfill(ctx context.Context) (BackfillReport, error)
}

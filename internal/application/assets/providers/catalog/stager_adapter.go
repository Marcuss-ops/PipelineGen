// Package catalog — stager_adapter.go (ART-002 P4.2, July 2026).
//
// CatalogStager is a SKELETON adapter that implements assets.SourceStager
// for the SourceKind=SourceKindExistingCatalog slot in assets.SourceStagerRegistry.
//
// godlike/07 no-fake-availability disclosure:
// The catalog (existing-catalog lookup) provider package has not yet
// been built (the internal/application/assets/providers/catalog/
// directory was empty before P4.2). The canonical catalog-lookup will
// live in this package once the catalog provider lands; in the
// meantime, this Stager exists only so the SourceStagerRegistry can
// resolve the ExistingCatalog slot without returning
// ErrSourceKindUnknown to higher-level orchestrators.
//
// Note for the real implementation: an existing-catalog "stage" is a
// lookup against the media_assets table — no download is required.
// The real CatalogStager will translate ref.URL (or ref.AssetID) into
// a catalog row and return a StagedAsset whose LocalPath is the
// already-cached file path. StageSource returns
// ErrCatalogStagerNotImplemented (typed sentinel, reachable via
// errors.Is) so callers can branch on the failure mode rather than
// silently defaulting to a different kind.
//
// Cleanup is a no-op (catalog rows are not removed by the stager).
//
// Forward-pointer: when the catalog provider package lands (tracked
// in architecture/current.yaml under the SourceStager wave), this
// skeleton will be replaced with a real adapter wrapping the
// canonical catalog lookup port.
package catalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

// Compile-time assertion: *CatalogStager satisfies assets.SourceStager.
var _ assets.SourceStager = (*CatalogStager)(nil)

// ErrCatalogStagerNotImplemented is returned by
// CatalogStager.StageSource for every call. Reachable via errors.Is
// from any caller seam.
//
// Per godlike/07: this sentinel is the typed "no fake availability"
// signal that the catalog provider package has not landed yet.
// Production callers MUST treat ErrCatalogStagerNotImplemented as a
// hard failure (not a fallback trigger) and surface the error to
// the operator.
var ErrCatalogStagerNotImplemented = errors.New("catalog stager: not implemented (provider package not yet built; SKELETON per godlike/07)")

// CatalogStager is the skeleton SourceStager adapter for the
// ExistingCatalog kind. Holds no state (the real adapter will hold a
// catalog-lookup port reference when the provider package lands).
type CatalogStager struct{}

// NewCatalogStager returns a new CatalogStager. No constructor
// arguments (skeleton: no dependencies to wire).
func NewCatalogStager() *CatalogStager {
	return &CatalogStager{}
}

// StageSource returns ErrCatalogStagerNotImplemented for every call.
// The real implementation will look up the asset in the catalog
// (media_assets table) and return a StagedAsset whose LocalPath is
// the cached file (TBD when the catalog provider lands).
func (s *CatalogStager) StageSource(ctx context.Context, ref assets.SourceRef) (*assets.StagedAsset, error) {
	_ = ctx
	return nil, fmt.Errorf("%w: url=%q", ErrCatalogStagerNotImplemented, ref.URL)
}

// Cleanup is a no-op for the catalog skeleton (catalog rows are not
// removed by the stager). The real implementation will also be a
// no-op (catalog lifecycle is owned by the catalog lookup port, not
// the stager).
// StageSourceV2 is the V2 variant of StageSource (Card 9 / source-stager
// consolidation, July 2026). Returns a canonical *asset.StagedSource
// (the typed-port contract) wrapping the legacy *assets.StagedAsset.
// Adapters that have not yet migrated to the V2 typed port return a
// typed error so the composition root can register a fail-closed
// capability (godlike/07 NO-FAKE-AVAILABILITY).
func (s *CatalogStager) StageSourceV2(ctx context.Context, ref assets.SourceRef) (*assets.StagedSource, error) {
	return nil, fmt.Errorf("%w: url=%q", ErrCatalogStagerNotImplemented, ref.URL)
}

// CleanupStagedSource is the V2 typed-port companion to Cleanup.
// Returns nil when staged is nil (idempotent). The legacy Cleanup
// is called via a wrapper *assets.StagedAsset to preserve the
// existing on-disk removal semantics.
func (s *CatalogStager) CleanupStagedSource(ctx context.Context, staged *assets.StagedSource) error {
	if staged == nil {
		return nil
	}
	return s.Cleanup(ctx, &assets.StagedAsset{
		LocalPath: staged.LocalPath,
		Bytes:     staged.Bytes,
	})
}

func (s *CatalogStager) Cleanup(ctx context.Context, staged *assets.StagedAsset) error {
	_ = ctx
	_ = staged
	return nil
}

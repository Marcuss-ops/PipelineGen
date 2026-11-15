// Package entities — entity_media_resolver.go owns the EntityMediaIndex and
// EntityMediaResolver: the canonical_entity_id-keyed media index and the
// resolver that picks the best asset for an entity.
//
// Pipeline position:
//
//	canonical_entity_id → EntityMediaIndex (available assets + quality)
//	                    → EntityMediaResolver → resolved asset id
//
// The index maps a canonical_entity_id (the human-readable identity produced
// by CanonicalEntityID, e.g. "person:floyd-mayweather-jr") to its available
// assets, each carrying a quality_score in [0,1]. The resolver ranks by
// quality and returns the best content-addressed asset (ResolvedAssetRef) —
// never a bare URL.
//
// It uses the shared EntityAsset / AssetRepository / InMemoryAssetRepository /
// ResolvedAssetRef data model (asset_resolver.go). IndexForEntity derives the
// EntityID from (type, name) via CanonicalEntityID, so callers never hand-write
// an id. Rust supplies canonical semantic identities; Go keeps the catalog
// lookup and asset binding at the media boundary.
package entities

import (
	"errors"
	"fmt"
)

// ErrEntityHasNoAssets is returned when an entity has no indexed assets and a
// caller asks the resolver to select one.
var ErrEntityHasNoAssets = errors.New("entity media resolver: entity has no assets")

// EntityMediaIndex is the canonical_entity_id-keyed media index. It stores the
// same EntityAsset records as the repository but keys them on the canonical
// entity id (the slug) carried by the semantic identity contract.
type EntityMediaIndex struct {
	repo AssetRepository
}

// NewEntityMediaIndex returns an index backed by a fresh in-memory asset
// repository.
func NewEntityMediaIndex() *EntityMediaIndex {
	return &EntityMediaIndex{repo: NewInMemoryAssetRepository()}
}

// IndexForEntity upserts an asset bound to the canonical entity identified by
// (type, name). The asset's EntityID is derived via CanonicalEntityID — the
// caller never supplies it. Fails closed when the (type, name) normalizes to
// an empty canonical id.
func (ix *EntityMediaIndex) IndexForEntity(entityType, name string, asset EntityAsset) error {
	canonical := CanonicalEntityID(entityType, name)
	if canonical == "" {
		return fmt.Errorf("%w: type %q name %q normalizes to an empty canonical entity id", ErrInvalidEntityAsset, entityType, name)
	}
	asset.EntityID = canonical
	return ix.repo.Upsert(asset)
}

// IndexForCanonicalID upserts an asset under an ALREADY-derived canonical
// entity id from the semantic linker, which may differ from
// CanonicalEntityID(type, surfaceName) when the surface is not the canonical
// name, WITHOUT re-deriving it. Fails closed on an empty id — an
// asset is never minted for an unknown identity.
func (ix *EntityMediaIndex) IndexForCanonicalID(canonicalID string, asset EntityAsset) error {
	if ix == nil || ix.repo == nil {
		return fmt.Errorf("%w: media index is not configured", ErrInvalidEntityAsset)
	}
	// Fold at the write boundary as well: an id minted before the type-alias
	// fold ("gpe:garlasco") and one minted now ("place:garlasco") must share
	// ONE index key, otherwise the asset is verified but never found.
	canonicalID = FoldCanonicalEntityID(canonicalID)
	if canonicalID == "" {
		return fmt.Errorf("%w: empty canonical entity id", ErrInvalidEntityAsset)
	}
	asset.EntityID = canonicalID
	return ix.repo.Upsert(asset)
}

// Assets returns every asset of a canonical entity, best quality first (ties
// broken by asset id) — deterministic, never map order.
//
// The lookup is the compatibility read for ids written before the fold: the
// canonical spelling is tried first, then each legacy spelling of the same
// type (FoldCanonicalEntityIDCandidates). A pre-migration asset stored under
// "gpe:garlasco" therefore resolves for a "location:garlasco" request
// instead of reporting ErrEntityHasNoAssets — the zero-image bug.
func (ix *EntityMediaIndex) Assets(canonicalEntityID string) []EntityAsset {
	if ix == nil || ix.repo == nil {
		return nil
	}
	for _, candidate := range FoldCanonicalEntityIDCandidates(canonicalEntityID) {
		if all := ix.repo.All(candidate); len(all) > 0 {
			return all
		}
	}
	return nil
}

// EntityMediaResolver selects the best asset for a canonical_entity_id. It is
// stateless and safe for concurrent use.
type EntityMediaResolver struct {
	index *EntityMediaIndex
}

// NewEntityMediaResolver returns a resolver backed by a fresh in-memory media
// index.
func NewEntityMediaResolver() *EntityMediaResolver {
	return &EntityMediaResolver{index: NewEntityMediaIndex()}
}

// SetIndex swaps the media index the resolver reads (e.g. a run-scoped index
// populated from the run's own entity image bindings). Nil-safe: a nil index
// keeps the current one.
func (r *EntityMediaResolver) SetIndex(index *EntityMediaIndex) {
	if r != nil && index != nil {
		r.index = index
	}
}

// ResolveBest returns the highest-quality content-addressed asset of the
// canonical entity, or ErrEntityHasNoAssets when the entity has none.
func (r *EntityMediaResolver) ResolveBest(canonicalEntityID string) (ResolvedAssetRef, error) {
	all := r.index.Assets(canonicalEntityID)
	if len(all) == 0 {
		return ResolvedAssetRef{}, fmt.Errorf("%w: %q", ErrEntityHasNoAssets, canonicalEntityID)
	}
	best := all[0] // index order: best quality first, then asset id
	return ResolvedAssetRef{
		AssetID:   best.AssetID,
		SHA256:    best.SHA256,
		URL:       best.StorageURL,
		LocalPath: best.LocalPath,
		MediaType: mediaTypeFor(best.AssetType),
	}, nil
}

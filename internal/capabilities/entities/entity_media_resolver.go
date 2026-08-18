// Package entities — entity_media_resolver.go owns the EntityMediaIndex and
// EntityMediaResolver: the canonical_entity_id-keyed media index and the
// resolver that picks the best asset for an entity.
//
// Pipeline position:
//
//	canonical_entity_id → EntityMediaIndex (available assets + quality)
//	                    → EntityMediaResolver → asset_id → VisualIntent.AssetID
//	                    → DeterministicPresetSampler → (preset, animation)
//
// The index maps a canonical_entity_id (the human-readable identity produced
// by CanonicalEntityID, e.g. "person:floyd-mayweather-jr") to its available
// assets, each carrying a quality_score in [0,1]. The resolver ranks by
// quality and returns the best content-addressed asset (ResolvedAssetRef) —
// never a bare URL.
//
// It REUSES the existing EntityAsset / AssetRepository / InMemoryAssetRepository
// / ResolvedAssetRef machinery (asset_resolver.go); only the key derivation is
// new: IndexForEntity derives the EntityID from (type, name) via
// CanonicalEntityID, so callers never hand-write an id. This is the media
// index the semantic-index pipeline reads; AssetResolver (StableEntityID-keyed)
// remains for the legacy entity-record path.
package entities

import (
	"errors"
	"fmt"
	"strings"
)

// ErrEntityHasNoAssets is returned when an entity has no indexed assets and a
// caller asks the resolver to select one.
var ErrEntityHasNoAssets = errors.New("entity media resolver: entity has no assets")

// EntityMediaIndex is the canonical_entity_id-keyed media index. It stores the
// same EntityAsset records as the repository but keys them on the canonical
// entity id (the slug), which is what flows through
// SemanticItem.CanonicalEntityID.
type EntityMediaIndex struct {
	repo AssetRepository
}

// NewEntityMediaIndex returns an index backed by a fresh in-memory asset
// repository. The composition root can swap the backend via SetRepository.
func NewEntityMediaIndex() *EntityMediaIndex {
	return &EntityMediaIndex{repo: NewInMemoryAssetRepository()}
}

// SetRepository swaps the backing store, e.g. a durable SQLite adapter.
func (ix *EntityMediaIndex) SetRepository(repo AssetRepository) {
	if repo != nil {
		ix.repo = repo
	}
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

// IndexAllForEntity upserts several assets for the same canonical entity and
// fails closed on the first invalid one (a poisoned asset never partially
// indexes).
func (ix *EntityMediaIndex) IndexAllForEntity(entityType, name string, assets ...EntityAsset) error {
	for _, asset := range assets {
		if err := ix.IndexForEntity(entityType, name, asset); err != nil {
			return err
		}
	}
	return nil
}

// IndexForCanonicalID upserts an asset under an ALREADY-derived canonical
// entity id (e.g. the imagesearch resolver's CanonicalID, which may differ
// from CanonicalEntityID(type, surfaceName) when the surface is not the
// canonical name) WITHOUT re-deriving it. Fails closed on an empty id — an
// asset is never minted for an unknown identity.
func (ix *EntityMediaIndex) IndexForCanonicalID(canonicalID string, asset EntityAsset) error {
	if ix == nil || ix.repo == nil {
		return fmt.Errorf("%w: media index is not configured", ErrInvalidEntityAsset)
	}
	if strings.TrimSpace(canonicalID) == "" {
		return fmt.Errorf("%w: empty canonical entity id", ErrInvalidEntityAsset)
	}
	asset.EntityID = canonicalID
	return ix.repo.Upsert(asset)
}

// Assets returns every asset of a canonical entity, best quality first (ties
// broken by asset id) — deterministic, never map order.
func (ix *EntityMediaIndex) Assets(canonicalEntityID string) []EntityAsset {
	if ix == nil || ix.repo == nil {
		return nil
	}
	return ix.repo.All(canonicalEntityID)
}

// Len returns the total number of indexed assets.
func (ix *EntityMediaIndex) Len() int {
	if ix == nil || ix.repo == nil {
		return 0
	}
	return ix.repo.Len()
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
		MediaType: mediaTypeFor(best.AssetType),
	}, nil
}

// ResolveTop returns the top-n content-addressed assets of the canonical
// entity in quality order (n <= 0 means all). Empty when the entity has none.
func (r *EntityMediaResolver) ResolveTop(canonicalEntityID string, n int) []ResolvedAssetRef {
	all := r.index.Assets(canonicalEntityID)
	if n > 0 && len(all) > n {
		all = all[:n]
	}
	out := make([]ResolvedAssetRef, 0, len(all))
	for _, asset := range all {
		out = append(out, ResolvedAssetRef{
			AssetID:   asset.AssetID,
			SHA256:    asset.SHA256,
			URL:       asset.StorageURL,
			MediaType: mediaTypeFor(asset.AssetType),
		})
	}
	return out
}

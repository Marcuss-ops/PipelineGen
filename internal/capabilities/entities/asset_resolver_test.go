package entities

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInMemoryAssetRepository_UpsertAndOrdering covers the storage used by
// the live canonical-ID media resolver.
func TestInMemoryAssetRepository_UpsertAndOrdering(t *testing.T) {
	r := NewInMemoryAssetRepository()
	timCookID := StableEntityID("PERSON", "Tim Cook")
	require.NoError(t, r.Upsert(
		EntityAsset{EntityID: timCookID, AssetID: "tim_cook_01", AssetType: "photo", SHA256: "aaa", StorageURL: "https://store/tim_cook_01.jpg", QualityScore: 0.4},
	))
	require.NoError(t, r.Upsert(EntityAsset{EntityID: timCookID, AssetID: "tim_cook_keynote", AssetType: "photo", SHA256: "bbb", StorageURL: "https://store/tim_cook_keynote.jpg", QualityScore: 0.9}))
	require.NoError(t, r.Upsert(EntityAsset{EntityID: timCookID, AssetID: "tim_cook_02", AssetType: "photo", SHA256: "ccc", StorageURL: "https://store/tim_cook_02.png", QualityScore: 0.7}))
	assets := r.All(timCookID)
	require.Len(t, assets, 3)
	require.Equal(t, []string{"tim_cook_keynote", "tim_cook_02", "tim_cook_01"}, assetIDs(assets))
}

// TestInMemoryAssetRepository_RejectsMissingContentAddress pins the fail-closed SSOT
// invariant: an asset without a sha256 content address is rejected — it
// could not be cached, verified or deduplicated, and RenderingGen would
// receive an unverifiable ref.
func TestInMemoryAssetRepository_RejectsMissingContentAddress(t *testing.T) {
	r := NewInMemoryAssetRepository()
	err := r.Upsert(EntityAsset{
		EntityID: StableEntityID("PERSON", "Tim Cook"), AssetID: "no-hash",
		StorageURL: "https://store/no-hash.jpg", QualityScore: 0.5,
	})
	require.ErrorIs(t, err, ErrInvalidEntityAsset)
	require.Empty(t, r.All(StableEntityID("PERSON", "Tim Cook")), "an invalid asset must never index")

	// Missing storage url also fails closed.
	err = r.Upsert(EntityAsset{
		EntityID: StableEntityID("PERSON", "Tim Cook"), AssetID: "no-url",
		SHA256: "aaa", QualityScore: 0.5,
	})
	require.ErrorIs(t, err, ErrInvalidEntityAsset)

	// Quality score out of range fails closed.
	err = r.Upsert(EntityAsset{
		EntityID: StableEntityID("PERSON", "Tim Cook"), AssetID: "bad-score",
		SHA256: "aaa", StorageURL: "https://store/x.jpg", QualityScore: 1.5,
	})
	require.ErrorIs(t, err, ErrInvalidEntityAsset)
}

// TestInMemoryAssetRepository_DedupByEntityAndAsset pins the (entity_id, asset_id)
// dedup: re-indexing the same asset (e.g. refreshed from the store) replaces
// the previous record instead of duplicating it.
func TestInMemoryAssetRepository_DedupByEntityAndAsset(t *testing.T) {
	r := NewInMemoryAssetRepository()
	timCookID := StableEntityID("PERSON", "Tim Cook")
	require.NoError(t, r.Upsert(EntityAsset{EntityID: timCookID, AssetID: "tim_cook_01", SHA256: "aaa", StorageURL: "https://store/v1.jpg", QualityScore: 0.5}))
	require.NoError(t, r.Upsert(EntityAsset{EntityID: timCookID, AssetID: "tim_cook_01", SHA256: "aaa", StorageURL: "https://store/v2.jpg", QualityScore: 0.8}))
	require.Len(t, r.All(timCookID), 1, "same (entity, asset) pair must dedup to one record")
	require.Equal(t, "https://store/v2.jpg", r.All(timCookID)[0].StorageURL, "refreshed record must win")
}

// Equal-quality repository entries are ordered by asset id after quality.
func TestInMemoryAssetRepository_SortsTiesByAssetID(t *testing.T) {
	r := NewInMemoryAssetRepository()
	timCookID := StableEntityID("PERSON", "Tim Cook")
	require.NoError(t, r.Upsert(
		EntityAsset{EntityID: timCookID, AssetID: "b", SHA256: "bb", StorageURL: "https://store/b.jpg", QualityScore: 0.5},
	))
	require.NoError(t, r.Upsert(EntityAsset{EntityID: timCookID, AssetID: "c", SHA256: "cc", StorageURL: "https://store/c.jpg", QualityScore: 0.5}))
	require.NoError(t, r.Upsert(EntityAsset{EntityID: timCookID, AssetID: "a", SHA256: "aa", StorageURL: "https://store/a.jpg", QualityScore: 0.9}))
	require.Equal(t, []string{"a", "b", "c"}, assetIDs(r.All(timCookID)))
}

// TestInMemoryAssetRepository_AssetsArePerEntity pins that stored assets are
// isolated by entity key.
func TestInMemoryAssetRepository_AssetsArePerEntity(t *testing.T) {
	r := NewInMemoryAssetRepository()
	timCookID := StableEntityID("PERSON", "Tim Cook")
	appleID := StableEntityID("ORGANIZATION", "Apple")
	require.NoError(t, r.Upsert(
		EntityAsset{EntityID: timCookID, AssetID: "tim_cook_01", SHA256: "aaa", StorageURL: "https://store/tim.jpg", QualityScore: 0.9},
	))
	require.NoError(t, r.Upsert(EntityAsset{EntityID: appleID, AssetID: "apple_logo", SHA256: "bbb", StorageURL: "https://store/apple.png", QualityScore: 0.8}))
	require.Equal(t, []string{"tim_cook_01"}, assetIDs(r.All(timCookID)))
	require.Equal(t, []string{"apple_logo"}, assetIDs(r.All(appleID)))
}

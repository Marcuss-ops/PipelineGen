package entities

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAssetResolver_TestD_ThreeImagesRankBestImage is the plan's Test D:
// Tim Cook has three images; the resolver ranks them by quality and returns
// the best one as a content-addressed ref (asset_id + sha256 + url) — never
// a bare URL.
func TestAssetResolver_TestD_ThreeImagesRankBestImage(t *testing.T) {
	r := NewAssetResolver()
	timCookID := StableEntityID("PERSON", "Tim Cook")
	require.NoError(t, r.IndexAll(
		EntityAsset{EntityID: timCookID, AssetID: "tim_cook_01", AssetType: "photo", SHA256: "aaa", StorageURL: "https://store/tim_cook_01.jpg", QualityScore: 0.4},
		EntityAsset{EntityID: timCookID, AssetID: "tim_cook_keynote", AssetType: "photo", SHA256: "bbb", StorageURL: "https://store/tim_cook_keynote.jpg", QualityScore: 0.9},
		EntityAsset{EntityID: timCookID, AssetID: "tim_cook_02", AssetType: "photo", SHA256: "ccc", StorageURL: "https://store/tim_cook_02.png", QualityScore: 0.7},
	))
	require.Equal(t, 3, r.assets.Len())

	// ── rank → best image ────────────────────────────────────────
	ref, ok := r.ResolveBest(timCookID)
	require.True(t, ok, "entity with assets must resolve")
	require.Equal(t, "tim_cook_keynote", ref.AssetID)
	require.Equal(t, "bbb", ref.SHA256)
	require.Equal(t, "https://store/tim_cook_keynote.jpg", ref.URL)
	require.NotEmpty(t, ref.MediaType)
}

// TestAssetResolver_NoAssetsFailsClosed pins that an entity without assets
// resolves to nothing — the resolver never fabricates a URL or a hash.
func TestAssetResolver_NoAssetsFailsClosed(t *testing.T) {
	r := NewAssetResolver()
	require.NoError(t, r.Index(EntityAsset{
		EntityID: StableEntityID("PERSON", "Tim Cook"), AssetID: "tim_cook_01",
		SHA256: "aaa", StorageURL: "https://store/tim_cook_01.jpg", QualityScore: 0.5,
	}))
	_, ok := r.ResolveBest(StableEntityID("PERSON", "Elon Musk"))
	require.False(t, ok)
	require.Empty(t, r.ResolveTop(StableEntityID("PERSON", "Elon Musk"), 1))
}

// TestAssetResolver_RejectsMissingContentAddress pins the fail-closed SSOT
// invariant: an asset without a sha256 content address is rejected — it
// could not be cached, verified or deduplicated, and RenderingGen would
// receive an unverifiable ref.
func TestAssetResolver_RejectsMissingContentAddress(t *testing.T) {
	r := NewAssetResolver()
	err := r.Index(EntityAsset{
		EntityID: StableEntityID("PERSON", "Tim Cook"), AssetID: "no-hash",
		StorageURL: "https://store/no-hash.jpg", QualityScore: 0.5,
	})
	require.ErrorIs(t, err, ErrInvalidEntityAsset)
	require.Equal(t, 0, r.assets.Len(), "an invalid asset must never index")

	// Missing storage url also fails closed.
	err = r.Index(EntityAsset{
		EntityID: StableEntityID("PERSON", "Tim Cook"), AssetID: "no-url",
		SHA256: "aaa", QualityScore: 0.5,
	})
	require.ErrorIs(t, err, ErrInvalidEntityAsset)

	// Quality score out of range fails closed.
	err = r.Index(EntityAsset{
		EntityID: StableEntityID("PERSON", "Tim Cook"), AssetID: "bad-score",
		SHA256: "aaa", StorageURL: "https://store/x.jpg", QualityScore: 1.5,
	})
	require.ErrorIs(t, err, ErrInvalidEntityAsset)
}

// TestAssetResolver_DedupByEntityAndAsset pins the (entity_id, asset_id)
// dedup: re-indexing the same asset (e.g. refreshed from the store) replaces
// the previous record instead of duplicating it.
func TestAssetResolver_DedupByEntityAndAsset(t *testing.T) {
	r := NewAssetResolver()
	timCookID := StableEntityID("PERSON", "Tim Cook")
	require.NoError(t, r.Index(EntityAsset{EntityID: timCookID, AssetID: "tim_cook_01", SHA256: "aaa", StorageURL: "https://store/v1.jpg", QualityScore: 0.5}))
	require.NoError(t, r.Index(EntityAsset{EntityID: timCookID, AssetID: "tim_cook_01", SHA256: "aaa", StorageURL: "https://store/v2.jpg", QualityScore: 0.8}))
	require.Equal(t, 1, r.assets.Len(), "same (entity, asset) pair must dedup to one record")

	ref, ok := r.ResolveBest(timCookID)
	require.True(t, ok)
	require.Equal(t, "https://store/v2.jpg", ref.URL, "refreshed record must win")
}

// TestAssetResolver_ResolveTopReturnsQualityOrder pins the top-n contract:
// the refs come back in quality order (best first), with deterministic ties
// broken by asset id.
func TestAssetResolver_ResolveTopReturnsQualityOrder(t *testing.T) {
	r := NewAssetResolver()
	timCookID := StableEntityID("PERSON", "Tim Cook")
	require.NoError(t, r.IndexAll(
		EntityAsset{EntityID: timCookID, AssetID: "b", SHA256: "bb", StorageURL: "https://store/b.jpg", QualityScore: 0.5},
		EntityAsset{EntityID: timCookID, AssetID: "c", SHA256: "cc", StorageURL: "https://store/c.jpg", QualityScore: 0.5},
		EntityAsset{EntityID: timCookID, AssetID: "a", SHA256: "aa", StorageURL: "https://store/a.jpg", QualityScore: 0.9},
	))
	top := r.ResolveTop(timCookID, 2)
	require.Len(t, top, 2)
	require.Equal(t, "a", top[0].AssetID, "best quality first")
	require.Equal(t, "b", top[1].AssetID, "tie broken deterministically by asset id")
	require.Equal(t, "aa", top[0].SHA256)
	require.Equal(t, "https://store/a.jpg", top[0].URL)
}

// TestAssetResolver_AssetsArePerEntity pins that assets are isolated per
// entity: resolving Apple never leaks Tim Cook's images.
func TestAssetResolver_AssetsArePerEntity(t *testing.T) {
	r := NewAssetResolver()
	timCookID := StableEntityID("PERSON", "Tim Cook")
	appleID := StableEntityID("ORGANIZATION", "Apple")
	require.NoError(t, r.IndexAll(
		EntityAsset{EntityID: timCookID, AssetID: "tim_cook_01", SHA256: "aaa", StorageURL: "https://store/tim.jpg", QualityScore: 0.9},
		EntityAsset{EntityID: appleID, AssetID: "apple_logo", SHA256: "bbb", StorageURL: "https://store/apple.png", QualityScore: 0.8},
	))

	ref, ok := r.ResolveBest(timCookID)
	require.True(t, ok)
	require.Equal(t, "tim_cook_01", ref.AssetID)

	ref, ok = r.ResolveBest(appleID)
	require.True(t, ok)
	require.Equal(t, "apple_logo", ref.AssetID)
	require.NotEqual(t, "tim_cook_01", ref.AssetID)
}

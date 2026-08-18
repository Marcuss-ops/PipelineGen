package entities

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func testAsset(assetID string, quality float64) EntityAsset {
	return EntityAsset{
		AssetID:      assetID,
		AssetType:    "photo",
		SHA256:       "sha256-" + assetID + "-abcdef0123456789abcdef0123456789",
		StorageURL:   "https://assets.example/" + assetID + ".png",
		QualityScore: quality,
	}
}

func TestEntityMediaIndex_IndexForEntityDerivesCanonicalID(t *testing.T) {
	ix := NewEntityMediaIndex()
	require.NoError(t, ix.IndexForEntity("PERSON", "Floyd Mayweather Jr.", testAsset("floyd_001", 0.91)))

	// The asset is retrievable by the human-readable canonical id, and its
	// EntityID was derived — never caller-supplied.
	require.Equal(t, []string{"floyd_001"}, assetIDs(ix.Assets("person:floyd-mayweather-jr")))
	require.Equal(t, 1, ix.Len())
}

func TestEntityMediaIndex_IndexForEntityEmptyCanonicalID(t *testing.T) {
	ix := NewEntityMediaIndex()
	err := ix.IndexForEntity("PERSON", "...", testAsset("bad", 0.5))
	require.ErrorIs(t, err, ErrInvalidEntityAsset)
}

func TestEntityMediaResolver_ResolveBestByQuality(t *testing.T) {
	r := NewEntityMediaResolver()
	require.NoError(t, r.index.IndexAllForEntity("PERSON", "Tim Cook",
		testAsset("tim_001", 0.84),
		testAsset("tim_002", 0.91),
		testAsset("tim_003", 0.77),
	))

	best, err := r.ResolveBest("person:tim-cook")
	require.NoError(t, err)
	require.Equal(t, "tim_002", best.AssetID)
	require.NotEmpty(t, best.SHA256)
	require.NotEmpty(t, best.URL)
}

func TestEntityMediaResolver_ResolveBestNoAssets(t *testing.T) {
	r := NewEntityMediaResolver()
	_, err := r.ResolveBest("person:unknown")
	require.ErrorIs(t, err, ErrEntityHasNoAssets)
}

func TestEntityMediaResolver_ResolveTopQualityOrder(t *testing.T) {
	r := NewEntityMediaResolver()
	require.NoError(t, r.index.IndexAllForEntity("PERSON", "Tim Cook",
		testAsset("a", 0.5),
		testAsset("b", 0.9),
		testAsset("c", 0.7),
	))

	top := r.ResolveTop("person:tim-cook", 2)
	require.Equal(t, []string{"b", "c"}, resolvedAssetIDs(top))
}

func TestEntityMediaResolver_Deterministic(t *testing.T) {
	r := NewEntityMediaResolver()
	require.NoError(t, r.index.IndexAllForEntity("PERSON", "Tim Cook",
		testAsset("a", 0.5),
		testAsset("b", 0.9),
		testAsset("c", 0.9), // tie with b → asset id asc keeps b before c
	))
	first, err := r.ResolveBest("person:tim-cook")
	require.NoError(t, err)
	for i := 0; i < 100; i++ {
		got, err := r.ResolveBest("person:tim-cook")
		require.NoError(t, err)
		require.Equal(t, first, got)
	}
}

func assetIDs(assets []EntityAsset) []string {
	out := make([]string, len(assets))
	for i, a := range assets {
		out[i] = a.AssetID
	}
	return out
}

func resolvedAssetIDs(refs []ResolvedAssetRef) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.AssetID
	}
	return out
}

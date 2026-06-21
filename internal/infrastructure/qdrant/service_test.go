package qdrant

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockStore implements Store for unit testing.
type mockStore struct {
	points         map[string]VectorAsset
	searches       []SearchRequest
	batchCalls     [][]VectorAsset                                    // tracks each UpsertAssets invocation
	listCalls      []int                                              // tracks each ListPointIDs invocation (limit values)
	deleteCalls    [][]string                                         // tracks each DeletePoints invocation (chunked asset_ids)
	listOverride   func(limit int) []string                           // optional override for the ListPointIDs cross-check test
	scrollOverride func(batchSize int, fn func([]string) error) error // optional override to simulate Qdrant scroll errors/mid-stream cancel
}

func newMockStore() *mockStore {
	return &mockStore{
		points: make(map[string]VectorAsset),
	}
}

func (m *mockStore) EnsureCollection(ctx context.Context) error {
	return nil
}

func (m *mockStore) UpsertAsset(ctx context.Context, asset VectorAsset) error {
	m.points[asset.AssetID] = asset
	return nil
}

func (m *mockStore) UpsertAssets(ctx context.Context, assets []VectorAsset) error {
	m.batchCalls = append(m.batchCalls, assets)
	for _, a := range assets {
		m.points[a.AssetID] = a
	}
	return nil
}

func (m *mockStore) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	m.searches = append(m.searches, req)
	return []SearchResult{
		{
			AssetID:   "clip_001",
			Score:     0.92,
			Source:    "artlist",
			Name:      "Test clip",
			LocalPath: "/data/media/test.mp4",
		},
	}, nil
}

func (m *mockStore) DeleteAsset(ctx context.Context, assetID string) error {
	delete(m.points, assetID)
	return nil
}

func (m *mockStore) Health(ctx context.Context) error {
	return nil
}

func (m *mockStore) HybridSearch(ctx context.Context, req HybridSearchRequest) ([]SearchResult, error) {
	return []SearchResult{
		{AssetID: "clip_hybrid_001", Score: 0.93, Source: "artlist", Name: "Hybrid match", LocalPath: "/data/media/hybrid.mp4"},
	}, nil
}

// ListPointIDs returns the assetIDs present in the mock. By default it
// mirrors UpsertAsset population (one map key per point). A test can
// install listOverride to simulate Qdrant-drift (orphan / missing IDs)
// without standing up a real Qdrant server. limit <= 0 yields an empty
// slice; limit > distinct assets yields all distinct assetIDs (no
// pagination is needed for unit-test scale).
func (m *mockStore) ListPointIDs(ctx context.Context, limit int) ([]string, error) {
	m.listCalls = append(m.listCalls, limit)
	if m.listOverride != nil {
		return m.listOverride(limit), nil
	}
	if limit <= 0 {
		return []string{}, nil
	}
	out := make([]string, 0, len(m.points))
	for id := range m.points {
		out = append(out, id)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *mockStore) CollectionInfo(ctx context.Context) (*CollectionInfo, error) {
	return &CollectionInfo{PointsCount: int64(len(m.points))}, nil
}

func (m *mockStore) IndexHealth(ctx context.Context) (*IndexHealthReport, error) {
	return &IndexHealthReport{QdrantPoints: int64(len(m.points)), OK: true}, nil
}

func (m *mockStore) CleanupStalePoints(ctx context.Context, validator func(assetID, driveFileID, driveLink string) (bool, error)) (int, error) {
	var deleted int
	for assetID, point := range m.points {
		if point.DriveLink == "" {
			continue
		}
		valid, err := validator(assetID, "", point.DriveLink)
		if err != nil || !valid {
			delete(m.points, assetID)
			deleted++
		}
	}
	return deleted, nil
}

func (m *mockStore) Close() error {
	return nil
}

func TestService_UpsertAndSearch(t *testing.T) {
	mock := newMockStore()
	log := zap.NewNop()
	svc := NewService(mock, Config{}, log)

	ctx := context.Background()

	// Test EnsureCollection
	err := svc.EnsureCollection(ctx)
	require.NoError(t, err)

	// Test UpsertAsset
	asset := VectorAsset{
		AssetID:         "clip_001",
		Source:          "artlist",
		Name:            "Space cat",
		LocalPath:       "/data/media/cat.mp4",
		Category:        "animals",
		MediaType:       "video",
		TextEmbedding:   make([]float32, 384),
		VisualEmbedding: make([]float32, 512),
		CreatedAt:       time.Now(),
	}
	err = svc.UpsertAsset(ctx, asset)
	require.NoError(t, err)

	// Verify through mock
	_, exists := mock.points["clip_001"]
	assert.True(t, exists)

	// Test Search
	results, err := svc.Search(ctx, SearchRequest{
		QueryVector: make([]float32, 384),
		VectorName:  "text",
		Limit:       10,
		MinScore:    0.85,
	})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, 0.92, results[0].Score)
	assert.Equal(t, "artlist", results[0].Source)

	// Test DeleteAsset
	err = svc.DeleteAsset(ctx, "clip_001")
	require.NoError(t, err)
	_, exists = mock.points["clip_001"]
	assert.False(t, exists)
}

func TestService_SkipUpsertWithoutEmbeddings(t *testing.T) {
	mock := newMockStore()
	log := zap.NewNop()
	svc := NewService(mock, Config{}, log)

	ctx := context.Background()
	asset := VectorAsset{
		AssetID: "clip_002",
		Source:  "artlist",
		Name:    "No embeddings",
	}

	// Should skip gracefully when no embeddings are provided
	err := svc.UpsertAsset(ctx, asset)
	require.NoError(t, err)
	_, exists := mock.points["clip_002"]
	assert.False(t, exists)
}

func TestService_UpsertAssets_Batch(t *testing.T) {
	mock := newMockStore()
	log := zap.NewNop()
	svc := NewService(mock, Config{
		SparseVectorName: "bm25_text",
	}, log)

	ctx := context.Background()

	assets := []VectorAsset{
		{
			AssetID:         "batch_001",
			Source:          "artlist",
			Name:            "Cat in space",
			SearchText:      "cat astronaut floating",
			TextEmbedding:   make([]float32, 768),
			VisualEmbedding: make([]float32, 512),
		},
		{
			AssetID: "batch_002",
			Source:  "stock",
			Name:    "No embeddings at all",
			// Zero embeddings — should be filtered out
		},
		{
			AssetID:    "batch_003",
			Source:     "youtube",
			Name:       "Ocean waves",
			SearchText: "ocean waves crashing rocks",
			// Only SearchText — BM25 should be auto-generated
		},
		{
			AssetID:        "batch_004",
			Source:         "voiceover",
			Name:           "Voice narration",
			SearchText:     "narrator speaking dramatically",
			AudioEmbedding: make([]float32, 512),
		},
	}

	err := svc.UpsertAssets(ctx, assets)
	require.NoError(t, err)

	// 1. Verify only one batch call was made (no chunking for < 500 assets)
	assert.Len(t, mock.batchCalls, 1, "should be exactly one batch upsert call")

	// 2. Verify that batch_002 (no embeddings) was filtered out
	_, hasNoEmb := mock.points["batch_002"]
	assert.False(t, hasNoEmb, "asset without embeddings should be filtered out")

	// 3. Verify the 3 valid assets were upserted
	upsertedIDs := make([]string, 0, len(mock.points))
	for id := range mock.points {
		upsertedIDs = append(upsertedIDs, id)
	}
	assert.Contains(t, upsertedIDs, "batch_001")
	assert.Contains(t, upsertedIDs, "batch_003")
	assert.Contains(t, upsertedIDs, "batch_004")
	assert.Len(t, upsertedIDs, 3, "exactly 3 out of 4 assets should be upserted")

	// 4. Verify BM25 auto-generation for batch_003 (had SearchText but no sparse vector)
	asset003 := mock.points["batch_003"]
	require.NotNil(t, asset003.SparseBM25, "batch_003 should have auto-generated BM25 sparse vector")
	assert.NotEmpty(t, asset003.SparseBM25.Indices, "BM25 indices should be non-empty")
	assert.NotEmpty(t, asset003.SparseBM25.Values, "BM25 values should be non-empty")
	assert.Equal(t, len(asset003.SparseBM25.Indices), len(asset003.SparseBM25.Values),
		"BM25 indices and values should have same length")

	// 5. Verify BM25 auto-generation for batch_004 (had SearchText + AudioEmbedding)
	asset004 := mock.points["batch_004"]
	require.NotNil(t, asset004.SparseBM25, "batch_004 should have auto-generated BM25")

	// 6. Verify batch_001 (which already had dense embeddings) also got BM25 from SearchText
	asset001 := mock.points["batch_001"]
	require.NotNil(t, asset001.SparseBM25, "batch_001 should have auto-generated BM25")

	// 7. Verify the batch call contains exactly 3 assets (not 4)
	require.Len(t, mock.batchCalls[0], 3, "batch call should contain exactly 3 assets")

	// 8. Verify IDs in the batch call match expected
	batchIDs := make(map[string]bool)
	for _, a := range mock.batchCalls[0] {
		batchIDs[a.AssetID] = true
	}
	assert.True(t, batchIDs["batch_001"])
	assert.False(t, batchIDs["batch_002"], "batch_002 should NOT be in the batch call")
	assert.True(t, batchIDs["batch_003"])
	assert.True(t, batchIDs["batch_004"])
}

func TestService_UpsertAssets_EmptyBatch(t *testing.T) {
	mock := newMockStore()
	log := zap.NewNop()
	svc := NewService(mock, Config{}, log)

	ctx := context.Background()

	// Empty slice should not error
	err := svc.UpsertAssets(ctx, nil)
	require.NoError(t, err)
	assert.Len(t, mock.batchCalls, 0)

	// All assets without embeddings should result in zero batch calls
	err = svc.UpsertAssets(ctx, []VectorAsset{
		{AssetID: "no_emb_1", Name: "Nothing"},
		{AssetID: "no_emb_2", Name: "Nothing else"},
	})
	require.NoError(t, err)
	assert.Len(t, mock.batchCalls, 0, "all-filtered batch should not call the store")
}

func TestService_UpsertAsset_DimensionMismatch(t *testing.T) {
	mock := newMockStore()
	log := zap.NewNop()
	svc := NewService(mock, Config{
		TextDimensions:   768,
		VisualDimensions: 512,
		AudioDimensions:  512,
		SparseVectorName: "bm25_text",
	}, log)

	ctx := context.Background()

	// Asset with wrong text embedding dimension (384 instead of 768)
	asset := VectorAsset{
		AssetID:       "dim_mismatch_001",
		Source:        "artlist",
		Name:          "Bad dimensions",
		TextEmbedding: make([]float32, 384), // Wrong! Should be 768
	}

	err := svc.UpsertAsset(ctx, asset)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "text embedding dim 384 != expected 768")
	_, exists := mock.points["dim_mismatch_001"]
	assert.False(t, exists, "asset with wrong dimensions should not be upserted")
}

func TestService_UpsertAsset_CorrectDimensionsPasses(t *testing.T) {
	mock := newMockStore()
	log := zap.NewNop()
	svc := NewService(mock, Config{
		TextDimensions:   768,
		VisualDimensions: 512,
		AudioDimensions:  512,
	}, log)

	ctx := context.Background()

	asset := VectorAsset{
		AssetID:       "dim_ok_001",
		Source:        "artlist",
		Name:          "Correct dimensions",
		TextEmbedding: make([]float32, 768),
	}

	err := svc.UpsertAsset(ctx, asset)
	require.NoError(t, err)
	_, exists := mock.points["dim_ok_001"]
	assert.True(t, exists, "asset with correct dimensions should be upserted")
}

func TestService_UpsertAsset_VisualDimensionMismatch(t *testing.T) {
	mock := newMockStore()
	log := zap.NewNop()
	svc := NewService(mock, Config{
		VisualDimensions: 512,
	}, log)

	ctx := context.Background()

	asset := VectorAsset{
		AssetID:         "dim_visual_001",
		Source:          "stock",
		Name:            "Wrong visual dim",
		VisualEmbedding: make([]float32, 768), // Wrong! Should be 512
	}

	err := svc.UpsertAsset(ctx, asset)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "visual embedding dim 768 != expected 512")
	_, exists := mock.points["dim_visual_001"]
	assert.False(t, exists)
}

// OperationCollectionInfo mirrors CollectionInfo for the alias-served
// collection. In a mock, both return the same in-memory point count; the
// distinction matters in the real QdrantClient where alias and physical
// collection can diverge during backfill.
func (m *mockStore) OperationCollectionInfo(ctx context.Context) (*CollectionInfo, error) {
	return &CollectionInfo{PointsCount: int64(len(m.points))}, nil
}

// PhysicalCollectionInfo mirrors CollectionInfo for the versioned
// physical collection.
func (m *mockStore) PhysicalCollectionInfo(ctx context.Context) (*CollectionInfo, error) {
	return &CollectionInfo{PointsCount: int64(len(m.points))}, nil
}

// ScrollAssetIDsPage mirrors the production iterator. Default impl
// chunks the mock points into batchSize-sized pages; tests can
// install scrollOverrideFn to control cursor behaviour or fail
// individual batches.
func (m *mockStore) ScrollAssetIDsPage(ctx context.Context, batchSize int, fn func([]string) error) error {
	if batchSize <= 0 {
		batchSize = 200
	}
	all := make([]string, 0, len(m.points))
	for id := range m.points {
		all = append(all, id)
	}
	// Sort for deterministic order across runs (maps are randomized).
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			if all[i] > all[j] {
				all[i], all[j] = all[j], all[i]
			}
		}
	}
	for i := 0; i < len(all); i += batchSize {
		end := i + batchSize
		if end > len(all) {
			end = len(all)
		}
		if err := fn(all[i:end]); err != nil {
			return err
		}
	}
	return nil
}

// DeletePoints removes every mockPoint whose assetID appears in the
// provided slice and records the call. Idempotent in the sense that
// re-deleting the same set is a no-op for the in-memory map.
func (m *mockStore) DeletePoints(ctx context.Context, assetIDs []string) error {
	m.deleteCalls = append(m.deleteCalls, assetIDs)
	for _, id := range assetIDs {
		delete(m.points, id)
	}
	return nil
}

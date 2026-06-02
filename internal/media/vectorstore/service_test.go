package vectorstore

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
	points     map[string]VectorAsset
	searches   []SearchRequest
	batchCalls [][]VectorAsset // tracks each UpsertAssets invocation
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

func (m *mockStore) CollectionInfo(ctx context.Context) (*CollectionInfo, error) {
	return &CollectionInfo{PointsCount: int64(len(m.points))}, nil
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

func TestService_WhenDisabled(t *testing.T) {
	mock := newMockStore()
	log := zap.NewNop()
	svc := NewService(mock, Config{}, log)
	svc.SetEnabled(false)

	ctx := context.Background()

	// All operations should be no-ops when disabled
	err := svc.UpsertAsset(ctx, VectorAsset{AssetID: "test", TextEmbedding: []float32{0.1}})
	require.NoError(t, err)

	results, err := svc.Search(ctx, SearchRequest{QueryVector: []float32{0.1}})
	require.NoError(t, err)
	assert.Nil(t, results)
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
			AssetID:       "batch_002",
			Source:        "stock",
			Name:          "No embeddings at all",
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
			AssetID:         "batch_004",
			Source:          "voiceover",
			Name:            "Voice narration",
			SearchText:      "narrator speaking dramatically",
			AudioEmbedding:  make([]float32, 512),
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
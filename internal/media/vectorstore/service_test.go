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
	points   map[string]VectorAsset
	searches []SearchRequest
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
package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"velox/go-master/internal/config"
	"velox/go-master/internal/media/vectorstore"
)

// ── Mock dependencies ──

type mockVectorStore struct {
	vectorstore.Store
	searched bool
	results  []vectorstore.SearchResult
	err      error
}

func (m *mockVectorStore) Search(ctx context.Context, req vectorstore.SearchRequest) ([]vectorstore.SearchResult, error) {
	m.searched = true
	return m.results, m.err
}

func (m *mockVectorStore) UpsertAssets(ctx context.Context, assets []vectorstore.VectorAsset) error {
	return nil
}

func (m *mockVectorStore) HybridSearch(ctx context.Context, req vectorstore.HybridSearchRequest) ([]vectorstore.SearchResult, error) {
	return []vectorstore.SearchResult{
		{AssetID: "hybrid_001", Score: 0.91, Name: "Hybrid mock"},
	}, nil
}

func (m *mockVectorStore) CollectionInfo(ctx context.Context) (*vectorstore.CollectionInfo, error) {
	return &vectorstore.CollectionInfo{}, nil
}

type mockEmbedder struct {
	embedding []float64
	err       error
}

func (m *mockEmbedder) EmbedText(ctx context.Context, text string) ([]float64, error) {
	return m.embedding, m.err
}

func (m *mockEmbedder) EmbedVisual(ctx context.Context, text string) ([]float64, error) {
	return m.embedding, m.err
}

func (m *mockEmbedder) EmbedAudio(ctx context.Context, text string) ([]float64, error) {
	return m.embedding, m.err
}

type mockJobService struct {
	enqueued bool
	query    string
	source   string
	jobID    string
	err      error
}

func (m *mockJobService) EnqueueMediaGeneration(ctx context.Context, query string, source string) (string, error) {
	m.enqueued = true
	m.query = query
	m.source = source
	return m.jobID, m.err
}

// ── Helper ──

func newTestService(t *testing.T, store *mockVectorStore, embedder *mockEmbedder, jobSvc *mockJobService, minScore float64) *Service {
	t.Helper()
	log := zap.NewNop()
	cfg := &config.VectorSearchConfig{
		TextVectorName:     "text",
		VisualVectorName:   "visual",
		MinInstantScore:    minScore,
		AllowBackgroundGen: true,
	}
	vectorSvc := vectorstore.NewService(store, vectorstore.Config{}, log)
	return NewService(vectorSvc, embedder, jobSvc, nil, config.RerankerConfig{}, cfg, log)
}

func makeEmbedding(dim int) []float64 {
	emb := make([]float64, dim)
	for i := range emb {
		emb[i] = float64(i) / float64(dim)
	}
	return emb
}

func makeFloat32(dim int) []float32 {
	emb := make([]float32, dim)
	for i := range emb {
		emb[i] = float32(i) / float32(dim)
	}
	return emb
}

// ── Tests ──

func TestMatch_InstantMatch_HighScore(t *testing.T) {
	store := &mockVectorStore{
		results: []vectorstore.SearchResult{
			{AssetID: "artlist_001", Score: 0.92, Source: "artlist", Name: "Space cat", LocalPath: "/data/cat.mp4"},
		},
	}
	embedder := &mockEmbedder{embedding: makeEmbedding(384)}
	jobSvc := &mockJobService{jobID: "job_001"}
	svc := newTestService(t, store, embedder, jobSvc, 0.85)

	resp, err := svc.Match(context.Background(), &MatchRequest{
		Query: "un gatto spaziale", Mode: "text", Limit: 3, MinScore: 0.85,
	})
	require.NoError(t, err)
	assert.True(t, resp.OK)
	assert.Equal(t, "instant_match", resp.Status)
	assert.NotNil(t, resp.Asset)
	assert.Equal(t, "artlist_001", resp.Asset.ID)
	assert.Equal(t, 0.92, resp.Asset.Score)
	assert.True(t, resp.LatencyMs >= 0)
	// Should NOT enqueue job when instant match found
	assert.False(t, jobSvc.enqueued)
}

func TestMatch_InstantMatch_VisualMode(t *testing.T) {
	store := &mockVectorStore{
		results: []vectorstore.SearchResult{
			{AssetID: "stock_001", Score: 0.88, Source: "stock", Name: "Night sky"},
		},
	}
	embedder := &mockEmbedder{embedding: makeEmbedding(512)}
	jobSvc := &mockJobService{jobID: "job_002"}
	svc := newTestService(t, store, embedder, jobSvc, 0.85)

	resp, err := svc.Match(context.Background(), &MatchRequest{
		Query: "night sky cinematic", Mode: "visual", Limit: 5, MinScore: 0.85,
	})
	require.NoError(t, err)
	assert.Equal(t, "instant_match", resp.Status)
	assert.NotNil(t, resp.Asset)
	assert.Equal(t, "stock_001", resp.Asset.ID)
	assert.Equal(t, 0.88, resp.Asset.Score)
}

func TestMatch_BelowThreshold_EnqueuesJob(t *testing.T) {
	store := &mockVectorStore{
		results: []vectorstore.SearchResult{
			{AssetID: "stock_002", Score: 0.62, Source: "stock", Name: "Blurry forest"},
		},
	}
	embedder := &mockEmbedder{embedding: makeEmbedding(384)}
	jobSvc := &mockJobService{jobID: "job_gen_001"}
	svc := newTestService(t, store, embedder, jobSvc, 0.85)

	resp, err := svc.Match(context.Background(), &MatchRequest{
		Query: "forest drone shot", Mode: "text", Limit: 3, MinScore: 0.85,
		AllowBackgroundGen: true,
	})
	require.NoError(t, err)
	assert.True(t, resp.OK)
	assert.Equal(t, "fallback_generating", resp.Status)
	assert.NotNil(t, resp.FallbackAsset)
	assert.Equal(t, 0.62, resp.FallbackAsset.Score)
	assert.Equal(t, "job_gen_001", resp.GenerationJobID)
	assert.True(t, jobSvc.enqueued)
	assert.Equal(t, "forest drone shot", jobSvc.query)
}

func TestMatch_NoResults_FallbackWithoutGeneration(t *testing.T) {
	store := &mockVectorStore{
		results: nil, // empty
	}
	embedder := &mockEmbedder{embedding: makeEmbedding(384)}
	jobSvc := &mockJobService{jobID: "job_003"}
	log := zap.NewNop()
	cfg := &config.VectorSearchConfig{
		TextVectorName:     "text",
		VisualVectorName:   "visual",
		MinInstantScore:    0.85,
		AllowBackgroundGen: false, // disabled globally
	}
	vectorSvc := vectorstore.NewService(store, vectorstore.Config{}, log)
	svc := NewService(vectorSvc, embedder, jobSvc, nil, config.RerankerConfig{}, cfg, log)

	resp, err := svc.Match(context.Background(), &MatchRequest{
		Query: "unicorn ballet cinematic", MinScore: 0.85,
		AllowBackgroundGen: false,
	})
	require.NoError(t, err)
	assert.Equal(t, "fallback_used", resp.Status)
	assert.Nil(t, resp.Asset)
	assert.Nil(t, resp.FallbackAsset)
	assert.Empty(t, resp.GenerationJobID)
	assert.False(t, jobSvc.enqueued)
}

func TestMatch_EmbeddingError_ReturnsStatus(t *testing.T) {
	store := &mockVectorStore{}
	embedder := &mockEmbedder{err: fmt.Errorf("connection refused")}
	jobSvc := &mockJobService{}
	svc := newTestService(t, store, embedder, jobSvc, 0.85)

	resp, err := svc.Match(context.Background(), &MatchRequest{
		Query: "test query", MinScore: 0.85,
	})
	require.NoError(t, err)
	assert.Equal(t, "embedding_failed", resp.Status)
	assert.Nil(t, resp.Asset)
}

func TestMatch_VectorSearchError_FallsBackGracefully(t *testing.T) {
	store := &mockVectorStore{err: fmt.Errorf("qdrant down")}
	embedder := &mockEmbedder{embedding: makeEmbedding(384)}
	jobSvc := &mockJobService{jobID: "job_004"}
	svc := newTestService(t, store, embedder, jobSvc, 0.85)

	resp, err := svc.Match(context.Background(), &MatchRequest{
		Query: "test query", MinScore: 0.85, AllowBackgroundGen: true,
	})
	require.NoError(t, err)
	// Should still fall through to background generation
	assert.Equal(t, "fallback_generating", resp.Status)
	assert.True(t, jobSvc.enqueued)
}

func TestMatch_EmptyQuery_ReturnsError(t *testing.T) {
	svc := newTestService(t, &mockVectorStore{}, &mockEmbedder{}, &mockJobService{}, 0.85)

	_, err := svc.Match(context.Background(), &MatchRequest{
		Query: "", MinScore: 0.85,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty query")
}

func TestMatch_DefaultValues(t *testing.T) {
	store := &mockVectorStore{
		results: []vectorstore.SearchResult{
			{AssetID: "artlist_003", Score: 0.91, Name: "Test"},
		},
	}
	embedder := &mockEmbedder{embedding: makeEmbedding(384)}
	jobSvc := &mockJobService{jobID: "job_005"}
	svc := newTestService(t, store, embedder, jobSvc, 0.85)

	// No limit/min_score provided — should use defaults
	resp, err := svc.Match(context.Background(), &MatchRequest{
		Query: "test", Limit: 0, MinScore: 0,
	})
	require.NoError(t, err)
	assert.Equal(t, "instant_match", resp.Status)
	assert.Equal(t, "artlist_003", resp.Asset.ID)
}

func TestMatch_CacheSpeedsUpRepeatedQuery(t *testing.T) {
	embedder := &mockEmbedder{embedding: makeEmbedding(384)}
	store := &mockVectorStore{
		results: []vectorstore.SearchResult{
			{AssetID: "artlist_004", Score: 0.90, Name: "Cached"},
		},
	}
	jobSvc := &mockJobService{jobID: "job_006"}
	svc := newTestService(t, store, embedder, jobSvc, 0.85)

	// First call
	resp1, err := svc.Match(context.Background(), &MatchRequest{Query: "same query", MinScore: 0.85})
	require.NoError(t, err)
	assert.Equal(t, "instant_match", resp1.Status)

	// Second call with same query — should use cache, not call embedder
	embedder2 := &mockEmbedder{embedding: nil, err: fmt.Errorf("should not be called")}
	svc2 := newTestService(t, store, embedder2, jobSvc, 0.85)
	// Manually populate cache (key format: mode + ":" + query)
	svc2.embeddingCache["text:same query"] = makeFloat32(384)

	resp2, err := svc2.Match(context.Background(), &MatchRequest{Query: "same query", MinScore: 0.85})
	require.NoError(t, err)
	assert.Equal(t, "instant_match", resp2.Status)
}

func TestClearEmbeddingCache(t *testing.T) {
	svc := newTestService(t, &mockVectorStore{}, &mockEmbedder{}, &mockJobService{}, 0.85)
	svc.embeddingCache["test"] = []float32{0.1, 0.2}
	assert.Len(t, svc.embeddingCache, 1)

	svc.ClearEmbeddingCache()
	assert.Empty(t, svc.embeddingCache)
}

// ── Test PythonEmbeddingAdapter ──

func TestPythonEmbeddingAdapter_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/embed", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"embedding": []float64{0.1, 0.2, 0.3},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	adapter := NewPythonEmbeddingAdapter(server.URL)
	emb, err := adapter.EmbedText(context.Background(), "test query")
	require.NoError(t, err)
	assert.Equal(t, []float64{0.1, 0.2, 0.3}, emb)
}

func TestPythonEmbeddingAdapter_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"model not loaded"}`))
	}))
	defer server.Close()

	adapter := NewPythonEmbeddingAdapter(server.URL)
	_, err := adapter.EmbedText(context.Background(), "test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestPythonEmbeddingAdapter_ConnectionError(t *testing.T) {
	adapter := NewPythonEmbeddingAdapter("http://127.0.0.1:1")
	_, err := adapter.EmbedText(context.Background(), "test")
	assert.Error(t, err)
}

// ── Test JobServiceAdapter ──

type enqueueRecorder struct {
	requests []struct {
		Type    string
		Payload map[string]any
	}
	jobID string
	err   error
}

func (e *enqueueRecorder) Enqueue(ctx context.Context, req interface{ Enqueue(interface{}) }) {
	// Not a full mock of jobs.Service — testing through JobServiceAdapter
	// would need a real or fully-mocked jobs.Service. This test verifies
	// the adapter signature compiles and the builder function.
}

func TestJobServiceAdapter_InterfaceSatisfied(t *testing.T) {
	// Verify that *JobServiceAdapter satisfies the JobService interface
	var _ JobService = (*JobServiceAdapter)(nil)
	t.Log("JobServiceAdapter satisfies JobService interface")
}

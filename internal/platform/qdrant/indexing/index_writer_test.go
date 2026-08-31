// Package qdrant — index_writer_test.go (P2, July 2026).
//
// Unit tests for IndexWriter.DeletePoints, UpsertFromClips, and ReindexAll.
// Uses httptest.Server mocks (mirroring searcher_cache_test.go and
// verifier_test.go patterns) and stub mapper implementations to exercise
// the full write paths without a real Qdrant instance.
// ValidatePoint tests live in qdrant_test.go (comprehensive coverage).
package indexing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// ── Stub mapper ──────────────────────────────────────────────────────

// stubWriteMapper implements FetchAsset (used by UpsertFromClips/ReindexAll)
// and ListAllAssetIDs (used by ReindexAll). Each test populates the assets
// map with precanned AssetData values before constructing the IndexWriter.
type stubWriteMapper struct {
	assets map[string]*AssetData
	ids    []string
}

func (s *stubWriteMapper) FetchAsset(_ context.Context, assetID string) (*AssetData, error) {
	if s.assets == nil {
		return nil, fmt.Errorf("asset %q not found", assetID)
	}
	a, ok := s.assets[assetID]
	if !ok {
		return nil, fmt.Errorf("asset %q not found", assetID)
	}
	return a, nil
}

func (s *stubWriteMapper) ListAllAssetIDs(_ context.Context) ([]string, error) {
	if s.ids != nil {
		return s.ids, nil
	}
	ids := make([]string, 0, len(s.assets))
	for id := range s.assets {
		ids = append(ids, id)
	}
	return ids, nil
}

// FetchAssetBatch returns a paginated batch of assets ordered by ID.
// Implements the cursor-based contract used by ReindexAll (HIGH #8).
func (s *stubWriteMapper) FetchAssetBatch(_ context.Context, afterID string, limit int) ([]*AssetData, error) {
	ids := s.sortedIDs()
	var start int
	if afterID != "" {
		found := false
		for i, id := range ids {
			if id > afterID {
				start = i
				found = true
				break
			}
		}
		if !found {
			return nil, nil // cursor past the last asset → end of data
		}
	}
	end := start + limit
	if end > len(ids) {
		end = len(ids)
	}
	if start >= len(ids) {
		return nil, nil
	}
	out := make([]*AssetData, 0, end-start)
	for _, id := range ids[start:end] {
		a, ok := s.assets[id]
		if !ok {
			return nil, fmt.Errorf("asset %q not found", id)
		}
		out = append(out, a)
	}
	return out, nil
}

func (s *stubWriteMapper) sortedIDs() []string {
	if s.ids != nil {
		return s.ids
	}
	ids := make([]string, 0, len(s.assets))
	for id := range s.assets {
		ids = append(ids, id)
	}
	// Sort for deterministic cursor behaviour.
	sort.Strings(ids)
	return ids
}

// ── Helpers ──────────────────────────────────────────────────────────

// testSchema returns a minimal v3 schema suitable for write-path tests.
func testSchema() *schema.IndexSchema {
	return &schema.IndexSchema{
		Version:      "v3-test",
		PhysicalName: "media_assets",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []schema.EmbeddingSpec{
			{Channel: "text", Dimensions: 4, Distance: "Cosine", ModelVersion: "test-v1"},
		},
	}
}

// newTestClient creates a transport.Client pointed at an httptest server.
func newTestClient(url string) *transport.Client {
	return transport.NewClient(&schema.Config{BaseURL: url, Timeout: 5}, zap.NewNop())
}

// ── DeletePoints ─────────────────────────────────────────────────────

func TestIndexWriter_DeletePoints_Success(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var deletedIDs []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/points/delete"):
			mu.Lock()
			defer mu.Unlock()
			var body struct {
				Points []string `json:"points"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			deletedIDs = append(deletedIDs, body.Points...)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{"status": "ok"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	w := NewIndexWriter(newTestClient(srv.URL), testSchema(), nil, zap.NewNop())
	err := w.DeleteAssetPoints(context.Background(), []string{"asset-1", "asset-2"})
	require.NoError(t, err)

	// Verify canonicalised IDs were sent.
	canonical1 := schema.AssetIDToQdrantPointID("asset-1")
	canonical2 := schema.AssetIDToQdrantPointID("asset-2")
	assert.Contains(t, deletedIDs, canonical1)
	assert.Contains(t, deletedIDs, canonical2)
}

func TestIndexWriter_DeletePoints_EmptyList(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP call for empty delete: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	w := NewIndexWriter(newTestClient(srv.URL), testSchema(), nil, zap.NewNop())
	require.NoError(t, w.DeleteAssetPoints(context.Background(), nil))
	require.NoError(t, w.DeleteAssetPoints(context.Background(), []string{}))
}

func TestIndexWriter_DeletePoints_ServerError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/points/delete") {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	w := NewIndexWriter(newTestClient(srv.URL), testSchema(), nil, zap.NewNop())
	err := w.DeleteAssetPoints(context.Background(), []string{"asset-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete points")
}

func TestIndexWriter_DeletePoints_FiltersEmptyCanonicalIDs(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var callCount int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/points/delete") {
			mu.Lock()
			callCount++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{"status": "ok"},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	w := NewIndexWriter(newTestClient(srv.URL), testSchema(), nil, zap.NewNop())
	err := w.DeleteAssetPoints(context.Background(), []string{""})
	require.NoError(t, err)
	assert.Equal(t, 0, callCount, "empty canonical ID should be filtered, no HTTP call made")
}

// ── UpsertFromClips ──────────────────────────────────────────────────

func TestIndexWriter_UpsertFromClips_Success(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var upsertedPoints []schema.Point

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/points"):
			mu.Lock()
			defer mu.Unlock()
			var body struct {
				Points []schema.Point `json:"points"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			upsertedPoints = append(upsertedPoints, body.Points...)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{"status": "ok"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	mapper := &stubWriteMapper{
		assets: map[string]*AssetData{
			"asset-1": {ID: "asset-1", Name: "clip-1", Source: "youtube", TextVector: []float32{0.1, 0.2, 0.3, 0.4}},
		},
	}
	w := NewIndexWriter(newTestClient(srv.URL), testSchema(), &PayloadMapper{store: mapper, log: zap.NewNop()}, zap.NewNop())
	err := w.UpsertFromClips(context.Background(), []string{"asset-1"})
	require.NoError(t, err)
	assert.Len(t, upsertedPoints, 1)
	assert.Equal(t, schema.AssetIDToQdrantPointID("asset-1"), upsertedPoints[0].ID)
}

func TestIndexWriter_UpsertFromClips_EmptyList(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP call for empty upsert: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	w := NewIndexWriter(newTestClient(srv.URL), testSchema(), nil, zap.NewNop())
	require.NoError(t, w.UpsertFromClips(context.Background(), nil))
	require.NoError(t, w.UpsertFromClips(context.Background(), []string{}))
}

func TestIndexWriter_UpsertFromClips_MapperError(t *testing.T) {
	t.Parallel()

	var callCount int32
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/points") {
			mu.Lock()
			callCount++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{"status": "ok"},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	mapper := &stubWriteMapper{} // NO assets — FetchAsset will fail for any ID.
	w := NewIndexWriter(newTestClient(srv.URL), testSchema(), &PayloadMapper{store: mapper, log: zap.NewNop()}, zap.NewNop())
	err := w.UpsertFromClips(context.Background(), []string{"nonexistent"})
	require.Error(t, err)

	var partial *transport.PartialUpsertError
	require.ErrorAs(t, err, &partial, "should wrap transport.PartialUpsertError")
	require.Len(t, partial.Failures, 1)
	assert.Equal(t, "nonexistent", partial.Failures[0].AssetID)
	assert.Equal(t, "fetch", partial.Failures[0].Phase)
	assert.NotNil(t, partial.Failures[0].Cause)
	assert.Equal(t, int32(0), callCount, "mapper failure must not trigger HTTP call")
}

func TestIndexWriter_UpsertFromClips_PartialFailure(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var upsertedIDs []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/points") {
			mu.Lock()
			defer mu.Unlock()
			var body struct {
				Points []schema.Point `json:"points"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			for _, p := range body.Points {
				upsertedIDs = append(upsertedIDs, p.ID)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{"status": "ok"},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	mapper := &stubWriteMapper{
		assets: map[string]*AssetData{
			"asset-1": {ID: "asset-1", Name: "clip-1", Source: "youtube", TextVector: []float32{0.1, 0.2, 0.3, 0.4}},
		},
	}
	w := NewIndexWriter(newTestClient(srv.URL), testSchema(), &PayloadMapper{store: mapper, log: zap.NewNop()}, zap.NewNop())
	err := w.UpsertFromClips(context.Background(), []string{"asset-1", "asset-2"})
	require.Error(t, err)

	var partial *transport.PartialUpsertError
	require.ErrorAs(t, err, &partial, "should return *transport.PartialUpsertError")
	assert.Len(t, partial.SuccessfulIDs, 1)
	assert.Len(t, partial.Failures, 1)
	assert.Equal(t, "asset-2", partial.Failures[0].AssetID)
	assert.Equal(t, "fetch", partial.Failures[0].Phase)
	assert.Len(t, upsertedIDs, 1)
}

// ── ReindexAll ───────────────────────────────────────────────────────

func TestIndexWriter_ReindexAll_Success(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var totalUpserted int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/points") {
			mu.Lock()
			defer mu.Unlock()
			var body struct {
				Points []schema.Point `json:"points"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			totalUpserted += len(body.Points)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{"status": "ok"},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	mapper := &stubWriteMapper{
		assets: map[string]*AssetData{
			"asset-1": {ID: "asset-1", Name: "clip-1", Source: "youtube", TextVector: []float32{0.1, 0.2, 0.3, 0.4}},
			"asset-2": {ID: "asset-2", Name: "clip-2", Source: "artlist", TextVector: []float32{0.5, 0.6, 0.7, 0.8}},
		},
	}
	w := NewIndexWriter(newTestClient(srv.URL), testSchema(), &PayloadMapper{store: mapper, log: zap.NewNop()}, zap.NewNop())

	result, err := w.ReindexAll(context.Background(), "media_assets", 0)
	require.NoError(t, err)
	assert.Equal(t, 2, result.IndexedAssets)
	assert.Equal(t, 2, result.TotalAssets)
	assert.Equal(t, 2, result.SQLiteIndexableAssets)
	assert.Equal(t, 0, result.FailedAssets)
	assert.Equal(t, totalUpserted, result.IndexedAssets)
}

func TestIndexWriter_ReindexAll_EmptyList(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP call for empty reindex: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	mapper := &stubWriteMapper{assets: map[string]*AssetData{}}
	w := NewIndexWriter(newTestClient(srv.URL), testSchema(), &PayloadMapper{store: mapper, log: zap.NewNop()}, zap.NewNop())

	result, err := w.ReindexAll(context.Background(), "media_assets", 0)
	require.NoError(t, err)
	assert.Equal(t, 0, result.IndexedAssets)
	assert.Equal(t, 0, result.TotalAssets)
}

func TestIndexWriter_ReindexAll_LimitCap(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var totalUpserted int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/points") {
			mu.Lock()
			defer mu.Unlock()
			var body struct {
				Points []schema.Point `json:"points"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			totalUpserted += len(body.Points)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{"status": "ok"},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	mapper := &stubWriteMapper{
		assets: map[string]*AssetData{
			"asset-1": {ID: "asset-1", Name: "c1", Source: "youtube", TextVector: []float32{0.1, 0.2, 0.3, 0.4}},
			"asset-2": {ID: "asset-2", Name: "c2", Source: "youtube", TextVector: []float32{0.5, 0.6, 0.7, 0.8}},
			"asset-3": {ID: "asset-3", Name: "c3", Source: "youtube", TextVector: []float32{0.9, 1.0, 1.1, 1.2}},
		},
	}
	w := NewIndexWriter(newTestClient(srv.URL), testSchema(), &PayloadMapper{store: mapper, log: zap.NewNop()}, zap.NewNop())

	result, err := w.ReindexAll(context.Background(), "media_assets", 2)
	require.NoError(t, err)
	assert.Equal(t, 2, result.IndexedAssets)
	assert.Equal(t, 2, result.TotalAssets)
	assert.Equal(t, totalUpserted, result.IndexedAssets)
}

func TestIndexWriter_ReindexAll_PartialFailures(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var upsertedIDs []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/points") {
			mu.Lock()
			defer mu.Unlock()
			var body struct {
				Points []schema.Point `json:"points"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			for _, p := range body.Points {
				upsertedIDs = append(upsertedIDs, p.ID)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{"status": "ok"},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	mapper := &stubWriteMapper{
		assets: map[string]*AssetData{
			"asset-1": {ID: "asset-1", Name: "c1", Source: "youtube", TextVector: []float32{0.1, 0.2, 0.3, 0.4}},
			"asset-2": {ID: "asset-2", Name: "c2", Source: "youtube"},
			"asset-3": {ID: "asset-3", Name: "c3", Source: "youtube", TextVector: []float32{0.1}},
		},
	}
	w := NewIndexWriter(newTestClient(srv.URL), testSchema(), &PayloadMapper{store: mapper, log: zap.NewNop()}, zap.NewNop())

	result, err := w.ReindexAll(context.Background(), "media_assets", 0)
	assert.NoError(t, err, "ReindexAll should not error on partial mapping failures")
	assert.Equal(t, 1, result.IndexedAssets)
	assert.Equal(t, 3, result.TotalAssets)
	assert.Equal(t, 2, result.FailedAssets)
	assert.Len(t, result.FailedAssetIDs, 2)
	assert.Len(t, upsertedIDs, 1)
}

func TestIndexWriter_ReindexAll_UpsertError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/points") {
			http.Error(w, "qdrant unavailable", http.StatusServiceUnavailable)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	mapper := &stubWriteMapper{
		assets: map[string]*AssetData{
			"asset-1": {ID: "asset-1", Name: "c1", Source: "youtube", TextVector: []float32{0.1, 0.2, 0.3, 0.4}},
		},
	}
	w := NewIndexWriter(newTestClient(srv.URL), testSchema(), &PayloadMapper{store: mapper, log: zap.NewNop()}, zap.NewNop())

	result, err := w.ReindexAll(context.Background(), "media_assets", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reindex")
	assert.Equal(t, 0, result.IndexedAssets)
}

// ── Batch flushing (ReindexAll > 100 assets) ─────────────────────────

func TestIndexWriter_ReindexAll_BatchFlushing(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var batches int
	var totalUpserted int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/points") {
			mu.Lock()
			defer mu.Unlock()
			batches++
			var body struct {
				Points []schema.Point `json:"points"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			totalUpserted += len(body.Points)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{"status": "ok"},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	// 250 assets — should produce 3 batches (100 + 100 + 50).
	assets := make(map[string]*AssetData, 250)
	for i := range 250 {
		id := fmt.Sprintf("asset-%d", i)
		assets[id] = &AssetData{
			ID:         id,
			Name:       fmt.Sprintf("clip-%d", i),
			Source:     "youtube",
			TextVector: []float32{0.1, 0.2, 0.3, 0.4},
		}
	}

	mapper := &stubWriteMapper{assets: assets}
	w := NewIndexWriter(newTestClient(srv.URL), testSchema(), &PayloadMapper{store: mapper, log: zap.NewNop()}, zap.NewNop())

	result, err := w.ReindexAll(context.Background(), "media_assets", 0)
	require.NoError(t, err)
	assert.Equal(t, 250, result.IndexedAssets)
	assert.Equal(t, 250, result.TotalAssets)
	assert.Equal(t, 0, result.FailedAssets)
	assert.Equal(t, 3, batches, "250 assets should flush in 3 batches (100+100+50)")
	assert.Equal(t, totalUpserted, result.IndexedAssets)
}

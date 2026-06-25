package qdrant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestService_QdrantLifecycleSearchAndCleanup(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	collectionCreated := false
	var lastUpsert map[string]any
	var lastSearch map[string]any
	var lastDelete map[string]any
	var payloadIndexFields []string
	var scrollCalls int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/readyz":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		case r.Method == http.MethodGet && r.URL.Path == "/collections/media_assets":
			if !collectionCreated {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{"points_count": 1},
			})
		case r.Method == http.MethodPut && r.URL.Path == "/collections/media_assets":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			_, ok := body["vectors"].(map[string]any)
			assert.True(t, ok)
			_, ok = body["sparse_vectors"].(map[string]any)
			assert.True(t, ok)
			collectionCreated = true
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "result": true})
		case r.Method == http.MethodPut && r.URL.Path == "/collections/media_assets/index":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			payloadIndexFields = append(payloadIndexFields, body["field_name"].(string))
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "result": map[string]any{"status": "acknowledged"}})
		case r.Method == http.MethodPost && r.URL.Path == "/collections/media_assets/points":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			lastUpsert = body
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "result": map[string]any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/collections/media_assets/points/search":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			lastSearch = body
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{
						"id":    "p1",
						"score": 0.91,
						"payload": map[string]any{
							"asset_id":   "asset-1",
							"name":       "Alpha",
							"source":     "catalog",
							"media_type": "video",
							"tags":       []any{"tag1", "tag2"},
						},
					},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/collections/media_assets/points/scroll":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			scrollCalls++
			if scrollCalls == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": map[string]any{
						"points": []map[string]any{
							{"id": "p1", "payload": map[string]any{"asset_id": "asset-1", "drive_link": "https://drive.example/file"}},
						},
						"next_page_offset": "p2",
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{"points": []any{}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/collections/media_assets/points/p1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{
					"id": "p1",
					"payload": map[string]any{
						"asset_id":      "asset-1",
						"drive_link":    "https://drive.example/file",
						"drive_file_id": "file-1",
					},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/collections/media_assets/points/delete":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			lastDelete = body
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	embedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/embed", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"embedding": []float64{0.1, 0.2, 0.3}})
	}))
	defer embedSrv.Close()

	svc := NewService(Config{
		Enabled:              true,
		URL:                  srv.URL,
		Collection:           "media_assets",
		EmbeddingServerURL:   embedSrv.URL,
		TextVectorName:       "text",
		VisualVectorName:     "visual",
		AudioVectorName:      "audio",
		TranscriptVectorName: "transcript",
		SparseVectorName:     "bm25_text",
		TextDimensions:       768,
		VisualDimensions:     512,
		AudioDimensions:      512,
		TranscriptDimensions: 768,
		TimeoutMs:            1000,
	})

	require.NoError(t, svc.Health(context.Background()))
	require.NoError(t, svc.EnsureCollection(context.Background()))
	embed, err := svc.EmbedTextForVector(context.Background(), "hello qdrant", "text")
	require.NoError(t, err)
	require.Len(t, embed, 3)

	require.NoError(t, svc.UpsertAsset(context.Background(), VectorAsset{
		AssetID:          "asset-1",
		Name:             "Alpha",
		Source:           "catalog",
		MediaType:        "video",
		TextEmbedding:    []float32{0.1, 0.2, 0.3},
		Tags:             []string{"tag1", "tag2"},
		EmbeddingVersion: CurrentEmbeddingVersion,
	}))

	results, err := svc.Search(context.Background(), SearchRequest{
		QueryVector: []float32{0.1, 0.2, 0.3},
		VectorName:  "text",
		Limit:       10,
		Source:      "catalog",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "asset-1", results[0].AssetID)
	assert.Equal(t, "Alpha", results[0].Name)
	assert.Equal(t, "catalog", results[0].Source)

	deleted, err := svc.CleanupStalePoints(context.Background(), func(assetID, driveFileID, driveLink string) (bool, error) {
		return false, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)
	require.NotNil(t, lastUpsert)
	require.NotNil(t, lastSearch)
	require.NotNil(t, lastDelete)
	assert.Contains(t, lastDelete["points"], "p1")
	assert.NotEmpty(t, payloadIndexFields)
	assert.Contains(t, payloadIndexFields, "asset_id")
}

func TestService_VersionedCollectionAliasAndIndexes(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	collectionCreated := false
	var payloadIndexFields []string
	var aliasUpdate map[string]any
	var lastUpsertPath string
	var lastSearchPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/collections/media_assets_v2":
			if !collectionCreated {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{"points_count": 1},
			})
		case r.Method == http.MethodPut && r.URL.Path == "/collections/media_assets_v2":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			_, ok := body["vectors"].(map[string]any)
			assert.True(t, ok)
			_, ok = body["sparse_vectors"].(map[string]any)
			assert.True(t, ok)
			collectionCreated = true
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "result": true})
		case r.Method == http.MethodPut && r.URL.Path == "/collections/media_assets_v2/index":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			payloadIndexFields = append(payloadIndexFields, body["field_name"].(string))
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "result": map[string]any{"status": "acknowledged"}})
		case r.Method == http.MethodGet && r.URL.Path == "/aliases":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{
					"aliases": []any{},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/collections/aliases":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			aliasUpdate = body
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "result": true})
		case r.Method == http.MethodPost && r.URL.Path == "/collections/media_assets_current/points":
			lastUpsertPath = r.URL.Path
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "result": map[string]any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/collections/media_assets_current/points/search":
			lastSearchPath = r.URL.Path
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{
						"id":    "p1",
						"score": 0.99,
						"payload": map[string]any{
							"asset_id":   "asset-2",
							"name":       "Beta",
							"source":     "catalog",
							"media_type": "video",
						},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	svc := NewService(Config{
		Enabled:              true,
		URL:                  srv.URL,
		Collection:           "media_assets",
		CollectionVersion:    "v2",
		CollectionAlias:      "media_assets_current",
		TextVectorName:       "text",
		VisualVectorName:     "visual",
		AudioVectorName:      "audio",
		TranscriptVectorName: "transcript",
		SparseVectorName:     "bm25_text",
		TextDimensions:       768,
		VisualDimensions:     512,
		AudioDimensions:      512,
		TranscriptDimensions: 768,
		TimeoutMs:            1000,
	})

	require.NoError(t, svc.EnsureCollection(context.Background()))

	require.NoError(t, svc.UpsertAsset(context.Background(), VectorAsset{
		AssetID:          "asset-2",
		Name:             "Beta",
		Source:           "catalog",
		MediaType:        "video",
		TextEmbedding:    []float32{0.4, 0.5, 0.6},
		EmbeddingVersion: CurrentEmbeddingVersion,
	}))

	results, err := svc.Search(context.Background(), SearchRequest{
		QueryVector: []float32{0.4, 0.5, 0.6},
		VectorName:  "text",
		Limit:       1,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "asset-2", results[0].AssetID)

	require.NotEmpty(t, payloadIndexFields)
	assert.Contains(t, payloadIndexFields, "asset_id")
	assert.Contains(t, payloadIndexFields, "source")
	assert.NotNil(t, aliasUpdate)
	assert.Equal(t, "/collections/media_assets_current/points", lastUpsertPath)
	assert.Equal(t, "/collections/media_assets_current/points/search", lastSearchPath)
}

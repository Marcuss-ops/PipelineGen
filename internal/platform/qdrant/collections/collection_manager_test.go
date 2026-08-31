package collections

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── Alias switch tests ───────────────────────────────────────────────

func TestCollectionManager_SwitchAlias_RequiresRegisteredProjection(t *testing.T) {
	t.Parallel()

	cm := NewCollectionManager(nil, schema.DefaultV3Schema(), zap.NewNop())
	err := cm.SwitchAlias(context.Background(), "media_assets_v3_old", "media_assets_v3_new")
	require.Error(t, err, "unregistered alias mutations must be rejected")
}

// (TestCollectionManager_RollbackAlias retired — PR-DEADC-QDRANT-ROLLBACK-ALIAS-RETIRE 2026-07-10;
//  the wrapper it tested was also retired — see collection_rollback.go. The
//  canonical typed-port contract is RollbackCandidate, tested via the
//  integration surface at internal/platform/qdrant/collections/collection_manager_bluegreen_test.go.)

func TestCollectionManager_EnsureSchema_CreatesNew(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	collectionCreated := false
	aliasCreated := false
	payloadIndexes := make(map[string]bool)

	idxSchema := schema.DefaultV3Schema()
	physicalName := idxSchema.PhysicalName

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		// Alias target check — no alias exists initially.
		case r.Method == http.MethodGet && r.URL.Path == "/collections/media_assets_current/aliases":
			http.NotFound(w, r)
		// Physical collection check.
		case r.Method == http.MethodGet && r.URL.Path == "/collections/"+physicalName:
			if !collectionCreated {
				http.NotFound(w, r)
				return
			}
			payloadSchema := make(map[string]interface{})
			for _, idx := range idxSchema.PayloadIndexes {
				if payloadIndexes[idx.FieldName] {
					payloadSchema[idx.FieldName] = map[string]interface{}{"data_type": idx.FieldType}
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"status":       "green",
					"points_count": 42.0,
					"config": map[string]interface{}{
						"params": map[string]interface{}{
							"vectors": map[string]interface{}{
								"text":       map[string]interface{}{"size": float64(768), "distance": "Cosine"},
								"transcript": map[string]interface{}{"size": float64(768), "distance": "Cosine"},
								"visual":     map[string]interface{}{"size": float64(768), "distance": "Cosine"},
								"audio":      map[string]interface{}{"size": float64(512), "distance": "Cosine"},
							},
							"sparse_vectors": map[string]interface{}{
								"bm25_text": map[string]interface{}{},
							},
						},
					},
					"payload_schema": payloadSchema,
				},
			})
		// Create collection.
		case r.Method == http.MethodPut && r.URL.Path == "/collections/"+physicalName:
			collectionCreated = true
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": true,
				"status": "ok",
			})
		// Create payload indexes.
		case r.Method == http.MethodPut && r.URL.Path == "/collections/"+physicalName+"/index":
			var body map[string]interface{}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			payloadIndexes[body["field_name"].(string)] = true
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{"status": "acknowledged"},
				"status": "ok",
			})
		// Create alias.
		case r.Method == http.MethodPost && r.URL.Path == "/collections/aliases":
			aliasCreated = true
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": true,
				"status": "ok",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := transport.NewClient(&schema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	cm := NewCollectionManager(client, idxSchema, zap.NewNop())

	result, err := cm.EnsureSchema(context.Background())
	// A newly-created projection must not become runtime-visible without the
	// SQLite-authoritative verifier. This fixture intentionally does not wire
	// one, so EnsureSchema must fail closed after preparing the candidate.
	require.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, collectionCreated)
	assert.False(t, aliasCreated)
	assert.NotEmpty(t, payloadIndexes)
	// Verify at least some expected indexes were created before validation
	// blocked activation.
	for _, idx := range idxSchema.PayloadIndexes {
		assert.True(t, payloadIndexes[idx.FieldName], "missing payload index %q", idx.FieldName)
	}
}

func TestCollectionManager_EnsureSchema_AlreadyCompatible(t *testing.T) {
	t.Parallel()

	// Use a custom schema without payload indexes so we don't need to mock all 16.
	idxSchema := &schema.IndexSchema{
		Version:      "v3",
		PhysicalName: "media_assets_v3_e5_768",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []schema.EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
			{Channel: "visual", Dimensions: 768, Distance: "Cosine"},
		},
	}
	require.NoError(t, idxSchema.Validate())

	var mu sync.Mutex
	createCalls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		// Alias target check — canonical /aliases endpoint (PR-ALIAS-RESOLVE-FIX 2026-07-04).
		case r.Method == http.MethodGet && r.URL.Path == "/aliases":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"aliases": []map[string]interface{}{
						{
							"alias_name":      "media_assets_current",
							"collection_name": "media_assets_v3_e5_768",
						},
					},
				},
			})
		// Physical collection exists and is compatible — issue the
		// canonical Qdrant wire envelope (PR1 — fix/qdrant-wire-contracts):
		// `result.config.params.vectors` instead of the legacy flat
		// `config` map. The mock intentionally exercises the nested
		// decoder path so the test cannot silently pass via the
		// unmarshalLegacyLeaf fallback.
		case r.Method == http.MethodGet && r.URL.Path == "/collections/media_assets_v3_e5_768":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"status":        "green",
					"vectors_count": 2.0,
					"points_count":  42.0,
					"config": map[string]interface{}{
						"params": map[string]interface{}{
							"vectors": map[string]interface{}{
								"text":   map[string]interface{}{"size": float64(768), "distance": "Cosine"},
								"visual": map[string]interface{}{"size": float64(768), "distance": "Cosine"},
							},
						},
					},
					"payload_schema": map[string]interface{}{},
				},
			})
		// PUT collection — should NOT be called.
		case r.Method == http.MethodPut && r.URL.Path == "/collections/media_assets_v3_e5_768":
			createCalls++
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": true})
		// PromoteCandidate's POST /collections/aliases (create_alias action) — success.
		case r.Method == http.MethodPost && r.URL.Path == "/collections/aliases":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": true,
				"status": "ok",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := transport.NewClient(&schema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	cm := NewCollectionManager(client, idxSchema, zap.NewNop())

	result, err := cm.EnsureSchema(context.Background())
	require.NoError(t, err)
	assert.True(t, result.Compatible)
	assert.False(t, result.Created, "should not recreate an already compatible collection")
	assert.Equal(t, 0, createCalls, "PUT should not have been called")
}

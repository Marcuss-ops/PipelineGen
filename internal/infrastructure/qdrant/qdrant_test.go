package qdrant

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── Manifest validation tests ────────────────────────────────────────

func TestDefaultV3Schema_Validate(t *testing.T) {
	t.Parallel()

	schema := DefaultV3Schema()
	require.NotNil(t, schema)
	require.NoError(t, schema.Validate())
}

func TestSchemaValidate_Nil(t *testing.T) {
	t.Parallel()

	var s *IndexSchema
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestSchemaValidate_EmptyVersion(t *testing.T) {
	t.Parallel()

	s := &IndexSchema{
		Version:      "",
		RuntimeAlias: "alias",
	}
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version")
}

func TestSchemaValidate_EmptyRuntimeAlias(t *testing.T) {
	t.Parallel()

	s := &IndexSchema{
		Version:      "v1",
		RuntimeAlias: "",
	}
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "alias")
}

func TestSchemaValidate_PhysicalNameEqualsAlias(t *testing.T) {
	t.Parallel()

	s := &IndexSchema{
		Version:      "v1",
		PhysicalName: "same_name",
		RuntimeAlias: "same_name",
	}
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "differ")
}

func TestSchemaValidate_NoDenseVectors(t *testing.T) {
	t.Parallel()

	s := &IndexSchema{
		Version:      "v1",
		PhysicalName: "media_assets_v1",
		RuntimeAlias: "media_assets_current",
		// No DenseVectors.
	}
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dense vector")
}

func TestSchemaValidate_EmptyChannelName(t *testing.T) {
	t.Parallel()

	s := &IndexSchema{
		Version:      "v1",
		PhysicalName: "media_assets_v1",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []EmbeddingSpec{
			{Channel: "", Dimensions: 768, Distance: "Cosine"},
		},
	}
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "channel")
}

func TestSchemaValidate_DuplicateChannel(t *testing.T) {
	t.Parallel()

	s := &IndexSchema{
		Version:      "v1",
		PhysicalName: "media_assets_v1",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
			{Channel: "text", Dimensions: 512, Distance: "Euclid"},
		},
	}
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestSchemaValidate_NegativeDimensions(t *testing.T) {
	t.Parallel()

	s := &IndexSchema{
		Version:      "v1",
		PhysicalName: "media_assets_v1",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []EmbeddingSpec{
			{Channel: "text", Dimensions: 0, Distance: "Cosine"},
		},
	}
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dimensions")
}

func TestSchemaValidate_InvalidDistance(t *testing.T) {
	t.Parallel()

	s := &IndexSchema{
		Version:      "v1",
		PhysicalName: "media_assets_v1",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Manhattan"},
		},
	}
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "distance")
}

func TestSchemaValidate_EmptySparseChannel(t *testing.T) {
	t.Parallel()

	s := &IndexSchema{
		Version:      "v1",
		PhysicalName: "media_assets_v1",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
		},
		SparseVectors: []SparseSpec{
			{Channel: "", Modifier: "bm25"},
		},
	}
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sparse")
}

func TestSchemaValidate_DuplicateSparseChannel(t *testing.T) {
	t.Parallel()

	s := &IndexSchema{
		Version:      "v1",
		PhysicalName: "media_assets_v1",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
		},
		SparseVectors: []SparseSpec{
			{Channel: "bm25_text", Modifier: "bm25"},
		},
	}
	s.DenseVectors = append(s.DenseVectors, EmbeddingSpec{Channel: "bm25_text", Dimensions: 768, Distance: "Cosine"})
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestSchemaValidate_BadPayloadIndexField(t *testing.T) {
	t.Parallel()

	s := &IndexSchema{
		Version:      "v1",
		PhysicalName: "media_assets_v1",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
		},
		PayloadIndexes: []PayloadIndexSpec{
			{FieldName: "", FieldType: "keyword"},
		},
	}
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "field_name")
}

func TestSchemaValidate_BadPayloadIndexType(t *testing.T) {
	t.Parallel()

	s := &IndexSchema{
		Version:      "v1",
		PhysicalName: "media_assets_v1",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
		},
		PayloadIndexes: []PayloadIndexSpec{
			{FieldName: "status", FieldType: "binary"},
		},
	}
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "field_type")
}

// ── Schema comparison tests ──────────────────────────────────────────

func TestCompareSchema_FullyCompatible(t *testing.T) {
	t.Parallel()

	expected := &IndexSchema{
		Version:      "v3",
		PhysicalName: "media_assets_v3",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
			{Channel: "visual", Dimensions: 768, Distance: "Cosine"},
		},
		PayloadIndexes: []PayloadIndexSpec{
			{FieldName: "source", FieldType: "keyword"},
			{FieldName: "status", FieldType: "keyword"},
		},
	}

	actual := &CollectionInfo{
		VectorConfigs: map[string]VectorConfig{
			"text":   {Size: 768, Distance: "Cosine"},
			"visual": {Size: 768, Distance: "Cosine"},
		},
		PayloadIndexes: []PayloadIndexInfo{
			{FieldName: "source", FieldType: "keyword"},
			{FieldName: "status", FieldType: "keyword"},
		},
	}

	diff := CompareSchema(expected, actual)
	assert.True(t, diff.Compatible)
	assert.Empty(t, diff.MissingVectors)
	assert.Empty(t, diff.ExtraVectors)
	assert.Empty(t, diff.DimensionMismatches)
	assert.Empty(t, diff.DistanceMismatches)
	assert.Empty(t, diff.MissingIndexes)
	assert.Empty(t, diff.ExtraIndexes)
}

func TestCompareSchema_DimensionMismatch(t *testing.T) {
	t.Parallel()

	expected := &IndexSchema{
		Version:      "v3",
		PhysicalName: "media_assets_v3",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
		},
	}

	actual := &CollectionInfo{
		VectorConfigs: map[string]VectorConfig{
			"text": {Size: 512, Distance: "Cosine"},
		},
	}

	diff := CompareSchema(expected, actual)
	assert.False(t, diff.Compatible)
	require.Len(t, diff.DimensionMismatches, 1)
	assert.Equal(t, "text", diff.DimensionMismatches[0].Channel)
	assert.Equal(t, 768, diff.DimensionMismatches[0].Expected)
	assert.Equal(t, 512, diff.DimensionMismatches[0].Actual)
}

func TestCompareSchema_DistanceMismatch(t *testing.T) {
	t.Parallel()

	expected := &IndexSchema{
		Version:      "v3",
		PhysicalName: "media_assets_v3",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
		},
	}

	actual := &CollectionInfo{
		VectorConfigs: map[string]VectorConfig{
			"text": {Size: 768, Distance: "Euclid"},
		},
	}

	diff := CompareSchema(expected, actual)
	assert.False(t, diff.Compatible)
	require.Len(t, diff.DistanceMismatches, 1)
	assert.Equal(t, "Cosine", diff.DistanceMismatches[0].Expected)
	assert.Equal(t, "Euclid", diff.DistanceMismatches[0].Actual)
}

func TestCompareSchema_MissingVector(t *testing.T) {
	t.Parallel()

	expected := &IndexSchema{
		Version:      "v3",
		PhysicalName: "media_assets_v3",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
			{Channel: "visual", Dimensions: 768, Distance: "Cosine"},
		},
	}

	actual := &CollectionInfo{
		VectorConfigs: map[string]VectorConfig{
			"text": {Size: 768, Distance: "Cosine"},
		},
	}

	diff := CompareSchema(expected, actual)
	assert.False(t, diff.Compatible)
	assert.Contains(t, diff.MissingVectors, "visual")
}

func TestCompareSchema_ExtraVector(t *testing.T) {
	t.Parallel()

	expected := &IndexSchema{
		Version:      "v3",
		PhysicalName: "media_assets_v3",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
		},
	}

	actual := &CollectionInfo{
		VectorConfigs: map[string]VectorConfig{
			"text":   {Size: 768, Distance: "Cosine"},
			"visual": {Size: 512, Distance: "Cosine"},
		},
	}

	diff := CompareSchema(expected, actual)
	// Extra vectors alone don't make the schema incompatible.
	assert.True(t, diff.Compatible)
	assert.Contains(t, diff.ExtraVectors, "visual")
}

func TestCompareSchema_MissingPayloadIndex(t *testing.T) {
	t.Parallel()

	expected := &IndexSchema{
		Version:      "v3",
		PhysicalName: "media_assets_v3",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
		},
		PayloadIndexes: []PayloadIndexSpec{
			{FieldName: "source", FieldType: "keyword"},
			{FieldName: "status", FieldType: "keyword"},
		},
	}

	actual := &CollectionInfo{
		VectorConfigs: map[string]VectorConfig{
			"text": {Size: 768, Distance: "Cosine"},
		},
		PayloadIndexes: []PayloadIndexInfo{
			{FieldName: "source", FieldType: "keyword"},
		},
	}

	diff := CompareSchema(expected, actual)
	assert.False(t, diff.Compatible)
	assert.Contains(t, diff.MissingIndexes, "status")
}

func TestCompareSchema_NoVectorConfigs(t *testing.T) {
	t.Parallel()

	expected := &IndexSchema{
		Version:      "v3",
		PhysicalName: "media_assets_v3",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
		},
	}

	actual := &CollectionInfo{
		VectorConfigs: nil,
	}

	diff := CompareSchema(expected, actual)
	assert.False(t, diff.Compatible)
	assert.Contains(t, diff.MissingVectors, "text")
}

func TestCompareSchema_SparseVectorExpected(t *testing.T) {
	t.Parallel()

	expected := &IndexSchema{
		Version:      "v3",
		PhysicalName: "media_assets_v3",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
		},
		SparseVectors: []SparseSpec{
			{Channel: "bm25_text", Modifier: "bm25"},
		},
	}

	actual := &CollectionInfo{
		VectorConfigs: map[string]VectorConfig{
			"text": {Size: 768, Distance: "Cosine"},
		},
	}

	diff := CompareSchema(expected, actual)
	assert.False(t, diff.Compatible)
	// Sparse vectors are expected but missing — Dimensions=-1 comparison handled.
	assert.Contains(t, diff.MissingVectors, "bm25_text")
}

// ── Alias switch tests ───────────────────────────────────────────────

func TestCollectionManager_SwitchAlias(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var aliasActions []map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if r.Method == http.MethodPost && r.URL.Path == "/collections/aliases" {
			var body map[string]interface{}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			actions, _ := body["actions"].([]interface{})
			for _, a := range actions {
				aliasActions = append(aliasActions, a.(map[string]interface{}))
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": true,
				"status": "ok",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	schema := DefaultV3Schema()
	client := NewClient(&Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	cm := NewCollectionManager(client, schema, zap.NewNop())

	err := cm.SwitchAlias(context.Background(), "media_assets_v3_old", "media_assets_v3_new")
	require.NoError(t, err)

	require.Len(t, aliasActions, 2)

	// First action: delete old alias.
	deleteAction := aliasActions[0]
	deleteAlias, ok := deleteAction["delete_alias"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "media_assets_current", deleteAlias["alias_name"])

	// Second action: create new alias.
	createAction := aliasActions[1]
	createAlias, ok := createAction["create_alias"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "media_assets_current", createAlias["alias_name"])
	assert.Equal(t, "media_assets_v3_new", createAlias["collection_name"])
}

func TestCollectionManager_RollbackAlias(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var aliasActions []map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if r.Method == http.MethodPost && r.URL.Path == "/collections/aliases" {
			var body map[string]interface{}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			actions, _ := body["actions"].([]interface{})
			for _, a := range actions {
				aliasActions = append(aliasActions, a.(map[string]interface{}))
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": true,
				"status": "ok",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	schema := DefaultV3Schema()
	client := NewClient(&Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	cm := NewCollectionManager(client, schema, zap.NewNop())

	err := cm.RollbackAlias(context.Background(), "media_assets_v3_broken", "media_assets_v3_previous")
	require.NoError(t, err)

	require.Len(t, aliasActions, 2)
	assert.Equal(t, "media_assets_v3_previous", aliasActions[1]["create_alias"].(map[string]interface{})["collection_name"])
}

func TestCollectionManager_EnsureSchema_CreatesNew(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	collectionCreated := false
	aliasCreated := false
	payloadIndexes := make(map[string]bool)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		// Alias target check — no alias exists initially.
		case r.Method == http.MethodGet && r.URL.Path == "/collections/media_assets_current/aliases":
			http.NotFound(w, r)
		// Physical collection check — doesn't exist.
		case r.Method == http.MethodGet && r.URL.Path == "/collections/media_assets_v3_e5_768_siglip_768":
			http.NotFound(w, r)
		// Create collection.
		case r.Method == http.MethodPut && r.URL.Path == "/collections/media_assets_v3_e5_768_siglip_768":
			collectionCreated = true
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": true,
				"status": "ok",
			})
		// Create payload indexes.
		case r.Method == http.MethodPut && r.URL.Path == "/collections/media_assets_v3_e5_768_siglip_768/index":
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

	schema := DefaultV3Schema()
	client := NewClient(&Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	cm := NewCollectionManager(client, schema, zap.NewNop())

	result, err := cm.EnsureSchema(context.Background())
	require.NoError(t, err)
	assert.True(t, result.Created)
	assert.True(t, result.Compatible)
	assert.True(t, collectionCreated)
	assert.True(t, aliasCreated)
	assert.NotEmpty(t, payloadIndexes)
	// Verify at least some expected indexes were created.
	for _, idx := range schema.PayloadIndexes {
		assert.True(t, payloadIndexes[idx.FieldName], "missing payload index %q", idx.FieldName)
	}
}

func TestCollectionManager_EnsureSchema_AlreadyCompatible(t *testing.T) {
	t.Parallel()

	// Use a custom schema without payload indexes so we don't need to mock all 16.
	schema := &IndexSchema{
		Version:      "v3",
		PhysicalName: "media_assets_v3_e5_768",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
			{Channel: "visual", Dimensions: 768, Distance: "Cosine"},
		},
	}
	require.NoError(t, schema.Validate())

	var mu sync.Mutex
	createCalls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		// Alias target check — returns the physical collection.
		case r.Method == http.MethodGet && r.URL.Path == "/collections/media_assets_current/aliases":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": []map[string]interface{}{
					{
						"alias_name":      "media_assets_current",
						"collection_name": "media_assets_v3_e5_768",
					},
				},
			})
		// Physical collection exists and is compatible.
		case r.Method == http.MethodGet && r.URL.Path == "/collections/media_assets_v3_e5_768":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"status": "green",
					"config": map[string]interface{}{
						"text":   map[string]interface{}{"size": float64(768), "distance": "Cosine"},
						"visual": map[string]interface{}{"size": float64(768), "distance": "Cosine"},
					},
				},
			})
		// PUT collection — should NOT be called.
		case r.Method == http.MethodPut && r.URL.Path == "/collections/media_assets_v3_e5_768":
			createCalls++
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := NewClient(&Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	cm := NewCollectionManager(client, schema, zap.NewNop())

	result, err := cm.EnsureSchema(context.Background())
	require.NoError(t, err)
	assert.True(t, result.Compatible)
	assert.False(t, result.Created, "should not recreate an already compatible collection")
	assert.Equal(t, 0, createCalls, "PUT should not have been called")
}

// ── Vector dimension rejection tests ─────────────────────────────────

func TestValidatePoint_ValidPoint(t *testing.T) {
	t.Parallel()

	schema := DefaultV3Schema()
	point := &Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text": makeFloat32Slice(768),
		},
	}

	err := ValidatePoint(point, schema)
	require.NoError(t, err)
}

func TestValidatePoint_NilPoint(t *testing.T) {
	t.Parallel()

	schema := DefaultV3Schema()
	err := ValidatePoint(nil, schema)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestValidatePoint_EmptyID(t *testing.T) {
	t.Parallel()

	schema := DefaultV3Schema()
	point := &Point{
		ID: "",
		Vectors: map[string]interface{}{
			"text": makeFloat32Slice(768),
		},
	}

	err := ValidatePoint(point, schema)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ID")
}

func TestValidatePoint_NoVectors(t *testing.T) {
	t.Parallel()

	schema := DefaultV3Schema()
	point := &Point{
		ID:      "asset-1",
		Vectors: map[string]interface{}{},
	}

	err := ValidatePoint(point, schema)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one vector")
}

func TestValidatePoint_WrongType(t *testing.T) {
	t.Parallel()

	schema := DefaultV3Schema()
	point := &Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text": "not-a-vector",
		},
	}

	err := ValidatePoint(point, schema)
	require.Error(t, err)
	var dimErr *ErrVectorDimensionMismatch
	require.ErrorAs(t, err, &dimErr)
	assert.Equal(t, "text", dimErr.Channel)
	assert.Equal(t, 0, dimErr.Actual)
}

func TestValidatePoint_EmptyVector(t *testing.T) {
	t.Parallel()

	schema := DefaultV3Schema()
	point := &Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text": []float32{},
		},
	}

	err := ValidatePoint(point, schema)
	require.Error(t, err)
	var emptyErr *ErrEmptyVector
	require.ErrorAs(t, err, &emptyErr)
	assert.Equal(t, "text", emptyErr.Channel)
	assert.Equal(t, "asset-1", emptyErr.AssetID)
}

func TestValidatePoint_DimensionMismatch(t *testing.T) {
	t.Parallel()

	schema := DefaultV3Schema()
	point := &Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text": makeFloat32Slice(512), // Expected 768.
		},
	}

	err := ValidatePoint(point, schema)
	require.Error(t, err)
	var dimErr *ErrVectorDimensionMismatch
	require.ErrorAs(t, err, &dimErr)
	assert.Equal(t, "text", dimErr.Channel)
	assert.Equal(t, 768, dimErr.Expected)
	assert.Equal(t, 512, dimErr.Actual)
}

func TestValidatePoint_VisualDimensionMismatch(t *testing.T) {
	t.Parallel()

	schema := DefaultV3Schema()
	point := &Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text":   makeFloat32Slice(768),
			"visual": makeFloat32Slice(256), // Expected 768.
		},
	}

	err := ValidatePoint(point, schema)
	require.Error(t, err)
	var dimErr *ErrVectorDimensionMismatch
	require.ErrorAs(t, err, &dimErr)
	assert.Equal(t, "visual", dimErr.Channel)
	assert.Equal(t, 768, dimErr.Expected)
	assert.Equal(t, 256, dimErr.Actual)
}

func TestValidatePoint_AudioDimensionMismatch(t *testing.T) {
	t.Parallel()

	schema := DefaultV3Schema()
	point := &Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text":  makeFloat32Slice(768),
			"audio": makeFloat32Slice(256), // Expected 512.
		},
	}

	err := ValidatePoint(point, schema)
	require.Error(t, err)
	var dimErr *ErrVectorDimensionMismatch
	require.ErrorAs(t, err, &dimErr)
	assert.Equal(t, "audio", dimErr.Channel)
	assert.Equal(t, 512, dimErr.Expected)
	assert.Equal(t, 256, dimErr.Actual)
}

func TestValidatePoint_OptionalChannel(t *testing.T) {
	t.Parallel()

	// audio is part of the schema but optional — not present should be fine.
	schema := DefaultV3Schema()
	point := &Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text": makeFloat32Slice(768),
		},
	}

	err := ValidatePoint(point, schema)
	require.NoError(t, err)
}

func TestValidatePoint_MultipleValidChannels(t *testing.T) {
	t.Parallel()

	schema := DefaultV3Schema()
	point := &Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text":       makeFloat32Slice(768),
			"transcript": makeFloat32Slice(768),
			"visual":     makeFloat32Slice(768),
			"audio":      makeFloat32Slice(512),
		},
	}

	err := ValidatePoint(point, schema)
	require.NoError(t, err)
}

// ── NaN/Inf rejection tests ──────────────────────────────────────────

func TestValidatePoint_NaNDetected(t *testing.T) {
	t.Parallel()

	schema := DefaultV3Schema()
	vec := makeFloat32Slice(768)
	vec[100] = float32(math.NaN())

	point := &Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text": vec,
		},
	}

	err := ValidatePoint(point, schema)
	require.Error(t, err)
	var nanErr *ErrNaNOrInf
	require.ErrorAs(t, err, &nanErr)
	assert.Equal(t, "text", nanErr.Channel)
	assert.Equal(t, "asset-1", nanErr.AssetID)
}

func TestValidatePoint_PositiveInfDetected(t *testing.T) {
	t.Parallel()

	schema := DefaultV3Schema()
	vec := makeFloat32Slice(768)
	vec[0] = float32(math.Inf(1))

	point := &Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text": vec,
		},
	}

	err := ValidatePoint(point, schema)
	require.Error(t, err)
	var nanErr *ErrNaNOrInf
	require.ErrorAs(t, err, &nanErr)
}

func TestValidatePoint_NegativeInfDetected(t *testing.T) {
	t.Parallel()

	schema := DefaultV3Schema()
	vec := makeFloat32Slice(768)
	vec[0] = float32(math.Inf(-1))

	point := &Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text": vec,
		},
	}

	err := ValidatePoint(point, schema)
	require.Error(t, err)
	var nanErr *ErrNaNOrInf
	require.ErrorAs(t, err, &nanErr)
}

func TestValidatePoint_NoNaNorInf(t *testing.T) {
	t.Parallel()

	schema := DefaultV3Schema()
	vec := makeFloat32Slice(768)
	vec[0] = 0.0
	vec[1] = 1.0
	vec[2] = -1.0
	vec[3] = 3.4028235e+38  // max float32
	vec[4] = -3.4028235e+38 // min float32

	point := &Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text": vec,
		},
	}

	err := ValidatePoint(point, schema)
	require.NoError(t, err)
}

func TestValidatePoint_NaNInVisualChannel(t *testing.T) {
	t.Parallel()

	schema := DefaultV3Schema()
	textVec := makeFloat32Slice(768)
	visualVec := makeFloat32Slice(768)
	visualVec[50] = float32(math.NaN())

	point := &Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text":   textVec,
			"visual": visualVec,
		},
	}

	err := ValidatePoint(point, schema)
	require.Error(t, err)
	var nanErr *ErrNaNOrInf
	require.ErrorAs(t, err, &nanErr)
	assert.Equal(t, "visual", nanErr.Channel)
}

// ── Error helpers tests ──────────────────────────────────────────────

func TestIsRetryable_PermanentErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{"SchemaIncompatible", &ErrSchemaIncompatible{Diff: &SchemaDiff{Compatible: false}}},
		{"DimensionMismatch", &ErrVectorDimensionMismatch{Channel: "text", Expected: 768, Actual: 512}},
		{"NaNOrInf", &ErrNaNOrInf{Channel: "text", AssetID: "a1"}},
		{"EmptyVector", &ErrEmptyVector{Channel: "text", AssetID: "a1"}},
		{"ChannelUnavailable", &ErrChannelUnavailable{Channel: "audio"}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.False(t, IsRetryable(tt.err), "%T should NOT be retryable", tt.err)
		})
	}
}

func TestIsRetryable_RetryableErrors(t *testing.T) {
	t.Parallel()

	errs := []error{
		&ErrCollectionNotFound{Name: "foo"},
		&ErrAliasNotFound{Alias: "bar"},
		&ErrAliasSwitchNotReady{Report: &SwitchReport{}},
	}

	for _, err := range errs {
		assert.True(t, IsRetryable(err), "%T should be retryable", err)
	}

	// nil is not retryable.
	assert.False(t, IsRetryable(nil))
}

func TestErrorMessages(t *testing.T) {
	t.Parallel()

	assert.Contains(t, (&ErrSchemaIncompatible{Diff: &SchemaDiff{}}).Error(), "schema incompatible")
	assert.Contains(t, (&ErrCollectionNotFound{Name: "c"}).Error(), "c")
	assert.Contains(t, (&ErrAliasNotFound{Alias: "a"}).Error(), "a")
	assert.Contains(t, (&ErrVectorDimensionMismatch{Channel: "x", Expected: 10, Actual: 5, AssetID: "id"}).Error(), "x")
	assert.Contains(t, (&ErrNaNOrInf{Channel: "x", AssetID: "id"}).Error(), "NaN")
	assert.Contains(t, (&ErrEmptyVector{Channel: "x", AssetID: "id"}).Error(), "empty")
	assert.Contains(t, (&ErrChannelUnavailable{Channel: "x"}).Error(), "unavailable")
	assert.Contains(t, (&ErrAliasSwitchNotReady{}).Error(), "not ready")
}

// ── IndexSchema helpers tests ────────────────────────────────────────

func TestIndexSchema_HasChannel(t *testing.T) {
	t.Parallel()

	s := DefaultV3Schema()

	assert.True(t, s.HasChannel("text"))
	assert.True(t, s.HasChannel("visual"))
	assert.True(t, s.HasChannel("bm25_text"))
	assert.False(t, s.HasChannel("nonexistent"))
}

func TestIndexSchema_GetDense(t *testing.T) {
	t.Parallel()

	s := DefaultV3Schema()

	spec := s.GetDense("text")
	require.NotNil(t, spec)
	assert.Equal(t, "text", spec.Channel)
	assert.Equal(t, 768, spec.Dimensions)
	assert.Equal(t, "multilingual-e5-base", spec.Model)

	spec = s.GetDense("visual")
	require.NotNil(t, spec)
	assert.Equal(t, 768, spec.Dimensions)
	assert.Equal(t, "siglip-so400m-patch14-384", spec.Model)

	assert.Nil(t, s.GetDense("bm25_text"), "bm25_text is sparse, not dense")
	assert.Nil(t, s.GetDense("nonexistent"))
}

func TestIndexSchema_PhysicalName(t *testing.T) {
	t.Parallel()

	s := DefaultV3Schema()
	assert.Equal(t, "media_assets_v3_e5_768_siglip_768", s.physicalName())

	// When PhysicalName is empty, derive from version.
	s2 := &IndexSchema{Version: "v4"}
	assert.Equal(t, "media_assets_v4", s2.physicalName())
}

// ── Schema valid-distance and valid-field-type ───────────────────────

func TestIsValidDistance(t *testing.T) {
	t.Parallel()

	assert.True(t, isValidDistance("Cosine"))
	assert.True(t, isValidDistance("Euclid"))
	assert.True(t, isValidDistance("Dot"))
	assert.False(t, isValidDistance(""))
	assert.False(t, isValidDistance("Manhattan"))
	assert.False(t, isValidDistance("cosine"))
}

func TestIsValidFieldType(t *testing.T) {
	t.Parallel()

	for _, ft := range []string{"keyword", "integer", "float", "datetime", "geo", "text", "bool"} {
		assert.True(t, isValidFieldType(ft), "expected %q to be valid", ft)
	}
	assert.False(t, isValidFieldType(""))
	assert.False(t, isValidFieldType("binary"))
	assert.False(t, isValidFieldType("uuid"))
}

// ── Helpers ──────────────────────────────────────────────────────────

// makeFloat32Slice creates a []float32 of the given size, filled with 1.0.
func makeFloat32Slice(size int) []float32 {
	v := make([]float32, size)
	for i := range v {
		v[i] = 1.0
	}
	return v
}

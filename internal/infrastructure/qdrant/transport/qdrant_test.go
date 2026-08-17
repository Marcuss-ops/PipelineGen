package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	qdrantSchema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── Manifest validation tests ────────────────────────────────────────

func TestDefaultV3Schema_Validate(t *testing.T) {
	t.Parallel()

	schema := qdrantSchema.DefaultV3Schema()
	require.NotNil(t, schema)
	require.NoError(t, schema.Validate())
}

func TestSchemaValidate_Nil(t *testing.T) {
	t.Parallel()

	var s *qdrantSchema.IndexSchema
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestSchemaValidate_EmptyVersion(t *testing.T) {
	t.Parallel()

	s := &qdrantSchema.IndexSchema{
		Version:      "",
		RuntimeAlias: "alias",
	}
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version")
}

func TestSchemaValidate_EmptyRuntimeAlias(t *testing.T) {
	t.Parallel()

	s := &qdrantSchema.IndexSchema{
		Version:      "v1",
		RuntimeAlias: "",
	}
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "alias")
}

func TestSchemaValidate_PhysicalNameEqualsAlias(t *testing.T) {
	t.Parallel()

	s := &qdrantSchema.IndexSchema{
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

	s := &qdrantSchema.IndexSchema{
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

	s := &qdrantSchema.IndexSchema{
		Version:      "v1",
		PhysicalName: "media_assets_v1",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []qdrantSchema.EmbeddingSpec{
			{Channel: "", Dimensions: 768, Distance: "Cosine"},
		},
	}
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "channel")
}

func TestSchemaValidate_DuplicateChannel(t *testing.T) {
	t.Parallel()

	s := &qdrantSchema.IndexSchema{
		Version:      "v1",
		PhysicalName: "media_assets_v1",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []qdrantSchema.EmbeddingSpec{
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

	s := &qdrantSchema.IndexSchema{
		Version:      "v1",
		PhysicalName: "media_assets_v1",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []qdrantSchema.EmbeddingSpec{
			{Channel: "text", Dimensions: 0, Distance: "Cosine"},
		},
	}
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dimensions")
}

func TestSchemaValidate_InvalidDistance(t *testing.T) {
	t.Parallel()

	s := &qdrantSchema.IndexSchema{
		Version:      "v1",
		PhysicalName: "media_assets_v1",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []qdrantSchema.EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Manhattan"},
		},
	}
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "distance")
}

func TestSchemaValidate_EmptySparseChannel(t *testing.T) {
	t.Parallel()

	s := &qdrantSchema.IndexSchema{
		Version:      "v1",
		PhysicalName: "media_assets_v1",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []qdrantSchema.EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
		},
		SparseVectors: []qdrantSchema.SparseSpec{
			{Channel: "", Modifier: "idf"},
		},
	}
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sparse")
}

func TestSchemaValidate_DuplicateSparseChannel(t *testing.T) {
	t.Parallel()

	s := &qdrantSchema.IndexSchema{
		Version:      "v1",
		PhysicalName: "media_assets_v1",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []qdrantSchema.EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
		},
		SparseVectors: []qdrantSchema.SparseSpec{
			{Channel: "bm25_text", Modifier: "idf"},
		},
	}
	s.DenseVectors = append(s.DenseVectors, qdrantSchema.EmbeddingSpec{Channel: "bm25_text", Dimensions: 768, Distance: "Cosine"})
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestSchemaValidate_BadPayloadIndexField(t *testing.T) {
	t.Parallel()

	s := &qdrantSchema.IndexSchema{
		Version:      "v1",
		PhysicalName: "media_assets_v1",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []qdrantSchema.EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
		},
		PayloadIndexes: []qdrantSchema.PayloadIndexSpec{
			{FieldName: "", FieldType: "keyword"},
		},
	}
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "field_name")
}

func TestSchemaValidate_BadPayloadIndexType(t *testing.T) {
	t.Parallel()

	s := &qdrantSchema.IndexSchema{
		Version:      "v1",
		PhysicalName: "media_assets_v1",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []qdrantSchema.EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
		},
		PayloadIndexes: []qdrantSchema.PayloadIndexSpec{
			{FieldName: "lifecycle_state", FieldType: "binary"},
		},
	}
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "field_type")
}

// ── Schema comparison tests ──────────────────────────────────────────

func TestCompareSchema_FullyCompatible(t *testing.T) {
	t.Parallel()

	expected := &qdrantSchema.IndexSchema{
		Version:      "v3",
		PhysicalName: "media_assets_v3",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []qdrantSchema.EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
			{Channel: "visual", Dimensions: 768, Distance: "Cosine"},
		},
		PayloadIndexes: []qdrantSchema.PayloadIndexSpec{
			{FieldName: "source", FieldType: "keyword"},
			{FieldName: "lifecycle_state", FieldType: "keyword"},
		},
	}

	actual := &qdrantSchema.CollectionInfo{
		VectorConfigs: map[string]qdrantSchema.VectorConfig{
			"text":   {Size: 768, Distance: "Cosine"},
			"visual": {Size: 768, Distance: "Cosine"},
		},
		PayloadIndexes: []qdrantSchema.PayloadIndexInfo{
			{FieldName: "source", FieldType: "keyword"},
			{FieldName: "lifecycle_state", FieldType: "keyword"},
		},
	}

	diff := qdrantSchema.CompareSchema(expected, actual)
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

	expected := &qdrantSchema.IndexSchema{
		Version:      "v3",
		PhysicalName: "media_assets_v3",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []qdrantSchema.EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
		},
	}

	actual := &qdrantSchema.CollectionInfo{
		VectorConfigs: map[string]qdrantSchema.VectorConfig{
			"text": {Size: 512, Distance: "Cosine"},
		},
	}

	diff := qdrantSchema.CompareSchema(expected, actual)
	assert.False(t, diff.Compatible)
	require.Len(t, diff.DimensionMismatches, 1)
	assert.Equal(t, "text", diff.DimensionMismatches[0].Channel)
	assert.Equal(t, 768, diff.DimensionMismatches[0].Expected)
	assert.Equal(t, 512, diff.DimensionMismatches[0].Actual)
}

func TestCompareSchema_DistanceMismatch(t *testing.T) {
	t.Parallel()

	expected := &qdrantSchema.IndexSchema{
		Version:      "v3",
		PhysicalName: "media_assets_v3",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []qdrantSchema.EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
		},
	}

	actual := &qdrantSchema.CollectionInfo{
		VectorConfigs: map[string]qdrantSchema.VectorConfig{
			"text": {Size: 768, Distance: "Euclid"},
		},
	}

	diff := qdrantSchema.CompareSchema(expected, actual)
	assert.False(t, diff.Compatible)
	require.Len(t, diff.DistanceMismatches, 1)
	assert.Equal(t, "Cosine", diff.DistanceMismatches[0].Expected)
	assert.Equal(t, "Euclid", diff.DistanceMismatches[0].Actual)
}

func TestCompareSchema_MissingVector(t *testing.T) {
	t.Parallel()

	expected := &qdrantSchema.IndexSchema{
		Version:      "v3",
		PhysicalName: "media_assets_v3",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []qdrantSchema.EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
			{Channel: "visual", Dimensions: 768, Distance: "Cosine"},
		},
	}

	actual := &qdrantSchema.CollectionInfo{
		VectorConfigs: map[string]qdrantSchema.VectorConfig{
			"text": {Size: 768, Distance: "Cosine"},
		},
	}

	diff := qdrantSchema.CompareSchema(expected, actual)
	assert.False(t, diff.Compatible)
	assert.Contains(t, diff.MissingVectors, "visual")
}

func TestCompareSchema_ExtraVector(t *testing.T) {
	t.Parallel()

	expected := &qdrantSchema.IndexSchema{
		Version:      "v3",
		PhysicalName: "media_assets_v3",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []qdrantSchema.EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
		},
	}

	actual := &qdrantSchema.CollectionInfo{
		VectorConfigs: map[string]qdrantSchema.VectorConfig{
			"text":   {Size: 768, Distance: "Cosine"},
			"visual": {Size: 512, Distance: "Cosine"},
		},
	}

	diff := qdrantSchema.CompareSchema(expected, actual)
	// Extra vectors alone don't make the schema incompatible.
	assert.True(t, diff.Compatible)
	assert.Contains(t, diff.ExtraVectors, "visual")
}

func TestCompareSchema_MissingPayloadIndex(t *testing.T) {
	t.Parallel()

	expected := &qdrantSchema.IndexSchema{
		Version:      "v3",
		PhysicalName: "media_assets_v3",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []qdrantSchema.EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
		},
		PayloadIndexes: []qdrantSchema.PayloadIndexSpec{
			{FieldName: "source", FieldType: "keyword"},
			{FieldName: "lifecycle_state", FieldType: "keyword"},
		},
	}

	actual := &qdrantSchema.CollectionInfo{
		VectorConfigs: map[string]qdrantSchema.VectorConfig{
			"text": {Size: 768, Distance: "Cosine"},
		},
		PayloadIndexes: []qdrantSchema.PayloadIndexInfo{
			{FieldName: "source", FieldType: "keyword"},
		},
	}

	diff := qdrantSchema.CompareSchema(expected, actual)
	assert.False(t, diff.Compatible)
	assert.Contains(t, diff.MissingIndexes, "lifecycle_state")
}

func TestCompareSchema_NoVectorConfigs(t *testing.T) {
	t.Parallel()

	expected := &qdrantSchema.IndexSchema{
		Version:      "v3",
		PhysicalName: "media_assets_v3",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []qdrantSchema.EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
		},
	}

	actual := &qdrantSchema.CollectionInfo{
		VectorConfigs: nil,
	}

	diff := qdrantSchema.CompareSchema(expected, actual)
	assert.False(t, diff.Compatible)
	assert.Contains(t, diff.MissingVectors, "text")
}

func TestCompareSchema_SparseVectorExpected(t *testing.T) {
	t.Parallel()

	expected := &qdrantSchema.IndexSchema{
		Version:      "v3",
		PhysicalName: "media_assets_v3",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []qdrantSchema.EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
		},
		SparseVectors: []qdrantSchema.SparseSpec{
			{Channel: "bm25_text", Modifier: "idf"},
		},
	}

	actual := &qdrantSchema.CollectionInfo{
		VectorConfigs: map[string]qdrantSchema.VectorConfig{
			"text": {Size: 768, Distance: "Cosine"},
		},
	}

	diff := qdrantSchema.CompareSchema(expected, actual)
	assert.False(t, diff.Compatible)
	// Sparse vectors are expected but missing — Dimensions=-1 comparison handled.
	assert.Contains(t, diff.MissingVectors, "bm25_text")
}

// ── Snapshot idempotency tests ────────────────────────────────────────

// TestCreateSnapshot_Idempotency locks in the QDRANT-005C PR3 invariant
// documented in client_dr.go::CreateSnapshot godoc: "Qdrant may return
// the same Name on repeated POSTs of the same collection." The test
// mocks a controlled Qdrant server that returns the SAME snapshot Name
// on every POST, and asserts that two consecutive client.CreateSnapshot
// calls against the same collection return the same Name.
//
// Why this matters: the dr package treats the snapshot Name as the
// canonical handle for subsequent List/Restore operations (see
// dr.RestoreService + client_dr.go::GetSnapshotURL). A future Qdrant
// server (or refactor of qdrant.Client.CreateSnapshot) that returns
// different Names on repeated POSTs would silently break the
// verify-then-switch contract — restore would resolve to a stale or
// missing URL. This test fails CI loudly if the invariant regresses.
func TestCreateSnapshot_Idempotency(t *testing.T) {
	t.Parallel()

	const collection = "test-collection-snapshot-idempotency"
	const expectedName = "snapshot-2026-06-27-stable-name"

	var mu sync.Mutex
	var postCalls int

	// Mock Qdrant that ALWAYS returns the canonical Name on POST, regardless
	// of how many calls arrive — this is the idempotency contract we are
	// pinning. We also count POST calls so a future client-side dedupe
	// (which would be equivalent but a different surface) does not silently
	// make this test pass on one round-trip.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if r.Method == http.MethodPost && r.URL.Path == "/collections/"+collection+"/snapshots" {
			postCalls++
			w.Header().Set("Content-Type", "application/json")
			// Mock deliberately omits CreationTime: real Qdrant UPDATES the
			// snapshot's CreationTime on every idempotent re-POST (the
			// snapshot is fresh, only the Name is preserved by the server).
			// Excluding it here prevents a future maintainer from being
			// tempted to assert snap1.CreationTime.Equal(snap2.CreationTime),
			// which would lock in a wrong expectation against real Qdrant.
			// Size is included to keep the wire shape realistic.
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": qdrantSchema.SnapshotDescription{
					Name:     expectedName,
					Size:     4096,
					Checksum: "stable-checksum-1",
				},
				"status": "ok",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	client := NewClient(&qdrantSchema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	ctx := context.Background()

	// First call — captures the canonical Name.
	snap1, err := client.CreateSnapshot(ctx, collection)
	require.NoError(t, err)
	require.NotNil(t, snap1)

	// Second call — Qdrant returns the SAME Name for the same collection per
	// QDRANT-005C PR3 invariant. This is the canonical idempotency signal:
	// the create-snapshot operation is repeatable and stable across POSTs.
	snap2, err := client.CreateSnapshot(ctx, collection)
	require.NoError(t, err)
	require.NotNil(t, snap2)

	// Primary invariant (QDRANT-005C PR3, client_dr.go::CreateSnapshot):
	// both calls return the same Name.
	assert.Equal(t, expectedName, snap1.Name,
		"CreateSnapshot must return the canonical Name on first call (QDRANT-005C PR3 invariant)")
	assert.Equal(t, expectedName, snap2.Name,
		"CreateSnapshot idempotency: second POST must return the SAME Name as first "+
			"(QDRANT-005C PR3 invariant — see client_dr.go::CreateSnapshot doc-comment)")
	assert.Equal(t, snap1.Name, snap2.Name,
		"CreateSnapshot Name equality across two POSTs is the canonical idempotency signal")

	// Defense-in-depth: confirm we actually triggered two POSTs. If
	// fewer than 2 POSTs hit the wire, the Name-equality assertion above is
	// vacuous (a future client-side cache of the snapshot response would
	// make Name-equal-by-construction). This guard fires LOUDLY to keep the
	// test surface at the wire level — exactly two independent round-trips
	// to the controlled server.
	assert.Equal(t, 2, postCalls,
		"test server should have received exactly 2 POST calls — fewer means "+
			"the Name-equality assertion above passed vacuously and is meaningless")
}

// ── Error helpers tests ──────────────────────────────────────────────

func TestIsRetryable_PermanentErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{"SchemaIncompatible", &ErrSchemaIncompatible{Diff: &qdrantSchema.SchemaDiff{Compatible: false}}},
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
		&ErrAliasSwitchNotReady{Report: &qdrantSchema.SwitchReport{}},
	}

	for _, err := range errs {
		assert.True(t, IsRetryable(err), "%T should be retryable", err)
	}

	// nil is not retryable.
	assert.False(t, IsRetryable(nil))
}

// TestIsRetryable_HTTPStatusClasses locks the Blocco 4d retryability
// contract across all Qdrant HTTP status classes and typed error surfaces.
// Each case represents a real Qdrant response path: APIError carries the
// wire-level status + Retryable hint from classifyRetryability; sentinel
// errors carry semantic wrappers from the client layer.
//
// P7 RETRY-INTEGRATION-TEST (July 2026): Blocco 4d changed the default
// for unknown errors from retryable → terminal. This test nails it down.
func TestIsRetryable_HTTPStatusClasses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		// ── Wire-level HTTP classes via classifyRetryability ──────
		{
			name:      "network timeout (Status=0)",
			err:       &APIError{Operation: "SearchPoints", Status: 0, Message: "dial tcp: i/o timeout", Retryable: true},
			retryable: true,
		},
		{
			name:      "HTTP 429 Too Many Requests",
			err:       &APIError{Operation: "UpsertPoints", Status: 429, Message: "too many requests", Retryable: true},
			retryable: true,
		},
		{
			name:      "HTTP 503 Service Unavailable",
			err:       &APIError{Operation: "DeletePoints", Status: 503, Message: "service unavailable", Retryable: true},
			retryable: true,
		},
		{
			name:      "HTTP 408 Request Timeout",
			err:       &APIError{Operation: "ScrollPoints", Status: 408, Message: "request timeout", Retryable: true},
			retryable: true,
		},
		{
			name:      "HTTP 502 Bad Gateway",
			err:       &APIError{Operation: "GetCollection", Status: 502, Message: "bad gateway", Retryable: true},
			retryable: true,
		},
		{
			name:      "HTTP 400 Bad Request (client error → terminal)",
			err:       &APIError{Operation: "SearchPoints", Status: 400, Message: "bad request: malformed filter", Retryable: false},
			retryable: false,
		},
		{
			name:      "HTTP 404 raw APIError (wire-level 404 is not retryable)",
			err:       &APIError{Operation: "GetCollection", Status: 404, Message: "collection not found", Retryable: false},
			retryable: false,
		},

		// ── Sentinel wrappers for 404 → semantic retryable ────────
		{
			name:      "ErrCollectionNotFound (404 wrapped → retryable)",
			err:       &ErrCollectionNotFound{Name: "media_assets_v3"},
			retryable: true,
		},

		// ── Permanent typed errors (never retry) ──────────────────
		{
			name:      "ErrVectorDimensionMismatch",
			err:       &ErrVectorDimensionMismatch{Channel: "text", Expected: 768, Actual: 512, AssetID: "a1"},
			retryable: false,
		},
		{
			name:      "ErrNaNOrInf",
			err:       &ErrNaNOrInf{Channel: "visual", AssetID: "a2"},
			retryable: false,
		},

		// ── Blocco 4d lock: unknown errors default to terminal ────
		{
			name:      "unknown plain error (Blocco 4d default → terminal)",
			err:       &errPlainTest{msg: "unknown error"}, // Blocco 4d: non-transient unknown error → terminal
			retryable: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := IsRetryable(tt.err)
			if tt.retryable {
				assert.True(t, got, "%s: %T should be retryable", tt.name, tt.err)
			} else {
				assert.False(t, got, "%s: %T should NOT be retryable", tt.name, tt.err)
			}
		})
	}
}

// errPlainTest is a non-Qdrant, non-pkg/retry error type used to verify
// Blocco 4d's unknown→terminal default. It does NOT implement any
// RetryableError or IsRetryable interface, so IsRetryable must classify
// it as terminal.
type errPlainTest struct{ msg string }

func (e *errPlainTest) Error() string { return e.msg }

func TestErrorMessages(t *testing.T) {
	t.Parallel()

	assert.Contains(t, (&ErrSchemaIncompatible{Diff: &qdrantSchema.SchemaDiff{}}).Error(), "schema incompatible")
	assert.Contains(t, (&ErrCollectionNotFound{Name: "c"}).Error(), "c")
	assert.Contains(t, (&ErrAliasNotFound{Alias: "a"}).Error(), "a")
	assert.Contains(t, (&ErrVectorDimensionMismatch{Channel: "x", Expected: 10, Actual: 5, AssetID: "id"}).Error(), "x")
	assert.Contains(t, (&ErrNaNOrInf{Channel: "x", AssetID: "id"}).Error(), "NaN")
	assert.Contains(t, (&ErrEmptyVector{Channel: "x", AssetID: "id"}).Error(), "empty")
	assert.Contains(t, (&ErrChannelUnavailable{Channel: "x"}).Error(), "unavailable")
	assert.Contains(t, (&ErrAliasSwitchNotReady{}).Error(), "not ready")
}

// ── qdrantSchema.IndexSchema helpers tests ────────────────────────────────────────

func TestIndexSchema_HasChannel(t *testing.T) {
	t.Parallel()

	s := qdrantSchema.DefaultV3Schema()

	assert.True(t, s.HasChannel("text"))
	assert.True(t, s.HasChannel("visual"))
	assert.True(t, s.HasChannel("bm25_text"))
	assert.False(t, s.HasChannel("nonexistent"))
}

func TestIndexSchema_GetDense(t *testing.T) {
	t.Parallel()

	s := qdrantSchema.DefaultV3Schema()

	spec := s.GetDense("text")
	require.NotNil(t, spec)
	assert.Equal(t, "text", spec.Channel)
	assert.Equal(t, 768, spec.Dimensions)
	assert.Equal(t, "intfloat/multilingual-e5-base", spec.Model)

	spec = s.GetDense("visual")
	require.NotNil(t, spec)
	assert.Equal(t, 768, spec.Dimensions)
	assert.Equal(t, "siglip-so400m-patch14-384", spec.Model)

	assert.Nil(t, s.GetDense("bm25_text"), "bm25_text is sparse, not dense")
	assert.Nil(t, s.GetDense("nonexistent"))
}

func TestIndexSchema_PhysicalName(t *testing.T) {
	t.Parallel()

	s := qdrantSchema.DefaultV3Schema()
	assert.Equal(t, "media_assets_v3_e5_768_siglip_768", s.CanonicalName())

	// When PhysicalName is empty, derive from version.
	s2 := &qdrantSchema.IndexSchema{Version: "v4"}
	assert.Equal(t, "media_assets_v4", s2.CanonicalName())
}

// ── Schema valid-distance and valid-field-type ───────────────────────

func TestIsValidDistance(t *testing.T) {
	t.Parallel()

	assert.True(t, qdrantSchema.IsValidDistance("Cosine"))
	assert.True(t, qdrantSchema.IsValidDistance("Euclid"))
	assert.True(t, qdrantSchema.IsValidDistance("Dot"))
	assert.False(t, qdrantSchema.IsValidDistance(""))
	assert.False(t, qdrantSchema.IsValidDistance("Manhattan"))
	assert.False(t, qdrantSchema.IsValidDistance("cosine"))
}

func TestIsValidFieldType(t *testing.T) {
	t.Parallel()

	for _, ft := range []string{"keyword", "integer", "float", "datetime", "geo", "text", "bool"} {
		assert.True(t, qdrantSchema.IsValidFieldType(ft), "expected %q to be valid", ft)
	}
	assert.False(t, qdrantSchema.IsValidFieldType(""))
	assert.False(t, qdrantSchema.IsValidFieldType("binary"))
	assert.False(t, qdrantSchema.IsValidFieldType("uuid"))
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

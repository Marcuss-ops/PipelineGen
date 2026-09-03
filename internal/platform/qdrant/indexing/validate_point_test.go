package indexing

import (
	"math"
	"testing"

	qdrantSchema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Vector dimension rejection tests ─────────────────────────────────

func TestValidatePoint_ValidPoint(t *testing.T) {
	t.Parallel()

	idxSchema := qdrantSchema.DefaultV3Schema()
	point := &qdrantSchema.Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text": makeFloat32Slice(768),
		},
	}

	err := ValidatePoint(point, idxSchema)
	require.NoError(t, err)
}

func TestValidatePoint_NilPoint(t *testing.T) {
	t.Parallel()

	idxSchema := qdrantSchema.DefaultV3Schema()
	err := ValidatePoint(nil, idxSchema)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestValidatePoint_EmptyID(t *testing.T) {
	t.Parallel()

	idxSchema := qdrantSchema.DefaultV3Schema()
	point := &qdrantSchema.Point{
		ID: "",
		Vectors: map[string]interface{}{
			"text": makeFloat32Slice(768),
		},
	}

	err := ValidatePoint(point, idxSchema)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ID")
}

func TestValidatePoint_NoVectors(t *testing.T) {
	t.Parallel()

	idxSchema := qdrantSchema.DefaultV3Schema()
	point := &qdrantSchema.Point{
		ID:      "asset-1",
		Vectors: map[string]interface{}{},
	}

	err := ValidatePoint(point, idxSchema)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one vector")
}

func TestValidatePoint_WrongType(t *testing.T) {
	t.Parallel()

	idxSchema := qdrantSchema.DefaultV3Schema()
	point := &qdrantSchema.Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text": "not-a-vector",
		},
	}

	err := ValidatePoint(point, idxSchema)
	require.Error(t, err)
	var dimErr *transport.ErrVectorDimensionMismatch
	require.ErrorAs(t, err, &dimErr)
	assert.Equal(t, "text", dimErr.Channel)
	assert.Equal(t, 0, dimErr.Actual)
}

func TestValidatePoint_EmptyVector(t *testing.T) {
	t.Parallel()

	idxSchema := qdrantSchema.DefaultV3Schema()
	point := &qdrantSchema.Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text": []float32{},
		},
	}

	err := ValidatePoint(point, idxSchema)
	require.Error(t, err)
	var emptyErr *transport.ErrEmptyVector
	require.ErrorAs(t, err, &emptyErr)
	assert.Equal(t, "text", emptyErr.Channel)
	assert.Equal(t, "asset-1", emptyErr.AssetID)
}

func TestValidatePoint_DimensionMismatch(t *testing.T) {
	t.Parallel()

	idxSchema := qdrantSchema.DefaultV3Schema()
	point := &qdrantSchema.Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text": makeFloat32Slice(512), // Expected 768.
		},
	}

	err := ValidatePoint(point, idxSchema)
	require.Error(t, err)
	var dimErr *transport.ErrVectorDimensionMismatch
	require.ErrorAs(t, err, &dimErr)
	assert.Equal(t, "text", dimErr.Channel)
	assert.Equal(t, 768, dimErr.Expected)
	assert.Equal(t, 512, dimErr.Actual)
}

func TestValidatePoint_VisualDimensionMismatch(t *testing.T) {
	t.Parallel()

	idxSchema := qdrantSchema.DefaultV3Schema()
	point := &qdrantSchema.Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text":   makeFloat32Slice(768),
			"visual": makeFloat32Slice(256), // Expected 1152.
		},
	}

	err := ValidatePoint(point, idxSchema)
	require.Error(t, err)
	var dimErr *transport.ErrVectorDimensionMismatch
	require.ErrorAs(t, err, &dimErr)
	assert.Equal(t, "visual", dimErr.Channel)
	assert.Equal(t, 1152, dimErr.Expected)
	assert.Equal(t, 256, dimErr.Actual)
}

func TestValidatePoint_AudioDimensionMismatch(t *testing.T) {
	t.Parallel()

	idxSchema := qdrantSchema.DefaultV3Schema()
	idxSchema.DenseVectors = append(idxSchema.DenseVectors, qdrantSchema.EmbeddingSpec{
		Channel:    "audio",
		Dimensions: 512,
		Distance:   "Cosine",
	})
	point := &qdrantSchema.Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text":  makeFloat32Slice(768),
			"audio": makeFloat32Slice(256), // Expected 512.
		},
	}

	err := ValidatePoint(point, idxSchema)
	require.Error(t, err)
	var dimErr *transport.ErrVectorDimensionMismatch
	require.ErrorAs(t, err, &dimErr)
	assert.Equal(t, "audio", dimErr.Channel)
	assert.Equal(t, 512, dimErr.Expected)
	assert.Equal(t, 256, dimErr.Actual)
}

func TestValidatePoint_OptionalChannel(t *testing.T) {
	t.Parallel()

	// audio is part of the schema but optional — not present should be fine.
	idxSchema := qdrantSchema.DefaultV3Schema()
	point := &qdrantSchema.Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text": makeFloat32Slice(768),
		},
	}

	err := ValidatePoint(point, idxSchema)
	require.NoError(t, err)
}

func TestValidatePoint_MultipleValidChannels(t *testing.T) {
	t.Parallel()

	idxSchema := qdrantSchema.DefaultV3Schema()
	point := &qdrantSchema.Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text":       makeFloat32Slice(768),
			"transcript": makeFloat32Slice(768),
			"visual":     makeFloat32Slice(qdrantSchema.VisualEmbeddingDim),
			"audio":      makeFloat32Slice(512),
		},
	}

	err := ValidatePoint(point, idxSchema)
	require.NoError(t, err)
}

// ── NaN/Inf rejection tests ──────────────────────────────────────────

func TestValidatePoint_NaNDetected(t *testing.T) {
	t.Parallel()

	idxSchema := qdrantSchema.DefaultV3Schema()
	vec := makeFloat32Slice(768)
	vec[100] = float32(math.NaN())

	point := &qdrantSchema.Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text": vec,
		},
	}

	err := ValidatePoint(point, idxSchema)
	require.Error(t, err)
	var nanErr *transport.ErrNaNOrInf
	require.ErrorAs(t, err, &nanErr)
	assert.Equal(t, "text", nanErr.Channel)
	assert.Equal(t, "asset-1", nanErr.AssetID)
}

func TestValidatePoint_PositiveInfDetected(t *testing.T) {
	t.Parallel()

	idxSchema := qdrantSchema.DefaultV3Schema()
	vec := makeFloat32Slice(768)
	vec[0] = float32(math.Inf(1))

	point := &qdrantSchema.Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text": vec,
		},
	}

	err := ValidatePoint(point, idxSchema)
	require.Error(t, err)
	var nanErr *transport.ErrNaNOrInf
	require.ErrorAs(t, err, &nanErr)
}

func TestValidatePoint_NegativeInfDetected(t *testing.T) {
	t.Parallel()

	idxSchema := qdrantSchema.DefaultV3Schema()
	vec := makeFloat32Slice(768)
	vec[0] = float32(math.Inf(-1))

	point := &qdrantSchema.Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text": vec,
		},
	}

	err := ValidatePoint(point, idxSchema)
	require.Error(t, err)
	var nanErr *transport.ErrNaNOrInf
	require.ErrorAs(t, err, &nanErr)
}

func TestValidatePoint_NoNaNorInf(t *testing.T) {
	t.Parallel()

	idxSchema := qdrantSchema.DefaultV3Schema()
	vec := makeFloat32Slice(768)
	vec[0] = 0.0
	vec[1] = 1.0
	vec[2] = -1.0
	vec[3] = 3.4028235e+38  // max float32
	vec[4] = -3.4028235e+38 // min float32

	point := &qdrantSchema.Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text": vec,
		},
	}

	err := ValidatePoint(point, idxSchema)
	require.NoError(t, err)
}

func TestValidatePoint_NaNInVisualChannel(t *testing.T) {
	t.Parallel()

	idxSchema := qdrantSchema.DefaultV3Schema()
	textVec := makeFloat32Slice(768)
	visualVec := makeFloat32Slice(qdrantSchema.VisualEmbeddingDim)
	visualVec[50] = float32(math.NaN())

	point := &qdrantSchema.Point{
		ID: "asset-1",
		Vectors: map[string]interface{}{
			"text":   textVec,
			"visual": visualVec,
		},
	}

	err := ValidatePoint(point, idxSchema)
	require.Error(t, err)
	var nanErr *transport.ErrNaNOrInf
	require.ErrorAs(t, err, &nanErr)
	assert.Equal(t, "visual", nanErr.Channel)
}

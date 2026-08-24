// Package clipindexer — wire_envelope_visual_test.go: 3-tier visual
// fallback TDD surface for QdrantWireEnvelope.ResolveVisualEmbedding
// (PR-LSDC-INTERFACE-TYPED-CONVERSION-CLIPINDEXER, July 2026).
//
// Background: the /index_visual_multi endpoint may return the visual
// vector in 3 mutually-exclusive shapes:
//  1. Top-level {"embedding": [...]} (happy-path — sidecar did the mean)
//  2. {"averaged_embedding": [...]} (sidecar exposed the pre-averaged value)
//  3. {"frame_embeddings": [[...], [...], ...]} (raw per-frame vectors; client averages)
//
// Pre-PR this 3-tier fallback was open-coded inline in
// indexVisualMultiViaAPI using map[string]any lookups. This file pins
// the typed-equivalent 3-tier resolution via ResolveVisualEmbedding.
package clipindexer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveVisualEmbedding_Tier1_TopLevel asserts the happy-path
// (top-level "embedding" field) wins over the 2 fallback tiers even
// when ALL 3 are present. Pre-PR indexVisualMultiViaAPI had the same
// precedence: extractEmbedding is called FIRST; if it succeeds, the
// function returns immediately.
func TestResolveVisualEmbedding_Tier1_TopLevel(t *testing.T) {
	envelope := &QdrantWireEnvelope{
		Embedding:         []float64{0.1, 0.2, 0.3},
		AveragedEmbedding: []float64{0.99, 0.99, 0.99}, // should be IGNORED
		FrameEmbeddings:   [][]float64{{0.5, 0.5}, {0.5, 0.5}},
	}

	resolved, err := envelope.ResolveVisualEmbedding()
	require.NoError(t, err)
	assert.Equal(t, []float64{0.1, 0.2, 0.3}, resolved, "tier-1 top-level embedding must win when present")
}

// TestResolveVisualEmbedding_Tier2_Averaged asserts the second-tier
// fallback (averaged_embedding) is used when top-level embedding is
// absent. Pre-PR this was the extractEmbeddingField(body, "averaged_embedding")
// path with the top-level extractEmbedding returning "response field
// \"embedding\" is not an array" or "missing field".
func TestResolveVisualEmbedding_Tier2_Averaged(t *testing.T) {
	envelope := &QdrantWireEnvelope{
		// Embedding is absent (zero-value nil slice)
		AveragedEmbedding: []float64{0.4, 0.5, 0.6},
		FrameEmbeddings:   [][]float64{{0.99, 0.99}, {0.99, 0.99}}, // should be IGNORED
	}

	resolved, err := envelope.ResolveVisualEmbedding()
	require.NoError(t, err)
	assert.Equal(t, []float64{0.4, 0.5, 0.6}, resolved, "tier-2 averaged_embedding must win when tier-1 is absent")
}

// TestResolveVisualEmbedding_Tier3_Frames asserts the third-tier
// fallback (frame_embeddings) is used when both Embedding and
// AveragedEmbedding are absent. The client-side average must
// produce the mean (per-frame sum / filled-count) — matches the
// pre-PR averageFrameEmbeddings contract.
func TestResolveVisualEmbedding_Tier3_Frames(t *testing.T) {
	// 2 frames, dimension-2 each. Sum / count per element.
	envelope := &QdrantWireEnvelope{
		FrameEmbeddings: [][]float64{
			{0.4, 0.6},
			{0.6, 0.4},
		},
	}

	resolved, err := envelope.ResolveVisualEmbedding()
	require.NoError(t, err)
	assert.InDeltaSlice(t, []float64{0.5, 0.5}, resolved, 1e-9, "tier-3 frame mean must be (sum/filled) per element")
}

// TestResolveVisualEmbedding_Frames_3Frames asserts the averaging
// contract with 3+ frames (matches the production frame_positions
// of [0.2, 0.5, 0.8] which yields 3 per-frame vectors).
func TestResolveVisualEmbedding_Frames_3Frames(t *testing.T) {
	envelope := &QdrantWireEnvelope{
		FrameEmbeddings: [][]float64{
			{0.0, 0.0, 0.0},
			{0.3, 0.6, 0.9},
			{0.6, 0.3, 0.0},
		},
	}

	resolved, err := envelope.ResolveVisualEmbedding()
	require.NoError(t, err)
	// Means: (0+0.3+0.6)/3=0.3, (0+0.6+0.3)/3=0.3, (0+0.9+0.0)/3=0.3
	assert.InDeltaSlice(t, []float64{0.3, 0.3, 0.3}, resolved, 1e-9, "3-frame mean must be element-wise sum/filled")
}

// TestResolveVisualEmbedding_Frames_DimensionMismatch asserts the
// dimension-mismatch fallback contract: frames with non-matching
// dimensions are SKIPPED (pre-PR averageFrameEmbeddings
// `if len(vec) != dim { continue }`); only filled-count frames
// contribute to the mean.
func TestResolveVisualEmbedding_Frames_DimensionMismatch(t *testing.T) {
	envelope := &QdrantWireEnvelope{
		FrameEmbeddings: [][]float64{
			{0.0, 0.0},      // dim 2 — first frame, sets dim=2
			{0.0, 0.0, 0.0}, // dim 3 — SKIPPED
			{0.4, 0.6},      // dim 2 — counted
		},
	}

	resolved, err := envelope.ResolveVisualEmbedding()
	require.NoError(t, err)
	// Means over dim-2 frames only: ((0+0.4)/2, (0+0.6)/2) = (0.2, 0.3)
	assert.InDeltaSlice(t, []float64{0.2, 0.3}, resolved, 1e-9, "dimension-mismatched frames must be skipped; only dim-2 frames contribute")
}

// TestResolveVisualEmbedding_NoFields asserts the canonical typed
// error when ALL 3 candidate fields are absent — matches the pre-PR
// indexVisualMultiViaAPI behavior of logging Debug and returning
// without persisting.
func TestResolveVisualEmbedding_NoFields(t *testing.T) {
	envelope := &QdrantWireEnvelope{route: "/index_visual_multi"}

	resolved, err := envelope.ResolveVisualEmbedding()
	require.Error(t, err, "must error when all 3 candidate fields are absent")
	assert.Nil(t, resolved)
	assert.Contains(t, err.Error(), "/index_visual_multi", "error must name the route for operator diagnostics")
}

// TestResolveVisualEmbedding_EmptyFrames asserts the "frames present
// but empty" case — a present-but-empty FrameEmbeddings slice must
// fall through to "no candidate fields" error.
func TestResolveVisualEmbedding_EmptyFrames(t *testing.T) {
	envelope := &QdrantWireEnvelope{
		FrameEmbeddings: [][]float64{}, // present but empty
	}

	resolved, err := envelope.ResolveVisualEmbedding()
	require.Error(t, err)
	assert.Nil(t, resolved)
}

// TestResolveVisualEmbedding_FramesAllMismatch asserts the
// "all frames dimension-mismatch" case — pre-PR returned "no frames
// with matching dimension"; the typed equivalent returns the same.
func TestResolveVisualEmbedding_FramesAllMismatch(t *testing.T) {
	envelope := &QdrantWireEnvelope{
		FrameEmbeddings: [][]float64{
			{0.0, 0.0, 0.0},      // dim 3 — first frame, sets dim=3
			{0.0, 0.0},           // dim 2 — SKIPPED
			{0.0, 0.0, 0.0, 0.0}, // dim 4 — SKIPPED
		},
	}

	resolved, err := envelope.ResolveVisualEmbedding()
	require.Error(t, err)
	assert.Nil(t, resolved)
}

// TestResolveVisualEmbedding_NilEnvelope asserts the nil-receiver
// safe behavior (godlike/07 nil-tolerance): a nil envelope returns
// a typed error and nil vector.
func TestResolveVisualEmbedding_NilEnvelope(t *testing.T) {
	var envelope *QdrantWireEnvelope

	resolved, err := envelope.ResolveVisualEmbedding()
	require.Error(t, err)
	assert.Nil(t, resolved)
}

// TestAverageFloat64FrameEmbeddings_Empty asserts the typed helper
// rejects empty frames with the canonical "no frames to average" error.
func TestAverageFloat64FrameEmbeddings_Empty(t *testing.T) {
	resolved, err := averageFloat64FrameEmbeddings([][]float64{})
	require.Error(t, err)
	assert.Nil(t, resolved)
}

// TestAverageFloat64FrameEmbeddings_EmptyFirstFrame asserts the
// "first frame is empty" edge case — pre-PR returned "first frame
// is not a vector"; the typed equivalent returns the same.
func TestAverageFloat64FrameEmbeddings_EmptyFirstFrame(t *testing.T) {
	resolved, err := averageFloat64FrameEmbeddings([][]float64{{}})
	require.Error(t, err)
	assert.Nil(t, resolved)
}

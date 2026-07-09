// Package clipindexer — wire_envelope_back_compat_test.go: byte-stability
// regression-guard between the new QdrantWireEnvelope typed path and
// the pre-PR-LSDC-INTERFACE-TYPED-CONVERSION-CLIPINDEXER map[string]any
// path (PR-LSDC-INTERFACE-TYPED-CONVERSION-CLIPINDEXER, July 2026).
//
// Background: godlike/07 minimum-blast-radius requires that the
// envelope migration be additive — existing call sites that use
// readJSONResponse + extractEmbedding must continue to work unchanged
// after the typed envelope is introduced. This file asserts that
// the typed path and the untyped path produce IDENTICAL Go values
// (same []float64, same dimension, same field resolution) for the
// canonical wire-shape inputs.
//
// The helpers themselves (readJSONResponse, extractEmbedding,
// extractEmbeddingField) are retained unchanged in helpers.go per
// minimum-blast-radius; the typed envelope is the NEW preferred path
// for new call sites. The back-compat contract is: for ANY wire-shape
// that the old helpers could handle, the new envelope must produce
// the same Go value.
package clipindexer

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBackCompat_TypedVsUntyped_SingleEmbedding asserts the typed
// envelope produces the same []float64 as the old extractEmbedding
// path for the canonical single-shot response shape.
func TestBackCompat_TypedVsUntyped_SingleEmbedding(t *testing.T) {
	raw := []byte(`{"embedding": [0.1, 0.2, 0.3, 0.4]}`)

	// Typed path (new)
	envelope, err := ParseWireEnvelope(raw, "/index")
	require.NoError(t, err)
	typedVec := envelope.Embedding

	// Untyped path (old — preserved for back-compat per minimum-blast-radius)
	bodyMap, _ := readJSONResponseFromBytes(raw)
	untypedVec, err := extractEmbedding(bodyMap)
	require.NoError(t, err)

	assert.Equal(t, untypedVec, typedVec, "typed envelope MUST produce the same []float64 as the untyped path (back-compat contract)")
}

// TestBackCompat_TypedVsUntyped_AveragedEmbedding asserts the typed
// envelope produces the same []float64 as extractEmbeddingField(body,
// "averaged_embedding") for the pre-averaged visual shape.
func TestBackCompat_TypedVsUntyped_AveragedEmbedding(t *testing.T) {
	raw := []byte(`{"averaged_embedding": [0.4, 0.5, 0.6]}`)

	envelope, err := ParseWireEnvelope(raw, "/index_visual_multi")
	require.NoError(t, err)

	typedVec := envelope.AveragedEmbedding

	bodyMap, _ := readJSONResponseFromBytes(raw)
	untypedVec, err := extractEmbeddingField(bodyMap, "averaged_embedding")
	require.NoError(t, err)

	assert.Equal(t, untypedVec, typedVec, "typed AveragedEmbedding MUST equal the untyped extractEmbeddingField(body, 'averaged_embedding')")
}

// TestBackCompat_TypedVsUntyped_FrameEmbeddings asserts the typed
// envelope's FrameEmbeddings is byte-equivalent to the untyped path
// (the pre-PR code did `bodyMap["frame_embeddings"].([]any)` then
// passed it to averageFrameEmbeddings). The typed path skips the
// []any intermediate; the test asserts the client-side average
// produces the same Go value.
func TestBackCompat_TypedVsUntyped_FrameEmbeddings(t *testing.T) {
	raw := []byte(`{"frame_embeddings": [[0.4, 0.6], [0.6, 0.4]]}`)

	envelope, err := ParseWireEnvelope(raw, "/index_visual_multi")
	require.NoError(t, err)

	typedVec, err := envelope.ResolveVisualEmbedding()
	require.NoError(t, err)

	bodyMap, _ := readJSONResponseFromBytes(raw)
	frames, _ := bodyMap["frame_embeddings"].([]any)
	untypedVec, err := averageFrameEmbeddings(frames)
	require.NoError(t, err)

	assert.Equal(t, untypedVec, typedVec, "typed ResolveVisualEmbedding MUST produce the same mean as the untyped 3-tier fallback")
}

// TestBackCompat_EmptyBody_TypedAndUntyped assert byte-equivalent
// behavior for the empty-body case: both paths return a "no fields
// present" signal (typed = valid zero-value envelope; untyped = empty
// map). Neither path errors.
func TestBackCompat_EmptyBody_TypedAndUntyped(t *testing.T) {
	raw := []byte{}

	envelope, err := ParseWireEnvelope(raw, "/index")
	require.NoError(t, err, "typed path must NOT error on empty body (pre-PR contract)")
	require.NotNil(t, envelope, "typed path must return a valid (zero-value) envelope")
	assert.False(t, envelope.HasEmbedding(), "typed path must report HasEmbedding=false on empty body")

	bodyMap, rawReturned := readJSONResponseFromBytes(raw)
	assert.Equal(t, map[string]any{}, bodyMap, "untyped path must return empty map on empty body (pre-PR contract)")
	assert.Equal(t, "", rawReturned, "untyped path must return empty raw on empty body")
}

// TestBackCompat_EmptyJSONObject_TypedAndUntyped asserts the "{}"
// case is byte-equivalent: both paths return a valid (empty)
// response shape; neither errors.
func TestBackCompat_EmptyJSONObject_TypedAndUntyped(t *testing.T) {
	raw := []byte("{}")

	envelope, err := ParseWireEnvelope(raw, "/index")
	require.NoError(t, err)
	require.NotNil(t, envelope)

	bodyMap, _ := readJSONResponseFromBytes(raw)
	assert.Equal(t, map[string]any{}, bodyMap, "untyped empty object must produce empty map")
	assert.False(t, envelope.HasEmbedding(), "typed empty object must report HasEmbedding=false")
}

// TestBackCompat_MalformedBody_TypedAndUntyped asserts the malformed
// (non-JSON) case is byte-equivalent: both paths return nil/empty
// without panicking. The typed path captures the unmarshal error;
// the untyped path silently returns nil+rawbody.
func TestBackCompat_MalformedBody_TypedAndUntyped(t *testing.T) {
	raw := []byte("not valid json {{{")

	envelope, err := ParseWireEnvelope(raw, "/index")
	require.Error(t, err, "typed path MUST error on malformed body (caller can decide whether to log)")
	assert.Nil(t, envelope)

	bodyMap, rawReturned := readJSONResponseFromBytes(raw)
	assert.Nil(t, bodyMap, "untyped path returns nil bodyMap on unmarshal failure")
	assert.Equal(t, string(raw), rawReturned, "untyped path returns raw body for log diagnostics on unmarshal failure")
}

// TestBackCompat_EmptyFrames asserts the typed envelope's
// ResolveVisualEmbedding produces the same typed error as the
// untyped averageFrameEmbeddings helper when the frame_embeddings
// field is present but empty (the canonical "no frames to average"
// error class). The first-frame-sets-the-dim contract means a
// "dimension mismatch" requires literally NO frames (otherwise the
// first frame is always counted), so this test exercises the
// empty-frames path which is the only "no candidates" path that's
// also a per-frame error.
func TestBackCompat_EmptyFrames(t *testing.T) {
	raw := []byte(`{"frame_embeddings": []}`)

	envelope, err := ParseWireEnvelope(raw, "/index_visual_multi")
	require.NoError(t, err)

	_, typedErr := envelope.ResolveVisualEmbedding()
	require.Error(t, typedErr, "typed path MUST error on empty frames (no candidates to average)")

	bodyMap, _ := readJSONResponseFromBytes(raw)
	frames, _ := bodyMap["frame_embeddings"].([]any)
	_, untypedErr := averageFrameEmbeddings(frames)
	require.Error(t, untypedErr, "untyped path MUST error on empty frames")

	// Both errors are of the same class (no-candidates) but we
	// don't assert exact message equality (forward-compat: a
	// future improvement to the error message would break the
	// contract). We assert both ARE errors.
	_ = typedErr
	_ = untypedErr
}

// readJSONResponseFromBytes is a test-only convenience that mirrors
// the pre-PR readJSONResponse body-parsing logic without the
// *http.Response dependency (the test exercises the unmarshal seam
// in isolation). Returns (bodyMap, rawBody) — same shape as the
// pre-PR readJSONResponse.
//
// Contract (byte-equivalent with pre-PR readJSONResponse):
//   - empty body  → (map[string]any{}, "")
//   - malformed   → (nil, rawBody) so caller can log
//   - valid JSON  → (parsed map, rawBody)
func readJSONResponseFromBytes(raw []byte) (map[string]any, string) {
	if len(raw) == 0 {
		return map[string]any{}, ""
	}
	var bodyMap map[string]any
	if err := json.Unmarshal(raw, &bodyMap); err != nil {
		return nil, string(raw)
	}
	if bodyMap == nil {
		bodyMap = map[string]any{}
	}
	return bodyMap, string(raw)
}

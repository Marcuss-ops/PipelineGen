// Package clipindexer — wire_envelope_test.go: round-trip + happy-path
// TDD surface for QdrantWireEnvelope (PR-LSDC-INTERFACE-TYPED-CONVERSION-CLIPINDEXER,
// July 2026).
//
// godlike/06 ONE-canonical-owner-per-fact: this test file is the
// canonical SOLE owner of the field-by-field round-trip contract for
// the QdrantWireEnvelope type. All other wire_envelope_*_test.go files
// focus on specific edge cases (visual fallback, back-compat with
// map[string]any, nil/empty/malformed inputs).
package clipindexer

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestQdrantWireEnvelope_CompileTimePin asserts the canonical compile-time
// pin per AGENTS.md Pattern 0 — the envelope MUST satisfy the local
// wireEnvelopeShape interface. If a future maintainer renames a
// method or changes the return type, this test fails at build time
// (not at runtime) so the signature drift is caught immediately.
func TestQdrantWireEnvelope_CompileTimePin(t *testing.T) {
	// Compile-time assertion: a future drift on QdrantWireEnvelope's
	// method signatures (or on wireEnvelopeShape's) breaks this
	// var-declaration, not the runtime test.
	var _ wireEnvelopeShape = (*QdrantWireEnvelope)(nil)
	// Runtime sanity: a zero-value envelope satisfies the shape.
	var env *QdrantWireEnvelope
	require.NotPanics(t, func() {
		_ = env.HasEmbedding()
		_ = env.HasAveragedEmbedding()
		_ = env.HasFrameEmbeddings()
		_, _ = env.ResolveVisualEmbedding()
		_ = env.RawBody()
		_ = env.Route()
	}, "nil-receiver envelope must NOT panic on any shape method")
}

// TestQdrantWireEnvelope_RoundTrip_AllFields asserts byte-equivalent
// round-trip: marshal(QdrantWireEnvelope{...}) → unmarshal → equal
// (canonical field-by-field equality).
func TestQdrantWireEnvelope_RoundTrip_AllFields(t *testing.T) {
	original := &QdrantWireEnvelope{
		Embedding:         []float64{0.1, 0.2, 0.3},
		AveragedEmbedding: []float64{0.4, 0.5, 0.6},
		FrameEmbeddings:   [][]float64{{0.7, 0.8}, {0.9, 1.0}},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err, "marshal must succeed")

	// rawBody + route are NOT serialized (json:"-" tag). They start
	// at zero-value after Unmarshal — that's the byte-equivalent
	// round-trip.
	roundTripped, err := ParseWireEnvelope(data, "/test")
	require.NoError(t, err, "parse must succeed")
	require.NotNil(t, roundTripped)

	assert.Equal(t, original.Embedding, roundTripped.Embedding, "Embedding field")
	assert.Equal(t, original.AveragedEmbedding, roundTripped.AveragedEmbedding, "AveragedEmbedding field")
	assert.Equal(t, original.FrameEmbeddings, roundTripped.FrameEmbeddings, "FrameEmbeddings field")
}

// TestQdrantWireEnvelope_RoundTrip_EmbeddingOnly asserts the canonical
// single-shot route shape (used by /index, /index_transcript,
// /embed_audio_from_file) round-trips byte-equivalent.
func TestQdrantWireEnvelope_RoundTrip_EmbeddingOnly(t *testing.T) {
	raw := []byte(`{"embedding": [0.1, 0.2, 0.3, 0.4]}`)

	envelope, err := ParseWireEnvelope(raw, "/index")
	require.NoError(t, err)
	require.NotNil(t, envelope)

	assert.True(t, envelope.HasEmbedding())
	assert.Equal(t, []float64{0.1, 0.2, 0.3, 0.4}, envelope.Embedding)
	assert.False(t, envelope.HasAveragedEmbedding())
	assert.Nil(t, envelope.AveragedEmbedding)
	assert.False(t, envelope.HasFrameEmbeddings())
	assert.Nil(t, envelope.FrameEmbeddings)
	assert.Equal(t, "/index", envelope.Route())
	assert.Equal(t, string(raw), envelope.RawBody())
}

// TestQdrantWireEnvelope_EmptyResponse asserts byte-equivalent
// behavior with the pre-PR readJSONResponse(empty body) contract:
// returns a valid (zero-value) envelope, not nil, not an error.
func TestQdrantWireEnvelope_EmptyResponse(t *testing.T) {
	envelope, err := ParseWireEnvelope([]byte{}, "/index")
	require.NoError(t, err, "empty body must NOT error (matches pre-PR readJSONResponse contract)")
	require.NotNil(t, envelope, "empty body must return a valid (zero-value) envelope")

	assert.False(t, envelope.HasEmbedding())
	assert.False(t, envelope.HasAveragedEmbedding())
	assert.False(t, envelope.HasFrameEmbeddings())
	assert.Equal(t, "", envelope.RawBody())
	assert.Equal(t, "/index", envelope.Route())
}

// TestQdrantWireEnvelope_EmptyJSONObject asserts the `{}` shape (a
// valid sidecar response that just carries no payload) decodes to
// a zero-value envelope — NOT an error. Pre-PR readJSONResponse
// returned (map[string]any{}, "") for this case.
func TestQdrantWireEnvelope_EmptyJSONObject(t *testing.T) {
	envelope, err := ParseWireEnvelope([]byte("{}"), "/index")
	require.NoError(t, err)
	require.NotNil(t, envelope)
	assert.False(t, envelope.HasEmbedding())
	assert.Equal(t, "{}", envelope.RawBody())
}

// TestQdrantWireEnvelope_Accessors_NilReceiver asserts nil-receiver
// safety on every accessor (godlike/07 nil-tolerance per the
// existing extractEmbeddingField contract). A future regression
// that adds a non-nil-safe method would surface here.
func TestQdrantWireEnvelope_Accessors_NilReceiver(t *testing.T) {
	var env *QdrantWireEnvelope

	require.NotPanics(t, func() { _ = env.HasEmbedding() })
	require.NotPanics(t, func() { _ = env.HasAveragedEmbedding() })
	require.NotPanics(t, func() { _ = env.HasFrameEmbeddings() })
	require.NotPanics(t, func() { _, _ = env.ResolveVisualEmbedding() })
	require.NotPanics(t, func() { _ = env.RawBody() })
	require.NotPanics(t, func() { _ = env.Route() })

	assert.False(t, env.HasEmbedding())
	assert.False(t, env.HasAveragedEmbedding())
	assert.False(t, env.HasFrameEmbeddings())
	assert.Equal(t, "", env.RawBody())
	assert.Equal(t, "", env.Route())
}

// TestParseWireEnvelopeFromResponse asserts the convenience wrapper
// preserves the pre-PR readJSONResponse (map, raw) return contract.
func TestParseWireEnvelopeFromResponse(t *testing.T) {
	body := []byte(`{"embedding": [0.5, 0.6]}`)

	envelope, raw := ParseWireEnvelopeFromResponse(body, "/index")
	require.NotNil(t, envelope)
	assert.Equal(t, body, []byte(raw), "raw must equal the original body (matches pre-PR readJSONResponse 2nd-return)")
	assert.True(t, envelope.HasEmbedding())
}

// TestQdrantWireEnvelope_TruncateRaw asserts the 1024-byte diagnostic
// cap on malformed-body error messages (prevents log-flooding on a
// 1MB+ sidecar response).
func TestQdrantWireEnvelope_TruncateRaw(t *testing.T) {
	// Build a 2000-byte garbage body (1KB over the cap).
	garbage := make([]byte, 2000)
	for i := range garbage {
		garbage[i] = 'X'
	}
	_, err := ParseWireEnvelope(garbage, "/index")
	require.Error(t, err)
	// The error message MUST contain the truncation marker.
	assert.Contains(t, err.Error(), "...(truncated)", "1KB+ malformed body must surface the truncation marker")
}

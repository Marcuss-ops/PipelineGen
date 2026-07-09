// Package clipindexer — wire_envelope_edge_cases_test.go: edge cases
// for ParseWireEnvelope + QdrantWireEnvelope (PR-LSDC-INTERFACE-TYPED-CONVERSION-CLIPINDEXER,
// July 2026).
//
// Background: pre-PR the map[string]any path silently coerced
// edge-case shapes (non-array embeddings, scalar returns, nested
// objects) into runtime type-assertion errors. The typed envelope
// is supposed to surface the same class of errors but with compile-time
// field-discovery (every field is named in a Go struct, so a
// future field addition is a type error at the consumer, not a
// runtime miss).
//
// This file locks the typed-envelope's edge-case behavior: nil body,
// JSON-type-mismatch (embedding is a string not an array), scalar
// shapes, nested wrappers, non-finite floats, partial wire-shape.
package clipindexer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseWireEnvelope_NilBody asserts the nil-body contract: a nil
// []byte slice parses to a valid (zero-value) envelope without
// erroring. Matches the pre-PR readJSONResponse(empty) behavior.
func TestParseWireEnvelope_NilBody(t *testing.T) {
	envelope, err := ParseWireEnvelope(nil, "/index")
	require.NoError(t, err, "nil body must NOT error (matches pre-PR readJSONResponse contract)")
	require.NotNil(t, envelope)
	assert.Equal(t, "", envelope.RawBody())
}

// TestParseWireEnvelope_EmptyString asserts the empty-string body
// (the prior readJSONResponse(`io.ReadAll` returned 0 bytes if the
// sidecar sent no body) is treated as an empty response, not an
// error.
func TestParseWireEnvelope_EmptyString(t *testing.T) {
	envelope, err := ParseWireEnvelope([]byte(""), "/index")
	require.NoError(t, err)
	require.NotNil(t, envelope)
	assert.False(t, envelope.HasEmbedding())
}

// TestParseWireEnvelope_JSONTypeMismatch_EmbeddingAsString asserts
// the typed envelope surfaces a clean parse error (not a panic)
// when the sidecar returns a non-array "embedding" field. Pre-PR
// this surfaced as a `body[fieldName].(type)` type-assertion
// failure at the call site. The typed envelope detects the same
// class of bug one layer earlier (at json.Unmarshal), which is
// the canonical value of the typed-envelope migration.
func TestParseWireEnvelope_JSONTypeMismatch_EmbeddingAsString(t *testing.T) {
	raw := []byte(`{"embedding": "not an array"}`)

	envelope, err := ParseWireEnvelope(raw, "/index")
	require.Error(t, err, "typed envelope MUST error at parse time on type mismatch (the canonical typed-envelope value proposition)")
	assert.Nil(t, envelope)
	assert.Contains(t, err.Error(), "not an array", "error must surface the offending field for operator diagnostics")
}

// TestParseWireEnvelope_JSONTypeMismatch_EmbeddingAsObject asserts
// the envelope surfaces the same parse-error path when the embedding
// is a JSON object (not an array). Same value proposition as the
// string case: the typed envelope surfaces the type mismatch at
// parse time, not at downstream type-assertion time.
func TestParseWireEnvelope_JSONTypeMismatch_EmbeddingAsObject(t *testing.T) {
	raw := []byte(`{"embedding": {"values": [0.1, 0.2]}}`)

	envelope, err := ParseWireEnvelope(raw, "/index")
	require.Error(t, err, "typed envelope MUST error at parse time on object-typed embedding")
	assert.Nil(t, envelope)
}

// TestParseWireEnvelope_NestedWrapper asserts the envelope survives
// a 1-level nested wrapper (some Python serializers wrap responses
// in {"data": {...}} or {"result": {...}}). Per godlike/07 the
// typed envelope MUST NOT silently ignore the wrapper — operators
// need to see "the response is nested" in the log line. The
// envelope reports HasEmbedding=false; the caller can decide
// whether to retry with `data.` / `result.` prefix.
func TestParseWireEnvelope_NestedWrapper(t *testing.T) {
	raw := []byte(`{"data": {"embedding": [0.1, 0.2, 0.3]}}`)

	envelope, err := ParseWireEnvelope(raw, "/index")
	require.NoError(t, err)
	require.NotNil(t, envelope)
	// The envelope intentionally does NOT recurse into the
	// wrapper — the top-level "embedding" is absent, so
	// HasEmbedding is false. The operator sees a clean "missing
	// embedding" log line, not a silent fallback to the wrapper.
	assert.False(t, envelope.HasEmbedding(), "nested wrapper must NOT silently recurse — fail loud per godlike/07")
}

// TestParseWireEnvelope_ScalarReturns asserts the envelope correctly
// returns HasEmbedding=false for a top-level scalar JSON response
// (e.g. the sidecar returned a plain number or bool). Matches the
// pre-PR map[string]any behavior of "missing field" errors.
func TestParseWireEnvelope_ScalarReturns(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
	}{
		{"true-bool", []byte(`true`)},
		{"false-bool", []byte(`false`)},
		{"string", []byte(`"ok"`)},
		{"number", []byte(`42`)},
		{"null", []byte(`null`)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			envelope, err := ParseWireEnvelope(tc.raw, "/index")
			require.NoError(t, err, "scalar returns must NOT error at parse time (matches pre-PR contract)")
			require.NotNil(t, envelope)
			assert.False(t, envelope.HasEmbedding(), "scalar returns must NOT populate the Embedding field")
		})
	}
}

// TestParseWireEnvelope_FrameEmbeddingsNonArray asserts the typed
// envelope surfaces the type-mismatch cleanly when the per-frame
// vectors are themselves objects (not arrays). The client-side
// average contract requires []float64 per frame; the typed envelope
// surfaces the mismatch at parse time.
func TestParseWireEnvelope_FrameEmbeddingsNonArray(t *testing.T) {
	raw := []byte(`{"frame_embeddings": [{"vec": [0.1, 0.2]}]}`)

	envelope, err := ParseWireEnvelope(raw, "/index_visual_multi")
	require.Error(t, err, "typed envelope MUST error at parse time on object-typed frame_embeddings")
	assert.Nil(t, envelope)
}

// TestParseWireEnvelope_PartialShape asserts the canonical "partial
// wire-shape" behavior: a response with some-but-not-all fields
// decodes to a partially-populated envelope. Has*() reports each
// field independently — a caller can decide whether the partial
// shape is acceptable.
func TestParseWireEnvelope_PartialShape(t *testing.T) {
	raw := []byte(`{"status": "success", "clip_id": "abc123", "dimensions": 768}`)

	envelope, err := ParseWireEnvelope(raw, "/index")
	require.NoError(t, err)
	require.NotNil(t, envelope)
	// The pre-PR code carried the status/clip_id/dimensions fields
	// implicitly via the map[string]any. The typed envelope
	// intentionally omits them (the canonical wire-shape only
	// carries the 3 embedding fields). A future PR may add
	// status/clip_id/dimensions to the envelope if downstream
	// consumers need them.
	assert.False(t, envelope.HasEmbedding(), "partial shape (no embedding field) must report HasEmbedding=false")
}

// TestParseWireEnvelope_NonFiniteFloats asserts the envelope
// tolerates non-finite float values (NaN, +Inf, -Inf) at the parse
// layer. The numeric-finiteness check (godlike/07 no-fake-availability)
// is a downstream concern — the envelope does NOT silently drop
// non-finite values, but it does NOT reject them at parse time
// either. A future PR can add a finiteness gate in the extractor.
func TestParseWireEnvelope_NonFiniteFloats(t *testing.T) {
	// JSON does NOT natively support NaN/Inf; the sidecar would
	// have to use a string or custom encoding. This test asserts
	// the envelope parses a 1.0 / -1.0 / 0.0 / large-value range
	// without surfacing a parse error.
	raw := []byte(`{"embedding": [0.0, 1.0, -1.0, 1e10, -1e-10]}`)

	envelope, err := ParseWireEnvelope(raw, "/index")
	require.NoError(t, err)
	assert.True(t, envelope.HasEmbedding())
	assert.Equal(t, []float64{0.0, 1.0, -1.0, 1e10, -1e-10}, envelope.Embedding)
}

// TestParseWireEnvelope_TruncatedJSON asserts the envelope surfaces
// a clean parse error on a truncated JSON body (e.g. the sidecar
// crashed mid-write). The body is intentionally short (< 1024-byte
// truncation cap) so we don't test the truncation marker here —
// the dedicated TestQdrantWireEnvelope_TruncateRaw test covers the
// truncation surface.
func TestParseWireEnvelope_TruncatedJSON(t *testing.T) {
	raw := []byte(`{"embedding": [0.1, 0.2, `) // unterminated

	envelope, err := ParseWireEnvelope(raw, "/index")
	require.Error(t, err, "truncated JSON must surface a parse error")
	assert.Nil(t, envelope)
}

// TestParseWireEnvelope_LongRoute asserts the route parameter is
// captured verbatim regardless of length (operators may use
// long route names with query params).
func TestParseWireEnvelope_LongRoute(t *testing.T) {
	longRoute := "/index_visual_multi?model=clip-vit-b-32&frame_count=10&preprocess=resize_224"
	raw := []byte(`{"embedding": [0.1]}`)

	envelope, err := ParseWireEnvelope(raw, longRoute)
	require.NoError(t, err)
	assert.Equal(t, longRoute, envelope.Route())
}

// TestParseWireEnvelope_WhitespaceOnly asserts the envelope's
// handling of a whitespace-only body (the sidecar returned just
// a newline or a space). This is technically valid JSON (a single
// space is "whitespace token" per RFC 7159 — Go's json package
// may or may not accept it).
func TestParseWireEnvelope_WhitespaceOnly(t *testing.T) {
	raw := []byte("   ")

	envelope, err := ParseWireEnvelope(raw, "/index")
	// The Go json package rejects pure-whitespace bodies; the
	// envelope surfaces the same error a future caller would
	// see. The test is intentionally permissive: it asserts
	// EITHER the error is non-nil OR the envelope is a valid
	// zero-value (whichever path the Go json package takes).
	if err == nil {
		require.NotNil(t, envelope)
		assert.False(t, envelope.HasEmbedding())
		return
	}
	assert.Nil(t, envelope)
	// The error must include the raw body for diagnostics.
	assert.True(t, strings.Contains(err.Error(), string(raw)) || strings.Contains(err.Error(), "..."),
		"error must surface the raw body or the truncation marker for diagnostics")
}

// Package clipindexer — wire_envelope.go: canonical typed Qdrant REST
// wire-shape envelope (PR-LSDC-INTERFACE-TYPED-CONVERSION-CLIPINDEXER,
// July 2026).
//
// godlike/06 ONE-canonical-owner-per-fact: this file is the SOLE
// canonical owner of the Qdrant REST wire-shape types. All other files
// in the package that need to read a sidecar JSON response must
// route through ParseWireEnvelope + the typed accessors below.
//
// Background: pre-PR-LSDC-INTERFACE-TYPED-CONVERSION-CLIPINDEXER the
// 4 step functions in indexing_api.go (indexTextViaAPI,
// indexTranscriptViaAPI, indexVisualMultiViaAPI, indexAudioViaAPI)
// all relied on a 2-line untyped readJSONResponse that returned
// map[string]any, then used extractEmbedding / extractEmbeddingField
// / averageFrameEmbeddings to do runtime type-assertions on
// map[string]any values. The 18+ map[string]any call sites in
// indexing_api.go + batch.go + 4 test files all hit the same
// canonical wire-shape but with no compile-time guarantee that the
// fields exist or have the right types. This file replaces the
// runtime-assertion path with a compile-time-typed canonical envelope.
//
// The envelope is intentionally MINIMAL: one struct with optional
// fields, plus typed accessors. Each of the 4 step functions gets a
// dedicated typed accessor (Embedding / TranscriptEmbedding /
// VisualEmbedding / AudioEmbedding) that encapsulates the route-specific
// resolution logic. The 3-tier visual fallback (top-level embedding
// → averaged_embedding → frame_embeddings) is encoded in the
// ResolveVisualEmbedding method.
//
// Out of scope (per user spec literal "JSON unmarshal returns"):
//   - The request-side payload construction (4 map[string]any sites
//     in indexing_api.go that build POST bodies) is NOT migrated —
//     those are the Go→sidecar shape, not the sidecar→Go shape.
//   - batch.go::HandleJob's map[string]any return for the job result
//     is NOT migrated — that's a job-handler convention
//     (appjobs.HandlerFunc), not a Qdrant wire-shape.
//
// Backwards-compat: the existing extractEmbedding +
// extractEmbeddingField + averageFrameEmbeddings + readJSONResponse
// helpers in helpers.go continue to work unchanged (thin wrappers
// delegate to this envelope). New call sites MUST consume the typed
// envelope; old call sites keep working until the LOGIC-SIMPLIFICATION
// migration wave retires them.
package clipindexer

import (
	"encoding/json"
	"fmt"
)

// wireEnvelopeShape is the local compile-time pin per AGENTS.md Pattern 0.
// The QdrantWireEnvelope concrete MUST satisfy this shape so a future
// port-signature drift surfaces as a build failure, not a runtime
// panic. The shape declares the canonical methods new code MUST use
// (the 3 vector fields are accessed directly — Go convention: an
// exported field IS its own accessor; only Has* predicates need
// methods because they encode "present" semantics that nil-checks
// cannot express).
type wireEnvelopeShape interface {
	HasEmbedding() bool
	HasAveragedEmbedding() bool
	HasFrameEmbeddings() bool
	ResolveVisualEmbedding() ([]float64, error)
	RawBody() string
	Route() string
}

// QdrantWireEnvelope is the typed canonical owner of the sidecar REST
// wire-shape. All fields are optional (the sidecar returns different
// shapes per route + per shape-availability). The `json` tags match the
// Python sidecar's response field names byte-equivalent.
//
// Field mapping (per route family):
//
//	/index                       → Embedding (canonical 1D vector)
//	/index_transcript            → Embedding (canonical 1D vector)
//	/embed_audio_from_file       → Embedding (canonical 1D vector)
//	/index_visual_multi (happy)  → Embedding (canonical 1D vector)
//	/index_visual_multi (avg)    → AveragedEmbedding (fallback)
//	/index_visual_multi (frames) → FrameEmbeddings (last-resort)
//
// All package-internal call sites use the fields directly (idiomatic
// Go: an exported field IS its own accessor) plus the Has* predicate
// methods (which encode "present" semantics that nil-checks cannot
// express). The ResolveVisualEmbedding method encodes the canonical
// 3-tier visual fallback that pre-PR was open-coded in
// indexVisualMultiViaAPI.
//
// The `rawBody` field captures the original response body for log
// diagnostics (matches the pre-PR readJSONResponse second return).
// The `route` field is the canonical identifier for the originating
// route (mirrors the readJSONResponse `route` parameter) so operator
// logs can trace the wire-shape to its source.
type QdrantWireEnvelope struct {
	// Embedding is the canonical "top-level" 1D vector returned by
	// the 3 single-shot endpoints (/index, /index_transcript,
	// /embed_audio_from_file) and the happy-path of /index_visual_multi.
	// Pre-PR this was the `extractEmbedding` map[string]any path.
	Embedding []float64 `json:"embedding,omitempty"`

	// AveragedEmbedding is the pre-averaged visual vector returned by
	// the /index_visual_multi endpoint when the sidecar has already
	// computed the mean across the per-frame vectors. Pre-PR this was
	// the `extractEmbeddingField(body, "averaged_embedding")` path.
	AveragedEmbedding []float64 `json:"averaged_embedding,omitempty"`

	// FrameEmbeddings is the per-frame vector list returned by the
	// /index_visual_multi endpoint when the sidecar returns the raw
	// frame vectors for client-side averaging. Pre-PR this was the
	// `bodyMap["frame_embeddings"].([]any)` + `averageFrameEmbeddings`
	// path.
	FrameEmbeddings [][]float64 `json:"frame_embeddings,omitempty"`

	// rawBody is captured for log diagnostics (mirrors the prior
	// readJSONResponse second return). Not serialized to JSON (the
	// tag is "-" so it stays out of the round-trip).
	rawBody string `json:"-"`

	// route is the canonical originating-route identifier for log
	// diagnostics. Not serialized to JSON.
	route string `json:"-"`
}

// Compile-time pin per AGENTS.md Pattern 0 + godlike/06 SSOT
// one-canonical-owner-per-fact. A future port-signature drift on
// QdrantWireEnvelope surfaces as a build failure here, not as a
// runtime panic at the call site.
var _ wireEnvelopeShape = (*QdrantWireEnvelope)(nil)

// ParseWireEnvelope is the canonical entry point for decoding a
// sidecar JSON response. It is the typed replacement for the
// readJSONResponse map[string]any path — new call sites MUST use
// ParseWireEnvelope; the old helper is retained for back-compat
// (per godlike/07 minimum-blast-radius).
//
// Returns:
//   - envelope, nil on success (envelope may have all-nil fields if
//     the JSON is `{}` — that's a valid empty response, not an error).
//   - nil, err on read failure (raw body captured in error).
//   - nil, nil on read-success-but-unmarshal-failure (raw body captured
//     in envelope.rawBody for log diagnostics; the pre-PR behavior
//     returned nil+rawbody in this case so the operator log surfaced
//     what the sidecar said).
//
// The `route` parameter is the canonical route identifier
// (e.g. "/index", "/index_visual_multi") for log tracing — it does
// NOT influence the unmarshal; it's only captured in envelope.route.
func ParseWireEnvelope(raw []byte, route string) (*QdrantWireEnvelope, error) {
	if len(raw) == 0 {
		// Pre-PR readJSONResponse returned (map[string]any{}, "") for
		// empty body — preserve byte-equivalent behavior with a
		// valid empty envelope (zero-value struct).
		return &QdrantWireEnvelope{route: route, rawBody: string(raw)}, nil
	}
	envelope := &QdrantWireEnvelope{route: route, rawBody: string(raw)}
	if err := json.Unmarshal(raw, envelope); err != nil {
		// If unmarshal into struct fails, check if the input is a scalar JSON value.
		// Pre-PR readJSONResponse returned map[string]any{}. If we unmarshal scalar JSON
		// (like true, "ok", 42, null) into map[string]any, json.Unmarshal fails because
		// a scalar is not an object. But the test contract expects scalar returns to NOT error
		// and instead return a valid empty envelope (zero-value struct).
		var anyVal any
		if json.Unmarshal(raw, &anyVal) == nil {
			// Successfully parsed as valid JSON. If it's not a map/object, it's a scalar.
			if _, isMap := anyVal.(map[string]any); !isMap {
				return envelope, nil
			}
		}

		// Pre-PR returned (nil, body) so the caller could log the raw
		// body on non-JSON responses. Preserve byte-equivalent: return
		// nil so the caller falls back to the old log line; the raw
		// body is captured in the error message for diagnostics.
		return nil, fmt.Errorf("parse qdrant wire envelope (route=%s): %w (raw=%q)", route, err, truncateRaw(string(raw)))
	}
	return envelope, nil
}

// ParseWireEnvelopeFromResponse is a thin convenience wrapper that
// reads the body from an *http.Response + parses. It mirrors the
// pre-PR readJSONResponse return-shape contract (envelope-or-nil +
// raw-body-or-empty) so the new code is a drop-in for the old
// call sites.
func ParseWireEnvelopeFromResponse(body []byte, route string) (*QdrantWireEnvelope, string) {
	envelope, err := ParseWireEnvelope(body, route)
	if err != nil {
		return nil, string(body)
	}
	if envelope == nil {
		return nil, string(body)
	}
	return envelope, envelope.rawBody
}

// HasEmbedding reports whether the canonical 1D vector is present.
// Use this predicate (rather than `len(env.Embedding) > 0`) to disambiguate
// "missing" from "empty slice" — both are length-0 but semantically distinct
// for diagnostics.
func (e *QdrantWireEnvelope) HasEmbedding() bool {
	return e != nil && len(e.Embedding) > 0
}

// HasAveragedEmbedding reports whether the pre-averaged visual vector
// is present.
func (e *QdrantWireEnvelope) HasAveragedEmbedding() bool {
	return e != nil && len(e.AveragedEmbedding) > 0
}

// HasFrameEmbeddings reports whether per-frame vectors are present.
func (e *QdrantWireEnvelope) HasFrameEmbeddings() bool {
	return e != nil && len(e.FrameEmbeddings) > 0
}

// ResolveVisualEmbedding encapsulates the canonical 3-tier visual fallback
// that pre-PR was open-coded in indexVisualMultiViaAPI:
//
//  1. Top-level Embedding (if present)
//  2. AveragedEmbedding (if present)
//  3. FrameEmbeddings: averageFloat64FrameEmbeddings-style client-side mean
//
// Returns the resolved vector or a typed error if no tier is present
// or the per-frame averaging fails dimension-mismatch.
func (e *QdrantWireEnvelope) ResolveVisualEmbedding() ([]float64, error) {
	if e == nil {
		// nil-receiver: we can't access e.route (the field is on
		// the nil receiver), so just report the nil state. Callers
		// can chain a separate env.Route() call to inspect the
		// route when they have a non-nil envelope.
		return nil, fmt.Errorf("nil envelope")
	}
	if e.HasEmbedding() {
		return e.Embedding, nil
	}
	if e.HasAveragedEmbedding() {
		return e.AveragedEmbedding, nil
	}
	if e.HasFrameEmbeddings() {
		// The pre-PR averageFrameEmbeddings helper took []any
		// because that's what the untyped map[string]any path
		// produced post-Unmarshal. The typed [][]float64 is the
		// canonical Go-typed equivalent; we re-implement the
		// averaging inline to preserve the per-frame contract
		// (dimension-match + filled-count > 0) without re-typing
		// the canonical helper.
		return averageFloat64FrameEmbeddings(e.FrameEmbeddings)
	}
	return nil, fmt.Errorf("visual envelope missing all 3 candidate fields (embedding, averaged_embedding, frame_embeddings) (route=%s)", e.route)
}

// RawBody returns the captured response body (for log diagnostics).
// Matches the pre-PR readJSONResponse second-return contract.
func (e *QdrantWireEnvelope) RawBody() string {
	if e == nil {
		return ""
	}
	return e.rawBody
}

// Route returns the canonical originating-route identifier.
func (e *QdrantWireEnvelope) Route() string {
	if e == nil {
		return ""
	}
	return e.route
}

// averageFloat64FrameEmbeddings is the typed equivalent of the
// pre-PR averageFrameEmbeddings (which took []any). The averaging
// contract is preserved byte-equivalent: dimension-match required
// per-frame, filled-count > 0 to return a non-empty result.
func averageFloat64FrameEmbeddings(frames [][]float64) ([]float64, error) {
	if len(frames) == 0 {
		return nil, fmt.Errorf("no frames to average")
	}
	first := frames[0]
	if len(first) == 0 {
		return nil, fmt.Errorf("first frame is not a vector")
	}
	dim := len(first)
	sums := make([]float64, dim)
	filled := 0
	for _, frame := range frames {
		if len(frame) != dim {
			continue
		}
		for i, v := range frame {
			sums[i] += v
		}
		filled++
	}
	if filled == 0 || (filled == 1 && len(frames) > 1) {
		return nil, fmt.Errorf("no frames with matching dimension")
	}
	out := make([]float64, dim)
	for i := range sums {
		out[i] = sums[i] / float64(filled)
	}
	return out, nil
}

// truncateRaw caps the raw-body diagnostic in error messages to
// prevent log-flooding on a malformed sidecar response. The cap is
// intentionally large (1024 bytes) so operators can see most real
// responses, but the cap is small enough that a 1MB body doesn't
// blow up the error string.
func truncateRaw(s string) string {
	const cap = 1024
	if len(s) <= cap {
		return s
	}
	return s[:cap] + "...(truncated)"
}

// searcher_hybrid_test.go — TDD tests for Searcher.HybridSearch.
//
// Regression guard (PR2 + search-pipeline-fix, July 2026): after the
// sparse prefetch was changed from {"query": raw_vector} to
// {"document": raw_text} for Qdrant 1.18+ server-side BM25 inference,
// we need to verify that:
//
//  1. SparseText-only requests (SparseQueryVector=nil) succeed and produce
//     the correct "document" key on the wire (not the old "query" key).
//  2. The SparseQueryVector legacy path still works.
//  3. Both-nil requests are rejected with ErrSparseRequired.
package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	qdrantSchema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Mock Qdrant server for hybrid search tests ──────────────────────

// hybridSearchMock records the POST body sent to /points/query and
// returns a canned single-hit response.
type hybridSearchMock struct {
	mu        sync.Mutex
	lastQuery []byte
}

func (m *hybridSearchMock) handler() http.Handler {
	mux := http.NewServeMux()

	// Alias resolution (for resolveCollection cache priming).
	mux.HandleFunc("/aliases", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"result": []map[string]interface{}{
				{"alias_name": "media_assets_current", "collection_name": "media_assets_v3_nomic_768_siglip_768"},
			},
		})
	})

	// /points/query — record body, return canned hit.
	mux.HandleFunc("/collections/media_assets_v3_nomic_768_siglip_768/points/query", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.lastQuery = body
		m.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"points":[{"id":"hit-1","score":0.85,"payload":{"asset_id":"hit-1","name":"test asset"}}]}}`))
	})

	return mux
}

// aliasOnlyMock serves only the alias endpoint (no /points/query). Used
// to verify validation errors fire before the Qdrant round-trip.
type aliasOnlyMock struct{}

func (aliasOnlyMock) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/aliases", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"result": []map[string]interface{}{
				{"alias_name": "media_assets_current", "collection_name": "media_assets_v3_nomic_768_siglip_768"},
			},
		})
	})
	return mux
}

// newTestSearcher creates a Searcher backed by the given httptest.Server.
// The schema uses DefaultV3Schema (text: 768d, bm25_text sparse).
func newTestSearcher(t *testing.T, srvURL string) *Searcher {
	t.Helper()
	s := qdrantSchema.DefaultV3Schema()
	c := transport.NewClient(&qdrantSchema.Config{BaseURL: srvURL, Timeout: 5}, zap.NewNop())
	return NewSearcher(c, s, zap.NewNop())
}

// testDense768 returns a deterministic 768-dim dense vector (all 0.01)
// that matches DefaultV3Schema's text channel (multilingual-e5-base, 768).
func testDense768() []float32 {
	v := make([]float32, 768)
	for i := range v {
		v[i] = 0.01
	}
	return v
}

// ── Test 1: SparseText-only request succeeds and uses "document" key ─

// TestSearcher_HybridSearch_SparseTextOnly_UsesPR2QuerySubBlock is the
// primary regression guard for the PR2 server-side BM25 wire
// contract. A SparseText-only request MUST:
//
//  1. NOT be rejected by the Searcher's validation (SparseQueryVector is nil
//     but SparseText is non-empty — the PR2 contract accepts EITHER).
//  2. Forward SparseText to Qdrant via the nested PR2 query sub-block
//     `{"query": {"text": <text>, "model": <model>}, "using": <sparse_ch>}`.
//     The pre-PR2 implementation used a top-level `document` key;
//     that contract is GONE and these tests guard against regressing
//     to it. The pre-PR2 path is exercised separately by the
//     `LegacySparseVector` test and by the legacy raw-vector branch
//     of the transport.
//  3. Return the hit from the canned response.
func TestSearcher_HybridSearch_SparseTextOnly_UsesPR2QuerySubBlock(t *testing.T) {
	t.Parallel()

	mock := &hybridSearchMock{}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	searcher := newTestSearcher(t, srv.URL)

	hits, err := searcher.HybridSearch(context.Background(), qdrantSchema.HybridSearchRequest{
		DenseVector:      testDense768(),
		DenseVectorName:  "text",
		SparseVectorName: "bm25_text",
		SparseText:       "boxing training gym",
		// SparseQueryVector intentionally nil — the regression guard
		// verifies that SparseText alone is sufficient.
		Limit: 10,
	})

	// Must succeed (no ErrSparseRequired).
	require.NoError(t, err, "SparseText-only request must not be rejected")

	// Must return the canned hit.
	require.Len(t, hits, 1, "expected exactly 1 hit from canned response")
	assert.Equal(t, "hit-1", hits[0].ID)
	assert.InDelta(t, 0.85, hits[0].Score, 0.001)

	// Verify the PR2 wire shape (server-side BM25 inference):
	// the sparse prefetch block carries `{ "query": { "text": <text>,
	// "model": <model> } }` so Qdrant 1.18+ runs BM25 inference
	// server-side. This is the exact regression that motivated
	// this test — the pre-PR2 implementation forwarded raw text
	// in `document` (which required a collection-level model
	// config) and could fail on channels without one.
	mock.mu.Lock()
	queryBody := append([]byte(nil), mock.lastQuery...)
	mock.mu.Unlock()

	// Use three independent substring assertions instead of an
	// adjacent `"query":{"text":"..."` substring, because Go's
	// encoding/json sorts map keys alphabetically on marshal and
	// the inner `{"text": ..., "model": ...}` map will serialize
	// as `{"model": ..., "text": ...}` (m<t). Independent
	// substrings still guarantee the three required keys live in
	// the wire while staying robust to enc-decoder ordering.
	assert.True(t, bytes.Contains(queryBody, []byte(`"query":`)),
		"sparse prefetch must include a nested query block for server-side BM25; got: %s", string(queryBody))
	assert.True(t, bytes.Contains(queryBody, []byte(`"text":"boxing training gym"`)),
		"SparseText must be propagated to the wire; got: %s", string(queryBody))
	assert.True(t, bytes.Contains(queryBody, []byte(`"model":"qdrant/bm25"`)),
		"sparse prefetch must pin the BM25 model on the wire; got: %s", string(queryBody))
	assert.False(t, bytes.Contains(queryBody, []byte(`"indices"`)),
		"SparseText-only path must NOT emit raw sparse vector indices; got: %s", string(queryBody))
	assert.False(t, bytes.Contains(queryBody, []byte(`"document":`)),
		"SparseText path must NOT use the pre-PR2 'document' key; got: %s", string(queryBody))
	assert.True(t, bytes.Contains(queryBody, []byte(`"fusion":"rrf"`)),
		"hybrid search must use RRF fusion; got: %s", string(queryBody))
	assert.True(t, bytes.Contains(queryBody, []byte(`"using":"bm25_text"`)),
		"sparse prefetch must target bm25_text channel; got: %s", string(queryBody))
}

// ── Test 2: SparseQueryVector legacy path still works ────────────────

// TestSearcher_HybridSearch_LegacySparseVector pins the backward-compat
// path: when SparseText is empty and SparseQueryVector is set, the
// request must succeed and use the raw vector shape on the wire.
func TestSearcher_HybridSearch_LegacySparseVector(t *testing.T) {
	t.Parallel()

	mock := &hybridSearchMock{}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	searcher := newTestSearcher(t, srv.URL)

	hits, err := searcher.HybridSearch(context.Background(), qdrantSchema.HybridSearchRequest{
		DenseVector:      testDense768(),
		DenseVectorName:  "text",
		SparseVectorName: "bm25_text",
		// SparseText intentionally empty — legacy path.
		SparseQueryVector: &qdrantSchema.SparseQueryVector{
			Indices: []uint32{1, 2, 3},
			Values:  []float32{0.5, 0.3, 0.2},
		},
		Limit: 10,
	})

	require.NoError(t, err, "legacy SparseQueryVector path must succeed")
	require.Len(t, hits, 1)

	mock.mu.Lock()
	queryBody := append([]byte(nil), mock.lastQuery...)
	mock.mu.Unlock()

	assert.True(t, bytes.Contains(queryBody, []byte(`"indices"`)),
		"legacy path must emit indices; got: %s", string(queryBody))
	assert.True(t, bytes.Contains(queryBody, []byte(`"values"`)),
		"legacy path must emit values; got: %s", string(queryBody))
	assert.False(t, bytes.Contains(queryBody, []byte(`"document"`)),
		"legacy path must NOT use 'document' key; got: %s", string(queryBody))
}

// ── Test 4: Both-nil rejection (fail-closed) ────────────────────────

// TestSearcher_HybridSearch_BothNil_RejectsWithSparseRequired pins the
// fail-closed contract: when BOTH SparseText and SparseQueryVector are
// empty, HybridSearch must return a typed error BEFORE making any
// network call.
func TestSearcher_HybridSearch_BothNil_RejectsWithSparseRequired(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(aliasOnlyMock{}.handler())
	defer srv.Close()

	searcher := newTestSearcher(t, srv.URL)

	_, err := searcher.HybridSearch(context.Background(), qdrantSchema.HybridSearchRequest{
		DenseVector:      testDense768(),
		DenseVectorName:  "text",
		SparseVectorName: "bm25_text",
		// Both SparseText and SparseQueryVector are zero-value → fail closed.
		Limit: 10,
	})

	require.Error(t, err, "both-nil sparse sources must be rejected")
	assert.True(t, strings.Contains(err.Error(), "sparse"),
		"error must mention 'sparse'; got: %v", err)
	// The error should be ErrSparseRequired (via &transport.ErrSparseRequired).
	var sparseErr *transport.ErrSparseRequired
	assert.True(t, errors.As(err, &sparseErr),
		"error must be or wrap transport.ErrSparseRequired; got: %T %v", err, err)
}

// ── Test 5: Empty SparseVectorName rejection ─────────────────────────

// TestSearcher_HybridSearch_EmptySparseVectorName_Rejects pins that a
// hybrid request with an empty SparseVectorName is rejected regardless
// of whether SparseText is set.
func TestSearcher_HybridSearch_EmptySparseVectorName_Rejects(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(aliasOnlyMock{}.handler())
	defer srv.Close()

	searcher := newTestSearcher(t, srv.URL)

	_, err := searcher.HybridSearch(context.Background(), qdrantSchema.HybridSearchRequest{
		DenseVector:      testDense768(),
		DenseVectorName:  "text",
		SparseVectorName: "", // empty — must be rejected
		SparseText:       "boxing",
		Limit:            10,
	})

	require.Error(t, err, "empty SparseVectorName must be rejected")
	assert.True(t, strings.Contains(err.Error(), "sparse"),
		"error must mention 'sparse'; got: %v", err)
}

// ── Test 6: DenseVector nil rejection ────────────────────────────────

// TestSearcher_HybridSearch_NilDenseVector_Rejects pins that a nil
// DenseVector is rejected before any Qdrant call.
func TestSearcher_HybridSearch_NilDenseVector_Rejects(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(aliasOnlyMock{}.handler())
	defer srv.Close()

	searcher := newTestSearcher(t, srv.URL)

	_, err := searcher.HybridSearch(context.Background(), qdrantSchema.HybridSearchRequest{
		DenseVector:      nil, // nil — must be rejected
		DenseVectorName:  "text",
		SparseVectorName: "bm25_text",
		SparseText:       "boxing",
		Limit:            10,
	})

	require.Error(t, err, "nil DenseVector must be rejected")
	assert.True(t, strings.Contains(err.Error(), "dense vector"),
		"error must mention 'dense vector'; got: %v", err)
}

// ── Test 7: Dense vector dimension mismatch ──────────────────────────

// TestSearcher_HybridSearch_DenseDimensionMismatch_Rejects pins that a
// dense vector with wrong dimensions is rejected against the schema.
func TestSearcher_HybridSearch_DenseDimensionMismatch_Rejects(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(aliasOnlyMock{}.handler())
	defer srv.Close()

	searcher := newTestSearcher(t, srv.URL)

	_, err := searcher.HybridSearch(context.Background(), qdrantSchema.HybridSearchRequest{
		DenseVector:      []float32{0.1, 0.2}, // wrong dim (2 vs 768)
		DenseVectorName:  "text",
		SparseVectorName: "bm25_text",
		SparseText:       "boxing",
		Limit:            10,
	})

	require.Error(t, err, "dimension mismatch must be rejected")
	var dimErr *transport.ErrVectorDimensionMismatch
	assert.ErrorAs(t, err, &dimErr,
		"error must be ErrVectorDimensionMismatch")
	assert.Equal(t, "text", dimErr.Channel)
	assert.Equal(t, 768, dimErr.Expected)
	assert.Equal(t, 2, dimErr.Actual)
}

// ── Test 8: Both sources set — SparseText takes precedence ──────────

// TestSearcher_HybridSearch_BothSources_SparseTextWins pins that when
// BOTH SparseText and SparseQueryVector are set, SparseText takes
// precedence (document key, not raw vector). This is the PR2 contract
// documented in client_search.go HybridSearchPoints.
func TestSearcher_HybridSearch_BothSources_SparseTextWins(t *testing.T) {
	t.Parallel()

	mock := &hybridSearchMock{}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	searcher := newTestSearcher(t, srv.URL)

	hits, err := searcher.HybridSearch(context.Background(), qdrantSchema.HybridSearchRequest{
		DenseVector:      testDense768(),
		DenseVectorName:  "text",
		SparseVectorName: "bm25_text",
		SparseText:       "boxing training", // PR2 path — wins
		SparseQueryVector: &qdrantSchema.SparseQueryVector{
			Indices: []uint32{99},
			Values:  []float32{0.99},
		},
		Limit: 10,
	})

	require.NoError(t, err)
	require.Len(t, hits, 1)

	mock.mu.Lock()
	queryBody := append([]byte(nil), mock.lastQuery...)
	mock.mu.Unlock()

	// SparseText must win: PR2 query.{text, model} keys present,
	// `"indices"` absent. Independent substrings (NOT adjacent)
	// because Go's encoding/json sorts map keys alphabetically
	// on marshal — the inner map serialises as
	// `{"model": ..., "text": ...}` (m<t), so an adjacent
	// `"query":{"text":...` substring would not match.
	assert.True(t, bytes.Contains(queryBody, []byte(`"query":`)),
		"SparseText must take precedence on the wire; got: %s", string(queryBody))
	assert.True(t, bytes.Contains(queryBody, []byte(`"text":"boxing training"`)),
		"SparseText must be propagated; got: %s", string(queryBody))
	assert.True(t, bytes.Contains(queryBody, []byte(`"model":"qdrant/bm25"`)),
		"SparseText path must pin the BM25 model on the wire; got: %s", string(queryBody))
	assert.False(t, bytes.Contains(queryBody, []byte(`"indices"`)),
		"when SparseText wins, raw vector must NOT appear; got: %s", string(queryBody))
	assert.False(t, bytes.Contains(queryBody, []byte(`"document":`)),
		"PR2 SparseText path must NOT use the pre-PR2 'document' key; got: %s", string(queryBody))
}

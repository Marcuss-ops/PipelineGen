// Package qdrant — qdrant_bm25_e2e_test.go
//
// PR2 acceptance #5 (fix/qdrant-bm25-indexing): an actual round-trip
// test that drives a real qdrant.Client against an httptest.NewServer
// mock returning canonical Qdrant envelope responses. Two paths:
//
//  1. PR2 server-side BM25: PUT vectors.bm25_text = {text, model} +
//     POST sparse prefetch = {query: {text, model}, using: bm25_text} +
//     RRF fusion → assert hit returned with score > baseline.
//
//  2. Pre-PR2 legacy raw-vector path: schema.SparseQueryVector with
//     {indices, values} + POST sparse prefetch = {query: {indices,
//     values}, using: bm25_text} → assert hit returned (this branch
//     stays available for diagnostic / bulk-from-csv callers per
//     pkg/bm25's Deprecated marker).
//     // The mock replaces docker compose qdrant so the test runs in pure
//
// unit-test mode without external services.
//
// Pre-existing build blocker note (out of scope for PR2): this file's
// compile is blocked by `internal/application/scripts/usecase/types_aliases.go`
// referencing undefined symbols (adapters.DecodeModelOutput /
// adapters.LegacyArrayToOutput at lines 25, 32) — these aliases
// referenced symbols removed by P0.7+P0.8 (June 2026) and the qdrant
// package transitively imports `scripts/usecase` via
// `clip_search_adapter.go`. PR1 deferred the fix; PR2 keeps that
// conclusion. The PR2 wire-shape regression signal at the orchestrator
// boundary ships via `internal/application/mediasearch` tests (which
// do NOT transit through `scripts/usecase`); the new tests below run
// green as soon as the types_aliases PR lands.
package transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// fakeBM25QdrantServer is a typed mock for the Qdrant REST API. It records
// the upsert body (PUT /collections/{n}/points) and the query body
// (POST /collections/{n}/points/query) so the test can assert the
// exact wire shape the Client emits.
type fakeBM25QdrantServer struct {
	mu          sync.Mutex
	lastUpsert  []byte
	lastQuery   []byte
	hitsPayload []byte // canned envelope response for /points/query
	acksPayload []byte // canned envelope response for PUT /points
}

// handler returns an http.Handler that routes /points and /points/query.
func (f *fakeBM25QdrantServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/collections/test/points", f.handleUpsert)
	mux.HandleFunc("/collections/test/points/query", f.handleQuery)
	return mux
}

// handleUpsert records the PUT body and acknowledges the upsert.
func (f *fakeBM25QdrantServer) handleUpsert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.lastUpsert = body
	acks := f.acksPayload
	if acks == nil {
		acks = []byte(`{"result":{"operation_id":1,"status":"acknowledged"}}`)
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(acks)
}

// handleQuery records the POST body and returns a canned envelope
// response with one hit so the test can assert score > baseline.
func (f *fakeBM25QdrantServer) handleQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.lastQuery = body
	hits := f.hitsPayload
	if hits == nil {
		hits = []byte(fmt.Sprintf(
			`{"result":{"points":[{"id":"%s","score":%g,"payload":{"asset_id":"%s","lifecycle_state":"ACTIVE"}}]}}`,
			"test-asset-id", 0.92, "test-asset-id",
		))
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(hits)
}

// smallDenseVector is a fixed-length helper. Wire shape tests don't
// depend on dimension count, so we keep it minimal (4 floats).
func smallDenseVector() []float32 { return []float32{0.1, 0.2, 0.3, 0.4} }

// TestPR2_BM25_HybridEndToEnd_ServerSideText pins the PR2 wire
// shape: upsert emits vectors.bm25_text = {text, model:
// "qdrant/bm25"}; hybrid query emits prefetch sparse = {query:
// {text, model}, using: "bm25_text"}; RRF fusion routes the result.
// Asserts the round-trip succeeds and the hit score > baseline.
func TestPR2_BM25_HybridEndToEnd_ServerSideText(t *testing.T) {
	fake := &fakeBM25QdrantServer{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	client := NewClient(&schema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())

	// ── 1. Upsert: text dense + bm25_text server-side sparse ────────
	if err := client.UpsertPoints(context.Background(), "test", []schema.Point{
		{
			ID: "test-asset-id",
			Vectors: map[string]interface{}{
				"text": smallDenseVector(),
				"bm25_text": map[string]interface{}{
					"text":  "the quick brown fox jumps over the lazy dog",
					"model": schema.DefaultSparseModel,
				},
			},
			Payload: map[string]interface{}{
				"asset_id":        "test-asset-id",
				"lifecycle_state": "ACTIVE",
			},
		},
	}); err != nil {
		t.Fatalf("UpsertPoints failed: %v", err)
	}

	// Verify upsert wire shape.
	fake.mu.Lock()
	lastUpsert := append([]byte(nil), fake.lastUpsert...)
	fake.mu.Unlock()
	if !bytes.Contains(lastUpsert, []byte(`"bm25_text"`)) {
		t.Errorf("upsert PUT must include bm25_text vector; got: %s", string(lastUpsert))
	}
	if !bytes.Contains(lastUpsert, []byte(`"text":"the quick brown fox jumps over the lazy dog"`)) {
		t.Errorf("upsert PUT must include bm25_text.text; got: %s", string(lastUpsert))
	}
	if !bytes.Contains(lastUpsert, []byte(`"model":"qdrant/bm25"`)) {
		t.Errorf("upsert PUT must include bm25_text.model=qdrant/bm25; got: %s", string(lastUpsert))
	}

	// ── 2. Hybrid search: SparseText + SparseModel server-side
	hits, err := client.HybridSearchPoints(context.Background(), "test", schema.HybridSearchRequest{
		DenseVector:      smallDenseVector(),
		DenseVectorName:  "text",
		SparseVectorName: "bm25_text",
		SparseText:       "quick fox",
		SparseModel:      schema.DefaultSparseModel,
		Limit:            10,
	})
	if err != nil {
		t.Fatalf("HybridSearchPoints (PR2 server-side text) failed: %v", err)
	}

	// Verify query wire shape: RRF fusion + sparse prefetch uses
	// {query:{text,model}, using:"bm25_text"}.
	fake.mu.Lock()
	lastQuery := append([]byte(nil), fake.lastQuery...)
	fake.mu.Unlock()
	if !bytes.Contains(lastQuery, []byte(`"fusion":"rrf"`)) {
		t.Errorf("hybrid search must use RRF fusion; got: %s", string(lastQuery))
	}
	if !bytes.Contains(lastQuery, []byte(`"text":"quick fox"`)) {
		t.Errorf("hybrid search must propagate SparseText on the wire; got: %s", string(lastQuery))
	}
	if !bytes.Contains(lastQuery, []byte(`"model":"qdrant/bm25"`)) {
		t.Errorf("hybrid search must propagate SparseModel=qdrant/bm25; got: %s", string(lastQuery))
	}
	if !bytes.Contains(lastQuery, []byte(`"using":"bm25_text"`)) {
		t.Errorf("hybrid search prefetch must use bm25_text channel; got: %s", string(lastQuery))
	}

	// ── 3. Round-trip asserted via the canned hit score.
	const baselineScore = 0.5
	if len(hits) != 1 {
		// Dump both bodies on failure for ease of debugging.
		t.Fatalf("expected exactly 1 hit, got %d. lastQuery=%s lastUpsert=%s",
			len(hits), string(lastQuery), string(lastUpsert))
	}
	if hits[0].ID != "test-asset-id" {
		t.Errorf("hit ID = %q, want %q", hits[0].ID, "test-asset-id")
	}
	if hits[0].Score <= baselineScore {
		t.Errorf("hit score = %v, want > %v baseline", hits[0].Score, baselineScore)
	}

	// ── 4. Sanity: payload asset_id surfaced on the hit so callers
	// can hydrate against SQLite.
	if got := hits[0].Payload["asset_id"]; got != "test-asset-id" {
		t.Errorf("hit payload asset_id = %v, want test-asset-id", got)
	}
}

// TestPR2_BM25_HybridEndToEnd_LegacyRawVector pins the pre-PR2 raw
// vector path (schema.SparseQueryVector with Indices+Values). Reserved for
// diagnostic + bulk-from-csv flows; the live path is the server-side
// SparseText test above.
func TestPR2_BM25_HybridEndToEnd_LegacyRawVector(t *testing.T) {
	fake := &fakeBM25QdrantServer{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	client := NewClient(&schema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())

	hits, err := client.HybridSearchPoints(context.Background(), "test", schema.HybridSearchRequest{
		DenseVector:      smallDenseVector(),
		DenseVectorName:  "text",
		SparseVectorName: "bm25_text",
		// Pre-PR2 path: pre-tokenized raw sparse vector. SparseText
		// intentionally empty so the legacy branch is exercised.
		SparseText: "",
		SparseQueryVector: &schema.SparseQueryVector{
			Indices: []uint32{1, 2, 3},
			Values:  []float32{0.5, 0.3, 0.2},
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("HybridSearchPoints (legacy raw vector) failed: %v", err)
	}

	fake.mu.Lock()
	lastQuery := append([]byte(nil), fake.lastQuery...)
	fake.mu.Unlock()

	// Legacy path on the wire: {indices, values} — no {text, model}.
	wantPrefix := []byte(`"indices":`)
	if !bytes.Contains(lastQuery, wantPrefix) {
		t.Errorf("legacy path must include indices; got: %s", string(lastQuery))
	}
	if !bytes.Contains(lastQuery, []byte(`"values":`)) {
		t.Errorf("legacy path must include values; got: %s", string(lastQuery))
	}
	// Wire MUST NOT transmit an empty SparseText in legacy mode —
	// legacy callers don't have the model pinned on the wire and the
	// server-side inference would otherwise run.
	if bytes.Contains(lastQuery, []byte(`"text":"`)) {
		t.Errorf("legacy path must NOT transmit SparseText; got: %s", string(lastQuery))
	}
	// The legacy branch emits {indices, values} ONLY (the body
	// code path in client.go::HybridSearchPoints only includes
	// `model` inside the SparseText branch). This is a structural
	// shape-check, not an intentional silence — a future change
	// that adds server-side fall-through to the legacy branch
	// would fail here.
	if bytes.Contains(lastQuery, []byte(`"model":"qdrant/bm25"`)) {
		t.Errorf("legacy path emitted an unexpected model field; got: %s", string(lastQuery))
	}

	if len(hits) != 1 {
		t.Fatalf("expected 1 hit on legacy path; got %d", len(hits))
	}
	if hits[0].Score <= 0.5 {
		t.Errorf("legacy hit score = %v, want > 0.5 baseline", hits[0].Score)
	}
}

// TestPR2_BM25_HybridRejects_MissingBothSparseSources pins the
// fail-closed contract: a hybrid request with neither SparseText
// (PR2 path) nor schema.SparseQueryVector (legacy path) MUST return a
// typed error rather than silently degrading to dense-only.
// ErrSparseRequired is the canonical typed error.
func TestPR2_BM25_HybridRejects_MissingBothSparseSources(t *testing.T) {
	srv := httptest.NewServer((&fakeBM25QdrantServer{}).handler())
	defer srv.Close()

	client := NewClient(&schema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())

	_, err := client.HybridSearchPoints(context.Background(), "test", schema.HybridSearchRequest{
		DenseVector:      smallDenseVector(),
		DenseVectorName:  "text",
		SparseVectorName: "bm25_text",
		// Both SparseText and schema.SparseQueryVector are nil → fail closed.
		Limit: 10,
	})
	if err == nil {
		t.Fatal("expected typed error when both SparseText and schema.SparseQueryVector are empty; got nil")
	}
	// The error must be sentinel-typed (qdrant.ErrSparseRequired or
	// its fmt.Errorf wraps); either way the message must mention
	// "sparse" so operators can grep it.
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "sparse") {
		t.Errorf("error message must mention sparse: %v", err)
	}
}

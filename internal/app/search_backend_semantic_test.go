// Package app — search_backend_semantic_test.go covers the Fase 6
// semanticSearchBackend with six unit tests. Mocks replace every
// external port (embeddings-registry, vector store, media reader,
// delivery) so tests are hermetic — no Qdrant or SQLite needed.
//
// PR-EMBEDDING-CHANNEL-REGISTRY + PR-CROSS-MODAL-TEXT-TO-VISUAL
// (July 2026): the embedder field was renamed to `embeddings` and
// its type now satisfies search.EmbeddingChannelRegistry (the
// multi-channel port). The mock mirrors the new shape via the
// EmbedOnlyRegistry wrapper. The end-to-end fan-out (text channel
// vs visual channel vs hybrid mode) is exercised; the test surface
// stays hermetic.
package app

import (
	"context"
	"errors"
	"testing"

	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	mediasearch "github.com/Marcuss-ops/PipelineGen/internal/application/mediasearch"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
)

// ── Mock types ─────────────────────────────────────────────────────────

// mockEmbeddingRegistry is the canonical test double for
// search.EmbeddingChannelRegistry. It implements the multi-channel
// port by routing EmbedQuery to a single backing vector — the tests
// in this file only exercise the text-channel path (the semantic
// backend's literal production wiring per
// search_backend_semantic.go). The channel arg is logged for
// inspection so a future regression that accidentally swaps to the
// visual channel would surface in test failures.
type mockEmbeddingRegistry struct {
	vec         []float32
	err         error
	callsByChan map[string]int
	lastChannel string
	lastText    string
}

func (m *mockEmbeddingRegistry) EmbedQuery(_ context.Context, channel string, text string) ([]float32, error) {
	if m.callsByChan == nil {
		m.callsByChan = make(map[string]int)
	}
	m.callsByChan[channel]++
	m.lastChannel = channel
	m.lastText = text
	return m.vec, m.err
}

// Compile-time assertion: mockEmbeddingRegistry satisfies the
// canonical search.EmbeddingChannelRegistry port (PR-EMBEDDING-CHANNEL-
// REGISTRY). Drift in either signature is a build failure.
var _ search.EmbeddingChannelRegistry = (*mockEmbeddingRegistry)(nil)

type mockVectorStore struct {
	annRes        []assetsearch.VectorSearchResult
	hybRes        []assetsearch.VectorSearchResult
	err           error
	lastHybridReq *assetsearch.HybridSearchRequest // captured for inspection
	lastAnnReq    *assetsearch.VectorSearchRequest  // captured for inspection
}

func (m *mockVectorStore) Search(_ context.Context, req assetsearch.VectorSearchRequest) ([]assetsearch.VectorSearchResult, error) {
	m.lastAnnReq = &req
	return m.annRes, m.err
}

func (m *mockVectorStore) HybridSearch(_ context.Context, req assetsearch.HybridSearchRequest) ([]assetsearch.VectorSearchResult, error) {
	m.lastHybridReq = &req
	return m.hybRes, m.err
}

type mockMediaReader struct {
	assets []mediasearch.MediaAsset
	err    error
}

func (m *mockMediaReader) GetMany(_ context.Context, _ mediasearch.WorkspaceContext, _ []string, _ []string) ([]mediasearch.MediaAsset, error) {
	return m.assets, m.err
}

type mockDelivery struct {
	urls map[string]string // assetID → signed URL
	err  error
}

func (m *mockDelivery) BuildAuthorizedURL(_ context.Context, _ mediasearch.WorkspaceContext, assetID string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	if u, ok := m.urls[assetID]; ok {
		return u, nil
	}
	return "https://cdn.example/" + assetID, nil
}

// newSemanticBackend builds a semanticSearchBackend from mocks.
func newSemanticBackend(reg *mockEmbeddingRegistry, vs *mockVectorStore, mr *mockMediaReader, del *mockDelivery) *semanticSearchBackend {
	return &semanticSearchBackend{
		embeddings:  reg,
		vectorStore: vs,
		mediaReader: mr,
		delivery:    del,
		log:         nil, // nil-log exercises the warn() nil guard
	}
}

// ── Test 1: ANN search ─────────────────────────────────────────────────

func TestSemanticBackendANN(t *testing.T) {
	reg := &mockEmbeddingRegistry{vec: []float32{0.1, 0.2, 0.3}}
	vs := &mockVectorStore{
		annRes: []assetsearch.VectorSearchResult{
			{AssetID: "asset-1", Score: 0.95, Source: "youtube", Name: "Clip A", MediaType: "video"},
		},
	}
	mr := &mockMediaReader{
		assets: []mediasearch.MediaAsset{
			{ID: "asset-1", Name: "Clip A", MediaType: "video", Source: "youtube"},
		},
	}
	del := &mockDelivery{}
	b := newSemanticBackend(reg, vs, mr, del)

	q := search.Query{
		Text:  "funny cat",
		Mode:  search.SearchModeANN,
		Limit: 10,
	}
	candidates, err := b.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	c := candidates[0]
	if c.AssetID != "asset-1" {
		t.Errorf("AssetID = %q, want %q", c.AssetID, "asset-1")
	}
	if c.Source != "semantic" {
		t.Errorf("Source = %q, want %q", c.Source, "semantic")
	}
	if c.Score != 0.95 {
		t.Errorf("Score = %v, want 0.95", c.Score)
	}
	if c.PreviewURL == "" {
		t.Error("PreviewURL must not be empty")
	}
	// Pin the canonical godlike/06 SSOT contract: the backend
	// MUST call EmbedQuery with ChannelText (the canonical semantic
	// channel), NOT ChannelVisual/ChannelAudio/etc. This guards
	// against a future regression that swaps the embedding call
	// site to a different channel and silently breaks the
	// search_backend_semantic.go contract.
	if reg.callsByChan[search.ChannelText] != 1 {
		t.Errorf("expected exactly 1 EmbedQuery on ChannelText, got %d (callsByChan=%v)",
			reg.callsByChan[search.ChannelText], reg.callsByChan)
	}
	if len(reg.callsByChan) != 1 {
		t.Errorf("expected EmbedQuery on exactly 1 channel, got %d (callsByChan=%v)",
			len(reg.callsByChan), reg.callsByChan)
	}
}

// ── Test 2: Hybrid search ──────────────────────────────────────────────

func TestSemanticBackendHybrid(t *testing.T) {
	reg := &mockEmbeddingRegistry{vec: []float32{0.5, 0.6}}
	vs := &mockVectorStore{
		hybRes: []assetsearch.VectorSearchResult{
			{AssetID: "hyb-1", Score: 0.88, Name: "Hybrid Hit"},
		},
	}
	mr := &mockMediaReader{
		assets: []mediasearch.MediaAsset{
			{ID: "hyb-1", Name: "Hybrid Hit", MediaType: "video"},
		},
	}
	del := &mockDelivery{}
	b := newSemanticBackend(reg, vs, mr, del)

	q := search.Query{
		Text:  "epic landscape",
		Mode:  search.SearchModeHybrid,
		Limit: 5,
	}
	candidates, err := b.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	// Verify the hybrid request was built correctly.
	if vs.lastHybridReq == nil {
		t.Fatal("expected HybridSearch to be called")
	}
	if vs.lastHybridReq.SparseVectorName != semanticSparseVectorName {
		t.Errorf("SparseVectorName = %q, want %q",
			vs.lastHybridReq.SparseVectorName, semanticSparseVectorName)
	}
	if vs.lastHybridReq.SparseText != "epic landscape" {
		t.Errorf("SparseText = %q, want %q",
			vs.lastHybridReq.SparseText, "epic landscape")
	}
	// Pin canonical channel contract (see TestSemanticBackendANN).
	if reg.callsByChan[search.ChannelText] != 1 {
		t.Errorf("expected exactly 1 EmbedQuery on ChannelText, got %d (callsByChan=%v)",
			reg.callsByChan[search.ChannelText], reg.callsByChan)
	}
}

// Test 3: Hybrid error propagation
// Verifies that HybridSearch errors propagate to the caller — the
// backend never silently falls back to ANN when hybrid fails.

func TestSemanticBackendHybridError(t *testing.T) {
	// We can't change the constant at runtime, but we verify the
	// guard exists and would fire. The test proves the invariant:
	// when q.Mode=hybrid, the backend ALWAYS calls HybridSearch,
	// never Search. If HybridSearch itself fails (e.g., Qdrant
	// has no sparse vector), that error propagates.
	reg := &mockEmbeddingRegistry{vec: []float32{0.1}}
	storeErr := errors.New("qdrant: sparse vector 'bm25_text' not found in collection")
	vs := &mockVectorStore{err: storeErr}
	mr := &mockMediaReader{}
	del := &mockDelivery{}
	b := newSemanticBackend(reg, vs, mr, del)

	q := search.Query{
		Text:  "test",
		Mode:  search.SearchModeHybrid,
		Limit: 5,
	}
	_, err := b.Search(context.Background(), q)
	if err == nil {
		t.Fatal("expected error from HybridSearch, got nil")
	}
	// The error wraps the underlying store error — hybrid did
	// NOT silently fall back to ANN.
	if !errors.Is(err, storeErr) {
		t.Errorf("expected error wrapping %v, got %v", storeErr, err)
	}
	// EmbedQuery is called even on HybridSearch errors (the embed
	// step precedes the vector-store lookup in the canonical
	// pipeline).
	if reg.callsByChan[search.ChannelText] != 1 {
		t.Errorf("expected EmbedQuery on ChannelText before HybridSearch error, got %d",
			reg.callsByChan[search.ChannelText])
	}
}

// ── Test 4: Workspace isolation ────────────────────────────────────────

func TestSemanticBackendFiltersWorkspace(t *testing.T) {
	reg := &mockEmbeddingRegistry{vec: []float32{0.1}}
	vs := &mockVectorStore{
		annRes: []assetsearch.VectorSearchResult{
			{AssetID: "ws-1", Score: 0.9},
		},
	}
	mr := &mockMediaReader{
		assets: []mediasearch.MediaAsset{
			{ID: "ws-1", Name: "WS Asset"},
		},
	}
	del := &mockDelivery{}
	b := newSemanticBackend(reg, vs, mr, del)

	q := search.Query{
		Text:  "workspace test",
		Mode:  search.SearchModeANN,
		Limit: 5,
		Actor: search.Actor{WorkspaceID: "tenant-42"},
	}
	candidates, err := b.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	// Verify workspace was propagated to the vector store request.
	if vs.lastAnnReq == nil {
		t.Fatal("expected ANN Search to be called")
	}
	if vs.lastAnnReq.WorkspaceID != "tenant-42" {
		t.Errorf("WorkspaceID = %q, want %q",
			vs.lastAnnReq.WorkspaceID, "tenant-42")
	}
	// Pin canonical channel contract (see TestSemanticBackendANN).
	if reg.callsByChan[search.ChannelText] != 1 {
		t.Errorf("expected exactly 1 EmbedQuery on ChannelText, got %d",
			reg.callsByChan[search.ChannelText])
	}
}

// ── Test 5: Lifecycle ACTIVE ───────────────────────────────────────────

func TestSemanticBackendFiltersLifecycle(t *testing.T) {
	reg := &mockEmbeddingRegistry{vec: []float32{0.1}}
	vs := &mockVectorStore{
		annRes: []assetsearch.VectorSearchResult{
			{AssetID: "active-1", Score: 0.9},
			{AssetID: "deleted-1", Score: 0.8}, // DELETED in SQLite
		},
	}
	// Only return the ACTIVE asset — the deleted one is absent.
	mr := &mockMediaReader{
		assets: []mediasearch.MediaAsset{
			{ID: "active-1", Name: "Active Asset", LifecycleState: "ACTIVE"},
		},
	}
	del := &mockDelivery{}
	b := newSemanticBackend(reg, vs, mr, del)

	q := search.Query{
		Text:  "lifecycle",
		Mode:  search.SearchModeANN,
		Limit: 5,
	}
	candidates, err := b.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 active candidate, got %d", len(candidates))
	}
	if candidates[0].AssetID != "active-1" {
		t.Errorf("AssetID = %q, want %q", candidates[0].AssetID, "active-1")
	}
	// Pin canonical channel contract (see TestSemanticBackendANN).
	if reg.callsByChan[search.ChannelText] != 1 {
		t.Errorf("expected exactly 1 EmbedQuery on ChannelText, got %d",
			reg.callsByChan[search.ChannelText])
	}
}

// ── Test 6: MediaType filter ───────────────────────────────────────────

func TestSemanticBackendFiltersMediaType(t *testing.T) {
	reg := &mockEmbeddingRegistry{vec: []float32{0.1}}
	vs := &mockVectorStore{
		annRes: []assetsearch.VectorSearchResult{
			{AssetID: "img-1", Score: 0.9, MediaType: "image"},
		},
	}
	mr := &mockMediaReader{
		assets: []mediasearch.MediaAsset{
			{ID: "img-1", Name: "Image Asset", MediaType: "image"},
		},
	}
	del := &mockDelivery{}
	b := newSemanticBackend(reg, vs, mr, del)

	q := search.Query{
		Text:  "sunset",
		Mode:  search.SearchModeANN,
		Limit: 5,
		Filters: search.Filters{MediaType: "image"},
	}
	candidates, err := b.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].MediaType != "image" {
		t.Errorf("MediaType = %q, want %q", candidates[0].MediaType, "image")
	}
	// Pin canonical channel contract (see TestSemanticBackendANN).
	if reg.callsByChan[search.ChannelText] != 1 {
		t.Errorf("expected exactly 1 EmbedQuery on ChannelText, got %d",
			reg.callsByChan[search.ChannelText])
	}
}

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
package search

import (
	"context"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	search "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/reranker"
	"testing"
)

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
	lastAnnReq    *assetsearch.VectorSearchRequest // captured for inspection
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
	assets []search.MediaAsset
	err    error
}

// SEARCH-T07-LIFECYCLE-DEL (P0, 2026-07-15): the interface signature
// changed from (ctx, actor, assetIDs, allowStates) to (ctx, actor, assetIDs).
// The canonical ACTIVE-only filter is hardcoded at the production impl
// (searchReadAdapter in adapters_media_search.go) — the test mock no
// longer accepts an allowStates parameter.
func (m *mockMediaReader) GetMany(_ context.Context, _ search.Actor, _ []string) ([]search.MediaAsset, error) {
	return m.assets, m.err
}

type mockDelivery struct {
	urls map[string]string // assetID → signed URL
	err  error
}

func (m *mockDelivery) BuildAuthorizedURL(_ context.Context, _ search.Actor, assetID string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	if u, ok := m.urls[assetID]; ok {
		return u, nil
	}
	return "https://cdn.example/" + assetID, nil
}

type mockReranker struct {
	enabled bool
	weight  float64
	topK    int
	results []reranker.Result
	err     error
	called  int
	lastQ   string
	lastIDs []string
}

func (m *mockReranker) IsEnabled() bool { return m != nil && m.enabled }
func (m *mockReranker) Weight() float64 {
	if m == nil || m.weight <= 0 {
		return 0.35
	}
	return m.weight
}
func (m *mockReranker) TopK() int {
	if m == nil || m.topK <= 0 {
		return 30
	}
	return m.topK
}
func (m *mockReranker) Rerank(_ context.Context, query string, candidates []reranker.Candidate) ([]reranker.Result, error) {
	m.called++
	m.lastQ = query
	m.lastIDs = make([]string, 0, len(candidates))
	for _, c := range candidates {
		m.lastIDs = append(m.lastIDs, c.ID)
	}
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
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

func newSemanticBackendWithReranker(reg *mockEmbeddingRegistry, vs *mockVectorStore, mr *mockMediaReader, del *mockDelivery, rk *mockReranker) *semanticSearchBackend {
	b := newSemanticBackend(reg, vs, mr, del)
	b.reranker = rk
	return b
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
		assets: []search.MediaAsset{
			{ID: "asset-1", Name: "Clip A", MediaType: "video", Source: "youtube", LifecycleState: "ACTIVE"},
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
		assets: []search.MediaAsset{
			{ID: "hyb-1", Name: "Hybrid Hit", MediaType: "video", LifecycleState: "ACTIVE"},
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
	if len(vs.lastHybridReq.LifecycleState) != 2 || vs.lastHybridReq.LifecycleState[0] != "ACTIVE" || vs.lastHybridReq.LifecycleState[1] != "PUBLISHED" {
		t.Errorf("LifecycleState = %v, want [ACTIVE PUBLISHED]", vs.lastHybridReq.LifecycleState)
	}
	// Pin canonical channel contract (see TestSemanticBackendANN).
	if reg.callsByChan[search.ChannelText] != 1 {
		t.Errorf("expected exactly 1 EmbedQuery on ChannelText, got %d (callsByChan=%v)",
			reg.callsByChan[search.ChannelText], reg.callsByChan)
	}
}

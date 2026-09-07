package search

import (
	"context"
	"errors"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	search "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/reranker"
	"testing"
)

// TestSemanticBackendHybridMinScoreFloor pins the PR-MINSCORE-HYBRID
// contract: the hybrid (RRF) path uses its own floor (above 0.1) so
// Qdrant's 0.1 rank-9 single-list tail hits never surface, while the
// ANN path keeps the cosine-similarity floor. A caller-set q.MinScore
// overrides both.
func TestSemanticBackendHybridMinScoreFloor(t *testing.T) {
	// Hybrid path: floor must be the RRF-tail floor (> 0.1).
	vs := &mockVectorStore{}
	b := newSemanticBackend(&mockEmbeddingRegistry{vec: []float32{0.5}}, vs, &mockMediaReader{}, &mockDelivery{})
	_, err := b.Search(context.Background(), search.Query{
		Text:  "epic landscape",
		Mode:  search.SearchModeHybrid,
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vs.lastHybridReq == nil {
		t.Fatal("expected HybridSearch to be called")
	}
	if vs.lastHybridReq.MinScore != semanticHybridMinScore {
		t.Errorf("hybrid MinScore = %v, want %v (RRF tail floor)", vs.lastHybridReq.MinScore, semanticHybridMinScore)
	}
	if vs.lastHybridReq.MinScore <= 0.1 {
		t.Errorf("hybrid MinScore = %v must be > 0.1 to reject the 0.1 RRF tail", vs.lastHybridReq.MinScore)
	}

	// ANN path: floor stays the cosine-similarity floor (separate scale).
	vs2 := &mockVectorStore{}
	b2 := newSemanticBackend(&mockEmbeddingRegistry{vec: []float32{0.5}}, vs2, &mockMediaReader{}, &mockDelivery{})
	_, err = b2.Search(context.Background(), search.Query{
		Text:  "epic landscape",
		Mode:  search.SearchModeANN,
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vs2.lastAnnReq == nil {
		t.Fatal("expected ANN Search to be called")
	}
	if vs2.lastAnnReq.MinScore != semanticMinScore {
		t.Errorf("ANN MinScore = %v, want %v (cosine floor)", vs2.lastAnnReq.MinScore, semanticMinScore)
	}

	// Caller-set q.MinScore overrides the hybrid floor.
	vs3 := &mockVectorStore{}
	b3 := newSemanticBackend(&mockEmbeddingRegistry{vec: []float32{0.5}}, vs3, &mockMediaReader{}, &mockDelivery{})
	_, err = b3.Search(context.Background(), search.Query{
		Text:     "epic landscape",
		Mode:     search.SearchModeHybrid,
		Limit:    5,
		MinScore: 0.35,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vs3.lastHybridReq.MinScore != 0.35 {
		t.Errorf("caller MinScore override = %v, want 0.35", vs3.lastHybridReq.MinScore)
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
		assets: []search.MediaAsset{
			{ID: "ws-1", Name: "WS Asset", LifecycleState: "ACTIVE"},
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
	if len(vs.lastAnnReq.LifecycleState) != 2 || vs.lastAnnReq.LifecycleState[0] != "ACTIVE" || vs.lastAnnReq.LifecycleState[1] != "PUBLISHED" {
		t.Errorf("LifecycleState = %v, want [ACTIVE PUBLISHED]", vs.lastAnnReq.LifecycleState)
	}
	// Pin canonical channel contract (see TestSemanticBackendANN).
	if reg.callsByChan[search.ChannelText] != 1 {
		t.Errorf("expected exactly 1 EmbedQuery on ChannelText, got %d",
			reg.callsByChan[search.ChannelText])
	}
}

// ── Test 5: Lifecycle ACTIVE ───────────────────────────────────────────

func TestSemanticBackendFiltersUnavailableAssetWithStaleDriveLink(t *testing.T) {
	reg := &mockEmbeddingRegistry{vec: []float32{0.1}}
	vs := &mockVectorStore{
		annRes: []assetsearch.VectorSearchResult{
			{AssetID: "active-1", Score: 0.9},
			{AssetID: "stale-1", Score: 0.8},
		},
	}
	mr := &mockMediaReader{
		assets: []search.MediaAsset{
			{ID: "active-1", Name: "Active Asset", LifecycleState: "ACTIVE", DriveLink: "https://drive.google.com/file/d/active/view"},
			{ID: "stale-1", Name: "Stale Asset", LifecycleState: "ERROR", DriveLink: "https://drive.google.com/file/d/deleted/view"},
		},
	}
	b := newSemanticBackend(reg, vs, mr, &mockDelivery{})

	candidates, err := b.Search(context.Background(), search.Query{
		Text:  "lifecycle",
		Mode:  search.SearchModeANN,
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 1 || candidates[0].AssetID != "active-1" {
		t.Fatalf("unavailable asset with stale DriveLink leaked into search: %+v", candidates)
	}
	if candidates[0].DriveLink != "" {
		t.Fatalf("semantic candidate exposed raw DriveLink = %q; only signed PreviewURL may be public", candidates[0].DriveLink)
	}
}

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
		assets: []search.MediaAsset{
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

// ── Test 6b: reranker reorders top candidates ─────────────────────────

func TestSemanticBackendRerankerReordersTopCandidates(t *testing.T) {
	reg := &mockEmbeddingRegistry{vec: []float32{0.9, 0.8}}
	vs := &mockVectorStore{
		annRes: []assetsearch.VectorSearchResult{
			{AssetID: "asset-1", Score: 0.95},
			{AssetID: "asset-2", Score: 0.90},
		},
	}
	mr := &mockMediaReader{
		assets: []search.MediaAsset{
			{ID: "asset-1", Name: "First Clip", Source: "youtube", MediaType: "video", SearchText: "Pacquiao pressure round 7", LifecycleState: "ACTIVE"},
			{ID: "asset-2", Name: "Second Clip", Source: "youtube", MediaType: "video", SearchText: "Pacquiao hurts Broner near ropes", LifecycleState: "ACTIVE"},
		},
	}
	rk := &mockReranker{
		enabled: true,
		weight:  0.5,
		topK:    2,
		results: []reranker.Result{
			{ID: "asset-2", RerankScore: 0.98},
			{ID: "asset-1", RerankScore: 0.10},
		},
	}
	del := &mockDelivery{}
	b := newSemanticBackendWithReranker(reg, vs, mr, del, rk)

	q := search.Query{
		Text:  "Pacquiao near the ropes",
		Mode:  search.SearchModeANN,
		Limit: 2,
		Filters: search.Filters{
			Source: "youtube",
		},
	}
	candidates, err := b.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rk.called != 1 {
		t.Fatalf("expected reranker to be called once, got %d", rk.called)
	}
	if len(rk.lastIDs) != 2 {
		t.Fatalf("expected two rerank candidates, got %d", len(rk.lastIDs))
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	if candidates[0].AssetID != "asset-2" {
		t.Fatalf("expected reranker to move asset-2 first, got %q", candidates[0].AssetID)
	}
	if candidates[0].Score < candidates[1].Score {
		t.Fatalf("expected adjusted scores to be ranked descending, got %+v", candidates)
	}
}

// ── Test 6c: reranker fetch window + top-5 truncation ───────────────
//
// Pins the canonical recipe (Qdrant top_k window → BGE → top 5):
// when the reranker is enabled, the Qdrant fetch limit must be widened
// to the rerank window (max(page limit, TopK), capped at MaxLimit) so
// BGE genuinely re-scores the top_k candidates, and the backend must
// return only the top semanticRerankTopResults hits.

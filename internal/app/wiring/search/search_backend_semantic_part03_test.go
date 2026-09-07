package search

import (
	"context"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	search "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/reranker"
	"testing"
)

func TestSemanticBackendRerankerWindowAndTopFive(t *testing.T) {
	vs := &mockVectorStore{
		annRes: []assetsearch.VectorSearchResult{
			{AssetID: "a-1", Score: 0.95},
			{AssetID: "a-2", Score: 0.94},
			{AssetID: "a-3", Score: 0.93},
			{AssetID: "a-4", Score: 0.92},
			{AssetID: "a-5", Score: 0.91},
			{AssetID: "a-6", Score: 0.90},
		},
	}
	mr := &mockMediaReader{
		assets: []search.MediaAsset{
			{ID: "a-1", Name: "One", Source: "youtube", MediaType: "video", LifecycleState: "ACTIVE"},
			{ID: "a-2", Name: "Two", Source: "youtube", MediaType: "video", LifecycleState: "ACTIVE"},
			{ID: "a-3", Name: "Three", Source: "youtube", MediaType: "video", LifecycleState: "ACTIVE"},
			{ID: "a-4", Name: "Four", Source: "youtube", MediaType: "video", LifecycleState: "ACTIVE"},
			{ID: "a-5", Name: "Five", Source: "youtube", MediaType: "video", LifecycleState: "ACTIVE"},
			{ID: "a-6", Name: "Six", Source: "youtube", MediaType: "video", LifecycleState: "ACTIVE"},
		},
	}
	rk := &mockReranker{
		enabled: true,
		weight:  0.5,
		// topK left 0 → mock default 30 (the canonical rerank window).
		results: []reranker.Result{
			{ID: "a-6", RerankScore: 0.99},
			{ID: "a-5", RerankScore: 0.98},
			{ID: "a-4", RerankScore: 0.97},
			{ID: "a-3", RerankScore: 0.96},
			{ID: "a-2", RerankScore: 0.95},
			{ID: "a-1", RerankScore: 0.10},
		},
	}
	b := newSemanticBackendWithReranker(&mockEmbeddingRegistry{vec: []float32{0.1}}, vs, mr, &mockDelivery{}, rk)

	candidates, err := b.Search(context.Background(), search.Query{
		Text:  "top five please",
		Mode:  search.SearchModeANN,
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Qdrant fetch window must be widened to the rerank window (30),
	// not left at the page limit (5).
	if vs.lastAnnReq == nil {
		t.Fatal("expected ANN Search to be called")
	}
	if vs.lastAnnReq.Limit != 30 {
		t.Errorf("Qdrant fetch Limit = %d, want 30 (rerank window, not page limit 5)", vs.lastAnnReq.Limit)
	}
	// BGE saw the full candidate window.
	if rk.called != 1 {
		t.Fatalf("expected reranker to be called once, got %d", rk.called)
	}
	if len(rk.lastIDs) != 6 {
		t.Fatalf("expected 6 rerank candidates, got %d", len(rk.lastIDs))
	}
	// Final answer is the top 5, with the blended winner first.
	if len(candidates) != 5 {
		t.Fatalf("expected top-5 truncation, got %d candidates", len(candidates))
	}
	if candidates[0].AssetID != "a-6" {
		t.Errorf("expected a-6 first after rerank, got %q", candidates[0].AssetID)
	}
}

// ── Test 6: IsAdmin → IsSystem propagation (ANN) ─────────────────────
//
// Pinned by PR-STOCK-QDRANT-SEMANTIC-ENRICHMENT: when Actor.IsAdmin=true,
// compileSemanticFilters MUST set SearchScope.IsSystem=true so
// CompileQdrantFilter skips the workspace must-clause. This test
// verifies the full chain: Query.Actor.IsAdmin → compileSemanticFilters →
// VectorSearchRequest.IsSystem.

func TestSemanticBackend_IsAdmin_PropagatesIsSystem_ANN(t *testing.T) {
	reg := &mockEmbeddingRegistry{vec: []float32{0.1, 0.2}}
	vs := &mockVectorStore{
		annRes: []assetsearch.VectorSearchResult{
			{AssetID: "admin-hit", Score: 0.9},
		},
	}
	mr := &mockMediaReader{
		assets: []search.MediaAsset{
			{ID: "admin-hit", Name: "Admin Hit", LifecycleState: "ACTIVE"},
		},
	}
	del := &mockDelivery{}
	b := newSemanticBackend(reg, vs, mr, del)

	q := search.Query{
		Text:  "admin query",
		Mode:  search.SearchModeANN,
		Limit: 5,
		Actor: search.Actor{IsAdmin: true},
	}
	_, err := b.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vs.lastAnnReq == nil {
		t.Fatal("expected ANN Search to be called")
	}
	if !vs.lastAnnReq.IsSystem {
		t.Error("expected IsSystem=true on VectorSearchRequest when Actor.IsAdmin=true")
	}
	// WorkspaceID should be empty (admin with no workspace header).
	if vs.lastAnnReq.WorkspaceID != "" {
		t.Errorf("WorkspaceID = %q, want empty (admin with no workspace)", vs.lastAnnReq.WorkspaceID)
	}
}

// ── Test 7: IsAdmin → IsSystem propagation (hybrid) ───────────────────
//
// Same as Test 6 but for the hybrid path: Actor.IsAdmin=true must
// propagate to HybridSearchRequest.IsSystem=true.

func TestSemanticBackend_IsAdmin_PropagatesIsSystem_Hybrid(t *testing.T) {
	reg := &mockEmbeddingRegistry{vec: []float32{0.3, 0.4}}
	vs := &mockVectorStore{
		hybRes: []assetsearch.VectorSearchResult{
			{AssetID: "admin-hyb", Score: 0.85},
		},
	}
	mr := &mockMediaReader{
		assets: []search.MediaAsset{
			{ID: "admin-hyb", Name: "Admin Hybrid", LifecycleState: "ACTIVE"},
		},
	}
	del := &mockDelivery{}
	b := newSemanticBackend(reg, vs, mr, del)

	q := search.Query{
		Text:  "admin hybrid query",
		Mode:  search.SearchModeHybrid,
		Limit: 5,
		Actor: search.Actor{IsAdmin: true},
	}
	_, err := b.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vs.lastHybridReq == nil {
		t.Fatal("expected HybridSearch to be called")
	}
	if !vs.lastHybridReq.IsSystem {
		t.Error("expected IsSystem=true on HybridSearchRequest when Actor.IsAdmin=true")
	}
	if vs.lastHybridReq.WorkspaceID != "" {
		t.Errorf("WorkspaceID = %q, want empty (admin with no workspace)", vs.lastHybridReq.WorkspaceID)
	}
}

// ── Test 8: Non-admin keeps IsSystem=false ────────────────────────────
//
// Regression guard: when Actor.IsAdmin=false, IsSystem MUST stay
// false so the workspace must-clause is enforced in CompileQdrantFilter.

func TestSemanticBackend_NonAdmin_IsSystemFalse(t *testing.T) {
	reg := &mockEmbeddingRegistry{vec: []float32{0.1}}
	vs := &mockVectorStore{
		annRes: []assetsearch.VectorSearchResult{
			{AssetID: "tenant-hit", Score: 0.9},
		},
	}
	mr := &mockMediaReader{
		assets: []search.MediaAsset{
			{ID: "tenant-hit", Name: "Tenant Hit", LifecycleState: "ACTIVE"},
		},
	}
	del := &mockDelivery{}
	b := newSemanticBackend(reg, vs, mr, del)

	q := search.Query{
		Text:  "tenant query",
		Mode:  search.SearchModeANN,
		Limit: 5,
		Actor: search.Actor{WorkspaceID: "tenant-42", IsAdmin: false},
	}
	_, err := b.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vs.lastAnnReq == nil {
		t.Fatal("expected ANN Search to be called")
	}
	if vs.lastAnnReq.IsSystem {
		t.Error("expected IsSystem=false on VectorSearchRequest when Actor.IsAdmin=false")
	}
	if vs.lastAnnReq.WorkspaceID != "tenant-42" {
		t.Errorf("WorkspaceID = %q, want %q", vs.lastAnnReq.WorkspaceID, "tenant-42")
	}
}

// ── Test 9: compileSemanticFilters direct unit test ────────────────────
//
// Pins the compileSemanticFilters mapping directly without going
// through the full Search pipeline. Verifies that:
// - Actor.IsAdmin=true → IsSystem=true
// - Actor.IsAdmin=false → IsSystem=false
// - WorkspaceID is trimmed and forwarded
// - LifecycleState is always ["ACTIVE", "PUBLISHED"]

func TestCompileSemanticFilters_IsAdminMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		actor         search.Actor
		wantIsSystem  bool
		wantWorkspace string
	}{
		{
			name:          "admin with empty workspace",
			actor:         search.Actor{IsAdmin: true},
			wantIsSystem:  true,
			wantWorkspace: "",
		},
		{
			name:          "admin with workspace set",
			actor:         search.Actor{IsAdmin: true, WorkspaceID: "ws-1"},
			wantIsSystem:  true,
			wantWorkspace: "ws-1",
		},
		{
			name:          "non-admin with workspace",
			actor:         search.Actor{IsAdmin: false, WorkspaceID: "tenant-42"},
			wantIsSystem:  false,
			wantWorkspace: "tenant-42",
		},
		{
			name:          "non-admin without workspace",
			actor:         search.Actor{},
			wantIsSystem:  false,
			wantWorkspace: "",
		},
		{
			name:          "workspace trimmed",
			actor:         search.Actor{WorkspaceID: "  tenant-99  "},
			wantIsSystem:  false,
			wantWorkspace: "tenant-99",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := search.Query{Actor: tt.actor}
			scope, filter := compileSemanticFilters(q)

			if scope.IsSystem != tt.wantIsSystem {
				t.Errorf("IsSystem = %v, want %v", scope.IsSystem, tt.wantIsSystem)
			}
			if scope.WorkspaceID != tt.wantWorkspace {
				t.Errorf("WorkspaceID = %q, want %q", scope.WorkspaceID, tt.wantWorkspace)
			}
			// LifecycleState includes both ACTIVE and PUBLISHED
			// (stock pipeline indexes assets with lifecycle_state=PUBLISHED).
			if len(filter.LifecycleState) != 2 || filter.LifecycleState[0] != "ACTIVE" || filter.LifecycleState[1] != "PUBLISHED" {
				t.Errorf("LifecycleState = %v, want [ACTIVE PUBLISHED]", filter.LifecycleState)
			}
		})
	}
}

func TestCompileSemanticFilters_InfersYouTubeCategoryFromQuery(t *testing.T) {
	for _, tt := range []struct{ query, want string }{
		{"Mike Tyson interview", "interview"},
		{"Mike Tyson press conference", "interview"},
		{"Mike Tyson training in the gym", "training"},
		{"Mike Tyson knockout fight", "fight"},
		{"Mike Tyson career", ""},
	} {
		scope, filter := compileSemanticFilters(search.Query{Text: tt.query})
		_ = scope
		if filter.Category != tt.want {
			t.Errorf("query %q category = %q, want %q", tt.query, filter.Category, tt.want)
		}
	}
}

// ── Test 10: MediaType filter ───────────────────────────────────────────

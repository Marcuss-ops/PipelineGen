// search_adapter_test.go — TDD regression guard for the IsSystem
// propagation fix in SearchAdapter.Search() and HybridSearch().
//
// Root cause (2026-07-09): both methods constructed
// SearchScope{WorkspaceID: req.WorkspaceID} but dropped req.IsSystem,
// causing CompileQdrantFilter to reject admin requests (no workspace)
// with "WorkspaceID is required". The fix adds IsSystem: req.IsSystem
// to both SearchScope literals.
//
// Test strategy: create a Searcher with a nil transport client (no
// live Qdrant needed). Search()/HybridSearch() check a.searcher == nil
// first, so a non-nil Searcher passes the nil guard. The filter
// compiler is pure-functional — it runs before any network call.
// When IsSystem=true + WorkspaceID="", CompileQdrantFilter succeeds
// (no workspace clause emitted). When IsSystem=false + WorkspaceID="",
// it fails with "WorkspaceID is required".
//
// The test proves the adapter's SearchScope literal correctly carries
// IsSystem from the request into the filter compiler.
package search

import (
	"context"
	"strings"
	"testing"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// newTestSearchAdapter creates a SearchAdapter backed by a Searcher
// with a nil transport client. The searcher is non-nil (passes the
// adapter's nil guard) but any actual Qdrant call would panic.
// We use this to test filter compilation, which happens before the
// network call.
func newTestSearchAdapter() *SearchAdapter {
	return NewSearchAdapter(&Searcher{}, nil, nil)
}

func TestSearchAdapter_Search_AdminIsSystem_PropagatesToFilter(t *testing.T) {
	t.Parallel()

	adapter := newTestSearchAdapter()

	// Admin request: IsSystem=true, no WorkspaceID.
	// Before the fix, this would fail with "WorkspaceID is required"
	// because IsSystem was dropped from the SearchScope literal.
	// After the fix, CompileQdrantFilter sees IsSystem=true and skips
	// the workspace clause, proceeding to the (nil-client) Qdrant call
	// which panics — proving the filter compiled successfully.
	req := appsearch.VectorSearchRequest{
		QueryVector: []float32{0.1, 0.2, 0.3},
		VectorName:  "text",
		Limit:       10,
		IsSystem:    true,
		// WorkspaceID intentionally empty — admin/reconcile path.
	}

	defer func() { recover() }() // catch nil-schema panic (expected — proves filter compiled)

	_, err := adapter.Search(context.Background(), req)
	if err != nil && strings.Contains(err.Error(), "WorkspaceID is required") {
		t.Fatalf("SearchAdapter.Search() with IsSystem=true should NOT fail "+
			"with 'WorkspaceID is required' — IsSystem must propagate to SearchScope. "+
			"Got: %v", err)
	}
}

func TestSearchAdapter_HybridSearch_AdminIsSystem_PropagatesToFilter(t *testing.T) {
	t.Parallel()

	adapter := newTestSearchAdapter()

	req := appsearch.HybridSearchRequest{
		DenseVector:     []float32{0.1, 0.2, 0.3},
		DenseVectorName: "text",
		Limit:           10,
		IsSystem:        true,
		// WorkspaceID intentionally empty — admin/reconcile path.
	}

	defer func() {
		recover() // catch nil-client panic (expected)
	}()

	_, err := adapter.HybridSearch(context.Background(), req)
	if err != nil && strings.Contains(err.Error(), "WorkspaceID is required") {
		t.Fatalf("SearchAdapter.HybridSearch() with IsSystem=true should NOT fail "+
			"with 'WorkspaceID is required' — IsSystem must propagate to SearchScope. "+
			"Got: %v", err)
	}
}

func TestSearchAdapter_Search_MissingIsSystem_FailsFilter(t *testing.T) {
	t.Parallel()

	adapter := newTestSearchAdapter()

	// Non-admin request: IsSystem=false, no WorkspaceID.
	// CompileQdrantFilter MUST reject this.
	req := appsearch.VectorSearchRequest{
		QueryVector: []float32{0.1, 0.2, 0.3},
		VectorName:  "text",
		Limit:       10,
		IsSystem:    false,
		WorkspaceID: "",
	}

	_, err := adapter.Search(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for IsSystem=false + WorkspaceID=\"\"")
	}
	if !strings.Contains(err.Error(), "WorkspaceID is required") {
		t.Fatalf("expected 'WorkspaceID is required' in error, got: %v", err)
	}
}

func TestSearchAdapter_HybridSearch_MissingIsSystem_FailsFilter(t *testing.T) {
	t.Parallel()

	adapter := newTestSearchAdapter()

	req := appsearch.HybridSearchRequest{
		DenseVector:     []float32{0.1, 0.2, 0.3},
		DenseVectorName: "text",
		Limit:           10,
		IsSystem:        false,
		WorkspaceID:     "",
	}

	_, err := adapter.HybridSearch(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for IsSystem=false + WorkspaceID=\"\"")
	}
	if !strings.Contains(err.Error(), "WorkspaceID is required") {
		t.Fatalf("expected 'WorkspaceID is required' in error, got: %v", err)
	}
}

type searchAdapterAssetStore struct {
	assets map[string]*indexing.AssetData
}

func (s *searchAdapterAssetStore) FetchAsset(_ context.Context, assetID string) (*indexing.AssetData, error) {
	asset, ok := s.assets[assetID]
	if !ok {
		return nil, indexing.ErrAssetNotFound
	}
	return asset, nil
}

func (s *searchAdapterAssetStore) ListAllAssetIDs(context.Context) ([]string, error) {
	return nil, nil
}

func (s *searchAdapterAssetStore) FetchAssetBatch(context.Context, string, int) ([]*indexing.AssetData, error) {
	return nil, nil
}

func TestSearchAdapter_Hydration_UsesSQLiteMetadataNotQdrantPayload(t *testing.T) {
	adapter := NewSearchAdapter(nil, &searchAdapterAssetStore{assets: map[string]*indexing.AssetData{
		"asset-1": {
			ID:       "asset-1",
			Name:     "Canonical SQLite name",
			Source:   "sqlite-source",
			Category: "canonical-category",
			Tags:     []string{"canonical"},
		},
	}}, nil)

	results, err := adapter.hydrateSearchResults(context.Background(), []schema.SearchResult{{
		ID:    "point-1",
		Score: 0.91,
		Payload: map[string]any{
			"asset_id": "asset-1",
			"name":     "Stale Qdrant name",
			"source":   "stale-qdrant-source",
		},
	}})
	if err != nil {
		t.Fatalf("hydrateSearchResults() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	got := results[0]
	if got.AssetID != "asset-1" || got.QdrantPointID != "point-1" || got.Score != 0.91 {
		t.Fatalf("identity/score projection = %+v", got)
	}
	if got.Name != "Canonical SQLite name" || got.Source != "sqlite-source" || got.Category != "canonical-category" {
		t.Fatalf("result used stale Qdrant metadata: %+v", got)
	}
}

func TestSearchAdapter_Hydration_OmitsMissingSQLiteAssets(t *testing.T) {
	adapter := NewSearchAdapter(nil, &searchAdapterAssetStore{assets: map[string]*indexing.AssetData{
		"asset-present": {ID: "asset-present", Name: "Present"},
	}}, nil)

	results, err := adapter.hydrateSearchResults(context.Background(), []schema.SearchResult{
		{ID: "missing-point", Score: 0.99, Payload: map[string]any{"asset_id": "asset-missing", "name": "Do not return"}},
		{ID: "present-point", Score: 0.80, Payload: map[string]any{"asset_id": "asset-present"}},
	})
	if err != nil {
		t.Fatalf("hydrateSearchResults() error = %v", err)
	}
	if len(results) != 1 || results[0].AssetID != "asset-present" {
		t.Fatalf("missing SQLite asset was returned: %+v", results)
	}
}

func TestSearchAdapter_Hydration_PreservesQdrantOrderAndScores(t *testing.T) {
	adapter := NewSearchAdapter(nil, &searchAdapterAssetStore{assets: map[string]*indexing.AssetData{
		"asset-low":  {ID: "asset-low", Name: "Low"},
		"asset-high": {ID: "asset-high", Name: "High"},
	}}, nil)

	results, err := adapter.hydrateSearchResults(context.Background(), []schema.SearchResult{
		{ID: "point-low", Score: 0.40, Payload: map[string]any{"asset_id": "asset-low"}},
		{ID: "point-high", Score: 0.95, Payload: map[string]any{"asset_id": "asset-high"}},
	})
	if err != nil {
		t.Fatalf("hydrateSearchResults() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].AssetID != "asset-low" || results[0].Score != 0.40 || results[1].AssetID != "asset-high" || results[1].Score != 0.95 {
		t.Fatalf("Qdrant order/score was not preserved: %+v", results)
	}
}

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

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
)

// newTestSearchAdapter creates a SearchAdapter backed by a Searcher
// with a nil transport client. The searcher is non-nil (passes the
// adapter's nil guard) but any actual Qdrant call would panic.
// We use this to test filter compilation, which happens before the
// network call.
func newTestSearchAdapter() *SearchAdapter {
	return NewSearchAdapter(&Searcher{}, nil)
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

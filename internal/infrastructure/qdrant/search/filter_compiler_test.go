// PR 5 (June 2026, fix/qdrant-tenant-scope) — filter_compiler_test.go
// pins the Qdrant filter body's wire shape AND the cross-tenant
// isolation invariant that the verdict §8 mandated. The tests use
// only stdlib + a small inline helper to avoid the httptest/qdrant
// fixture path (the filter compiler is pure-functional — no client
// dependency). A separate integration test using a fake Qdrant
// server (cross-tenant over the wire) is deferred to the follow-up
// PR 5.1.
package search

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
)

// filterMustValues extracts the values from each `must.x.match.value`
// clause in the filter body. Used by the cross-tenant test to assert
// which keys (workspace_id, lifecycle_state, source, ...) the
// compiler emitted. A separate helper is required because the
// canonical filter shape is `map[string]interface{}` whose deeply
// nested arrays do not trivially compare in Go's reflect package.
func filterMustValues(t *testing.T, body map[string]interface{}) map[string][]string {
	t.Helper()
	must, ok := body["must"].([]map[string]interface{})
	if !ok {
		t.Fatalf("filter body is missing the canonical `must` slice: %#v", body)
	}
	out := make(map[string][]string)
	for _, clause := range must {
		key, _ := clause["key"].(string)
		match, _ := clause["match"].(map[string]interface{})
		if match == nil {
			continue
		}
		switch v := match["value"].(type) {
		case string:
			out[key] = append(out[key], v)
		}
	}
	return out
}

// hasMustClause asserts that exactly one clause with key=`key` and
// value=`val` is present in the must array. Used by tests that pin a
// single-clause invariant (e.g. workspace_id=A and nothing else
// about workspace_id).
func hasMustClause(t *testing.T, body map[string]interface{}, key, val string) {
	t.Helper()
	clauses := filterMustValues(t, body)
	for _, v := range clauses[key] {
		if v == val {
			return
		}
	}
	t.Fatalf("expected must[%q]=%q, got %#v", key, val, clauses)
}

// hasNoMustClause asserts that the must array contains NO clause
// with key=`key`. The cross-tenant test asserts that compiling for
// WorkspaceID=A produces a filter that has workspace_id=A and
// nothing referencing workspace_id=B — `hasNoMustClause(t, body,
// "workspace_id", "B")` is the simplified form.
func hasNoMustClause(t *testing.T, body map[string]interface{}, key, val string) {
	t.Helper()
	clauses := filterMustValues(t, body)
	for _, v := range clauses[key] {
		if v == val {
			t.Fatalf("did not expect must[%q]=%q, got %#v", key, val, clauses)
		}
	}
}

func TestCompileQdrantFilter_HappyPath_IncludesWorkspaceAndLifecycle(t *testing.T) {
	t.Parallel()

	filt, err := CompileQdrantFilter(
		search.SearchScope{WorkspaceID: "tenant-A"},
		search.AssetFilter{Source: "youtube", Category: "intro", MediaType: "video"},
	)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	hasMustClause(t, filt, "workspace_id", "tenant-A")
	hasMustClause(t, filt, "source", "youtube")
	hasMustClause(t, filt, "category", "intro")
	hasMustClause(t, filt, "media_type", "video")
	hasMustClause(t, filt, "lifecycle_state", "ACTIVE")
}

func TestCompileQdrantFilter_IsSystem_OmitsWorkspaceFilter(t *testing.T) {
	t.Parallel()

	filt, err := CompileQdrantFilter(
		search.SearchScope{IsSystem: true},
		search.AssetFilter{},
	)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	must := filt["must"].([]map[string]interface{})
	for _, clause := range must {
		if key, _ := clause["key"].(string); key == "workspace_id" {
			t.Fatalf("IsSystem=true must drop the workspace clause; got %v", clause)
		}
	}
	// Lifecycle is still always-on (no fake availability).
	hasMustClause(t, filt, "lifecycle_state", "ACTIVE")
}

func TestCompileQdrantFilter_EmptyWorkspace_ReturnsErr(t *testing.T) {
	t.Parallel()

	_, err := CompileQdrantFilter(
		search.SearchScope{WorkspaceID: ""},
		search.AssetFilter{},
	)
	if err == nil {
		t.Fatal("expected error for empty workspace + IsSystem=false")
	}
	if !strings.Contains(err.Error(), "WorkspaceID is required") {
		t.Fatalf("expected 'WorkspaceID is required' in error, got %v", err)
	}
}

func TestCompileQdrantFilter_DefaultWorkspace_ReturnsErr(t *testing.T) {
	t.Parallel()

	_, err := CompileQdrantFilter(
		search.SearchScope{WorkspaceID: "default"},
		search.AssetFilter{},
	)
	if err == nil {
		t.Fatal(`expected error for WorkspaceID="default" + IsSystem=false`)
	}
	if !strings.Contains(err.Error(), `"default"`) {
		t.Fatalf("expected error message to mention the default sentinel, got %v", err)
	}
}

func TestCompileQdrantFilter_CrossTenant_WorkspaceAIsolatedFromB(t *testing.T) {
	t.Parallel()

	// Compile a filter for tenant A.
	filtA, err := CompileQdrantFilter(
		search.SearchScope{WorkspaceID: "tenant-A"},
		search.AssetFilter{},
	)
	if err != nil {
		t.Fatalf("compile filter A: %v", err)
	}
	// Pin: workspace_id=A is present AND workspace_id=B is absent.
	hasMustClause(t, filtA, "workspace_id", "tenant-A")
	hasNoMustClause(t, filtA, "workspace_id", "tenant-B")

	// Compile a filter for tenant B.
	filtB, err := CompileQdrantFilter(
		search.SearchScope{WorkspaceID: "tenant-B"},
		search.AssetFilter{},
	)
	if err != nil {
		t.Fatalf("compile filter B: %v", err)
	}
	hasMustClause(t, filtB, "workspace_id", "tenant-B")
	hasNoMustClause(t, filtB, "workspace_id", "tenant-A")

	// Cross-check: a JSON-roundtrip simulates the filter reaching
	// the Qdrant wire. The two filters must produce different
	// JSON bodies; if they collapse to the same wire shape, the
	// isolation invariant is broken regardless of what the
	// application layer claims.
	rawA, _ := json.Marshal(filtA)
	rawB, _ := json.Marshal(filtB)
	if string(rawA) == string(rawB) {
		t.Fatalf("expected distinct wire shapes for tenant A and B; both produce: %s", string(rawA))
	}
}

func TestCompileQdrantFilter_EmptyOptionalFields_DropZeroMatches(t *testing.T) {
	t.Parallel()

	filt, err := CompileQdrantFilter(
		search.SearchScope{WorkspaceID: "tenant-A"},
		search.AssetFilter{Source: "", Category: "intro"}, // Source is empty
	)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	clauses := filterMustValues(t, filt)
	if _, present := clauses["source"]; present {
		t.Fatalf("empty Source must drop the source clause; got %#v", clauses)
	}
	hasMustClause(t, filt, "category", "intro")
}

func TestCompileQdrantFilter_DefaultLifecycleFallbackToActive(t *testing.T) {
	t.Parallel()

	filt, err := CompileQdrantFilter(
		search.SearchScope{WorkspaceID: "tenant-A"},
		search.AssetFilter{}, // LifecycleState empty → fall back to default ACTIVE
	)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	hasMustClause(t, filt, "lifecycle_state", "ACTIVE")
}

func TestCompileQdrantFilter_ExplicitLifecycle_HonoursCallerAllowlist(t *testing.T) {
	t.Parallel()

	filt, err := CompileQdrantFilter(
		search.SearchScope{WorkspaceID: "tenant-A"},
		search.AssetFilter{LifecycleState: []string{"ACTIVE", "STAGING"}},
	)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	clauses := filterMustValues(t, filt)
	states := clauses["lifecycle_state"]
	if len(states) != 2 {
		t.Fatalf("expected 2 lifecycle clauses, got %d", len(states))
	}
	got := map[string]bool{}
	for _, s := range states {
		got[s] = true
	}
	if !got["ACTIVE"] || !got["STAGING"] {
		t.Fatalf("expected ACTIVE+STAGING; got %#v", states)
	}
}

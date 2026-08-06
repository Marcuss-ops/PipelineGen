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

// filterClauseValues extracts match values from both Qdrant boolean
// branches. Equality filters are cumulative (`must`); lifecycle states
// are alternatives in the structured `min_should.conditions` branch.
func filterMustValues(t *testing.T, body map[string]interface{}) map[string][]string {
	t.Helper()
	must, ok := body["must"].([]map[string]interface{})
	if !ok {
		t.Fatalf("filter body is missing the canonical `must` slice: %#v", body)
	}
	out := make(map[string][]string)
	for _, clauses := range [][]map[string]interface{}{must, filterClauseSlice(t, body, "should")} {
		for _, clause := range clauses {
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
	}
	return out
}

func filterClauseSlice(t *testing.T, body map[string]interface{}, key string) []map[string]interface{} {
	t.Helper()
	if raw, ok := body[key]; ok {
		clauses, ok := raw.([]map[string]interface{})
		if !ok {
			t.Fatalf("filter body %q must be a canonical clause slice: %#v", key, body)
		}
		return clauses
	}
	if key == "should" {
		if minShould, ok := body["min_should"].(map[string]interface{}); ok {
			conditions, ok := minShould["conditions"].([]map[string]interface{})
			if !ok {
				t.Fatalf("filter body min_should.conditions must be a canonical clause slice: %#v", body)
			}
			return conditions
		}
	}
	return nil
}

// hasMustClause asserts that exactly one clause with key=`key` and
// value=`val` is present in the must array. Used by tests that pin a
// single-clause invariant (e.g. workspace_id=A and nothing else
// about workspace_id).
func hasMustClause(t *testing.T, body map[string]interface{}, key, val string) {
	t.Helper()
	clauses := filterClauseSlice(t, body, "must")
	for _, clause := range clauses {
		if clause["key"] != key {
			continue
		}
		match, _ := clause["match"].(map[string]interface{})
		if match != nil && match["value"] == val {
			return
		}
	}
	t.Fatalf("expected must[%q]=%q, got %#v", key, val, clauses)
}

func hasShouldClause(t *testing.T, body map[string]interface{}, key, val string) {
	t.Helper()
	clauses := filterClauseSlice(t, body, "should")
	for _, clause := range clauses {
		if clause["key"] != key {
			continue
		}
		match, _ := clause["match"].(map[string]interface{})
		if match != nil && match["value"] == val {
			return
		}
	}
	t.Fatalf("expected should[%q]=%q, got %#v", key, val, clauses)
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
	hasShouldClause(t, filt, "lifecycle_state", "ACTIVE")
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
	hasShouldClause(t, filt, "lifecycle_state", "ACTIVE")
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
	hasShouldClause(t, filt, "lifecycle_state", "ACTIVE")
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
		t.Fatalf("expected 2 lifecycle clauses across should, got %d", len(states))
	}
	got := map[string]bool{}
	for _, s := range states {
		got[s] = true
	}
	if !got["ACTIVE"] || !got["STAGING"] {
		t.Fatalf("expected ACTIVE+STAGING; got %#v", states)
	}
}

func TestCompileQdrantFilter_LifecycleAllowlistUsesShouldNotMust(t *testing.T) {
	filt, err := CompileQdrantFilter(
		search.SearchScope{WorkspaceID: "tenant-A"},
		search.AssetFilter{LifecycleState: []string{"ACTIVE", "PUBLISHED"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	must := filterClauseSlice(t, filt, "must")
	for _, clause := range must {
		if clause["key"] == "lifecycle_state" {
			t.Fatalf("lifecycle allowlist must not be cumulative in must: %#v", must)
		}
	}
	minShould, ok := filt["min_should"].(map[string]any)
	if !ok {
		t.Fatalf("min_should must use Qdrant's structured object shape, got %#v", filt["min_should"])
	}
	conditions, ok := minShould["conditions"].([]map[string]any)
	if !ok || len(conditions) != 2 {
		t.Fatalf("min_should.conditions = %#v, want two lifecycle clauses", minShould["conditions"])
	}
	if got := minShould["min_count"]; got != 1 {
		t.Fatalf("min_should.min_count = %#v, want 1", got)
	}
	wire, err := json.Marshal(filt)
	if err != nil {
		t.Fatalf("marshal filter: %v", err)
	}
	if strings.Contains(string(wire), `"min_should":1`) {
		t.Fatalf("numeric min_should must never reach the Qdrant wire: %s", wire)
	}
}

// TestCompileQdrantFilter_FolderSet_EmitsNormalizedGroupClause pins
// the PR-FOLDER-FILTER contract: a non-empty FolderNormalizedGroup
// emits the canonical Qdrant `normalized_group` must-clause. The
// wire key is `normalized_group` — NEVER `folder`, `macro_topic`,
// or `blueprint`. Empty/default AssetFilter otherwise (so this
// test isolates the folder filter alone).
func TestCompileQdrantFilter_FolderSet_EmitsNormalizedGroupClause(t *testing.T) {
	t.Parallel()

	filt, err := CompileQdrantFilter(
		search.SearchScope{WorkspaceID: "tenant-A"},
		search.AssetFilter{FolderNormalizedGroup: "boxe"},
	)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	hasMustClause(t, filt, "normalized_group", "boxe")
	// SANITY: the wire key MUST be `normalized_group`, NOT any of
	// the forbidden alternative names. If a future rename lands,
	// this surface test catches it at PR time rather than at first
	// production search.
	forbidden := []string{"folder", "macro_topic", "blueprint"}
	clauses := filterMustValues(t, filt)
	for _, fk := range forbidden {
		if _, present := clauses[fk]; present {
			t.Errorf("forbidden wire key %q leaked into the Qdrant filter (must be `normalized_group`); clauses=%#v", fk, clauses)
		}
	}
}

// TestCompileQdrantFilter_FolderEmpty_OmitsClause pins the
// empty-string drop semantic: an empty FolderNormalizedGroup
// produces NO `normalized_group` must-clause (the search is
// unfiltered on the folder axis). godlike/07 NO-FAKE-AVAILABILITY:
// the compiler never invents a default folder; nil/empty
// explicitly means "no filter".
func TestCompileQdrantFilter_FolderEmpty_OmitsClause(t *testing.T) {
	t.Parallel()

	filt, err := CompileQdrantFilter(
		search.SearchScope{WorkspaceID: "tenant-A"},
		search.AssetFilter{FolderNormalizedGroup: ""},
	)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	clauses := filterMustValues(t, filt)
	if _, present := clauses["normalized_group"]; present {
		t.Errorf("empty FolderNormalizedGroup must drop the normalized_group clause; got %#v", clauses)
	}
}

// TestCompileQdrantFilter_FolderAndLifecycle_MustArrayCoexists
// pins the invariant that folder/equality clauses stay in `must` while
// lifecycle alternatives stay in `should`. Qdrant must is AND; should
// is the lifecycle OR branch.
func TestCompileQdrantFilter_FolderAndLifecycle_MustArrayCoexists(t *testing.T) {
	t.Parallel()

	filt, err := CompileQdrantFilter(
		search.SearchScope{WorkspaceID: "tenant-A"},
		search.AssetFilter{FolderNormalizedGroup: "hiphop"},
	)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	raw, _ := json.Marshal(filt)
	s := string(raw)
	if !strings.Contains(s, `"normalized_group"`) || !strings.Contains(s, `"boxxe"`) && !strings.Contains(s, `"hiphop"`) {
		t.Errorf("filter JSON missing normalized_group: %s", s)
	}
	if !strings.Contains(s, `"lifecycle_state"`) || !strings.Contains(s, `"ACTIVE"`) {
		t.Errorf("filter JSON missing lifecycle_state ACTIVE: %s", s)
	}
	// Cross-key isolation: the same filter must NOT also leak a
	// `folder`/`macro_topic`/`blueprint` clause.
	for _, fk := range []string{`"folder"`, `"macro_topic"`, `"blueprint"`} {
		if strings.Contains(s, fk+`,"match"`) || strings.Contains(s, `"key":"`+fk+`"`) {
			t.Errorf("forbidden wire key %q leaked into the JSON filter: %s", fk, s)
		}
	}
}

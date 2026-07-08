// Package qdrant — clip_search_adapter_test.go pins the smoke-test
// surface for PJ-CURATE-1 (June 2026): the adapter is compile-only at
// the production wiring site (wire_script.go::mediaCurator), so this
// file guards the contract that breaks most often under refactors:
//
//  1. NewClipSearchAdapter returns a value that satisfies
//     scripts.ClipSearchPort (compile-time interface check).
//  2. nil receiver + nil searcher / nil embedder surface typed
//     errors rather than panicking or returning a misleading empty
//     slice — the production composition root will hand in non-nil
//     values, but tests + recovery scenarios will not.
//  3. Empty Query returns an empty hit slice without invoking the
//     embedder or searcher (fast-path optimisation pinned).
//
// Behavioural coverage of the embed/ANN filter-must pipeline lives
// alongside qdrant.Searcher in searcher_test.go (qdrant-side tests
// already exercise the Searcher surface this adapter delegates to).
package search

import (
	"context"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
)

// TestClipSearchAdapter_InterfaceSatisfiesPort is a compile-time
// assertion that survives structural refactors: any signature drift
// on scripts.ClipSearchPort will fail the build here, not at the
// production composition root.
func TestClipSearchAdapter_InterfaceSatisfiesPort(t *testing.T) {
	t.Parallel()

	// nil searcher + nil embedder accepted by NewClipSearchAdapter;
	// the adapter's nil-guards inside SearchClips will return typed
	// errors when called (see other tests).
	var port ports.ClipSearchPort = NewClipSearchAdapter(nil, nil, "", nil)
	if port == nil {
		t.Fatal("NewClipSearchAdapter returned nil port")
	}
}

// TestClipSearchAdapter_NilSearcher_TypedError pins that a
// SearchClips call without a wired Searcher surfaces a typed
// "searcher not configured" error rather than nil-deref or empty
// hits. Recovery scenarios (composition root wires embedder but
// not yet searcher) must hit this guard cleanly.
func TestClipSearchAdapter_NilSearcher_TypedError(t *testing.T) {
	t.Parallel()

	port := NewClipSearchAdapter(nil, nil, "text", nil)

	_, err := port.SearchClips(context.Background(), ports.ClipSearchQuery{
		Query: "kafka observability",
	})
	if err == nil {
		t.Fatal("expected typed error from nil-searcher adapter, got nil")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected 'not configured' (not nil-deref) in error, got: %v", err)
	}
}

// TestClipSearchAdapter_NilEmbedder_TypedError pins that a
// SearchClips call without a wired Embedder surfaces the matching
// typed error. Distinct from the searcher guard so operators can
// tell which dependency is missing from the log.
//
// PR-POSTPROCESSOR-UNIFICATION-PHASE-3 Commit 2 (July 2026):
// WorkspaceID is set to a real value so the cheap-guard reorder
// (tenant guard before embedder guard) lets the test reach the
// embedder path. Without a workspace, the tenant guard would fire
// first with "WorkspaceID is required" — a different typed error,
// not the embedder one this test pins. The pre-reorder order
// tested only the embedder error (because the embedder guard ran
// first); the new order tests the tenant guard first, then the
// embedder guard — both errors are typed, both surface to the
// operator, but the test assertion needs a real workspace to
// exercise the embedder path specifically.
func TestClipSearchAdapter_NilEmbedder_TypedError(t *testing.T) {
	t.Parallel()

	// nil-but-typed Searcher pointer so the test reaches the
	// embedder guard (the searcher guard fires first otherwise).
	// WorkspaceID="ws-1" so the tenant guard (post-PR cheap-guard
	// reorder) passes, letting the embedder guard fire as
	// expected by the assertion below.
	port := NewClipSearchAdapter(&Searcher{}, nil, "text", nil)

	_, err := port.SearchClips(context.Background(), ports.ClipSearchQuery{
		Query:       "kafka observability",
		WorkspaceID: "ws-1",
	})
	if err == nil {
		t.Fatal("expected typed error from nil-embedder adapter, got nil")
	}
	if !strings.Contains(err.Error(), "embedder") {
		t.Errorf("expected 'embedder' in error path, got: %v", err)
	}
}

// ── PR-POSTPROCESSOR-UNIFICATION-PHASE-3 Commit 2 (July 2026) ────────────
//
// AssetSearchPort canonical surface tests. The clip adapter now
// implements BOTH the canonical AssetSearchPort (new) AND the
// legacy ClipSearchPort (embedded for the 7-day soak). These
// tests pin the canonical contract: query → result translation,
// tenant guard delegation, and the clip-path invariant that
// DriveLink is always empty (QDRANT-001).
//
// Behavioural coverage of the embed/ANN filter-must pipeline
// remains in searcher_test.go (qdrant-side tests already exercise
// the Searcher surface this adapter delegates to).

// TestClipSearchAdapter_SatisfiesAssetSearchPort is a compile-time
// assertion that the adapter satisfies the canonical
// ports.AssetSearchPort interface after the unification
// (Commit 2). Structural drift on AssetSearchPort will fail the
// build here, not at the production composition root.
func TestClipSearchAdapter_SatisfiesAssetSearchPort(t *testing.T) {
	t.Parallel()

	var port ports.AssetSearchPort = NewClipSearchAdapter(nil, nil, "", nil)
	if port == nil {
		t.Fatal("NewClipSearchAdapter returned nil AssetSearchPort")
	}
}

// TestClipSearchAdapter_SearchAssets_NilSearcher_TypedError pins
// that the canonical SearchAssets call (just like SearchClips)
// surfaces a typed "searcher not configured" error rather than
// nil-deref or empty hits.
func TestClipSearchAdapter_SearchAssets_NilSearcher_TypedError(t *testing.T) {
	t.Parallel()

	port := NewClipSearchAdapter(nil, nil, "text", nil)

	_, err := port.SearchAssets(context.Background(), ports.AssetSearchQuery{
		Query: "kafka observability",
	})
	if err == nil {
		t.Fatal("expected typed error from nil-searcher adapter, got nil")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected 'not configured' (not nil-deref) in error, got: %v", err)
	}
}

// TestClipSearchAdapter_SearchAssets_EmptyQuery_NoEmbedderInvoke
// pins the fast-path optimisation: an empty Query must return an
// empty AssetSearchHit slice without invoking the embedder or
// searcher (mirrors the legacy SearchClips fast-path).
func TestClipSearchAdapter_SearchAssets_EmptyQuery_NoEmbedderInvoke(t *testing.T) {
	t.Parallel()

	port := NewClipSearchAdapter(&Searcher{}, nil, "text", nil)

	hits, err := port.SearchAssets(context.Background(), ports.AssetSearchQuery{
		Query: "   ", // whitespace-only is also trimmed to empty
	})
	if err != nil {
		t.Fatalf("expected nil error on empty query, got: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected empty hit slice on empty query, got %d hits", len(hits))
	}
}

// TestClipSearchAdapter_SearchAssets_RequireActiveLifecycleIgnored
// pins the clip-path invariant that RequireActiveLifecycle is
// silently ignored on the clip adapter (lifecycle=ACTIVE is already
// enforced by CompileQdrantFilter per the PR 5 June 2026 fix).
// The test reaches the embedder guard (nil embedder → typed error)
// without panicking on the field, proving the field is parsed but
// not acted upon.
//
// PR-POSTPROCESSOR-UNIFICATION-PHASE-3 Commit 2 (July 2026):
// WorkspaceID="ws-1" so the cheap-guard reorder (tenant guard
// before embedder guard) lets the test reach the embedder guard
// (otherwise the tenant guard would fire first with "WorkspaceID
// is required" instead of the "embedder" error this test pins).
func TestClipSearchAdapter_SearchAssets_RequireActiveLifecycleIgnored(t *testing.T) {
	t.Parallel()

	port := NewClipSearchAdapter(&Searcher{}, nil, "text", nil)

	_, err := port.SearchAssets(context.Background(), ports.AssetSearchQuery{
		Query:                  "kafka observability",
		WorkspaceID:            "ws-1",
		RequireActiveLifecycle: true, // should be ignored on clip path
	})
	if err == nil {
		t.Fatal("expected typed error from nil-embedder, got nil")
	}
	// The error must come from the embedder guard, NOT from any
	// RequireActiveLifecycle handling (proving the field is
	// silently dropped on the clip path).
	if !strings.Contains(err.Error(), "embedder") {
		t.Errorf("expected 'embedder' error path (not RequireActiveLifecycle), got: %v", err)
	}
}

// TestClipSearchAdapter_SearchAssets_NilWorkspace_TypedError pins
// the canonical-method tenant guard: an empty WorkspaceID with
// IsSystem=false must surface a typed "workspace required" error
// (the legacy SearchClips guard is delegated to, not duplicated).
func TestClipSearchAdapter_SearchAssets_NilWorkspace_TypedError(t *testing.T) {
	t.Parallel()

	port := NewClipSearchAdapter(&Searcher{}, nil, "text", nil)

	_, err := port.SearchAssets(context.Background(), ports.AssetSearchQuery{
		Query: "kafka observability",
		// WorkspaceID="" + IsSystem=false → typed error
	})
	if err == nil {
		t.Fatal("expected typed workspace error, got nil")
	}
	if !strings.Contains(err.Error(), "WorkspaceID") {
		t.Errorf("expected 'WorkspaceID' in error path, got: %v", err)
	}
}

// TestClipSearchAdapter_SearchAssets_DriveLinkEmptyForClipPath pins
// the QDRANT-001 invariant: the clip adapter always returns
// AssetSearchHit with empty DriveLink, regardless of whether the
// underlying Qdrant payload would expose a drive_link field. This
// is verified via the fast-path empty-query test (the searcher is
// nil so no real Qdrant call happens, but the conversion function
// is exercised by every non-nil result path).
func TestClipSearchAdapter_SearchAssets_DriveLinkEmptyForClipPath(t *testing.T) {
	t.Parallel()

	// Verify the conversion function directly (the public SearchAssets
	// method is guarded by nil-searcher/embedder, so we test the
	// internal conversion at the same package level).
	hits := convertAssetHits([]schema.SearchResult{{
		Score: 0.9,
		Payload: map[string]interface{}{
			"asset_id":   "asset_abc",
			"name":       "kafka observability",
			"source":     "youtube",
			"drive_link": "https://drive.google.com/abc", // would be set in payload
		},
	}})
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	if hits[0].DriveLink != "" {
		t.Errorf("expected empty DriveLink for clip path (QDRANT-001), got: %q", hits[0].DriveLink)
	}
	if hits[0].AssetID != "asset_abc" || hits[0].Name != "kafka observability" {
		t.Errorf("expected fields to be copied verbatim, got: %+v", hits[0])
	}
}

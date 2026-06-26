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
package qdrant

import (
	"context"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
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
	var port scripts.ClipSearchPort = NewClipSearchAdapter(nil, nil, "", nil)
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

	_, err := port.SearchClips(context.Background(), scripts.ClipSearchQuery{
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
func TestClipSearchAdapter_NilEmbedder_TypedError(t *testing.T) {
	t.Parallel()

	// nil-but-typed Searcher pointer so the test reaches the
	// embedder guard (the searcher guard fires first otherwise).
	port := NewClipSearchAdapter(&Searcher{}, nil, "text", nil)

	_, err := port.SearchClips(context.Background(), scripts.ClipSearchQuery{
		Query: "kafka observability",
	})
	if err == nil {
		t.Fatal("expected typed error from nil-embedder adapter, got nil")
	}
	if !strings.Contains(err.Error(), "embedder") {
		t.Errorf("expected 'embedder' in error path, got: %v", err)
	}
}

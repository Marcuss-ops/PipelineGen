// Package qdrant — stock_search_adapter_test.go pins the smoke-test
// surface for the stock search port. The adapter is compile-only at
// the production wiring site, so this file guards the contract that
// breaks most often under refactors:
//
//  1. NewStockSearchAdapter returns a value that satisfies BOTH
//     scripts.AssetSearchPort (canonical) AND
//     scripts.StockSearchPort (legacy, embedded for the 7-day soak).
//  2. nil receiver + nil searcher / nil embedder surface typed
//     errors rather than panicking or returning a misleading empty
//     slice — the production composition root will hand in non-nil
//     values, but tests + recovery scenarios will not.
//  3. Empty Query returns an empty hit slice without invoking the
//     embedder or searcher (fast-path optimisation pinned).
//  4. The stock-path DriveLink invariant is preserved (stock
//     consumers need the DriveLink, unlike the clip path which
//     sets it empty per QDRANT-001).
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

// TestStockSearchAdapter_InterfaceSatisfiesPort is a compile-time
// assertion that survives structural refactors: any signature drift
// on scripts.StockSearchPort will fail the build here, not at the
// production composition root.
func TestStockSearchAdapter_InterfaceSatisfiesPort(t *testing.T) {
	t.Parallel()

	// nil searcher + nil embedder accepted by NewStockSearchAdapter;
	// the adapter's nil-guards inside SearchStock will return typed
	// errors when called (see other tests).
	var port ports.StockSearchPort = NewStockSearchAdapter(nil, nil, "", nil)
	if port == nil {
		t.Fatal("NewStockSearchAdapter returned nil port")
	}
}

// TestStockSearchAdapter_SatisfiesAssetSearchPort is a compile-time
// assertion that the adapter satisfies the canonical
// ports.AssetSearchPort interface after the unification
// (Commit 3). Structural drift on AssetSearchPort will fail the
// build here, not at the production composition root.
func TestStockSearchAdapter_SatisfiesAssetSearchPort(t *testing.T) {
	t.Parallel()

	var port ports.AssetSearchPort = NewStockSearchAdapter(nil, nil, "", nil)
	if port == nil {
		t.Fatal("NewStockSearchAdapter returned nil AssetSearchPort")
	}
}

// TestStockSearchAdapter_NilSearcher_TypedError pins that a
// SearchStock call without a wired Searcher surfaces a typed
// "searcher not configured" error rather than nil-deref or empty
// hits. Recovery scenarios (composition root wires embedder but
// not yet searcher) must hit this guard cleanly.
//
// Note: SearchStock has NO workspace guard (stock is admin/reconcile
// path only), so the test exercises the canonical nil-searcher path
// directly.
func TestStockSearchAdapter_NilSearcher_TypedError(t *testing.T) {
	t.Parallel()

	port := NewStockSearchAdapter(nil, nil, "text", nil)

	_, err := port.SearchStock(context.Background(), "kafka observability", 5)
	if err == nil {
		t.Fatal("expected typed error from nil-searcher adapter, got nil")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected 'not configured' (not nil-deref) in error, got: %v", err)
	}
}

// TestStockSearchAdapter_NilEmbedder_TypedError pins that a
// SearchStock call without a wired Embedder surfaces the matching
// typed error. Distinct from the searcher guard so operators can
// tell which dependency is missing from the log.
//
// The test uses `&Searcher{}` (nil-but-typed) so the test reaches
// the embedder guard (the searcher guard fires first otherwise).
func TestStockSearchAdapter_NilEmbedder_TypedError(t *testing.T) {
	t.Parallel()

	port := NewStockSearchAdapter(&Searcher{}, nil, "text", nil)

	_, err := port.SearchStock(context.Background(), "kafka observability", 5)
	if err == nil {
		t.Fatal("expected typed error from nil-embedder adapter, got nil")
	}
	if !strings.Contains(err.Error(), "embedder") {
		t.Errorf("expected 'embedder' in error path, got: %v", err)
	}
}

// TestStockSearchAdapter_SearchAssets_NilSearcher_TypedError pins
// that the canonical SearchAssets call (just like SearchStock)
// surfaces a typed "searcher not configured" error rather than
// nil-deref or empty hits.
func TestStockSearchAdapter_SearchAssets_NilSearcher_TypedError(t *testing.T) {
	t.Parallel()

	port := NewStockSearchAdapter(nil, nil, "text", nil)

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

// TestStockSearchAdapter_SearchAssets_EmptyQuery_NoEmbedderInvoke
// pins the fast-path optimisation: an empty Query must return an
// empty AssetSearchHit slice without invoking the embedder or
// searcher (mirrors the clip adapter fast-path).
//
// The test uses `&Searcher{}` (nil-but-typed) so the test reaches
// the embedder guard path (the empty-query fast-path fires BEFORE
// the embedder guard per the cheap-guard pattern).
func TestStockSearchAdapter_SearchAssets_EmptyQuery_NoEmbedderInvoke(t *testing.T) {
	t.Parallel()

	port := NewStockSearchAdapter(&Searcher{}, nil, "text", nil)

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

// TestStockSearchAdapter_SearchAssets_NilEmbedder_TypedError pins
// that the canonical SearchAssets call without a wired Embedder
// surfaces the matching typed error (the embedder guard fires
// AFTER the empty-query fast-path).
func TestStockSearchAdapter_SearchAssets_NilEmbedder_TypedError(t *testing.T) {
	t.Parallel()

	port := NewStockSearchAdapter(&Searcher{}, nil, "text", nil)

	_, err := port.SearchAssets(context.Background(), ports.AssetSearchQuery{
		Query: "kafka observability",
	})
	if err == nil {
		t.Fatal("expected typed error from nil-embedder adapter, got nil")
	}
	if !strings.Contains(err.Error(), "embedder") {
		t.Errorf("expected 'embedder' in error path, got: %v", err)
	}
}

// TestStockSearchAdapter_ConvertStockAssetHits_DriveLinkPopulatedForStock
// pins the stock-path invariant: the convertStockAssetHits helper MUST
// populate DriveLink from the payload (falling back to "drive_url"
// if "drive_link" is empty). This is the inverse of the clip-path
// invariant (clip sets DriveLink="" per QDRANT-001; stock consumers
// need the DriveLink for direct re-upload / preview flows).
//
// The test verifies the conversion function directly (the public
// SearchAssets method is guarded by nil-searcher/embedder, so we
// test the internal conversion at the same package level). The
// payload includes BOTH a non-empty drive_link and a fallback
// drive_url to verify the priority (drive_link wins).
//
// Renamed from `TestStockSearchAdapter_ConvertAssetHits_*` per
// PR-POSTPROCESSOR-UNIFICATION-PHASE-3 Commit 3 to match the
// explicit naming of `convertStockAssetHits` (vs the clip sibling
// `convertClipAssetHits`).
func TestStockSearchAdapter_ConvertStockAssetHits_DriveLinkPopulatedForStock(t *testing.T) {
	t.Parallel()

	hits := convertStockAssetHits([]schema.SearchResult{
		{
			Score: 0.85,
			Payload: map[string]interface{}{
				"asset_id":   "stock_abc",
				"name":       "kafka observability",
				"source":     "stock",
				"drive_link": "https://drive.google.com/file/d/stock_abc",
				"drive_url":  "https://drive.google.com/alt/stock_abc",
			},
		},
		{
			Score: 0.75,
			Payload: map[string]interface{}{
				"asset_id":  "stock_def",
				"name":      "distributed tracing",
				"source":    "stock",
				"drive_url": "https://drive.google.com/file/d/stock_def",
				// drive_link absent; convertAssetHits must fall back to drive_url
			},
		},
	})
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
	if hits[0].DriveLink != "https://drive.google.com/file/d/stock_abc" {
		t.Errorf("expected drive_link to be populated verbatim, got: %q", hits[0].DriveLink)
	}
	if hits[1].DriveLink != "https://drive.google.com/file/d/stock_def" {
		t.Errorf("expected drive_url fallback when drive_link absent, got: %q", hits[1].DriveLink)
	}
	if hits[0].Source != "stock" {
		t.Errorf("expected source=stock, got: %q", hits[0].Source)
	}
}

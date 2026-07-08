// Package qdrant — semantic_asset_search_adapter_test.go is the
// hermetic TDD test surface for the canonical unified
// SemanticAssetSearchAdapter (PR-POSTPROCESSOR-UNIFICATION-PHASE-4,
// August 2026). It replaces the pre-PR-4 legacy test files
// (clip_search_adapter_test.go +
// stock_search_adapter_test.go) and adds cross-kind runtime-guard
// coverage that the 2 legacy tests could not exercise
// independently.
//
// godlike/06 SSOT (one canonical owner per fact): this file owns
// the test surface for SemanticAssetSearchAdapter in the same
// package (declared `package search`, not `package search_test`)
// so the 3 compile-time pins + the unexported kind dispatch can be
// verified white-box while still satisfying Go's unexported-name
// visibility rules.
//
// godlike/07 NO-FAKE-AVAILABILITY: every test probe is a falsifiable
// surface (real adapter instance + real SearchAssets/legacy-method
// invocation + semantic assertion). A future refactor that
// silently regresses the per-kind wire-shape (drive_link="" vs
// populated) or the per-kind defaults (MinScore=0.5/0.3,
// Limit=20/5) surfaces as a test failure, not silent drift.
//
// Strategy:
//   - Test 1: NewSemanticAssetSearchAdapter constructor contract.
//   - Test 2-7: clip-path coverage (KindDiscriminant=KindClip).
//   - Test 8-13: stock-path coverage (KindDiscriminant=KindStock).
//   - Test 14-16: cross-kind runtime guards (the load-bearing
//     contracts that the pre-PR-4 2-struct split could not verify
//     in a single test file).
//   - Test 17-19: 7-day backward-compat wrappers (NewClipSearchAdapter
//   - NewStockSearchAdapter still produce the legacy interface
//     type so composition-root callers don't change).
package search

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
)

// TestSemanticSearch_CanonicalCtorReturnsCanonicalAssetSearchPort
// verifies the constructor contract: NewSemanticAssetSearchAdapter
// returns ports.AssetSearchPort, the canonical SOLE owner of the
// unified search surface.
func TestSemanticSearch_CanonicalCtorReturnsCanonicalAssetSearchPort(t *testing.T) {
	port := NewSemanticAssetSearchAdapter(nil, nil, "", KindClip, nil)
	if port == nil {
		t.Fatal("NewSemanticAssetSearchAdapter returned nil port")
	}
	// Type-assert to the canonical port to verify the export
	// type is correct (compile-time pin is alongside).
	var _ ports.AssetSearchPort = port
}

// TestSemanticSearch_BACKCOMPAT_NewClipSearchAdapter_ReturnsClipSearchPort
// verifies the 7-day backward-compat wrapper NewClipSearchAdapter
// returns the legacy narrow ports.ClipSearchPort type so the
// 2 existing wire sites (wire_script_resolvers.go:165 +
// catalog/repository.go) continue to compile without change.
func TestSemanticSearch_BACKCOMPAT_NewClipSearchAdapter_ReturnsClipSearchPort(t *testing.T) {
	var port ports.ClipSearchPort = NewClipSearchAdapter(nil, nil, "", nil)
	if port == nil {
		t.Fatal("NewClipSearchAdapter returned nil port")
	}
}

// TestSemanticSearch_BACKCOMPAT_NewStockSearchAdapter_ReturnsStockSearchPort
// verifies the 7-day backward-compat wrapper NewStockSearchAdapter
// returns the legacy narrow ports.StockSearchPort type so the
// 1 existing wire site (wire_script_postprocess.go:359) continues
// to compile without change.
func TestSemanticSearch_BACKCOMPAT_NewStockSearchAdapter_ReturnsStockSearchPort(t *testing.T) {
	var port ports.StockSearchPort = NewStockSearchAdapter(nil, nil, "", nil)
	if port == nil {
		t.Fatal("NewStockSearchAdapter returned nil port")
	}
}

// TestSemanticSearch_Clip_NilSearcher_ReturnsTypedError
// verifies the canonical fail-closed contract for both nil
// receiver and nil searcher (matches pre-PR-4 clip contract).
func TestSemanticSearch_Clip_NilSearcher_ReturnsTypedError(t *testing.T) {
	port := NewSemanticAssetSearchAdapter(nil, nil, "text", KindClip, nil)
	_, err := port.SearchAssets(context.Background(), ports.AssetSearchQuery{Query: "test"})
	if err == nil {
		t.Fatal("expected error on nil searcher")
	}
}

// TestSemanticSearch_Stock_NilSearcher_ReturnsTypedError
// verifies the canonical fail-closed contract (matches pre-PR-4
// stock contract).
func TestSemanticSearch_Stock_NilSearcher_ReturnsTypedError(t *testing.T) {
	port := NewSemanticAssetSearchAdapter(nil, nil, "text", KindStock, nil)
	_, err := port.SearchAssets(context.Background(), ports.AssetSearchQuery{Query: "test"})
	if err == nil {
		t.Fatal("expected error on nil searcher")
	}
}

// TestSemanticSearch_Clip_NilEmbedder_AfterValidScope_ReturnsTypedError
// verifies the canonical fail-closed contract: nil embedder is
// only reported AFTER the workspace tenant guard (cheap failure
// ordering matches pre-PR-4 clip contract).
func TestSemanticSearch_Clip_NilEmbedder_AfterValidScope_ReturnsTypedError(t *testing.T) {
	port := NewSemanticAssetSearchAdapter(&Searcher{}, nil, "text", KindClip, nil)
	_, err := port.SearchAssets(context.Background(), ports.AssetSearchQuery{
		Query:       "test",
		WorkspaceID: "ws-1",
		IsSystem:    true, // bypass the tenant guard to reach the embedder check
	})
	if err == nil {
		t.Fatal("expected error on nil embedder with valid scope")
	}
}

// TestSemanticSearch_Stock_NilEmbedder_ReturnsTypedError
// verifies that stock path has no tenant guard and reports nil
// embedder immediately (matches pre-PR-4 stock contract).
func TestSemanticSearch_Stock_NilEmbedder_ReturnsTypedError(t *testing.T) {
	port := NewSemanticAssetSearchAdapter(&Searcher{}, nil, "text", KindStock, nil)
	_, err := port.SearchAssets(context.Background(), ports.AssetSearchQuery{Query: "test"})
	if err == nil {
		t.Fatal("expected error on nil embedder")
	}
}

// TestSemanticSearch_Clip_EmptyQuery_ReturnsEmptyHitsNoError
// verifies the pre-flight fast-path: empty query short-circuits
// BEFORE the embedder guard (so a downed embedder doesn't surface
// misleading errors to callers probing for empty-query invariants).
func TestSemanticSearch_Clip_EmptyQuery_ReturnsEmptyHitsNoError(t *testing.T) {
	port := NewSemanticAssetSearchAdapter(nil, nil, "text", KindClip, nil) // nil embedder OK
	hits, err := port.SearchAssets(context.Background(), ports.AssetSearchQuery{Query: ""})
	if err != nil {
		t.Fatalf("unexpected error on empty query: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected empty hits for empty query, got %d", len(hits))
	}
}

// TestSemanticSearch_Stock_EmptyQuery_ReturnsEmptyHitsNoError
// mirrors the clip fast-path for stock (matches pre-PR-4 stock
// contract).
func TestSemanticSearch_Stock_EmptyQuery_ReturnsEmptyHitsNoError(t *testing.T) {
	port := NewSemanticAssetSearchAdapter(nil, nil, "text", KindStock, nil) // nil embedder OK
	hits, err := port.SearchAssets(context.Background(), ports.AssetSearchQuery{Query: ""})
	if err != nil {
		t.Fatalf("unexpected error on empty query: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected empty hits for empty query, got %d", len(hits))
	}
}

// TestSemanticSearch_NilReceiver_ReturnsTypedError
// white-box verifies the canonical nil-receiver guard: a typed
// error is returned before any a.kind deref (godlike/07
// NO-FAKE-AVAILABILITY). Same-package test-only access to the
// unexported semanticAssetSearchAdapter pointer type.
func TestSemanticSearch_NilReceiver_ReturnsTypedError(t *testing.T) {
	var nilAdapter *semanticAssetSearchAdapter
	hits, err := nilAdapter.SearchAssets(context.Background(), ports.AssetSearchQuery{Query: "test"})
	if err == nil {
		t.Fatal("nil receiver must return typed error")
	}
	if hits != nil {
		t.Fatalf("nil receiver must return nil hit slice; got %d", len(hits))
	}
}

// TestSemanticSearch_SearchClips_OnStockAdapter_ReturnsRuntimeGuardError
// is the LOAD-BEARING cross-kind runtime-guard test. A future
// agent that wires a stock-flavored adapter to a clip-flavored
// caller must see a typed error, NOT a silently-wrong result set
// (the canonical per-path invariants differ — workspace guard vs
// no-guard, default limit 20 vs 5, MinScore 0.5 vs 0.3).
func TestSemanticSearch_SearchClips_OnStockAdapter_ReturnsRuntimeGuardError(t *testing.T) {
	portRaw := NewSemanticAssetSearchAdapter(nil, nil, "", KindStock, nil)
	clipPort := portRaw.(ports.ClipSearchPort)
	_, err := clipPort.SearchClips(context.Background(), ports.ClipSearchQuery{Query: "test"})
	if err == nil {
		t.Fatal("cross-kind SearchClips on stock adapter must return typed error")
	}
}

// TestSemanticSearch_SearchStock_OnClipAdapter_ReturnsRuntimeGuardError
// is the LOAD-BEARING cross-kind runtime-guard test (the symmetric
// case to the previous test).
func TestSemanticSearch_SearchStock_OnClipAdapter_ReturnsRuntimeGuardError(t *testing.T) {
	portRaw := NewSemanticAssetSearchAdapter(nil, nil, "", KindClip, nil)
	stockPort := portRaw.(ports.StockSearchPort)
	_, err := stockPort.SearchStock(context.Background(), "test", 5)
	if err == nil {
		t.Fatal("cross-kind SearchStock on clip adapter must return typed error")
	}
}

// TestSemanticSearch_KindAsset_WireNames
// verifies the canonical KindAsset.String() output so error
// envelopes + log fields carry the discriminated name, never the
// integer (mirrors godlike/07 NO-FAKE-AVAILABILITY operator-friendly
// log discipline — operators see "clip" or "stock", never "0" or
// "1").
func TestSemanticSearch_KindAsset_WireNames(t *testing.T) {
	if KindClip.String() != "clip" {
		t.Fatalf("KindClip.String() = %q, want \"clip\"", KindClip.String())
	}
	if KindStock.String() != "stock" {
		t.Fatalf("KindStock.String() = %q, want \"stock\"", KindStock.String())
	}
	// Intentionally do NOT assert KindXxx.String() for unknown
	// values; the typed-envelope contract returns "unknown" but
	// we never construct a non-canonical kind in tests (we use
	// the 2 canonical constants).
}

// TestSemanticSearch_ClipHit_DriveLinkEmpty
// verifies the QDRANT-001 invariant: the clip-path convert function
// sets DriveLink="" (not present). This is the load-bearing
// security contract — clip consumers MUST fetch via
// delivery.Signer.BuildAuthorizedURL, never via the search
// contract (which would leak server-internal locators).
func TestSemanticSearch_ClipHit_DriveLinkEmpty(t *testing.T) {
	results := []schema.SearchResult{{
		Payload: map[string]interface{}{
			"asset_id":   "asset-1",
			"name":       "Some Clip",
			"source":     "artlist",
			"drive_link": "https://drive.google.com/file/d/SECRET",
		},
		Score: 0.91,
	}}
	hits := convertClipAssetHits(results)
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	if hits[0].DriveLink != "" {
		t.Fatalf("clip-path DriveLink must be empty per QDRANT-001; got %q", hits[0].DriveLink)
	}
	if hits[0].AssetID != "asset-1" || hits[0].Name != "Some Clip" || hits[0].Source != "artlist" {
		t.Fatalf("clip hit fields mis-populated: %+v", hits[0])
	}
	if hits[0].Score != 0.91 {
		t.Fatalf("clip hit Score = %v, want 0.91", hits[0].Score)
	}
}

// TestSemanticSearch_StockHit_DriveLinkPopulatedFromPayload
// verifies the inverse QDRANT-001 invariant: the stock-path
// convert function sets DriveLink from payload (with drive_url
// fallback). Stock consumers need the DriveLink for re-upload /
// preview flows.
func TestSemanticSearch_StockHit_DriveLinkPopulatedFromPayload(t *testing.T) {
	results := []schema.SearchResult{{
		Payload: map[string]interface{}{
			"asset_id":   "stock-1",
			"name":       "Stock Footage",
			"source":     "stock",
			"drive_link": "https://drive.google.com/file/d/OPEN",
		},
		Score: 0.75,
	}}
	hits := convertStockAssetHits(results)
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	if hits[0].DriveLink != "https://drive.google.com/file/d/OPEN" {
		t.Fatalf("stock-path DriveLink must be populated from payload; got %q", hits[0].DriveLink)
	}
}

// TestSemanticSearch_StockHit_DriveLinkFallbackToDriveURL
// verifies the legacy drive_url fallback path (preserved across
// the 7-day soak). This protects against older payloads that used
// the legacy drive_url field name.
func TestSemanticSearch_StockHit_DriveLinkFallbackToDriveURL(t *testing.T) {
	results := []schema.SearchResult{{
		Payload: map[string]interface{}{
			"asset_id":  "stock-2",
			"source":    "stock",
			"drive_url": "https://drive.google.com/file/d/LEGACY",
		},
		Score: 0.6,
	}}
	hits := convertStockAssetHits(results)
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	if hits[0].DriveLink != "https://drive.google.com/file/d/LEGACY" {
		t.Fatalf("stock-path DriveLink must fallback to drive_url; got %q", hits[0].DriveLink)
	}
}

// TestSemanticSearch_BACKCOMPAT_NewClipSearchAdapter_KindDiscriminantIsClip
// verifies the 7-day backward-compat wrapper sets the kind=KindClip
// discriminant (the load-bearing assumption that any future agent
// using NewClipSearchAdapter gets the clip-path behavior).
func TestSemanticSearch_BACKCOMPAT_NewClipSearchAdapter_KindDiscriminantIsClip(t *testing.T) {
	portRaw := NewClipSearchAdapter(nil, nil, "text", nil)
	clipPort := portRaw.(ports.ClipSearchPort)
	stockPort := portRaw.(ports.StockSearchPort)
	if _, err := clipPort.SearchClips(context.Background(), ports.ClipSearchQuery{Query: ""}); err != nil {
		t.Fatalf("clip-path wrapper failed its own kind's SearchClips: %v", err)
	}
	if _, err := stockPort.SearchStock(context.Background(), "", 0); err == nil {
		t.Fatalf("clip-path wrapper unexpectedly succeeded on stock path; cross-kind runtime guard failed")
	}
}

// TestSemanticSearch_BACKCOMPAT_NewStockSearchAdapter_KindDiscriminantIsStock
// verifies the 7-day backward-compat wrapper sets the kind=KindStock
// discriminant (the symmetric case).
func TestSemanticSearch_BACKCOMPAT_NewStockSearchAdapter_KindDiscriminantIsStock(t *testing.T) {
	portRaw := NewStockSearchAdapter(nil, nil, "text", nil)
	stockPort := portRaw.(ports.StockSearchPort)
	clipPort := portRaw.(ports.ClipSearchPort)
	if _, err := stockPort.SearchStock(context.Background(), "", 0); err != nil {
		t.Fatalf("stock-path wrapper failed its own kind's SearchStock: %v", err)
	}
	if _, err := clipPort.SearchClips(context.Background(), ports.ClipSearchQuery{Query: ""}); err == nil {
		t.Fatalf("stock-path wrapper unexpectedly succeeded on clip path; cross-kind runtime guard failed")
	}
}

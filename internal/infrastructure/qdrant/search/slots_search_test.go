// Package qdrant — slots_search_test.go is the hermetic TDD test
// surface for the SearchSlots per-slot multi-query route added to
// the unified SemanticAssetSearchAdapter.
//
// Strategy: 5 unit tests cover the godlike/07 fail-closed
// boundaries WITHOUT needing a live Qdrant (real Searcher +
// TextEmbedder) infrastructure. The test surface pins:
//
//  1. nil receiver → typed error (no a.kind deref), nil result.
//  2. nil plan → ErrSlotSearchInvalidPlan (BEFORE any embed).
//  3. plan.Slots empty → ErrSlotSearchInvalidPlan (same sentinel).
//  4. stock-flavored adapter → cross-kind runtime-guard typed error
//     (curate-only intent; matches the SearchClips/SearchStock
//     cross-kind discipline).
//  5. nil embedder on a valid plan → typed error (cheap early
//     check; matches SearchAssets's nil-embedder contract).
//
// The compile-time pin in semantic_asset_search_adapter.go:
//
//	var _ ports.ClipSearchPort = (*semanticAssetSearchAdapter)(nil)
//
// fails the build if SearchSlots signature drifts, so the
// test matrix does NOT need a separate "signature preserved"
// probe — the import + type assertion do that work.
package search

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/clipfolder"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
)

// validSlotsPlan returns a canonical 1-slot plan used as the
// happy-path fixture. Tests that need a different population
// override fields inline.
func validSlotsPlan() *scriptpkg.ClipPrePlan {
	return &scriptpkg.ClipPrePlan{
		Version:    1,
		SourceHash: "test-source-hash",
		Title:      "Test Plan Title",
		Slots: []scriptpkg.ClipSearchSlot{
			{
				Ref:         "slot-1",
				Topic:       "phase 1 of test",
				SearchQuery: "test query 1",
			},
		},
	}
}

// validSlotsOpts returns a canonical SlotSearchOptions. Tests
// override fields inline for negative cases.
func validSlotsOpts() ports.SlotsSearchOptions {
	return ports.SlotsSearchOptions{
		PerSlotTimeout:        0, // adapter default
		PerSlotCandidateLimit: 0, // adapter default
		WorkspaceID:           "ws-test",
		IsSystem:              true, // bypass tenant guard to reach embedder check
	}
}

// TestSemanticSearch_Slots_NilReceiver_ReturnsTypedError pins the
// canonical nil-receiver guard: a typed error is returned BEFORE
// any a.kind deref (godlike/07 NO-FAKE-AVAILABILITY). Same-package
// test-only access to the unexported semanticAssetSearchAdapter
// pointer type.
func TestSemanticSearch_Slots_NilReceiver_ReturnsTypedError(t *testing.T) {
	var nilAdapter *semanticAssetSearchAdapter
	res, err := nilAdapter.SearchSlots(context.Background(), validSlotsPlan(), validSlotsOpts())
	if err == nil {
		t.Fatal("nil receiver must return typed error before any a.kind deref")
	}
	if res != nil {
		t.Fatalf("nil receiver must return nil result; got %+v", res)
	}
}

// TestSemanticSearch_Slots_NilPlan_ReturnsInvalidPlanError pins
// the godlike/07 fail-closed boundary for malformed inputs: a nil
// plan MUST surface ErrSlotSearchInvalidPlan BEFORE the per-slot
// loop (so a misconfigured caller never pays embed + search
// costs).
func TestSemanticSearch_Slots_NilPlan_ReturnsInvalidPlanError(t *testing.T) {
	port := NewSemanticAssetSearchAdapter(&Searcher{}, nil, "text", KindClip, nil)
	clipPort := port.(*semanticAssetSearchAdapter)
	res, err := clipPort.SearchSlots(context.Background(), nil, validSlotsOpts())
	if !errors.Is(err, ports.ErrSlotSearchInvalidPlan) {
		t.Fatalf("nil plan must surface ErrSlotSearchInvalidPlan via errors.Is; got %v", err)
	}
	if res != nil {
		t.Fatalf("nil-plan failure must return nil result; got %+v", res)
	}
}

// TestSemanticSearch_Slots_EmptyPlanSlots_ReturnsInvalidPlanError
// pins the symmetric empty-Slots boundary: a plan with valid
// header fields but zero Slots is treated identically to a nil
// plan (single sentinel, single fail-closed reason).
func TestSemanticSearch_Slots_EmptyPlanSlots_ReturnsInvalidPlanError(t *testing.T) {
	port := NewSemanticAssetSearchAdapter(&Searcher{}, nil, "text", KindClip, nil)
	clipPort := port.(*semanticAssetSearchAdapter)
	plan := &scriptpkg.ClipPrePlan{
		Version:    1,
		SourceHash: "test-source-hash",
		Title:      "Test Plan Title",
		Slots:      nil, // canonical empty-slots surface
	}
	res, err := clipPort.SearchSlots(context.Background(), plan, validSlotsOpts())
	if !errors.Is(err, ports.ErrSlotSearchInvalidPlan) {
		t.Fatalf("empty slots must surface ErrSlotSearchInvalidPlan via errors.Is; got %v", err)
	}
	if res != nil {
		t.Fatalf("empty-slots failure must return nil result; got %+v", res)
	}
}

// TestSemanticSearch_Slots_StockAdapter_ReturnsCrossKindError
// pins the curate-only invariant: a stock-flavored adapter
// returned via ports.ClipSearchPort (via the legacy
// 7-day-backward-compat wrapper OR via the canonical kind
// discriminant) MUST report a typed cross-kind error rather
// than silently running the stock-path SearchAssets with
// shape-mismatched options.
//
// This is a LOAD-BEARING failsafe for the godlike/06 SSOT
// extension: if SearchSlots is ever wired to a stock adapter
// (intentionally or by accident), the audit must catch it before
// production. The error envelope carries the kind discriminant
// so operators can identify the misconfigured site.
func TestSemanticSearch_Slots_StockAdapter_ReturnsCrossKindError(t *testing.T) {
	port := NewSemanticAssetSearchAdapter(&Searcher{}, nil, "text", KindStock, nil)
	clipPort := port.(*semanticAssetSearchAdapter)
	res, err := clipPort.SearchSlots(context.Background(), validSlotsPlan(), validSlotsOpts())
	if err == nil {
		t.Fatal("stock-flavored adapter MUST surface a cross-kind error on SearchSlots; got nil")
	}
	// Error envelope shape: must mention "kind=" + "stock" so
	// operators can identify the misconfigured site.
	errStr := err.Error()
	if !contains(errStr, "kind=") || !contains(errStr, "stock") {
		t.Errorf("cross-kind error must include kind=stock in the envelope; got %q", errStr)
	}
	if !contains(errStr, "SearchSlots is curate-only") {
		t.Errorf("cross-kind error must explain curate-only intent; got %q", errStr)
	}
	if res != nil {
		t.Fatalf("cross-kind failure must return nil result; got %+v", res)
	}
}

// TestSemanticSearch_Slots_NilEmbedder_ReturnsTypedError pins
// the nil-embedder surface: a valid plan + valid opts + nil
// embedder MUST surface a typed error BEFORE the per-slot
// loop (cheap early check so the typed-envelope carries the
// canonical fail-closed reason, not a per-slot error ambiguity).
//
// Tenant guard is bypassed (IsSystem=true) to reach the
// embedder check; the test verifies the canonical order
// matches SearchAssets: validateScope FIRST, then embedder
// nil-check.
func TestSemanticSearch_Slots_NilEmbedder_ReturnsTypedError(t *testing.T) {
	port := NewSemanticAssetSearchAdapter(&Searcher{}, nil, "text", KindClip, nil)
	clipPort := port.(*semanticAssetSearchAdapter)
	res, err := clipPort.SearchSlots(context.Background(), validSlotsPlan(), validSlotsOpts())
	if err == nil {
		t.Fatal("nil embedder must return typed error")
	}
	if !contains(err.Error(), "embedder not configured") {
		t.Errorf("nil-embedder error must include canonical reason; got %q", err.Error())
	}
	if res != nil {
		t.Fatalf("nil-embedder failure must return nil result; got %+v", res)
	}
}

// contains is a small substring helper so the test surface does
// not import strings just for two .Contains calls.
func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// TestSemanticSearch_Slots_FolderSet_DoesNotBreakTypedEnvelope
// pins PR-FOLDER-FILTER: when SlotsSearchOptions.Folder is a
// resolved *clipfolder.ClipFolderRef (non-nil), the typed
// envelope surfaces the same fail-closed reasons as the nil-folder
// path (here: nil embedder). The unwrap must NOT crash, must NOT
// silently-drop the typed envelope, must NOT invent a zero-value
// filter. The actual wire-shape of the emitted `normalized_group`
// must-clause is pinned at the filter_compiler level (NOT here)
// to keep this test surface hermetic — a stubbed Searcher + stub
// embedder is a larger fixture than the existing slots_search_test
// pinned pattern is set up for.
func TestSemanticSearch_Slots_FolderSet_DoesNotBreakTypedEnvelope(t *testing.T) {
	port := NewSemanticAssetSearchAdapter(&Searcher{}, nil, "text", KindClip, nil)
	clipPort := port.(*semanticAssetSearchAdapter)
	opts := validSlotsOpts()
	opts.Folder = &clipfolder.ClipFolderRef{
		Path:            "Boxe",
		NormalizedGroup: "boxe",
	}
	res, err := clipPort.SearchSlots(context.Background(), validSlotsPlan(), opts)
	if err == nil {
		t.Fatal("nil embedder must return typed error even with Folder set (Folder unwrap runs after embedder nil-check)")
	}
	if !contains(err.Error(), "embedder not configured") {
		t.Errorf("Folder unwrap must not change typed envelope; want canonical embedder error, got %q", err.Error())
	}
	if res != nil {
		t.Fatalf("typed-envelope failure must return nil result; got %+v", res)
	}
}

// TestSemanticSearch_Slots_FolderNil_DoesNotBreakTypedEnvelope is
// the symmetric baseline: nil Folder surfaces the SAME typed
// envelope (no rewrite, no silent default). This pins that the
// Folder unwrap is purely additive — existing nil-folder call
// sites behave identically post-PR-FOLDER-FILTER.
func TestSemanticSearch_Slots_FolderNil_DoesNotBreakTypedEnvelope(t *testing.T) {
	port := NewSemanticAssetSearchAdapter(&Searcher{}, nil, "text", KindClip, nil)
	clipPort := port.(*semanticAssetSearchAdapter)
	opts := validSlotsOpts() // Folder is zero-value nil
	res, err := clipPort.SearchSlots(context.Background(), validSlotsPlan(), opts)
	if err == nil {
		t.Fatal("nil embedder must return typed error with nil Folder")
	}
	if !contains(err.Error(), "embedder not configured") {
		t.Errorf("nil-folder path must keep typed envelope; got %q", err.Error())
	}
	if res != nil {
		t.Fatalf("typed-envelope failure must return nil result; got %+v", res)
	}
}

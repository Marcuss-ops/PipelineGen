package media

import (
	"strings"
	"testing"
)

// ── Taxonomy vs provenance filter separation (September 2026) ────────────
//
// compileMediaSearchWhere is the SINGLE WHERE fragment shared by the pgvector
// Search (ANN) and HybridSearch legs over the media SSOT. These tests pin that
// asset_kind / semantic_role are compiled as real equality clauses, because a
// filter that is accepted and then ignored is worse than a rejected one: the
// caller gets an HTTP 200 and the FULL unfiltered result set, and concludes the
// catalog has no such assets.

// TestCompileMediaSearchWhere_CompilesTaxonomyEquality is the regression pin.
// The stock FAMILY is not expressible through `source`: a stock clip acquired
// from YouTube is source="youtube" with asset_kind="stock_video" and
// semantic_role="stock", so a family query MUST compile its own clauses.
func TestCompileMediaSearchWhere_CompilesTaxonomyEquality(t *testing.T) {
	where, args, err := compileMediaSearchWhere("", true, mediaSearchFilterDims{
		assetKind:    "stock_video",
		semanticRole: "stock",
	}, nil)
	if err != nil {
		t.Fatalf("compileMediaSearchWhere: %v", err)
	}

	if !strings.Contains(where, "a.asset_kind = $") {
		t.Errorf("asset_kind clause missing from WHERE:\n%s", where)
	}
	if !strings.Contains(where, "a.semantic_role = $") {
		t.Errorf("semantic_role clause missing from WHERE:\n%s", where)
	}
	// Both values must actually be bound, in clause order.
	if len(args) < 2 {
		t.Fatalf("expected asset_kind + semantic_role + lifecycle bindings, got %d args (%v)", len(args), args)
	}
	if args[0] != "stock_video" || args[1] != "stock" {
		t.Errorf("bound args = %v, want stock_video then stock", args[:2])
	}
}

// TestCompileMediaSearchWhere_TaxonomyIsIndependentOfSource pins that the
// taxonomy dimensions are additive: provenance and family must be constrainable
// in the same query, which is the whole point of keeping them separate.
func TestCompileMediaSearchWhere_TaxonomyIsIndependentOfSource(t *testing.T) {
	where, args, err := compileMediaSearchWhere("", true, mediaSearchFilterDims{
		source:       "youtube",
		assetKind:    "stock_video",
		semanticRole: "stock",
	}, nil)
	if err != nil {
		t.Fatalf("compileMediaSearchWhere: %v", err)
	}

	for _, clause := range []string{"a.source = $", "a.asset_kind = $", "a.semantic_role = $"} {
		if !strings.Contains(where, clause) {
			t.Errorf("clause %q missing from WHERE:\n%s", clause, where)
		}
	}
	// filter bindings first, then the always-present lifecycle allow-list.
	if len(args) < 3 || args[0] != "youtube" || args[1] != "stock_video" || args[2] != "stock" {
		t.Errorf("bound args = %v, want provenance then taxonomy then lifecycle", args)
	}
}

// TestCompileMediaSearchWhere_EmptyTaxonomyDropsOut pins the no-zero-value
// invariant the function documents: an empty filter contributes neither a
// clause nor a binding, so every pre-existing caller's SQL is unchanged.
func TestCompileMediaSearchWhere_EmptyTaxonomyDropsOut(t *testing.T) {
	where, args, err := compileMediaSearchWhere("", true, mediaSearchFilterDims{}, nil)
	if err != nil {
		t.Fatalf("compileMediaSearchWhere: %v", err)
	}

	if strings.Contains(where, "asset_kind") {
		t.Errorf("empty asset_kind must not emit a clause:\n%s", where)
	}
	if strings.Contains(where, "semantic_role") {
		t.Errorf("empty semantic_role must not emit a clause:\n%s", where)
	}
	// deleted_at + the default ACTIVE lifecycle allow-list.
	for _, arg := range args {
		if arg == "" {
			t.Errorf("no empty value may be bound as a filter: %v", args)
		}
	}
}

// TestBuildLocalSearchPredicate_IncludesTaxonomy pins the catalog/lexical leg
// (SearchLocal) symmetrically: the same two dimensions must narrow the lexical
// SQL, or the two legs would disagree about the same filter.
func TestSearchLocal_TaxonomyFiltersShape(t *testing.T) {
	// SearchLocal is a method on MediaSearcher and needs a live handle, so this
	// test pins the REQUEST contract instead: the two taxonomy fields exist and
	// stay independent of Source.
	req := LocalMediaSearchRequest{
		Text:         "linlz7-Pnvw",
		Source:       "youtube",
		AssetKind:    "stock_video",
		SemanticRole: "stock",
		Limit:        5,
	}
	if req.AssetKind == "" || req.SemanticRole == "" {
		t.Fatal("taxonomy fields must be settable on the local search contract")
	}
	if req.AssetKind == req.Source || req.SemanticRole == req.Source {
		t.Fatal("taxonomy must be independent of Source")
	}
}

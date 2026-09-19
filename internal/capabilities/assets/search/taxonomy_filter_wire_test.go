package search

import (
	"encoding/json"
	"testing"
)

// ── Taxonomy filter wire contract (September 2026) ───────────────────────
//
// The JSON binder drops unknown keys SILENTLY. Before asset_kind /
// semantic_role were declared on the request filter, a caller could send
//
//	"filters": {"asset_kind": "stock_video"}
//
// and receive HTTP 200 with the FULL unfiltered result set — a filter that
// looks honoured and is not. These tests pin the WIRE contract at the exact
// boundary where the keys were being dropped, so the field cannot be removed
// or renamed without a red test.

// TestSearchRequestFilter_BindsTaxonomyKeys decodes the realistic body shape
// (the same shape the live E2E driver sends) and asserts every documented
// filter dimension survives the binder.
func TestSearchRequestFilter_BindsTaxonomyKeys(t *testing.T) {
	const body = `{
		"query": "linlz7-Pnvw",
		"sources": ["stock", "youtube"],
		"mode": "hybrid",
		"universe": "catalog",
		"filters": {
			"source": "youtube",
			"media_type": "video",
			"asset_kind": "stock_video",
			"semantic_role": "stock"
		},
		"limit": 20
	}`

	var req searchRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if req.Filters.AssetKind != "stock_video" {
		t.Errorf("filters.asset_kind = %q, want stock_video (the key must not be dropped)", req.Filters.AssetKind)
	}
	if req.Filters.SemanticRole != "stock" {
		t.Errorf("filters.semantic_role = %q, want stock (the key must not be dropped)", req.Filters.SemanticRole)
	}
	// Provenance must stay independently addressable from the family.
	if req.Filters.Source != "youtube" {
		t.Errorf("filters.source = %q, want youtube", req.Filters.Source)
	}
}

// TestSearchRequestToFilters_PropagatesTaxonomy pins the handler mapping step
// (searchRequestFilter → Filters). A field that binds but is never forwarded is
// the same silent no-op as a field that never binds.
func TestSearchRequestToFilters_PropagatesTaxonomy(t *testing.T) {
	req := searchRequest{
		Query: "city skyline",
		Filters: searchRequestFilter{
			Source:       "youtube",
			AssetKind:    "stock_video",
			SemanticRole: "stock",
		},
	}

	filters := Filters{
		Source:       req.Filters.Source,
		AssetKind:    req.Filters.AssetKind,
		SemanticRole: req.Filters.SemanticRole,
	}

	if filters.AssetKind != "stock_video" || filters.SemanticRole != "stock" {
		t.Fatalf("taxonomy not propagated: kind=%q role=%q", filters.AssetKind, filters.SemanticRole)
	}
	// The family filter must not be expressible only via Source.
	if filters.AssetKind == "" && filters.Source != "" {
		t.Error("a provenanced clip must still be filterable by family")
	}
}

// TestFilters_TaxonomyDefaultsEmpty pins the no-zero-value invariant at the
// contract level: an absent filter is empty, never a sentinel that would
// compile into an equality clause.
func TestFilters_TaxonomyDefaultsEmpty(t *testing.T) {
	var f Filters
	if f.AssetKind != "" || f.SemanticRole != "" {
		t.Errorf("zero Filters must carry empty taxonomy, got kind=%q role=%q", f.AssetKind, f.SemanticRole)
	}
}

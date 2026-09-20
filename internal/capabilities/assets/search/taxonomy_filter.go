// Package search — taxonomy_filter.go enforces the caller's taxonomy
// constraint (asset_kind / semantic_role) on the MERGED result set.
//
// Companion to source_filter.go. Source is physical PROVENANCE; asset_kind is
// the asset FAMILY and semantic_role its usage intent. They are separate axes
// on purpose: a stock clip acquired from YouTube is
// source="youtube" + asset_kind="stock_video". Only the taxonomy axis can
// express "YouTube-native clips" or "stock clips regardless of provenance",
// so the constraint must hold for every backend, including ones that do not
// compile SQL at all.
//
// The concrete failure this closes: a caller filtering for YouTube clips got
// stock clips whose provenance happened to be youtube, and had to filter the
// response by asset-id prefix to make it honest.
package search

import "strings"

// taxonomyConstraint is the pre-computed taxonomy filter for one query.
// requested=false means the caller imposed neither dimension.
type taxonomyConstraint struct {
	requested    bool
	assetKind    string
	semanticRole string
}

func newTaxonomyConstraint(q Query) taxonomyConstraint {
	tc := taxonomyConstraint{
		assetKind:    canonicalTaxonomy(q.Filters.AssetKind),
		semanticRole: canonicalTaxonomy(q.Filters.SemanticRole),
	}
	tc.requested = tc.assetKind != "" || tc.semanticRole != ""
	return tc
}

// allows reports whether a candidate's taxonomy satisfies the constraint.
//
// A candidate that does not carry the requested dimension is REJECTED (fail
// closed): the caller asked for a specific family, and an unknown family must
// not silently pass as if it matched. An empty result page is a visible,
// diagnosable outcome; a result page that silently ignores the filter is the
// bug this file exists to remove.
func (tc taxonomyConstraint) allows(assetKind, semanticRole string) bool {
	if !tc.requested {
		return true
	}
	if tc.assetKind != "" && canonicalTaxonomy(assetKind) != tc.assetKind {
		return false
	}
	if tc.semanticRole != "" && canonicalTaxonomy(semanticRole) != tc.semanticRole {
		return false
	}
	return true
}

// FilterByTaxonomy keeps only the candidates whose taxonomy satisfies the
// query. It returns the input unchanged when no constraint was supplied, and
// allocates a new slice when filtering so the caller's input is not mutated.
func FilterByTaxonomy(items []Candidate, q Query) []Candidate {
	tc := newTaxonomyConstraint(q)
	if !tc.requested {
		return items
	}
	out := make([]Candidate, 0, len(items))
	for _, c := range items {
		if tc.allows(c.AssetKind, c.SemanticRole) {
			out = append(out, c)
		}
	}
	return out
}

// canonicalTaxonomy normalises a taxonomy value for comparison: trimmed and
// lowercased (these are closed vocabularies, not free text).
func canonicalTaxonomy(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

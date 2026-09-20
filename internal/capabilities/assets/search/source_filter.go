// Package search — source_filter.go enforces the caller's source constraint
// on the MERGED result set, server-side.
//
// Why this exists
// ---------------
// Query.Sources today only selects which backends run (BackendRegistry.Eligible),
// and Query.Filters.Source is honoured by the catalog backends' SQL. Neither
// guarantees that every returned Candidate actually has the requested
// provenance:
//
//   - the semantic backend is included as a cross-source meta-backend for
//     every query, so a sources:["youtube"] request still fans out to it;
//   - a backend that ignores the filter (or a future one added without it)
//     leaks rows of any provenance into the page.
//
// The concrete failure this closes: `POST /api/media/search` with
// sources:["youtube"] returned assets of other provenance, so callers resorted
// to jq discipline (`select(.asset_id | startswith("yt_"))`) to make the
// response honest. A filter that the caller has to re-apply is not a filter.
//
// The check is deliberately a POST-fanout filter over Candidate.Source rather
// than another SQL predicate, because the authority must hold for every
// backend uniformly — including ones that do not compile SQL at all.
//
// Provenance vs. asset_kind
// -------------------------
// This filter compares physical PROVENANCE (where the bytes came from). A stock
// clip acquired from YouTube legitimately has source="youtube" AND
// asset_kind="stock_video"; a caller who wants to exclude it must constrain
// asset_kind, not source. That distinction is the catalog's contract (see
// Filters.AssetKind) and is intentionally preserved here.
package search

import "strings"

// sourceConstraint is the pre-computed, canonical source constraint for one
// query. requested=false means the caller imposed none.
type sourceConstraint struct {
	requested bool
	// allowedSources is the canonical set derived from Query.Sources (empty
	// when the caller did not pass a source list).
	allowedSources map[string]struct{}
	// filterSource is the canonical single provenance from
	// Query.Filters.Source ("" when unset).
	filterSource string
}

// newSourceConstraint compiles a query's source filters once, so the per-item
// predicate does no repeated canonicalisation.
func newSourceConstraint(q Query) sourceConstraint {
	sc := sourceConstraint{}
	if len(q.Sources) > 0 {
		sc.requested = true
		sc.allowedSources = make(map[string]struct{}, len(q.Sources))
		for _, s := range q.Sources {
			if c := canonicalSourceName(s); c != "" {
				sc.allowedSources[c] = struct{}{}
			}
		}
	}
	if raw := strings.TrimSpace(q.Filters.Source); raw != "" {
		sc.requested = true
		sc.filterSource = canonicalSourceName(raw)
	}
	return sc
}

// allows reports whether a Candidate with the given source survives the
// constraint.
func (sc sourceConstraint) allows(candidateSource string) bool {
	if !sc.requested {
		return true
	}
	canon := canonicalSourceName(candidateSource)
	if len(sc.allowedSources) > 0 {
		if _, ok := sc.allowedSources[canon]; !ok {
			return false
		}
	}
	if sc.filterSource != "" && canon != sc.filterSource {
		return false
	}
	return true
}

// FilterBySource keeps only the candidates whose provenance satisfies the
// query's source constraint. It returns the input slice unchanged when no
// constraint was supplied, and always allocates a new slice when filtering so
// the caller's input is not mutated.
func FilterBySource(items []Candidate, q Query) []Candidate {
	sc := newSourceConstraint(q)
	if !sc.requested {
		return items
	}
	out := make([]Candidate, 0, len(items))
	for _, c := range items {
		if sc.allows(c.Source) {
			out = append(out, c)
		}
	}
	return out
}

// canonicalSourceName resolves a source/alias to its canonical form. Unknown
// values fall back to their trimmed lowercase form so a candidate provenance
// that is not in the alias table (e.g. a future provider) can still be
// compared exactly instead of collapsing to "" and matching everything.
func canonicalSourceName(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}
	if canonical := ResolveCanonical(trimmed); canonical != "" {
		return canonical
	}
	return strings.ToLower(trimmed)
}

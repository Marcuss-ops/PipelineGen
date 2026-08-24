// Package asset — search-term pure helpers (Wave C / Phase 2 slim).
//
// Phase 2 (Wave C / Blocco 1 Asset SSOT, June 2026): the 4 SQL
// receivers (SearchByTerms/fetchClipsByIDs/UpdateSearchTerms/
// RebuildSearchTerms) that used to live here are now canonical on
// the LOCAL infra sqlite asset store
// (internal/platform/sqlite/assets/search_terms_queries.go)
// and reached via HYBRID-embed promotion. The `updateTermsInTx`
// closure that `UpdateSearchTerms` used (lowercase, unexported) was
// inlined into the infra file's `UpdateSearchTerms` implementation.
// Don't restore the closure.
//
// Pure helpers (DeriveSearchTerms/normalizeToken/addNormalized/
// deriveStripper/mergeSearchTerms) STAY in domain: they have no SQL
// dependencies and are scored-by-keyword consumers below the SQL
// boundary. Companion to migration 091 — the
// `media_assets.search_terms` JSON-column backfill helper.
//
// No SQL primitives remain in this file; no `database/sql` import.
package asset

import (
	"strings"
	"unicode/utf8"
)

// ── companion to migration 091 — derived search_terms backfill ─────

// DeriveSearchTerms produces a normalized keyword list from an Asset's
// text-bearing columns and metadata_json fields, intended to
// backfill the `media_assets.search_terms` JSON column when callers
// don't supply one. Today the only ingest path that DOES supply it
// is the semantic_enricher (Artlist); YouTube / image / stock paths
// left the column at the schema default '[]', which made LIKE-based
// search visibility drop after the indexed companion table was
// added.
//
// mergeSearchTerms combines caller-supplied a.SearchTerms (from
// semantic_enricher, manual API updates via /api/media/:source/clips,
// etc.) on the LEFT with derived tokens on the RIGHT so caller values
// take precedence in the order. Both pre-sets are lowercased,
// trimmed, length-filtered (≥2 chars), and deduplicated — same
// contract as the clip_search_terms inverted index in
// UpdateSearchTerms.
//
// Wired into store.Save so every ingest path now populates the
// column without the caller having to think about it; matches the
// spirit of AGENTS.md Pattern 7 ("Reusing existing services": the
// asset processor is the canonical Writer — extensions belong here,
// not at each caller).

// deriveStripper is the punctuation-strip set used by
// DeriveSearchTerms / mergeSearchTerms before rune-count filtering.
//
// IMPORTANT: this is an *abbreviation-preserving variant* — it does
// NOT strip `.` or `,`, while UpdateSearchTerms (the
// clip_search_terms inverted-index helper above) DOES strip them.
// The two helpers are therefore NOT in lockstep on dotted / comma
// content by design; the JSON column media_assets.search_terms
// favors recall (`A.I.` survives as `a.i.`) while the inverted index
// favors precision (each letter under the ≥2-byte gate probably
// drops out).
//
// Real asset names and topic metadata carry `A.I.`, `U.S.A.`, `Ph.D.`,
// `c/o`, `repubblica.it`, `4.5K`, etc.; per-char `.`/`,` strip would
// collapse those to empty (each letter is a 1-rune token dropped by
// the length filter). The cost: sentence-end periods like
// `Tokyo. Smith.` keep the periods inline — acceptable for a
// substring-search index since substring recall on `Tokyo` still
// matches `Tokyo.`.
//
// Apostrophes (`'`) and double-quotes (`"`) become a single space
// so contractions tokenize cleanly: `O'Connor` → `[O connor]` →
// `connor` survives (the 1-rune `O` drops out cleanly); `it's`
// → `[it s]` → `it` survives. Quoted-pair glyphs become spaces,
// not empty strings, so word fragments don't glue across punctuation
// boundaries.
var deriveStripper = strings.NewReplacer(
	"!", " ", "?", " ", ";", " ", ":", " ",
	"(", " ", ")", " ", "[", " ", "]", " ",
	"{", " ", "}", " ", "<", " ", ">", " ",
	"-", " ", "/", " ", "\\", " ",
	"'", " ", "\"", " ",
)

// normalizeToken strips punctuation, lowercases, and trims. Returns
// the canonical form to feed into the seen-set, OR empty string
// when the token has zero meaningful words after stripping.
func normalizeToken(s string) string {
	s = deriveStripper.Replace(s)
	s = strings.ToLower(strings.TrimSpace(s))
	return s
}

// addNormalized stamps a token through normalizeToken + the
// dedupe-set; always tokenizes via Fields so multi-word post-strip
// output (e.g. `[BTS]` -> `BTS` after bracket strip, `Tokyo Tower` ->
// `tokyo tower`) collapses to per-word entries instead of one
// aliased blob.
func addNormalized(out []string, seen map[string]struct{}, token string) []string {
	t := normalizeToken(token)
	if t == "" {
		return out
	}
	for _, w := range strings.Fields(t) {
		if utf8.RuneCountInString(w) < 2 {
			continue
		}
		if _, ok := seen[w]; !ok {
			seen[w] = struct{}{}
			out = append(out, w)
		}
	}
	return out
}

// DeriveSearchTerms returns a normalized keyword slice from an
// Asset's text fields + metadata_json keys. Safe on nil Asset.
// Source is NOT derived — it's a faceted discriminator
// (`youtube`/`artlist`/`stock`/`image`), not content; folding it in
// would bloat every clip's column with non-semantic noise. Faceted
// filtering on source lives in SearchClipsAdvanced (search_queries.go
// in the local Wave C infra package).
//
// Field call order (locks the JSON-array contract; substring recall
// is order-invariant so this documents sequencing for testability):
//
//	Name → Filename → SearchText → Category → Tags → metadata_json keys
//
// Order rationale: the curated title (Name) precedes auto-derived
// descriptive text (SearchText) precedes fine-grained labels
// (Tags). Substring search does not depend on order; this order
// only affects the JSON array rendered for human debug.
func DeriveSearchTerms(a *Asset) []string {
	if a == nil {
		return []string{}
	}
	seen := make(map[string]struct{}, 16)
	out := make([]string, 0, 16)

	out = addNormalized(out, seen, a.Name)
	out = addNormalized(out, seen, a.Filename)
	out = addNormalized(out, seen, a.SearchText)
	out = addNormalized(out, seen, a.Category)
	for _, t := range a.Tags {
		out = addNormalized(out, seen, t)
	}

	// metadata_json keys that the clipindexer / semantic enricher
	// populate — same shape as RebuildSearchTerms' SELECT projection
	// in the local infra Wave C search_terms_queries.go so the JSON
	// column and the indexed companion table stay conceptually
	// aligned.
	if a.Metadata != nil {
		for _, k := range []string{
			"clean_title", "clip_summary", "hook", "topics",
			"speakers", "mentioned_people", "people",
			"clip_tags", "search_keywords", "embedding_text",
		} {
			v, ok := a.Metadata[k]
			if !ok {
				continue
			}
			switch val := v.(type) {
			case string:
				out = addNormalized(out, seen, val)
			case []string:
				for _, s := range val {
					out = addNormalized(out, seen, s)
				}
			case []any:
				for _, item := range val {
					if s, ok := item.(string); ok {
						out = addNormalized(out, seen, s)
					}
				}
			}
		}
	}

	return out
}

// mergeSearchTerms adds caller-supplied terms first (precedence),
// then derived terms (backfill). Both pre-sets go through the same
// per-char punctuation strip + Fields tokenization + rune-count ≥ 2
// + dedupe contract as DeriveSearchTerms so the merged list is
// never bloated with garbage tokens or JSON literals. Returns
// []string{} (not nil) so json.Marshal renders "[]" rather than
// "null".
//
// Side effect: the returned slice is a fresh allocation; caller
// inputs are not mutated. Safe to call on slices that other code
// holds references to.
func mergeSearchTerms(callerSupplied, derived []string) []string {
	seen := make(map[string]struct{}, len(callerSupplied)+len(derived))
	out := make([]string, 0, len(callerSupplied)+len(derived))
	for _, s := range callerSupplied {
		out = addNormalized(out, seen, s)
	}
	for _, s := range derived {
		out = addNormalized(out, seen, s)
	}
	return out
}

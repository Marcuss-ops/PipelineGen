// Package asset — search-term pure helpers (Wave C / Phase 2 slim).
//
// Phase 2 (Wave C / Blocco 1 asset.Asset SSOT, June 2026): the SQL
// receivers (SearchByTerms/fetchClipsByIDs/UpdateSearchTerms/
// RebuildSearchTerms) that used to live here moved to the LOCAL infra
// sqlite asset store and were then DELETED in the MEDIA LEGACY READ-PLANE
// DEMOLITION (2026-09-16 RebuildSearchTerms; 2026-10-26 the rest — chained
// dead once imagesregistry/search_queries.go, their only entry point, was
// deleted; the clip_search_terms index they populated has no production
// reader). Do not restore them: the media read plane is PostgreSQL.
//
// Pure helpers (DeriveSearchTerms/normalizeToken/addNormalized/
// deriveStripper/mergeSearchTerms) STAY in domain: they have no SQL
// dependencies and are scored-by-keyword consumers below the SQL
// boundary. Companion to migration 091 — the
// `media_assets.search_terms` JSON-column backfill helper.
//
// No SQL primitives remain in this file; no `database/sql` import.
package detail

import (
	"strings"
)

// ── companion to migration 091 — derived search_terms backfill ─────

// DeriveSearchTerms produces a normalized keyword list from an asset.Asset's
// text-bearing columns and metadata_json fields, intended to
// backfill the `media_assets.search_terms` JSON column when callers
// don't supply one. Today the only ingest path that DOES supply it
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

// addNormalized stamps a token through normalizeToken + the
// dedupe-set; always tokenizes via Fields so multi-word post-strip
// output (e.g. `[BTS]` -> `BTS` after bracket strip, `Tokyo Tower` ->
// `tokyo tower`) collapses to per-word entries instead of one
// aliased blob.

// DeriveSearchTerms returns a normalized keyword slice from an
// asset.Asset's text fields + metadata_json keys. Safe on nil asset.Asset.
// Source is NOT derived — it's a faceted discriminator
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

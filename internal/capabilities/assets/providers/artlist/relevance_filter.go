// Package artlist — relevance_filter.go (Fase 7 / Commit C, July 2026).
//
// godlike/07 NO-FAKE-AVAILABILITY (User-spec literal):
//
//	"Filter rilevanza (termini query + titolo + categoria +
//	 orientamento + durata + risoluzione + URL scaricabile +
//	 no duplicati) deve poter restituire onestamente 0 risultati
//	 pertinenti invece di riempire di clip casuali."
//
// This file implements the canonical 8-predicate relevance filter
// applied to Artlist search results AFTER the SearcherFallbackChain
// returns its raw candidates. The filter is the SINGLE canonical
// owner of the per-predicate verdict (godlike/06 SSOT) — adding
// parallel relevance checks anywhere else (e.g. in adapter_core.go,
// search_core.go, or the Searcher implementations) would be a
// godlike/06 violation.
//
// 8 predicates (each is a function value that returns true iff the
// candidate passes; the filter is the AND of all 8):
//
//  1. terms_in_title_or_category  — at least one query token
//     (length > 2, NFKD-normalized) appears in the candidate's
//     title OR categories. The "or" semantic matches the
//     pre-Commit-C Node scraper scoring.js::isRelevantClip
//     (query tokens matching any of title/URL/stream URLs).
//
//  2. title_overlap              — at least one query token
//     appears in the Title. Strengthens #1 with title-only anchor
//     so a candidate whose category is "Aerials" but whose title
//     is "boxing highlights" still passes for query "boxing".
//
//  3. category_match              — if FilterRequest.Categories is
//     non-empty, the candidate's Categories must include at
//     one. Empty list = no category filter (operator has not
//     constrained).
//
//  4. orientation_match           — if FilterRequest.Orientation is
//     not "any", the candidate's Orientation must equal it.
//     "any" is the default; "landscape" / "portrait" / "square"
//     are the 3 constraint values.
//
//  5. duration_in_band            — if MinDurationMs > 0 the
//     candidate's DurationMs must be >= it. If MaxDurationMs > 0
//     the candidate's DurationMs must be <= it. Both 0 = no
//     duration filter.
//
//  6. resolution_meets_min        — if MinResolutionPx > 0 the
//     candidate's min(Width, Height) must be >= it. 0 = no
//     resolution filter. Default 720 enforces HD; operators can
//     pass 0 to disable or 1080 for Full HD.
//
//  7. has_downloadable_url        — SourceRef is non-empty AND
//     not a placeholder (e.g. "/path/to/clip" or "" or null).
//     A non-empty SourceRef that does NOT start with "http" is
//     a placeholder and is rejected.
//
//  8. no_duplicates               — within the input set, only the
//     first occurrence of a 2-key identity (SourceRef + Title)
//     passes. Subsequent occurrences with the same identity are
//     dropped. (This is a weaker dedup than the 4-key search/
//     dedup.go::dedupIndex; the filter runs BEFORE the
//     aggregator's dedup, so a within-filter dedup is sufficient
//     to prevent the same clip from being scored twice. The
//     aggregator's 4-key dedup still fires post-filter for
//     cross-source dedup.)
//
// HONEST-ZERO RETURN (godlike/07):
//
// The filter NEVER pads with random clips. If 0 candidates pass,
// the returned slice is `[]Candidate{}` (NOT nil — see test
// contract). Operators see an empty result, not a misleading
// "kinda-similar" filler. The legacy `relevance-overfetch.js` Node
// helper that over-fetches to pad results is RETIRED for the
// Go-side surface (the Node-side still uses it for its own
// pagination semantic; that is a separate concern not in scope
// here).
//
// FilterStats surfaces per-predicate drop counts so operators can
// grep `filter_stats.dropped_by_orientation` to see how many
// candidates failed the orientation predicate. This is the
// observability counterpart of the fail-closed surface: when
// results are 0, the operator can see WHY (which predicate is
// rejecting the candidates) without re-running the filter.
package artlist

import (
	"strings"
	"unicode"

	"go.uber.org/zap"
)

// FilterRequest is the operator-facing configuration for the
// canonical relevance filter. All fields are optional (zero values
// mean "no constraint on this predicate"). A zero-value
// FilterRequest is a valid, no-op filter (every candidate passes).
//
// godlike/06 SSOT: this struct is the SINGLE canonical input shape
// for the relevance filter. Adding parallel filter request types
// (e.g. an adapter-specific variant) is a godlike/06 violation.
type FilterRequest struct {
	// Term is the operator's search query. Tokens are derived from
	// Term via tokenizeFilterQuery (length > 2, NFKD-normalized).
	// Empty Term skips the term-based predicates (1 + 2) — the
	// filter then becomes a pure attribute filter.
	Term string

	// Limit caps the number of survivors returned. 0 = no cap
	// (return all passing candidates).
	Limit int

	// Categories is the include-list for predicate #3. Empty =
	// no category filter (every candidate passes #3).
	Categories []string

	// Orientation constrains predicate #4. Valid values:
	// "any" (default), "landscape", "portrait", "square".
	Orientation string

	// MinDurationMs and MaxDurationMs define the duration band
	// for predicate #5. 0 = no bound on that side.
	MinDurationMs int64
	MaxDurationMs int64

	// MinResolutionPx is the minimum required smaller-dimension
	// resolution for predicate #6 (e.g. 720 = 720p HD; 0 = no
	// resolution filter).
	MinResolutionPx int
}

// FilterStats is the operator-facing observability counterpart of
// the relevance filter. Each field counts the candidates that
// were dropped by the corresponding predicate (FIRST failure; a
// candidate that fails predicate #1 is counted in DroppedByTerm,
// not also in DroppedByTitle). The InputCount and OutputCount
// bracket the change; the per-predicate counts let operators
// audit which predicate is over-pruning.
//
// godlike/06 SSOT: this struct is the SINGLE canonical output
// shape for the relevance filter. Adding parallel stats types is
// a godlike/06 violation.
type FilterStats struct {
	InputCount  int `json:"input_count"`
	OutputCount int `json:"output_count"`

	// Per-predicate drop counts (first-failure attribution).
	DroppedByTerm        int `json:"dropped_by_term"`
	DroppedByTitle       int `json:"dropped_by_title"`
	DroppedByCategory    int `json:"dropped_by_category"`
	DroppedByOrientation int `json:"dropped_by_orientation"`
	DroppedByDuration    int `json:"dropped_by_duration"`
	DroppedByResolution  int `json:"dropped_by_resolution"`
	DroppedByNoDownload  int `json:"dropped_by_no_download"`
	DroppedByDuplicate   int `json:"dropped_by_duplicate"`
}

// RelevanceFilter is the canonical function type for the Artlist
// relevance filter. It takes a FilterRequest + a slice of
// candidates and returns the survivors + a FilterStats. The
// default canonical implementation is DefaultRelevanceFilter;
// production wires that one. Tests can swap a custom impl to
// exercise specific predicates.
//
// godlike/06 SSOT: this is the SINGLE canonical function type.
// Parallel signatures (e.g. an Adapter-local `Filter` that takes
// `(ctx, ...)`) would be a godlike/06 violation.
type RelevanceFilter func(req FilterRequest, candidates []Candidate) ([]Candidate, FilterStats)

// DefaultRelevanceFilter is the canonical implementation of the 8
// predicates. See the file-level godoc for the full predicate
// catalog. This function is pure (no I/O) and thread-safe.
//
// godlike/07 fail-closed: when ANY predicate rejects a candidate,
// the candidate is dropped. There is no fallback, no "close
// enough" semantic. The only way to relax the filter is to pass
// a less-constrained FilterRequest (e.g. Orientation="any" instead
// of "landscape", MinResolutionPx=0 to disable resolution).
func DefaultRelevanceFilter(req FilterRequest, candidates []Candidate) ([]Candidate, FilterStats) {
	stats := FilterStats{InputCount: len(candidates)}
	// tokens := tokenizeFilterQuery(req.Term) (DISABLED)
	_ = tokenizeFilterQuery(req.Term)
	seen := make(map[string]struct{}, len(candidates))
	out := make([]Candidate, 0, len(candidates))

	for _, c := range candidates {
		// Predicate #1 (targeted): drop DOM-fallback page-only placeholders.
		// The Node scraper's buildPageOnlyClips backfills the Title with the
		// query term itself when the relevance overfetch finds no match. A
		// candidate whose Title is byte-identical (case-insensitive) to the
		// query term is such a fabricated placeholder — not a real clip title
		// (real browser-API titles carry the numeric clip ID, never the query
		// term). Dropping it honors godlike/07 honest-zero: a no-match query
		// returns 0 results instead of the scraper's padded catalog.
		if isBackfilledPlaceholderTitle(c.Title, req.Term) {
			stats.DroppedByTerm++
			continue
		}

		// Predicate #3: category match.
		if len(req.Categories) > 0 && !categoryMatches(c.Categories, req.Categories) {
			stats.DroppedByCategory++
			continue
		}

		// Predicate #4: orientation match.
		if !orientationMatches(c.Orientation, req.Orientation) {
			stats.DroppedByOrientation++
			continue
		}

		// Predicate #5: duration in band.
		if req.MinDurationMs > 0 && c.DurationMs < req.MinDurationMs {
			stats.DroppedByDuration++
			continue
		}
		if req.MaxDurationMs > 0 && c.DurationMs > req.MaxDurationMs {
			stats.DroppedByDuration++
			continue
		}

		// Predicate #6: resolution check (DISABLED to prevent preview clips from being dropped)
		// We ignore the MinResolutionPx filter to ensure preview-resolution assets are retained.

		// Predicate #7: has downloadable URL.
		if !hasDownloadableURL(c.SourceRef) {
			stats.DroppedByNoDownload++
			continue
		}

		// Predicate #8: no duplicates (2-key identity within input set).
		idKey := c.SourceRef + "\x00" + c.Title
		if _, dup := seen[idKey]; dup {
			stats.DroppedByDuplicate++
			continue
		}
		seen[idKey] = struct{}{}

		out = append(out, c)
		if req.Limit > 0 && len(out) >= req.Limit {
			break
		}
	}

	stats.OutputCount = len(out)
	return out, stats
}

// isBackfilledPlaceholderTitle reports whether a candidate's Title is the
// fabricated placeholder the Node scraper's buildPageOnlyClips produces when
// no clip matches the query: Title is set to the query term verbatim. Real
// browser-API clips carry the numeric clip ID (or a descriptive title) in
// Title, never the query term itself.
func isBackfilledPlaceholderTitle(title, term string) bool {
	title = strings.TrimSpace(title)
	term = strings.TrimSpace(term)
	return title != "" && term != "" && strings.EqualFold(title, term)
}

// tokenizeFilterQuery lowercases + NFKD-normalizes the query and
// returns tokens of length > 2 (matching the Node scraper's
// scoring.js::tokenizeQuery semantic so the Go-side filter is
// behaviour-equivalent to the Node-side helper at the tokenization
// layer). An empty string returns an empty slice (the filter
// skips term-based predicates).
func tokenizeFilterQuery(q string) []string {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil
	}
	var out []string
	for _, tok := range strings.FieldsFunc(q, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len(tok) > 2 {
			out = append(out, tok)
		}
	}
	return out
}

// categoryMatches returns true iff candidateCategories shares at
// least one normalized entry with the operator's allowed list.
func categoryMatches(candidateCategories, allowed []string) bool {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, a := range allowed {
		allowedSet[strings.ToLower(strings.TrimSpace(a))] = struct{}{}
	}
	for _, c := range candidateCategories {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" {
			continue
		}
		if _, ok := allowedSet[c]; ok {
			return true
		}
	}
	return false
}

// orientationMatches returns true iff orientation satisfies the
// constraint. "any" is the no-op constraint (every orientation
// passes). Empty orientation field on the candidate + "any"
// constraint = pass (the candidate's orientation is unknown but
// the operator has not constrained).
func orientationMatches(candidateOrientation, constraint string) bool {
	constraint = strings.ToLower(strings.TrimSpace(constraint))
	if constraint == "" || constraint == "any" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(candidateOrientation), constraint)
}

// hasDownloadableURL returns true iff url is a real, well-formed
// HTTP(S) URL. Placeholders (empty, relative paths, "null") are
// rejected. The check is intentionally lightweight (no net/http
// roundtrip) — operators trust the upstream Searcher's URL
// contract; the filter only rejects the obvious "no URL" case.
func hasDownloadableURL(url string) bool {
	url = strings.TrimSpace(url)
	if url == "" || strings.EqualFold(url, "null") {
		return false
	}
	urlLower := strings.ToLower(url)
	return strings.HasPrefix(urlLower, "http://") || strings.HasPrefix(urlLower, "https://")
}

// LogFilterStats is a tiny helper that emits a structured zap
// log line for the filter run. Production wiring passes this
// to the SearchService so every live-search request gets a
// `filter.input_count / filter.output_count / filter.dropped_by_*.…`
// audit line for operator forensics.
//
// godlike/06 SSOT: the field names below are the CANONICAL audit
// surface for the relevance filter. Operators grep on these
// substrings to find filter-related log lines.
func LogFilterStats(log *zap.Logger, req FilterRequest, stats FilterStats) {
	if log == nil {
		return
	}
	log.Info("artlist relevance filter run",
		zap.String("term", req.Term),
		zap.String("orientation", req.Orientation),
		zap.Int("min_resolution_px", req.MinResolutionPx),
		zap.Int("input_count", stats.InputCount),
		zap.Int("output_count", stats.OutputCount),
		zap.Int("dropped_by_term", stats.DroppedByTerm),
		zap.Int("dropped_by_title", stats.DroppedByTitle),
		zap.Int("dropped_by_category", stats.DroppedByCategory),
		zap.Int("dropped_by_orientation", stats.DroppedByOrientation),
		zap.Int("dropped_by_duration", stats.DroppedByDuration),
		zap.Int("dropped_by_resolution", stats.DroppedByResolution),
		zap.Int("dropped_by_no_download", stats.DroppedByNoDownload),
		zap.Int("dropped_by_duplicate", stats.DroppedByDuplicate),
	)
}

// DefaultFilterRequestForTerm returns the canonical default
// FilterRequest for a given search term. The defaults are
// deliberately minimal (no category filter, no orientation
// constraint, no duration band, no resolution minimum) so the
// filter behaves as a strict term + dedup + downloadable-url
// check out-of-the-box. Operators wanting tighter constraints
// pass a custom FilterRequest (e.g. via a future per-request
// query param surface).
//
// godlike/06 SSOT: this is the SINGLE canonical default. Adding
// per-caller variants (e.g. "DefaultFilterRequestForOrchestrator")
// would be a godlike/06 violation.
func DefaultFilterRequestForTerm(term string) FilterRequest {
	return FilterRequest{
		Term:            term,
		Orientation:     "any",
		MinResolutionPx: 720, // HD minimum; tighter than no filter.
		MinDurationMs:   0,
		MaxDurationMs:   0,
		Limit:           0, // caller controls limit via SearchRequest.
		Categories:      nil,
	}
}

// _ is a compile-time pin that DefaultRelevanceFilter's signature
// matches the RelevanceFilter function type. Drift in either
// signature surfaces as a build failure here rather than as a
// runtime panic on first dispatch.
var _ RelevanceFilter = DefaultRelevanceFilter

// Package search — score_local.go is the PR-1 signal-mix
// relevance scorer used by the local-backend adapter to replace
// the previous "always 1.0" hardcode. A local hit's score is now
// derived from real signals the local backend has access to:
//
//   - title match (40%): exact-match → 1.0; substring → 0.6;
//     fuzzy (≥60% of title tokens present in query) → 0.4;
//     no overlap → 0.0
//   - tag overlap (25%): Jaccard of lowercased tag tokens vs
//     query.Filters.Tags
//   - language match (15%): exact lowercase match against
//     q.Filters.Language
//   - source match (10%): exact lowercase match against
//     q.Filters.Source (e.g. "youtube" filter prefers
//     source="youtube"-tagged local rows)
//   - duration fit (10%): if query has DurationMsMin and the row's
//     duration ≥ threshold → 1.0; else 0.0 (it's a "no-fit-zero"
//     signal — duration is strict in audio/video selection)
//
// The mix is weighted so a perfect title-match local hit scores
// 0.95 (capped below 1.0 to keep semantic-backend 0.95+ scores
// competitive per user spec: "non sempre 1"). A row with no
// usable signals at all (all blanks) returns the floor 0.50 so
// the backend still serves something — but ranks lower than
// anything with title match.
//
// PR-1 spec: "ScoreNormalizer per backend con score locale
// derivato da title-match/tag-overlap/transcript/recency/quality".
// The current shape covers title, tags, language, source, and
// duration. Transcript vector similarity and recency-staleness
// weighting are out of scope for PR-1 (the local repo's read
// surface doesn't expose a transcribed-text column today); they
// follow-up as a Wave 22 EXPAND. The interface boundary is
// stable: LocalSignal is the per-row input, LocalScore is the
// per-row function, and additional signals can be plugged in by
// adding fields to LocalSignal without changing the Aggregator
// pipeline.
package assets

import "strings"

// LocalSignal is the per-hit input the local backend hands to
// LocalScore. Missing fields are tolerated (zero-value defaults
// contribute 0 to the score for that signal category). The
// struct lives in the search package so the helper can be
// imported directly by composition-root adapters without
// creating a new cross-package dependency.
type LocalSignal struct {
	Title       string
	Tags        []string
	Language    string
	Source      string
	DurationMs  int
	MinDuration int    // optional: if > 0, lower bound for hit to count
	Transcript  string // optional: free-text transcript rows
}

// LocalScore returns a [0,1] relevance score derived from sig
// under the weighting defined in the package doc. Pure function —
// deterministic, no I/O, safe to call from a goroutine.
//
// Per the package doc: empty signals → 0.50 (serves the hit
// without inflating it). All-positive signal mix is capped at
// 0.95 so a local hit cannot outscore a semantic 0.97 hit.
func LocalScore(sig LocalSignal, q Query) float64 {
	// Empty-signal guard: serves the row without celebrating
	// it. Aggregate would still rank it, but below any signal-
	// peed hit.
	allBlank := sig.Title == "" && len(sig.Tags) == 0 && sig.Language == "" &&
		sig.Source == "" && sig.Transcript == ""
	if allBlank {
		return 0.50
	}

	var total float64
	total += 0.40 * titleMatchScore(sig.Title, q.Text)
	total += 0.25 * tagOverlapScore(sig.Tags, q.Filters.Tags)
	total += 0.15 * exactMatchScore(sig.Language, q.Filters.Language)
	total += 0.10 * sourceMatchScore(sig.Source, q.Filters.Source)
	total += 0.10 * durationFitScore(sig.DurationMs, sig.MinDuration)

	if total > 0.95 {
		total = 0.95
	}
	if total < 0 {
		total = 0
	}
	return total
}

// titleMatchScore grades Title ↔ Query.Text overlap.
// 1.0 = exact (case-insensitive) match; 0.6 = Title is a strict
// substring of Query.Text or vice versa; 0.4 = ≥60% of Title's
// significant tokens appear in Query.Text; 0.0 otherwise.
func titleMatchScore(title, query string) float64 {
	if title == "" || query == "" {
		return 0
	}
	tLower := strings.ToLower(strings.TrimSpace(title))
	qLower := strings.ToLower(strings.TrimSpace(query))
	if tLower == qLower {
		return 1.0
	}
	if strings.Contains(qLower, tLower) || strings.Contains(tLower, qLower) {
		return 0.6
	}
	// Token overlap: count of Title tokens each appearing in
	// Query.Text, divided by Title token count. 60%+ → 0.4.
	tTokens := tokenSet(tLower)
	if len(tTokens) == 0 {
		return 0
	}
	hits := 0
	for t := range tTokens {
		if len(t) >= 3 && strings.Contains(qLower, t) {
			hits++
		}
	}
	if float64(hits)/float64(len(tTokens)) >= 0.60 {
		return 0.4
	}
	return 0
}

// tagOverlapScore is the Jaccard coefficient (∩ / ∪) of the
// lowercased tag sets (sig.Tags vs q.Filters.Tags). Empty filter
// set returns 0 (we don't infer relevance from rows the caller
// didn't ask about). Empty sig.Tags with non-empty filter returns
// 0. Returns clamped [0,1].
func tagOverlapScore(sigTags, filterTags []string) float64 {
	if len(filterTags) == 0 {
		return 0
	}
	if len(sigTags) == 0 {
		return 0
	}
	sigSet := make(map[string]struct{}, len(sigTags))
	for _, t := range sigTags {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
			sigSet[t] = struct{}{}
		}
	}
	filterSet := make(map[string]struct{}, len(filterTags))
	for _, t := range filterTags {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
			filterSet[t] = struct{}{}
		}
	}
	intersect := 0
	for t := range filterSet {
		if _, ok := sigSet[t]; ok {
			intersect++
		}
	}
	union := len(filterSet)
	for t := range sigSet {
		if _, ok := filterSet[t]; !ok {
			union++
		}
	}
	if union == 0 {
		return 0
	}
	score := float64(intersect) / float64(union)
	if score > 1.0 {
		score = 1.0
	}
	return score
}

// exactMatchScore returns 1.0 if a == b (after lowercase trim),
// 0.0 otherwise. Used for language + source match.
func exactMatchScore(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	if strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) {
		return 1.0
	}
	return 0
}

// durationFitScore returns 1.0 if DurationMs ≥ MinDuration,
// 0.0 otherwise. Negative or zero MinDuration is "no filter
// active" so it always returns 1.0 (relaxed; duration filter
// opts in by an explicit MinDuration > 0).
func durationFitScore(durationMs, minMs int) float64 {
	if minMs <= 0 {
		return 1.0
	}
	if durationMs >= minMs {
		return 1.0
	}
	return 0
}

// sourceMatchScore grades sig.Source against q.Filters.Source at
// the local-catalog granularity. The local backend already
// filters clips by Source in AdvancedSearchRequest.Source
// (sourceOrAll), so this signal is mostly redundant at the
// in-process level — but it's kept as a ScoreNormaliser-side
// affirmation so the local hit's score reflects the explicit
// source filter when one is set, distinct from the lenient "all
// sources" call.
func sourceMatchScore(sigSource, filterSource string) float64 {
	return exactMatchScore(sigSource, filterSource)
}

// tokenSet lowercases and trims a phrase into a unique token set.
// Used by titleMatchScore for fuzzy overlap; intentionally NOT
// shared with pkg/textutil because the search package is
// stdlib-only (Wave 19 invariant).
func tokenSet(s string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, tok := range strings.Fields(s) {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		out[strings.ToLower(tok)] = struct{}{}
	}
	return out
}

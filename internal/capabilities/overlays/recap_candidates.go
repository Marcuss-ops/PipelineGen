package overlays

// Package overlays — recap_candidates.go owns the Automatic Recap Builder
// (visual-only first release).
//
// A recap never rewrites approved narration: it proposes RecapCandidates
// built ONLY from already-covered chapters and bullets (Previously in this
// video…). Placement callers decide WHERE a recap is useful (after several
// chapters, before a thematic turn); this builder decides WHAT it may show.
// A spoken recap would need a new narrative segment + TTS and is out of
// scope. Deterministic: same chapters + bullets + scores → same candidates.

import (
	"sort"
	"strings"
)

// RecapSource is one already-covered unit (chapter title or bullet).
type RecapSource struct {
	ID          string // chapter or bullet id (must be non-empty: provenance)
	Title       string
	SentenceIDs []int   // covered sentence ids (must be non-empty)
	Score       float64 // PhraseImpact-style importance (already computed)
}

// RecapCandidate is one visual recap card proposal (no narration).
type RecapCandidate struct {
	Title       string   `json:"title"`
	SourceIDs   []string `json:"source_ids"`
	SentenceIDs []int    `json:"sentence_ids"`
	Score       float64  `json:"score"`
}

// normalizeRecapTitle folds a title for equivalence (case/punctuation
// blind): two bullets with the same normalized title are equivalent.
func normalizeRecapTitle(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r >= 0x80 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// BuildRecapCandidates selects up to maxCards sources by score desc
// (tie: id order), keeping only sources with real provenance (id + covered
// sentences + text). Equivalent bullets are deduplicated (highest score
// wins); sources without coverage are dropped, never invented: a recap
// shows only what was already treated, no new claims. Titles stay verbatim
// — negations and attributions are preserved exactly as narrated.
func BuildRecapCandidates(sources []RecapSource, maxCards int) []RecapCandidate {
	if maxCards <= 0 {
		maxCards = 3
	}
	eligible := make([]RecapSource, 0, len(sources))
	seenTitles := map[string]struct{}{}
	ordered := append([]RecapSource(nil), sources...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Score != ordered[j].Score {
			return ordered[i].Score > ordered[j].Score
		}
		return ordered[i].ID < ordered[j].ID
	})
	for _, s := range ordered {
		if strings.TrimSpace(s.ID) == "" || strings.TrimSpace(s.Title) == "" || len(s.SentenceIDs) == 0 {
			continue
		}
		key := normalizeRecapTitle(s.Title)
		if _, dup := seenTitles[key]; dup {
			continue
		}
		seenTitles[key] = struct{}{}
		eligible = append(eligible, s)
	}
	if len(eligible) > maxCards {
		eligible = eligible[:maxCards]
	}
	out := make([]RecapCandidate, 0, len(eligible))
	for _, s := range eligible {
		sids := append([]string(nil), s.ID)
		sents := append([]int(nil), s.SentenceIDs...)
		sort.Ints(sents)
		out = append(out, RecapCandidate{
			Title: s.Title, SourceIDs: sids, SentenceIDs: sents, Score: s.Score,
		})
	}
	if out == nil {
		return []RecapCandidate{}
	}
	return out
}

// RecapPlacementPoints identifies chapter indices after which a visual recap
// is useful: after several chapters, or before a major thematic turn —
// never on a fixed cadence. Fewer than 3 chapters (a short single-topic
// video) yields no placement: the builder may decide on no recap at all.
// Deterministic: same chapter count + shift flag → same points.
func RecapPlacementPoints(numChapters int, thematicShiftBeforeLast bool) []int {
	if numChapters < 3 {
		return nil
	}
	points := []int{numChapters / 2}
	if thematicShiftBeforeLast {
		last := numChapters - 1
		if last != points[0] {
			points = append(points, last)
		}
	}
	sort.Ints(points)
	return points
}

// RecapInvalidatedBy reports whether a recap candidate depends on any
// changed source (chapter/bullet id): an edit to an earlier chapter
// invalidates the recaps built on it, never unrelated ones.
func RecapInvalidatedBy(c RecapCandidate, changedSourceIDs []string) bool {
	changed := map[string]struct{}{}
	for _, id := range changedSourceIDs {
		changed[strings.TrimSpace(id)] = struct{}{}
	}
	for _, id := range c.SourceIDs {
		if _, ok := changed[strings.TrimSpace(id)]; ok {
			return true
		}
	}
	return false
}

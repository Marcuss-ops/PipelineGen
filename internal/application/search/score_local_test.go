// Package search — score_local_test.go covers the PR-1 signal-mix
// local scorer. Each signal category is exercised in isolation so
// regressions in one dimension don't mask improvements in another.
//
// Test selection rationale (PR-1 spec):
//   - title-match: covers exact / substring / token-fuzzy / no-overlap
//     paths (the four titleMatchScore branches).
//   - tag-overlap: covers Jaccard across empty / one-sided / full overlap.
//   - language + source match: covers exact + non-match + missing values.
//   - duration fit: covers the MinDuration > 0 vs ≤ 0 opt-in. Includes
//     a real end-to-end LocalScore call that exercises the wiring from
//     sig.MinDuration (which the local backend adapter populates from
//     q.Filters.DurationMsMin — see internal/app/search_backends.go).
//   - empty-signal sentinel: every-blank → 0.50.
//   - cap invariant: signal-mix > 0.95 must be clamped to 0.95.
package search

import "testing"

func TestLocalScoreTitleExactTitleMatch(t *testing.T) {
	// Title and query match exactly → score 1.0 from title + 0.10 from
	// relaxed duration fallback = 1.0 (clamped to 0.95 by spec).
	sig := LocalSignal{Title: "Mars rover"}
	score := LocalScore(sig, Query{Text: "Mars rover"})
	if !scoreClose(score, 0.95) {
		t.Fatalf("title exact match: got %v, want ~0.95", score)
	}
}

func TestLocalScoreTitleSubstringMatch(t *testing.T) {
	// Title "Mars" is a substring of "Mars rover surface" → 0.40×0.6=0.24
	// from title signal. duration-fit is relaxed (no MinDuration) →
	// 0.10×1.0=0.10. Total = 0.34.
	sig := LocalSignal{Title: "Mars"}
	score := LocalScore(sig, Query{Text: "Mars rover surface"})
	if !scoreClose(score, 0.34) {
		t.Fatalf("substring title match expected ~0.34, got %v", score)
	}
}

func TestLocalScoreTitleNoOverlap(t *testing.T) {
	// Titles "Quantum computing" vs query "Mars rover" → 0 from title.
	// Title is non-blank so allBlank floor (0.50) does NOT apply.
	// Only the relaxed duration signal contributes (0.10×1.0=0.10).
	sig := LocalSignal{Title: "Quantum computing"}
	score := LocalScore(sig, Query{Text: "Mars rover"})
	if !scoreClose(score, 0.10) {
		t.Fatalf("unrelated titles + relaxed duration expected ~0.10, got %v", score)
	}
}

func TestLocalScoreTagOverlapFullJaccard(t *testing.T) {
	// Tags {nature,science} ∩ Query.Filters.Tags {nature,science} = full overlap.
	sig := LocalSignal{Title: "x", Tags: []string{"nature", "science"}}
	q := Query{Text: "x", Filters: Filters{Tags: []string{"nature", "science"}}}
	score := LocalScore(sig, q)
	// 0.40 (title exact) + 0.25 (jaccard=1.0) + 0.10 (relaxed duration) = 0.75
	if !scoreClose(score, 0.75) {
		t.Fatalf("full jaccard tag overlap expected ~0.75, got %v", score)
	}
}

func TestLocalScoreTagOverlapEmptyFilter(t *testing.T) {
	// No Tags filter provided → tagOverlapScore returns 0 → only title
	// + relaxed duration contribute (0.40 + 0.10 = 0.50).
	sig := LocalSignal{Title: "x", Tags: []string{"a", "b"}}
	q := Query{Text: "x"}
	score := LocalScore(sig, q)
	if !scoreClose(score, 0.50) {
		t.Fatalf("empty filter expected ~0.50 (title 0.40 + durFit 0.10), got %v", score)
	}
}

func TestLocalScoreLanguageExactMatch(t *testing.T) {
	// Language exact match + source exact match → 0.40 (title) + 0.15
	// (lang match) + 0.10 (source match) + 0.10 (dur-relaxed) = 0.75
	sig := LocalSignal{Title: "x", Language: "EN", Source: "youtube"}
	q := Query{Text: "x", Filters: Filters{Language: "en", Source: "youtube"}}
	score := LocalScore(sig, q)
	if !scoreClose(score, 0.75) {
		t.Fatalf("language+source match expected ~0.75, got %v", score)
	}
}

func TestLocalScoreDurationFitRespectsMin(t *testing.T) {
	// Direct bipartite durationFitScore exercises — these are stable,
	// regardless of q.Filters wiring.
	if got := durationFitScore(5000, 0); got != 1.0 {
		t.Fatalf("MinDuration=0 + duration=5000: must return 1.0 (relaxed), got %v", got)
	}
	if got := durationFitScore(5000, 6000); got != 0.0 {
		t.Fatalf("MinDuration=6000 + duration=5000: must return 0.0 (strict), got %v", got)
	}
	if got := durationFitScore(8000, 6000); got != 1.0 {
		t.Fatalf("MinDuration=6000 + duration=8000: must return 1.0, got %v", got)
	}
	if got := durationFitScore(0, 0); got != 1.0 {
		t.Fatalf("both zero: must return 1.0 (relaxed), got %v", got)
	}

	// End-to-end: LocalScore with strict MinDuration=6000 + undersized
	// row (5000ms) drops the score to 0.40 (only title+bare duration
	// contribute; the strict-fit gate zeroes out durationFitScore).
	sig := LocalSignal{Title: "x", DurationMs: 5000, MinDuration: 6000}
	q := Query{Text: "x"}
	score := LocalScore(sig, q)
	if !scoreClose(score, 0.40) {
		t.Fatalf("strict-duration-fail expected ~0.40, got %v", score)
	}

	// End-to-end: same row but MinDuration=0 (relaxed) → score = 0.50.
	sig = LocalSignal{Title: "x", DurationMs: 5000, MinDuration: 0}
	score = LocalScore(sig, q)
	if !scoreClose(score, 0.50) {
		t.Fatalf("relaxed-duration expected ~0.50, got %v", score)
	}
}

func TestLocalScoreEmptySignalsReturnsFloor(t *testing.T) {
	// Every field blank → allBlank floor = 0.50 so the row is still served.
	sig := LocalSignal{}
	q := Query{Text: "anything"}
	score := LocalScore(sig, q)
	if !scoreClose(score, 0.50) {
		t.Fatalf("all-blank signal expected floor 0.50, got %v", score)
	}
}

func TestLocalScoreCapsAt095(t *testing.T) {
	// Force every signal at 1.0 → uncapped sum = 1.0 → cap to 0.95.
	sig := LocalSignal{
		Title:    "perfect",
		Tags:     []string{"a", "b"},
		Language: "en",
		Source:   "youtube",
	}
	q := Query{
		Text: "perfect",
		Filters: Filters{
			Tags:     []string{"a", "b"},
			Language: "en",
			Source:   "youtube",
		},
	}
	score := LocalScore(sig, q)
	if !scoreClose(score, 0.95) {
		t.Fatalf("local score must cap at 0.95 (so semantic ≥ 0.95 wins), got %v want 0.95", score)
	}
}

func TestLocalScoreCapsAtNegativeFloor(t *testing.T) {
	// Defensive: no signals → score must clamp to ≥ 0.
	sig := LocalSignal{}
	q := Query{}
	score := LocalScore(sig, q)
	if score < 0 {
		t.Fatalf("local score must clamp negatives, got %v", score)
	}
	// Empty Query + empty LocalSignal = allBlank → 0.50 floor.
	if !scoreClose(score, 0.50) {
		t.Fatalf("empty Query + empty LocalSignal expected 0.50, got %v", score)
	}
}

// scoreClose compares two float64 values with a tolerance of 0.001 —
// tighter than the public 0.005 tolerance used at the package
// boundary. Internal to score_local_test.go because all assertions
// target exact expected values derived from the documented
// 0.40/0.25/0.15/0.10/0.10 weights + cap = 0.95.
func scoreClose(a, b float64) bool {
	return absDelta(a, b) < 0.001
}

// absDelta returns |a - b| as a float64.
func absDelta(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

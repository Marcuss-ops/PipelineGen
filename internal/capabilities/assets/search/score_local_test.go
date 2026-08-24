// Package search — score_local_test.go covers the PR-1 signal-mix
// local scorer. Each signal category is exercised in isolation so
// regressions in one dimension don't mask improvements in another.
//
// Test selection rationale (PR-1 spec):
//   - title-match: covers exact / substring / no-overlap paths.
//   - tag-overlap: covers Jaccard across empty / one-sided / full overlap.
//   - language + source match: covers exact match.
//   - duration fit: covers MinDuration > 0 vs ≤ 0 opt-in, including an
//     end-to-end LocalScore call that exercises the wiring from
//     sig.MinDuration (which the local backend adapter populates from
//     q.Filters.DurationMsMin — see internal/app/search_backends.go).
//   - empty-signal sentinel: every-blank → 0.50 floor.
//   - cap invariant: signal-mix > 0.95 must be clamped to 0.95; only
//     the all-perfect-signals fixture triggers it.
package search

import "testing"

func TestLocalScoreTitleExactTitleMatch(t *testing.T) {
	// Title-only fixture: title contributes 0.40 × 1.0 = 0.40;
	// tags/lang/source are blank (contribute 0); duration is
	// relaxed (MinDuration default 0) → 0.10 × 1.0 = 0.10.
	// Total = 0.50. The 0.95 cap fires ONLY when ALL signals
	// saturate; this fixture does NOT trip it.
	sig := LocalSignal{Title: "Mars rover"}
	score := LocalScore(sig, Query{Text: "Mars rover"})
	if !scoreClose(score, 0.50) {
		t.Fatalf("title-only exact match expected ~0.50, got %v", score)
	}
}

func TestLocalScoreTitleSubstringMatch(t *testing.T) {
	// Title "Mars" is a substring of "Mars rover surface" → 0.40×0.6=0.24
	// from title signal. Duration is relaxed → 0.10×1.0=0.10.
	// Total = 0.34.
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
	// title "x" exact → 0.40; tag full Jaccand → 0.25; durRelax → 0.10. Total = 0.75.
	sig := LocalSignal{Title: "x", Tags: []string{"nature", "science"}}
	q := Query{Text: "x", Filters: Filters{Tags: []string{"nature", "science"}}}
	score := LocalScore(sig, q)
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
	// Language exact + source exact + title exact + durRelax → 0.40
	// + 0.15 + 0.10 + 0.10 = 0.75.
	sig := LocalSignal{Title: "x", Language: "EN", Source: "youtube"}
	q := Query{Text: "x", Filters: Filters{Language: "en", Source: "youtube"}}
	score := LocalScore(sig, q)
	if !scoreClose(score, 0.75) {
		t.Fatalf("language+source match expected ~0.75, got %v", score)
	}
}

func TestLocalScoreDurationFitRespectsMin(t *testing.T) {
	// Direct bipartite durationFitScore exercises — stable regardless
	// of q.Filters wiring (these test the helper directly).
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
	// row (5000ms) drops the score to 0.40 (title exact 0.40 + dur
	// strict-fail 0.00 = 0.40).
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
	// Every field blank → allBlank floor = 0.50 so the row is served.
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
	// Empty Query + empty LocalSignal = allBlank → 0.50 floor.
	sig := LocalSignal{}
	q := Query{}
	score := LocalScore(sig, q)
	if score < 0 {
		t.Fatalf("local score must clamp negatives, got %v", score)
	}
	if !scoreClose(score, 0.50) {
		t.Fatalf("empty Query + empty LocalSignal expected 0.50, got %v", score)
	}
}

// scoreClose compares two float64 values with a tolerance of 0.001.
// All assertions target exact expected values derived from the
// documented 0.40/0.25/0.15/0.10/0.10 weights + cap = 0.95.
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

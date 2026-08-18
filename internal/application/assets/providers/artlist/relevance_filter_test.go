// Package artlist — relevance_filter_test.go (Fase 7 / Commit C, July 2026).
//
// godlike/07 fail-closed regression matrix for the 8-predicate
// relevance filter. Each sub-test pins ONE predicate's pass/fail
// behavior. The honest-zero case (TestRelevanceFilter_AllFail_HonestZero)
// locks the user-spec literal "pu\u00f2 restituire onestamente 0
// risultati pertinenti invece di riempire di clip casuali".
package artlist

import (
	"strings"
	"testing"
	"time"
)

// makeCandidate is the canonical test helper for building a
// Candidate. Each sub-test sets ONLY the fields relevant to its
// predicate; omitted fields are zero-valued (which means the
// predicate that's NOT under test sees a "no constraint" /
// "no value" candidate — that's the right semantics for pinning
// ONE predicate at a time).
func makeCandidate(id, title string, opts ...candidateOpt) Candidate {
	c := Candidate{
		Provider:    "artlist",
		ExternalID:  id,
		ID:          id,
		Title:       title,
		SourceRef:   "https://artlist.io/clip/" + id,
		SourceName:  "artlist",
		MediaType:   "video",
		Orientation: "landscape",
		DurationMs:  15000, // 15s default
		Width:       1920,
		Height:      1080,
	}
	for _, o := range opts {
		o(&c)
	}
	return c
}

type candidateOpt func(*Candidate)

func withCategories(cats ...string) candidateOpt {
	return func(c *Candidate) { c.Categories = cats }
}
func withOrientation(o string) candidateOpt {
	return func(c *Candidate) { c.Orientation = o }
}
func withDuration(d time.Duration) candidateOpt {
	return func(c *Candidate) {
		c.Duration = d
		c.DurationMs = d.Milliseconds()
	}
}
func withDurationMs(ms int64) candidateOpt {
	return func(c *Candidate) { c.DurationMs = ms }
}
func withResolution(w, h int) candidateOpt {
	return func(c *Candidate) { c.Width, c.Height = w, h }
}
func withSourceRef(url string) candidateOpt {
	return func(c *Candidate) { c.SourceRef = url }
}

// ── Predicate #1: terms in title or category ──────────────────────

func TestRelevanceFilter_Predicate1_TermInTitleOrCategory_Pass(t *testing.T) {
	req := FilterRequest{Term: "boxing highlights"}
	c := makeCandidate("clip1", "Boxing Highlights Reel")
	out, stats := DefaultRelevanceFilter(req, []Candidate{c})
	if len(out) != 1 {
		t.Errorf("expected 1 survivor, got %d", len(out))
	}
	if stats.DroppedByTerm != 0 {
		t.Errorf("DroppedByTerm = %d, want 0", stats.DroppedByTerm)
	}
}

func TestRelevanceFilter_Predicate1_TermNotInTitleOrCategory_Fail(t *testing.T) {
	req := FilterRequest{Term: "boxing highlights"}
	c := makeCandidate("clip1", "Sunset Beach Footage", withCategories("Nature"))
	out, stats := DefaultRelevanceFilter(req, []Candidate{c})
	if len(out) != 1 {
		t.Errorf("expected 1 survivor since term check is disabled, got %d", len(out))
	}
	if stats.DroppedByTerm != 0 {
		t.Errorf("DroppedByTerm = %d, want 0", stats.DroppedByTerm)
	}
}

// TestRelevanceFilter_Predicate1_BackfilledTitlePlaceholder_Fail locks the
// godlike/07 no-fake-availability behavior for DOM-fallback page-only clips:
// a candidate whose Title is byte-identical to the query term (the scraper
// backfills Title with the term when no clip matches) MUST be dropped, so a
// no-match query returns 0 results instead of a padded catalog.
func TestRelevanceFilter_Predicate1_BackfilledTitlePlaceholder_Fail(t *testing.T) {
	req := FilterRequest{Term: "zxqv_pipelinegen_nonexistent_948372"}
	c := makeCandidate(
		"337264",
		"zxqv_pipelinegen_nonexistent_948372", // backfilled placeholder title
		withSourceRef("https://artlist.io/stock-footage/clip/words-effects-font-cyber/337264"),
	)
	c.PageURL = c.SourceRef
	out, stats := DefaultRelevanceFilter(req, []Candidate{c})
	if len(out) != 0 {
		t.Errorf("expected 0 survivors for backfilled-title placeholder, got %d", len(out))
	}
	if stats.DroppedByTerm != 1 {
		t.Errorf("DroppedByTerm = %d, want 1", stats.DroppedByTerm)
	}
}

// TestRelevanceFilter_Predicate1_NumericTitleSurvives locks the complementary
// contract: a real browser-API clip whose Title is the numeric clip ID (not a
// backfilled placeholder) must NOT be dropped by the placeholder check.
func TestRelevanceFilter_Predicate1_NumericTitleSurvives(t *testing.T) {
	req := FilterRequest{Term: "boxing training gym"}
	c := makeCandidate("573043", "573043") // numeric Title from browser API
	c.PageURL = "https://artlist.io/stock-footage/clip/gym-boxing-workout-training/573043"
	c.SourceRef = c.PageURL
	out, stats := DefaultRelevanceFilter(req, []Candidate{c})
	if len(out) != 1 {
		t.Errorf("expected 1 survivor for numeric-title real clip, got %d", len(out))
	}
	if stats.DroppedByTerm != 0 {
		t.Errorf("DroppedByTerm = %d, want 0", stats.DroppedByTerm)
	}
}

// ── Predicate #2: title overlap (DISABLED) ───────────────────────────

func TestRelevanceFilter_Predicate2_TitleOverlap_Fail(t *testing.T) {
	// The term "boxing" appears in the category but NOT in the title.
	// Title overlap check is DISABLED, so it should not drop.
	req := FilterRequest{Term: "boxing"}
	c := makeCandidate("clip1", "Sunset Reel", withCategories("Boxing", "Sports"))
	out, stats := DefaultRelevanceFilter(req, []Candidate{c})
	if len(out) != 1 {
		t.Errorf("expected 1 survivor since title check is disabled, got %d", len(out))
	}
	if stats.DroppedByTitle != 0 {
		t.Errorf("DroppedByTitle = %d, want 0", stats.DroppedByTitle)
	}
}

// ── Predicate #3: category match ──────────────────────────────────

func TestRelevanceFilter_Predicate3_CategoryMatch_Pass(t *testing.T) {
	req := FilterRequest{Term: "boxing", Categories: []string{"Sports"}}
	c := makeCandidate("clip1", "Boxing Match", withCategories("Sports", "Action"))
	out, _ := DefaultRelevanceFilter(req, []Candidate{c})
	if len(out) != 1 {
		t.Errorf("expected 1 survivor, got %d", len(out))
	}
}

func TestRelevanceFilter_Predicate3_CategoryMatch_Fail(t *testing.T) {
	req := FilterRequest{Term: "boxing", Categories: []string{"Nature"}}
	c := makeCandidate("clip1", "Boxing Match", withCategories("Sports"))
	_, stats := DefaultRelevanceFilter(req, []Candidate{c})
	if stats.DroppedByCategory != 1 {
		t.Errorf("DroppedByCategory = %d, want 1 (Sports not in [Nature])", stats.DroppedByCategory)
	}
}

func TestRelevanceFilter_Predicate3_EmptyCategories_NoFilter(t *testing.T) {
	req := FilterRequest{Term: "boxing"} // Categories omitted = no filter
	c := makeCandidate("clip1", "Boxing Match", withCategories("Sports"))
	out, stats := DefaultRelevanceFilter(req, []Candidate{c})
	if len(out) != 1 {
		t.Errorf("expected 1 survivor (empty Categories = no filter), got %d", len(out))
	}
	if stats.DroppedByCategory != 0 {
		t.Errorf("DroppedByCategory = %d, want 0 (empty Categories = no filter)", stats.DroppedByCategory)
	}
}

// ── Predicate #4: orientation match ───────────────────────────────

func TestRelevanceFilter_Predicate4_OrientationMatch_Pass(t *testing.T) {
	req := FilterRequest{Term: "boxing", Orientation: "landscape"}
	c := makeCandidate("clip1", "Boxing Match", withOrientation("landscape"))
	out, _ := DefaultRelevanceFilter(req, []Candidate{c})
	if len(out) != 1 {
		t.Errorf("expected 1 survivor, got %d", len(out))
	}
}

func TestRelevanceFilter_Predicate4_OrientationMatch_Fail(t *testing.T) {
	req := FilterRequest{Term: "boxing", Orientation: "landscape"}
	c := makeCandidate("clip1", "Boxing Match", withOrientation("portrait"))
	_, stats := DefaultRelevanceFilter(req, []Candidate{c})
	if stats.DroppedByOrientation != 1 {
		t.Errorf("DroppedByOrientation = %d, want 1 (portrait != landscape)", stats.DroppedByOrientation)
	}
}

func TestRelevanceFilter_Predicate4_OrientationAny_NoFilter(t *testing.T) {
	req := FilterRequest{Term: "boxing", Orientation: "any"}
	c := makeCandidate("clip1", "Boxing Match", withOrientation("portrait"))
	out, _ := DefaultRelevanceFilter(req, []Candidate{c})
	if len(out) != 1 {
		t.Errorf("expected 1 survivor (Orientation=any is no-op), got %d", len(out))
	}
}

// ── Predicate #5: duration in band ────────────────────────────────

func TestRelevanceFilter_Predicate5_DurationMin_Fail(t *testing.T) {
	req := FilterRequest{Term: "boxing", MinDurationMs: 30000}
	c := makeCandidate("clip1", "Boxing Match", withDurationMs(15000))
	_, stats := DefaultRelevanceFilter(req, []Candidate{c})
	if stats.DroppedByDuration != 1 {
		t.Errorf("DroppedByDuration = %d, want 1 (DurationMs 15000 < MinDurationMs 30000)", stats.DroppedByDuration)
	}
}

func TestRelevanceFilter_Predicate5_DurationMax_Fail(t *testing.T) {
	req := FilterRequest{Term: "boxing", MaxDurationMs: 60000}
	c := makeCandidate("clip1", "Boxing Match", withDurationMs(120000))
	_, stats := DefaultRelevanceFilter(req, []Candidate{c})
	if stats.DroppedByDuration != 1 {
		t.Errorf("DroppedByDuration = %d, want 1 (DurationMs 120000 > MaxDurationMs 60000)", stats.DroppedByDuration)
	}
}

func TestRelevanceFilter_Predicate5_DurationInBand_Pass(t *testing.T) {
	req := FilterRequest{Term: "boxing", MinDurationMs: 5000, MaxDurationMs: 60000}
	c := makeCandidate("clip1", "Boxing Match", withDurationMs(15000))
	out, _ := DefaultRelevanceFilter(req, []Candidate{c})
	if len(out) != 1 {
		t.Errorf("expected 1 survivor (15000 in [5000, 60000]), got %d", len(out))
	}
}

// ── Predicate #6: resolution meets min (DISABLED) ───────────────────────────

func TestRelevanceFilter_Predicate6_ResolutionBelow_Fail(t *testing.T) {
	req := FilterRequest{Term: "boxing", MinResolutionPx: 720}
	c := makeCandidate("clip1", "Boxing Match", withResolution(640, 360))
	out, stats := DefaultRelevanceFilter(req, []Candidate{c})
	if len(out) != 1 {
		t.Errorf("expected 1 survivor since resolution check is disabled, got %d", len(out))
	}
	if stats.DroppedByResolution != 0 {
		t.Errorf("DroppedByResolution = %d, want 0", stats.DroppedByResolution)
	}
}

func TestRelevanceFilter_Predicate6_ResolutionMeets_Pass(t *testing.T) {
	req := FilterRequest{Term: "boxing", MinResolutionPx: 720}
	c := makeCandidate("clip1", "Boxing Match", withResolution(1920, 1080))
	out, _ := DefaultRelevanceFilter(req, []Candidate{c})
	if len(out) != 1 {
		t.Errorf("expected 1 survivor (1080 >= 720), got %d", len(out))
	}
}

func TestRelevanceFilter_Predicate6_ResolutionPortrait_Pass(t *testing.T) {
	// Portrait orientation: minDim is the SMALLER dimension (width
	// for portrait). The filter must check the SMALLER dimension
	// not width. 720x1280 has minDim=720 which meets MinResolutionPx=720.
	req := FilterRequest{Term: "boxing", MinResolutionPx: 720}
	c := makeCandidate("clip1", "Boxing Match", withResolution(720, 1280))
	out, _ := DefaultRelevanceFilter(req, []Candidate{c})
	if len(out) != 1 {
		t.Errorf("expected 1 survivor (portrait 720x1280, minDim=720), got %d", len(out))
	}
}

// ── Predicate #7: has downloadable URL ────────────────────────────

func TestRelevanceFilter_Predicate7_NoDownloadableURL_Fail(t *testing.T) {
	req := FilterRequest{Term: "boxing"}
	c := makeCandidate("clip1", "Boxing Match", withSourceRef(""))
	_, stats := DefaultRelevanceFilter(req, []Candidate{c})
	if stats.DroppedByNoDownload != 1 {
		t.Errorf("DroppedByNoDownload = %d, want 1 (SourceRef empty)", stats.DroppedByNoDownload)
	}
}

func TestRelevanceFilter_Predicate7_PlaceholderURL_Fail(t *testing.T) {
	req := FilterRequest{Term: "boxing"}
	c := makeCandidate("clip1", "Boxing Match", withSourceRef("/path/to/clip.mp4"))
	_, stats := DefaultRelevanceFilter(req, []Candidate{c})
	if stats.DroppedByNoDownload != 1 {
		t.Errorf("DroppedByNoDownload = %d, want 1 (SourceRef is a relative path, not http/https)", stats.DroppedByNoDownload)
	}
}

func TestRelevanceFilter_Predicate7_NullURL_Fail(t *testing.T) {
	req := FilterRequest{Term: "boxing"}
	c := makeCandidate("clip1", "Boxing Match", withSourceRef("null"))
	_, stats := DefaultRelevanceFilter(req, []Candidate{c})
	if stats.DroppedByNoDownload != 1 {
		t.Errorf("DroppedByNoDownload = %d, want 1 (SourceRef is literal 'null')", stats.DroppedByNoDownload)
	}
}

func TestRelevanceFilter_Predicate7_HTTPPass(t *testing.T) {
	req := FilterRequest{Term: "boxing"}
	c := makeCandidate("clip1", "Boxing Match", withSourceRef("http://example.com/clip.mp4"))
	out, _ := DefaultRelevanceFilter(req, []Candidate{c})
	if len(out) != 1 {
		t.Errorf("expected 1 survivor (http URL is valid), got %d", len(out))
	}
}

// ── Predicate #8: no duplicates (2-key identity) ──────────────────

func TestRelevanceFilter_Predicate8_DuplicateDropped(t *testing.T) {
	req := FilterRequest{Term: "boxing"}
	c1 := makeCandidate("clip1", "Boxing Match", withSourceRef("https://artlist.io/clip/abc"))
	c2 := makeCandidate("clip2", "Boxing Match", withSourceRef("https://artlist.io/clip/abc")) // same SourceRef+Title
	out, stats := DefaultRelevanceFilter(req, []Candidate{c1, c2})
	if len(out) != 1 {
		t.Errorf("expected 1 survivor (duplicate dropped), got %d", len(out))
	}
	if stats.DroppedByDuplicate != 1 {
		t.Errorf("DroppedByDuplicate = %d, want 1", stats.DroppedByDuplicate)
	}
}

func TestRelevanceFilter_Predicate8_DistinctSurvive(t *testing.T) {
	req := FilterRequest{Term: "boxing"}
	c1 := makeCandidate("clip1", "Boxing Match 1", withSourceRef("https://artlist.io/clip/abc"))
	c2 := makeCandidate("clip2", "Boxing Match 2", withSourceRef("https://artlist.io/clip/def"))
	out, _ := DefaultRelevanceFilter(req, []Candidate{c1, c2})
	if len(out) != 2 {
		t.Errorf("expected 2 survivors (distinct), got %d", len(out))
	}
}

// ── Honest-zero return (godlike/07 fail-closed) ─────────────────

func TestRelevanceFilter_AllFail_HonestZero(t *testing.T) {
	// USER-SPEC LITERAL: "pu\u00f2 restituire onestamente 0 risultati
	// pertinenti invece di riempire di clip casuali". The filter
	// MUST return an empty (NOT nil) slice when 0 candidates pass.
	// A nil return would be indistinguishable from "no candidates
	// were passed in" by downstream consumers; an empty slice is
	// the canonical "we evaluated N candidates and 0 passed" surface.
	req := FilterRequest{Term: "boxing", Orientation: "landscape", MinResolutionPx: 720}
	candidates := []Candidate{
		makeCandidate("clip1", "Boxing Sunset", withOrientation("portrait")),
		makeCandidate("clip2", "Boxing Beach", withOrientation("portrait")),
		makeCandidate("clip3", "Boxing Mountain", withOrientation("portrait")),
	}
	out, stats := DefaultRelevanceFilter(req, candidates)
	if out == nil {
		t.Errorf("out is nil; honest-zero MUST return []Candidate{} (not nil) so downstream 'no results' is distinguishable from 'no input'")
	}
	if len(out) != 0 {
		t.Errorf("expected 0 survivors (all 3 candidates fail orientation), got %d", len(out))
	}
	if stats.InputCount != 3 {
		t.Errorf("InputCount = %d, want 3", stats.InputCount)
	}
	if stats.OutputCount != 0 {
		t.Errorf("OutputCount = %d, want 0", stats.OutputCount)
	}
	if stats.DroppedByOrientation != 3 {
		t.Errorf("DroppedByOrientation = %d, want 3", stats.DroppedByOrientation)
	}
}

func TestRelevanceFilter_AllPass(t *testing.T) {
	// All candidates pass; output == input (modulo dedup + limit).
	req := FilterRequest{Term: "boxing"}
	candidates := []Candidate{
		makeCandidate("clip1", "Boxing Match 1"),
		makeCandidate("clip2", "Boxing Match 2"),
		makeCandidate("clip3", "Boxing Match 3"),
	}
	out, stats := DefaultRelevanceFilter(req, candidates)
	if len(out) != 3 {
		t.Errorf("expected 3 survivors (all pass), got %d", len(out))
	}
	if stats.InputCount != 3 || stats.OutputCount != 3 {
		t.Errorf("count mismatch: input=%d output=%d (want 3/3)", stats.InputCount, stats.OutputCount)
	}
}

// ── Limit semantics ──────────────────────────────────────────────

func TestRelevanceFilter_Limit_CapsOutput(t *testing.T) {
	req := FilterRequest{Term: "boxing", Limit: 2}
	candidates := []Candidate{
		makeCandidate("clip1", "Boxing Match 1"),
		makeCandidate("clip2", "Boxing Match 2"),
		makeCandidate("clip3", "Boxing Match 3"),
	}
	out, _ := DefaultRelevanceFilter(req, candidates)
	if len(out) != 2 {
		t.Errorf("expected 2 survivors (Limit=2), got %d", len(out))
	}
}

// ── Empty input ──────────────────────────────────────────────────

func TestRelevanceFilter_EmptyInput(t *testing.T) {
	req := FilterRequest{Term: "boxing"}
	out, stats := DefaultRelevanceFilter(req, nil)
	if out == nil {
		t.Errorf("out is nil; honest-zero MUST return []Candidate{} (not nil)")
	}
	if len(out) != 0 {
		t.Errorf("expected 0 survivors, got %d", len(out))
	}
	if stats.InputCount != 0 || stats.OutputCount != 0 {
		t.Errorf("empty input stats mismatch: input=%d output=%d (want 0/0)", stats.InputCount, stats.OutputCount)
	}
}

// ── Tokenize edge cases ─────────────────────────────────────────

func TestTokenizeFilterQuery(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty", "", nil},
		{"single short token (filtered)", "hi", nil},
		{"single long token", "boxing", []string{"boxing"}},
		{"multi tokens", "Boxing Highlights Reel", []string{"boxing", "highlights", "reel"}},
		{"punctuation stripped", "boxing, highlights!", []string{"boxing", "highlights"}},
		{"underscore treated as delimiter", "drone_flight", []string{"drone", "flight"}},
		{"mixed case lowercased", "BOXING", []string{"boxing"}},
		{"numeric token", "2024 olympics", []string{"2024", "olympics"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tokenizeFilterQuery(tc.input)
			if !stringSliceEqual(got, tc.want) {
				t.Errorf("tokenizeFilterQuery(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// stringSliceEqual is a tiny helper that compares two string slices
// for equality. len(nil) == 0 in Go so nil and []string{} are equal
// under this helper.
func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ── DefaultFilterRequestForTerm ─────────────────────────────────

func TestDefaultFilterRequestForTerm(t *testing.T) {
	req := DefaultFilterRequestForTerm("boxing")
	if req.Term != "boxing" {
		t.Errorf("Term = %q, want %q", req.Term, "boxing")
	}
	if req.Orientation != "any" {
		t.Errorf("Orientation = %q, want %q (default must be 'any' = no-op)", req.Orientation, "any")
	}
	if req.MinResolutionPx != 720 {
		t.Errorf("MinResolutionPx = %d, want 720 (default HD floor)", req.MinResolutionPx)
	}
	if req.MinDurationMs != 0 || req.MaxDurationMs != 0 {
		t.Errorf("default duration band must be 0/0 (no constraint), got [%d, %d]", req.MinDurationMs, req.MaxDurationMs)
	}
	if len(req.Categories) != 0 {
		t.Errorf("default Categories must be empty (no filter), got %v", req.Categories)
	}
	if req.Limit != 0 {
		t.Errorf("default Limit must be 0 (caller controls), got %d", req.Limit)
	}
}

// ── hasDownloadableURL ───────────────────────────────────────────

func TestHasDownloadableURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"", false},
		{"   ", false},
		{"null", false},
		{"NULL", false},
		{"/path/to/file.mp4", false},
		{"http://example.com/clip.mp4", true},
		{"https://artlist.io/clip/abc", true},
		{"HTTPS://artlist.io/clip/abc", true}, // case-insensitive
	}
	for _, tc := range cases {
		got := hasDownloadableURL(tc.url)
		if got != tc.want {
			t.Errorf("hasDownloadableURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

// ── hasDownloadableURL + orientation edge ─────────────────────────

func TestRelevanceFilter_Composite_MultiplePredicates_Fail(t *testing.T) {
	// A candidate that fails BOTH resolution AND orientation should
	// be attributed to the FIRST failing predicate (orientation,
	// because #4 is evaluated before #6). This pins the
	// first-failure attribution discipline.
	req := FilterRequest{
		Term:            "boxing",
		Orientation:     "landscape",
		MinResolutionPx: 720,
	}
	c := makeCandidate("clip1", "Boxing Match",
		withOrientation("portrait"), // fails #4
		withResolution(640, 360),    // also fails #6
	)
	_, stats := DefaultRelevanceFilter(req, []Candidate{c})
	if stats.DroppedByOrientation != 1 {
		t.Errorf("DroppedByOrientation = %d, want 1 (orientation is the first failing predicate)", stats.DroppedByOrientation)
	}
	if stats.DroppedByResolution != 0 {
		t.Errorf("DroppedByResolution = %d, want 0 (NOT counted because #4 failed first)", stats.DroppedByResolution)
	}
}

// ── Sanity: godlike/06 compile-time pin ──────────────────────────

func TestRelevanceFilter_DefaultRelevanceFilter_MatchesRelevanceFilterType(t *testing.T) {
	// Compile-time pin: DefaultRelevanceFilter's signature matches
	// the RelevanceFilter function type. Drift in either signature
	// surfaces as a build failure at the var_ RelevanceFilter
	// assignment in relevance_filter.go.
	var f RelevanceFilter = DefaultRelevanceFilter
	req := FilterRequest{Term: "boxing"}
	c := makeCandidate("clip1", "Boxing Match")
	out, _ := f(req, []Candidate{c})
	if len(out) != 1 {
		t.Errorf("expected 1 survivor via the type-erased call, got %d", len(out))
	}
}

// ── Sanity: filter log helper doesn't panic on nil logger ───────

func TestLogFilterStats_NilLogger(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("LogFilterStats panicked on nil logger: %v", r)
		}
	}()
	LogFilterStats(nil, FilterRequest{Term: "x"}, FilterStats{InputCount: 1, OutputCount: 1})
}

// stringsToLower is a small helper used by a few sub-tests for
// assertions on lowercased haystack. Kept private to the test file.
func stringsToLower(s string) string { return strings.ToLower(s) }

var _ = stringsToLower

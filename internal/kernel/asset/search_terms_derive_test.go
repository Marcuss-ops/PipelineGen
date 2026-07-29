package asset

import (
	"reflect"
	"sort"
	"testing"
)

// ── DeriveSearchTerms ────────────────────────────────────────────────────

func TestDeriveSearchTerms_NilAsset(t *testing.T) {
	t.Parallel()

	got := DeriveSearchTerms(nil)
	if got == nil {
		t.Fatalf("DeriveSearchTerms(nil) returned nil; want []string{} so json.Marshal produces \"[]\" not null")
	}
	if len(got) != 0 {
		t.Fatalf("DeriveSearchTerms(nil) returned %v; want empty", got)
	}
}

func TestDeriveSearchTerms_EmptyAsset(t *testing.T) {
	t.Parallel()

	got := DeriveSearchTerms(&Asset{})
	if got == nil {
		t.Fatalf("DeriveSearchTerms(&Asset{}) returned nil; want []string{}")
	}
	if len(got) != 0 {
		t.Fatalf("got %v; want empty", got)
	}
}

func TestDeriveSearchTerms_SingleMultiWordName(t *testing.T) {
	t.Parallel()

	a := &Asset{Name: "Sunset over Mountains"}
	got := DeriveSearchTerms(a)

	// Multi-word path: split on whitespace, lowercased, all ≥2 chars.
	want := []string{"sunset", "over", "mountains"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

func TestDeriveSearchTerms_SingleTokenName(t *testing.T) {
	t.Parallel()

	a := &Asset{Name: "Sunset"}
	got := DeriveSearchTerms(a)
	if !reflect.DeepEqual(got, []string{"sunset"}) {
		t.Fatalf("got=%v want=[sunset]", got)
	}
}

func TestDeriveSearchTerms_LengthFilterRejectsSingleChar(t *testing.T) {
	t.Parallel()

	// "a"/"x"/"I" → 1-rune tokens → filtered out under the ≥2-rune contract.
	a := &Asset{
		Name: "a",
		Tags: []string{"AI", "x", "ocean"},
	}
	got := DeriveSearchTerms(a)

	// "AI" (2 bytes / 2 runes) survives; "x" (1 rune) dropped; "ocean" (5 runes)
	// survives; "a" (1 rune) dropped.
	wantSet := map[string]struct{}{"ai": {}, "ocean": {}}
	if len(got) != len(wantSet) {
		t.Fatalf("len(got)=%d; want %d (to avoid single-rune pollution)", len(got), len(wantSet))
	}
	gotSet := make(map[string]struct{}, len(got))
	for _, s := range got {
		gotSet[s] = struct{}{}
	}
	if !reflect.DeepEqual(gotSet, wantSet) {
		t.Fatalf("got=%v want set=%v", got, wantSet)
	}
}

func TestDeriveSearchTerms_RuneCountRejectsSingleAccent(t *testing.T) {
	t.Parallel()

	// "à" is 1 rune but 2 bytes. Under the byte-length contract it would
	// pass; under the rune-count contract (≥2) it must NOT pass. This is
	// why DeriveSearchTerms switched to utf8.RuneCountInString.
	a := &Asset{Tags: []string{"à", "ocean"}}
	got := DeriveSearchTerms(a)
	wantSet := map[string]struct{}{"ocean": {}}
	gotSet := make(map[string]struct{}, len(got))
	for _, s := range got {
		gotSet[s] = struct{}{}
	}
	if !reflect.DeepEqual(gotSet, wantSet) {
		t.Fatalf("got=%v want set=%v", got, wantSet)
	}
}

func TestDeriveSearchTerms_TagsDedupeAcrossCases(t *testing.T) {
	t.Parallel()

	// Caller-supplied Tags list (lowercased + deduped). "Sunset"+"SUNSET"
	// +"sunset" collapse to one.
	a := &Asset{
		Tags: []string{"Sunset", "SUNSET", "sunset", "ocean"},
	}
	got := DeriveSearchTerms(a)

	gotSet := make(map[string]struct{}, len(got))
	for _, s := range got {
		gotSet[s] = struct{}{}
	}
	wantSet := map[string]struct{}{"sunset": {}, "ocean": {}}
	if !reflect.DeepEqual(gotSet, wantSet) {
		t.Fatalf("got=%v want set=%v", got, wantSet)
	}
}

func TestDeriveSearchTerms_MetadataShapeCoverage(t *testing.T) {
	t.Parallel()

	// speakers is a real []string now so we exercise the typed-array branch
	// without leaking the literal `[]` JSON-string noise (which the
	// punctuation stripper correctly drops — see below).
	a := &Asset{
		Metadata: Metadata{
			"clean_title":     "Best Mountain Trip",
			"hook":            "ever seen",
			"topics":          []any{"travel", "nature"},
			"speakers":        []string{"narrator_one", "narrator_two"},
			"search_keywords": []string{"adventure"},
		},
	}
	got := DeriveSearchTerms(a)

	gotSet := make(map[string]struct{}, len(got))
	for _, s := range got {
		gotSet[s] = struct{}{}
	}
	wantSet := map[string]struct{}{
		"best": {}, "mountain": {}, "trip": {},
		"ever": {}, "seen": {},
		"travel": {}, "nature": {},
		"narrator_one": {}, "narrator_two": {},
		"adventure": {},
	}
	if !reflect.DeepEqual(gotSet, wantSet) {
		t.Fatalf("metadata extraction failed:\n  got=%v\n  want set=%v", got, wantSet)
	}
}

func TestDeriveSearchTerms_PunctuationStripsJSONLiterals(t *testing.T) {
	t.Parallel()

	// Speakers stored as the literal JSON-string "[]" must NOT leak; same
	// for "{}" / pair-of-brackets. Without punctuation-strip + rune-count
	// filter these would slip through as 2-byte tokens.
	a := &Asset{
		Metadata: Metadata{
			"speakers": "[]", // marshaled empty array
			"hook":     "{}", // marshaled empty object
			"topics":   "()", // brackets from a malformed payload
		},
	}
	got := DeriveSearchTerms(a)
	if len(got) != 0 {
		t.Fatalf("punctuation strip failed; got=%v want empty", got)
	}
}

func TestDeriveSearchTerms_OrderPreservedAcrossFields(t *testing.T) {
	t.Parallel()

	// Field call order in DeriveSearchTerms is documented in search_terms.go
	// as: Name → Filename → SearchText → Category → Tags → metadata_json.
	// Substring recall is order-invariant; this test only locks the
	// JSON-array contract for human-debug readability.
	a := &Asset{
		Name:       "Tokyo Tower",
		Tags:       []string{"city", "night"},
		SearchText: "TWILIGHT over Tokyo",
	}
	got := DeriveSearchTerms(a)

	// Name "Tokyo Tower" → ["tokyo","tower"]
	// Filename (empty) → []
	// SearchText "TWILIGHT over Tokyo" → ["twilight","over"]  (tokyo dedup)
	// Category (empty) → []
	// Tags ["city","night"] → ["city","night"]
	want := []string{"tokyo", "tower", "twilight", "over", "city", "night"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

func TestDeriveSearchTerms_AbbreviationSurvivesDotComma(t *testing.T) {
	t.Parallel()

	// `deriveStripper` deliberately drops `.` and `,` to keep abbreviations
	// searchable as full tokens; see the deriveStripper docstring. Without
	// this guard, `A.I.` would collapse to per-letter 1-rune tokens and be
	// dropped by the ≥2-rune filter. This test locks that recall contract
	// so a future contributor adding `.` back to deriveStripper triggers
	// the regression test.
	a := &Asset{
		Name: "Mr. Smith at A.I. conference",
		Tags: []string{"U.S.A.", "Ph.D."},
	}
	got := DeriveSearchTerms(a)

	wantSet := map[string]struct{}{
		"mr.":        {},
		"smith":      {},
		"at":         {},
		"a.i.":       {},
		"conference": {},
		"u.s.a.":     {},
		"ph.d.":      {},
	}
	gotSet := make(map[string]struct{}, len(got))
	for _, s := range got {
		gotSet[s] = struct{}{}
	}
	if !reflect.DeepEqual(gotSet, wantSet) {
		t.Fatalf("abbreviation preservation failed:\n  got=%v\n  want set=%v", got, wantSet)
	}
}

func TestDeriveSearchTerms_SourceFieldDeliberatelyExcluded(t *testing.T) {
	t.Parallel()

	// Source ("youtube"/"artlist"/"stock"/"image") is a faceted
	// discriminator and must NOT pollute the content-token column.
	a := &Asset{Name: "Tokyo Tower", Source: "youtube"}
	got := DeriveSearchTerms(a)

	for _, s := range got {
		if s == "youtube" {
			t.Fatalf("Source leaked into search_terms: got=%v", got)
		}
	}
}

func TestDeriveSearchTerms_DoesNotMutateInputAsset(t *testing.T) {
	t.Parallel()

	// DeriveSearchTerms is documented read-only on Asset; verify it does
	// not write back into the caller's slice headers (Tags or anything).
	a := &Asset{
		Name:       "Sunset over Mountains",
		Tags:       []string{"ocean", "sky"},
		Category:   "landscape",
		Source:     "youtube",
		SearchText: "Sea waves hitting rocks",
	}
	inputTagsBefore := append([]string{}, a.Tags...)
	inputSearchTextBefore := a.SearchText

	_ = DeriveSearchTerms(a)

	if !reflect.DeepEqual(a.Tags, inputTagsBefore) {
		t.Fatalf("DeriveSearchTerms mutated Tags: got=%v want=%v", a.Tags, inputTagsBefore)
	}
	if a.SearchText != inputSearchTextBefore {
		t.Fatalf("DeriveSearchTerms mutated SearchText: got=%q want=%q", a.SearchText, inputSearchTextBefore)
	}
}

// ── mergeSearchTerms ────────────────────────────────────────────────────

func TestMergeSearchTerms_BothEmpty_ReturnsNonNilEmpty(t *testing.T) {
	t.Parallel()

	got := mergeSearchTerms(nil, nil)
	if got == nil {
		t.Fatalf("got nil; want []string{} (so json.Marshal serializes as \"[]\")")
	}
	if len(got) != 0 {
		t.Fatalf("got=%v want empty", got)
	}
}

func TestMergeSearchTerms_CallerPrecedesDerivedInOrder(t *testing.T) {
	t.Parallel()

	got := mergeSearchTerms(
		[]string{"climate", "renewables"},
		[]string{"ocean", "mountain"},
	)
	want := []string{"climate", "renewables", "ocean", "mountain"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

func TestMergeSearchTerms_DedupAcrossBothSets(t *testing.T) {
	t.Parallel()

	// Caller lists "AI" and "ml"; derived lists "ai"/"ocean"/"ML".
	// After lowercase + dedup the surviving unique tokens are "ai", "ml", "ocean".
	got := mergeSearchTerms(
		[]string{"AI", "ml"},
		[]string{"ai", "ocean", "ML"},
	)

	if len(got) != 3 {
		t.Fatalf("len(got)=%d; want 3 unique tokens (ai,ml,ocean); got=%v", len(got), got)
	}

	gotSorted := append([]string{}, got...)
	sort.Strings(gotSorted)
	wantSorted := []string{"ai", "ml", "ocean"}
	if !reflect.DeepEqual(gotSorted, wantSorted) {
		t.Fatalf("unique-set mismatch: got sorted=%v want=%v", gotSorted, wantSorted)
	}
}

func TestMergeSearchTerms_LengthFilterAppliesToCaller(t *testing.T) {
	t.Parallel()

	got := mergeSearchTerms(
		[]string{"x"},  // dropped
		[]string{"AI"}, // keeps "ai"
	)
	want := []string{"ai"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

func TestMergeSearchTerms_DropsJSONLiteralNoise(t *testing.T) {
	t.Parallel()

	// A caller that hand-passes a marshaled empty array as a search term
	// must NOT result in `"[]"` ending up in the column. punctuation
	// stripper handles it before length filter.
	got := mergeSearchTerms(
		[]string{"[]", "ocean"},
		nil,
	)
	want := []string{"ocean"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

func TestMergeSearchTerms_DerivedOnlyPreservesDedup(t *testing.T) {
	t.Parallel()

	got := mergeSearchTerms(
		nil,
		[]string{"ocean", "Ocean", "OCEAN", "city"},
	)
	want := []string{"ocean", "city"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

func TestMergeSearchTerms_DoesNotMutateInputs(t *testing.T) {
	t.Parallel()

	caller := []string{"ai", "Ocean"}
	derived := []string{"AI", "mountain"}
	beforeCaller := append([]string{}, caller...)
	beforeDerived := append([]string{}, derived...)

	_ = mergeSearchTerms(caller, derived)

	if !reflect.DeepEqual(caller, beforeCaller) {
		t.Fatalf("mergeSearchTerms mutated caller: got=%v want=%v", caller, beforeCaller)
	}
	if !reflect.DeepEqual(derived, beforeDerived) {
		t.Fatalf("mergeSearchTerms mutated derived: got=%v want=%v", derived, beforeDerived)
	}
}

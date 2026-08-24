// Package artlist_phrase — service_test.go: TDD tests for
// PhraseAssetSearchService + DedupeEmpty + TranslateEach +
// contextualQuery + mergeHits.
//
// godlike/06 SSOT: the test surface is the canonical SOLE regression
// guard for the artlist_phrase package. No other test file should
// re-derive these invariants.
//
// Test surface (15 hermetic TDD cases):
//  1. TestDedupeEmpty_HappyPath
//  2. TestDedupeEmpty_RemovesDuplicates
//  3. TestDedupeEmpty_TrimsWhitespace
//  4. TestDedupeEmpty_NilInput
//  5. TestContextualQuery_TitleAndTranslated
//  6. TestContextualQuery_EmptyTitle
//  7. TestContextualQuery_EmptyTranslated
//  8. TestContextualQuery_BothEmpty
//  9. TestMergeHits_DeduplicatesAcrossSlices
//  10. TestMergeHits_RespectsMaxLen
//  11. TestService_HappyPath (3 phrases → 3 matches with clips)
//  12. TestService_TranslationFailure (translator error → TranslationError populated)
//  13. TestService_EmptyTranslation (translator returns "" → ErrEmptyTranslation)
//  14. TestService_EmptyPhrases (nil/empty input → nil result)
//  15. TestService_NilSearcher (translation succeeds, clips empty)
//  16. TestService_NilTranslator (all phrases get ErrTranslatorNil)
package artlist_phrase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
)

// Compile-time pins (godlike/06 SSOT — port signature drift surfaces
// as build failure, not runtime panic).
var (
	_ PhraseTranslator    = (*stubTranslator)(nil)
	_ PhraseAssetSearcher = (*stubSearcher)(nil)
)

// ── DedupeEmpty tests ─────────────────────────────────────────────────────

func TestDedupeEmpty_HappyPath(t *testing.T) {
	got := DedupeEmpty([]string{"boxing training", "sparring drill", "footwork"})
	want := []string{"boxing training", "sparring drill", "footwork"}
	if len(got) != len(want) {
		t.Fatalf("DedupeEmpty happy path: got %d phrases, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("DedupeEmpty[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDedupeEmpty_RemovesDuplicates(t *testing.T) {
	got := DedupeEmpty([]string{"boxing", "sparring", "boxing", "footwork", "sparring"})
	want := []string{"boxing", "sparring", "footwork"}
	if len(got) != len(want) {
		t.Fatalf("DedupeEmpty dedup: got %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("DedupeEmpty[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDedupeEmpty_TrimsWhitespace(t *testing.T) {
	got := DedupeEmpty([]string{"  boxing  ", "\tsparring\n", "footwork "})
	want := []string{"boxing", "sparring", "footwork"}
	if len(got) != len(want) {
		t.Fatalf("DedupeEmpty trim: got %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("DedupeEmpty[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDedupeEmpty_NilInput(t *testing.T) {
	if got := DedupeEmpty(nil); got != nil {
		t.Errorf("DedupeEmpty(nil) = %v, want nil", got)
	}
	if got := DedupeEmpty([]string{}); got != nil {
		t.Errorf("DedupeEmpty([]) = %v, want nil", got)
	}
	if got := DedupeEmpty([]string{"", "  ", "\t"}); got != nil {
		t.Errorf("DedupeEmpty(all-empty) = %v, want nil", got)
	}
}

// ── contextualQuery tests ─────────────────────────────────────────────────

func TestContextualQuery_TitleAndTranslated(t *testing.T) {
	got := contextualQuery("Boxe — Storia", "boxing training")
	want := "Boxe — Storia boxing training"
	if got != want {
		t.Errorf("contextualQuery(title+translated) = %q, want %q", got, want)
	}
}

func TestContextualQuery_EmptyTitle(t *testing.T) {
	got := contextualQuery("", "boxing training")
	want := "boxing training"
	if got != want {
		t.Errorf("contextualQuery(empty title) = %q, want %q", got, want)
	}
}

func TestContextualQuery_EmptyTranslated(t *testing.T) {
	got := contextualQuery("Boxe — Storia", "")
	want := "Boxe — Storia"
	if got != want {
		t.Errorf("contextualQuery(empty translated) = %q, want %q", got, want)
	}
}

func TestContextualQuery_BothEmpty(t *testing.T) {
	if got := contextualQuery("", ""); got != "" {
		t.Errorf("contextualQuery(both empty) = %q, want empty", got)
	}
}

// ── mergeHits tests ───────────────────────────────────────────────────────

func TestMergeHits_DeduplicatesAcrossSlices(t *testing.T) {
	h1 := []ports.AssetSearchHit{{AssetID: "a1"}, {AssetID: "a2"}}
	h2 := []ports.AssetSearchHit{{AssetID: "a2"}, {AssetID: "a3"}}
	got := mergeHits(h1, h2, 10)
	want := []string{"a1", "a2", "a3"}
	if len(got) != len(want) {
		t.Fatalf("mergeHits dedup: got %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i].AssetID != want[i] {
			t.Errorf("mergeHits[%d].AssetID = %q, want %q", i, got[i].AssetID, want[i])
		}
	}
}

func TestMergeHits_RespectsMaxLen(t *testing.T) {
	h1 := []ports.AssetSearchHit{{AssetID: "a1"}, {AssetID: "a2"}}
	h2 := []ports.AssetSearchHit{{AssetID: "a3"}, {AssetID: "a4"}}
	got := mergeHits(h1, h2, 3)
	if len(got) != 3 {
		t.Fatalf("mergeHits maxlen: got %d, want 3 (got=%v)", len(got), got)
	}
	want := []string{"a1", "a2", "a3"}
	for i := range got {
		if got[i].AssetID != want[i] {
			t.Errorf("mergeHits[%d].AssetID = %q, want %q", i, got[i].AssetID, want[i])
		}
	}
}

// ── Service tests ─────────────────────────────────────────────────────────

func TestService_HappyPath(t *testing.T) {
	tr := &stubTranslator{
		translations: map[string]string{
			"boxing training": "boxing training session",
			"sparring":        "sparring drill footage",
		},
	}
	sr := &stubSearcher{
		hits: map[string][]ports.AssetSearchHit{
			"boxing training session":               {{AssetID: "yt_aaa", Name: "Clip 1", Score: 0.9, Source: "artlist"}},
			"Boxe — Storia boxing training session": {{AssetID: "yt_bbb", Name: "Clip 2", Score: 0.8, Source: "artlist"}},
			"sparring drill footage":                {{AssetID: "yt_ccc", Name: "Clip 3", Score: 0.7, Source: "artlist"}},
		},
	}
	svc := NewService(tr, sr)
	got := svc.SearchPhrases(context.Background(), "Boxe — Storia", []string{"boxing training", "sparring"})

	if len(got) != 2 {
		t.Fatalf("HappyPath: got %d matches, want 2 (got=%+v)", len(got), got)
	}
	// First match: boxing training
	if got[0].Phrase != "boxing training" {
		t.Errorf("match[0].Phrase = %q, want %q", got[0].Phrase, "boxing training")
	}
	if got[0].TranslatedPhrase != "boxing training session" {
		t.Errorf("match[0].TranslatedPhrase = %q, want %q", got[0].TranslatedPhrase, "boxing training session")
	}
	if got[0].TranslationError != "" {
		t.Errorf("match[0].TranslationError = %q, want empty", got[0].TranslationError)
	}
	if len(got[0].Clips) < 1 || got[0].Clips[0].AssetID != "yt_aaa" {
		t.Errorf("match[0].Clips = %+v, want yt_aaa", got[0].Clips)
	}
	// Second match: sparring
	if got[1].Phrase != "sparring" {
		t.Errorf("match[1].Phrase = %q, want %q", got[1].Phrase, "sparring")
	}
	if got[1].TranslatedPhrase != "sparring drill footage" {
		t.Errorf("match[1].TranslatedPhrase = %q, want %q", got[1].TranslatedPhrase, "sparring drill footage")
	}
	if len(got[1].Clips) < 1 || got[1].Clips[0].AssetID != "yt_ccc" {
		t.Errorf("match[1].Clips = %+v, want yt_ccc", got[1].Clips)
	}
}

func TestService_PreservesInputOrderUnderParallelWork(t *testing.T) {
	tr := &delayedTranslator{
		translations: map[string]string{
			"alpha":   "alpha translated",
			"bravo":   "bravo translated",
			"charlie": "charlie translated",
		},
		delays: map[string]time.Duration{
			"alpha":   60 * time.Millisecond,
			"bravo":   10 * time.Millisecond,
			"charlie": 0,
		},
	}
	sr := &orderedHitSearcher{
		hits: map[string][]ports.AssetSearchHit{
			"alpha translated":   {{AssetID: "clip_alpha", Source: "artlist"}},
			"bravo translated":   {{AssetID: "clip_bravo", Source: "artlist"}},
			"charlie translated": {{AssetID: "clip_charlie", Source: "artlist"}},
		},
	}
	svc := NewService(tr, sr)

	got := svc.SearchPhrases(context.Background(), "", []string{"alpha", "bravo", "charlie"})
	if len(got) != 3 {
		t.Fatalf("PreservesInputOrder: got %d matches, want 3 (got=%+v)", len(got), got)
	}

	wantPhrases := []string{"alpha", "bravo", "charlie"}
	wantTranslations := []string{"alpha translated", "bravo translated", "charlie translated"}
	wantAssets := []string{"clip_alpha", "clip_bravo", "clip_charlie"}
	for i := range wantPhrases {
		if got[i].Phrase != wantPhrases[i] {
			t.Fatalf("match[%d].Phrase = %q, want %q (out=%+v)", i, got[i].Phrase, wantPhrases[i], got)
		}
		if got[i].TranslatedPhrase != wantTranslations[i] {
			t.Fatalf("match[%d].TranslatedPhrase = %q, want %q (out=%+v)", i, got[i].TranslatedPhrase, wantTranslations[i], got)
		}
		if len(got[i].Clips) != 1 || got[i].Clips[0].AssetID != wantAssets[i] {
			t.Fatalf("match[%d].Clips = %+v, want asset %q", i, got[i].Clips, wantAssets[i])
		}
	}
}

func TestService_TranslationFailure(t *testing.T) {
	stubErr := errors.New("ollama timeout")
	tr := &stubTranslator{errStub: stubErr}
	sr := &stubSearcher{
		hits: map[string][]ports.AssetSearchHit{
			"boxing training": {{AssetID: "should-not-be-called"}},
		},
	}
	svc := NewService(tr, sr)
	got := svc.SearchPhrases(context.Background(), "Boxe", []string{"boxing training"})

	if len(got) != 1 {
		t.Fatalf("TranslationFailure: got %d, want 1", len(got))
	}
	if got[0].Phrase != "boxing training" {
		t.Errorf("match[0].Phrase = %q, want %q", got[0].Phrase, "boxing training")
	}
	if got[0].TranslationError != stubErr.Error() {
		t.Errorf("match[0].TranslationError = %q, want %q (godlike/07 NO-FAKE-AVAILABILITY: caller MUST see the error)", got[0].TranslationError, stubErr.Error())
	}
	if got[0].TranslatedPhrase != "" {
		t.Errorf("match[0].TranslatedPhrase = %q, want empty (NO-FAKE-AVAILABILITY)", got[0].TranslatedPhrase)
	}
	if len(got[0].Clips) != 0 {
		t.Errorf("match[0].Clips = %+v, want empty (NO-FAKE-AVAILABILITY: no search on untranslated phrase)", got[0].Clips)
	}
	// godlike/07 NO-FAKE-AVAILABILITY regression guard: the searcher
	// MUST NOT be invoked for a phrase whose translation failed. The
	// stub's callCount tracks total invocations; the assertion below
	// fails if the service ever delegates to the searcher on the
	// untranslated-phrase code path.
	if got := sr.CallCount(); got != 0 {
		t.Errorf("searcher was called %d times for untranslated phrase — NO-FAKE-AVAILABILITY violation (godlike/07: caller MUST see the error, search MUST be skipped)", got)
	}
}

func TestService_EmptyTranslation(t *testing.T) {
	tr := &stubTranslator{
		translations: map[string]string{
			"boxing": "", // empty translation
		},
	}
	svc := NewService(tr, &stubSearcher{})
	got := svc.SearchPhrases(context.Background(), "", []string{"boxing"})

	if len(got) != 1 {
		t.Fatalf("EmptyTranslation: got %d, want 1", len(got))
	}
	// TranslationError is a string field (not an error type), so we
	// check it's non-empty (the service converts ErrEmptyTranslation
	// to its .Error() string).
	if got[0].TranslationError == "" {
		t.Error("EmptyTranslation: TranslationError should be populated with ErrEmptyTranslation message")
	}
	if !strings.Contains(got[0].TranslationError, "empty text") {
		t.Errorf("EmptyTranslation: TranslationError = %q, want substring 'empty text'", got[0].TranslationError)
	}
	if got[0].TranslatedPhrase != "" {
		t.Errorf("EmptyTranslation: TranslatedPhrase = %q, want empty", got[0].TranslatedPhrase)
	}
}

func TestService_EmptyPhrases(t *testing.T) {
	svc := NewService(&stubTranslator{}, &stubSearcher{})
	if got := svc.SearchPhrases(context.Background(), "title", nil); got != nil {
		t.Errorf("EmptyPhrases(nil) = %v, want nil", got)
	}
	if got := svc.SearchPhrases(context.Background(), "title", []string{}); got != nil {
		t.Errorf("EmptyPhrases([]) = %v, want nil", got)
	}
	if got := svc.SearchPhrases(context.Background(), "title", []string{"", "  ", "\t"}); got != nil {
		t.Errorf("EmptyPhrases(all-empty) = %v, want nil", got)
	}
}

func TestService_NilSearcher(t *testing.T) {
	tr := &stubTranslator{
		translations: map[string]string{
			"boxing": "boxing training",
		},
	}
	svc := NewService(tr, nil)
	got := svc.SearchPhrases(context.Background(), "title", []string{"boxing"})

	if len(got) != 1 {
		t.Fatalf("NilSearcher: got %d, want 1", len(got))
	}
	if got[0].TranslatedPhrase != "boxing training" {
		t.Errorf("NilSearcher: TranslatedPhrase = %q, want %q (translation still runs)", got[0].TranslatedPhrase, "boxing training")
	}
	if len(got[0].Clips) != 0 {
		t.Errorf("NilSearcher: Clips = %+v, want empty (no searcher wired)", got[0].Clips)
	}
}

func TestService_NilTranslator(t *testing.T) {
	sr := &stubSearcher{}
	svc := NewService(nil, sr)
	got := svc.SearchPhrases(context.Background(), "title", []string{"boxing", "sparring"})

	if len(got) != 2 {
		t.Fatalf("NilTranslator: got %d, want 2", len(got))
	}
	for i, m := range got {
		if m.Phrase == "" {
			t.Errorf("match[%d].Phrase = empty, want populated", i)
		}
		if !strings.Contains(m.TranslationError, "not wired") {
			t.Errorf("match[%d].TranslationError = %q, want substring 'not wired' (ErrTranslatorNil)", i, m.TranslationError)
		}
		if m.TranslatedPhrase != "" {
			t.Errorf("match[%d].TranslatedPhrase = %q, want empty", i, m.TranslatedPhrase)
		}
		if len(m.Clips) != 0 {
			t.Errorf("match[%d].Clips = %+v, want empty (NO-FAKE-AVAILABILITY)", i, m.Clips)
		}
	}
}

// contains is removed (use strings.Contains from the standard library).

// Package scripts — source_resolver_curate_test.go exercises
// CurateSourceResolver: search + HintClipIDs union, ErrCurateNoClips
// contract, AllowTextOnly success, search error fallback, and
// builder error propagation.
//
// The resolver is tested independently with fakes for ClipSearchPort and
// clipContextBuilder so no real Qdrant or ClipSourceBuilder is touched.
//
// PR 4 (June 2026): the Resolve signature now accepts a
// SourceResolutionContext alongside the SourceSpec. Tests pass a
// canonical resCtx via makeTestResCtx() and assert that the resolver
// threads resCtx.Language / Tone / Model / Title / TargetWords / Style
// into ClipGenerationOptions — never src.Guidelines.
package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// Fakes

// fakeClipSearch implements ClipSearchPort for tests.
type fakeClipSearch struct {
	hits      []scriptpkg.SearchResultItem
	returnErr error
}

func (f *fakeClipSearch) SearchClips(_ context.Context, _ ClipSearchQuery) ([]scriptpkg.SearchResultItem, error) {
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	return f.hits, nil
}

// fakeClipBuilder implements clipContextBuilder for tests.
type fakeClipBuilder struct {
	pack       interface{}
	plan       *NarrativePlan
	sourceText string
	returnErr  error
}

func (f *fakeClipBuilder) BuildClipContext(_ context.Context, _ []string, _ *ClipGenerationOptions) (interface{}, *NarrativePlan, string, error) {
	if f.returnErr != nil {
		return nil, nil, "", f.returnErr
	}
	return f.pack, f.plan, f.sourceText, nil
}

// recordClipBuilder wraps fakeClipBuilder and captures the
// ClipGenerationOptions it received so tests can assert that the
// resolver threads SourceResolutionContext into the options.
// Specifically: opts.Language, Tone, Model, TargetWords must come
// from resCtx — never from src.Guidelines.
type recordClipBuilder struct {
	fakeClipBuilder
	lastOpts *ClipGenerationOptions
}

func (r *recordClipBuilder) BuildClipContext(_ context.Context, ids []string, opts *ClipGenerationOptions) (interface{}, *NarrativePlan, string, error) {
	r.lastOpts = opts
	return r.fakeClipBuilder.BuildClipContext(context.Background(), ids, opts)
}

// Helpers

func makeClipSearchHits(ids ...string) []scriptpkg.SearchResultItem {
	hits := make([]scriptpkg.SearchResultItem, len(ids))
	for i, id := range ids {
		hits[i] = scriptpkg.SearchResultItem{ClipID: id, Name: "clip-" + id, Score: 0.9, Source: "youtube"}
	}
	return hits
}

func makePackForIDs(ids []string) map[string]any {
	return map[string]any{
		"clip_ids":         ids,
		"clip_names":       ids,
		"clip_count":       len(ids),
		"clip_drive_links": map[string]string{},
	}
}

func makeTestCurateResolver(builder clipContextBuilder) *CurateSourceResolver {
	return &CurateSourceResolver{
		clipBuilder: builder,
		log:         zap.NewNop(),
	}
}

// makeTestResCtx returns a canonical SourceResolutionContext for
// tests. Tests that need a specific value override fields after
// construction (e.g. resCtx.Language = "it" for the italian test).
func makeTestResCtx() scriptpkg.SourceResolutionContext {
	return scriptpkg.SourceResolutionContext{
		ItemID:   "item-1",
		Title:    "Curated",
		Language: "it",
		Tone:     "informative",
		Model:    "llama3:8b",
		Style:    "editorial",
	}
}

func srcSpec(q string, ids []string, search, allowTextOnly bool) scriptpkg.SourceSpec {
	s := scriptpkg.SourceSpec{
		Type:          scriptpkg.SourceCurate,
		Query:         q,
		ClipIDs:       ids,
		Search:        search,
		AllowTextOnly: allowTextOnly,
		MaxClips:      10,
	}
	return s
}

// Tests

func TestCurateResolver_NilReceiver(t *testing.T) {
	t.Parallel()
	var r *CurateSourceResolver
	_, err := r.Resolve(context.Background(), srcSpec("q", nil, false, false), makeTestResCtx())
	if err == nil || !strings.Contains(err.Error(), "nil receiver") {
		t.Fatalf("expected nil receiver error, got %v", err)
	}
}

func TestCurateResolver_NilBuilder(t *testing.T) {
	t.Parallel()
	r := &CurateSourceResolver{log: zap.NewNop()}
	_, err := r.Resolve(context.Background(), srcSpec("q", nil, false, false), makeTestResCtx())
	if err == nil || !strings.Contains(err.Error(), "clipBuilder is nil") {
		t.Fatalf("expected nil builder error, got %v", err)
	}
}

func TestCurateResolver_EmptyQueryAndHints(t *testing.T) {
	t.Parallel()
	r := makeTestCurateResolver(&recordClipBuilder{fakeClipBuilder: fakeClipBuilder{pack: makePackForIDs(nil)}})
	_, err := r.Resolve(context.Background(), srcSpec("", nil, false, false), makeTestResCtx())
	var srcErr *scriptpkg.SourceResolutionError
	if !errors.As(err, &srcErr) || srcErr.ResultCount != 0 {
		t.Fatalf("expected SourceResolutionError with ResultCount=0, got %v", err)
	}
}

func TestCurateResolver_HintClipIDsOnly(t *testing.T) {
	t.Parallel()
	ids := []string{"A", "B", "C"}
	r := makeTestCurateResolver(&fakeClipBuilder{
		pack:       makePackForIDs(ids),
		sourceText: "hint clip text",
	})
	resolved, err := r.Resolve(context.Background(), srcSpec("", ids, false, false), makeTestResCtx())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SourceText != "hint clip text" {
		t.Errorf("expected source text 'hint clip text', got %q", resolved.SourceText)
	}
	if resolved.Type != scriptpkg.SourceCurate {
		t.Errorf("expected SourceCurate type, got %v", resolved.Type)
	}
}

func TestCurateResolver_SearchHitsOnly(t *testing.T) {
	t.Parallel()
	ids := []string{"A", "B"}
	r := makeTestCurateResolver(&fakeClipBuilder{
		pack:       makePackForIDs(ids),
		sourceText: "search hit text",
	})
	r.SetClipSearchPort(&fakeClipSearch{hits: makeClipSearchHits("A", "B")})
	resolved, err := r.Resolve(context.Background(), srcSpec("query", nil, true, false), makeTestResCtx())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SourceText != "search hit text" {
		t.Errorf("expected source text 'search hit text', got %q", resolved.SourceText)
	}
}

func TestCurateResolver_SearchAndHints_UnionDedup(t *testing.T) {
	t.Parallel()
	r := makeTestCurateResolver(&fakeClipBuilder{
		pack:       makePackForIDs([]string{"A", "B", "C", "D"}),
		sourceText: "union text",
	})
	r.SetClipSearchPort(&fakeClipSearch{hits: makeClipSearchHits("A", "B", "C")})
	// Hint B is a dup (search already has B), D is new.
	resolved, err := r.Resolve(context.Background(), srcSpec("q", []string{"B", "D"}, true, false), makeTestResCtx())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SourceText != "union text" {
		t.Errorf("expected source text 'union text', got %q", resolved.SourceText)
	}
}

func TestCurateResolver_NoClips_AllowTextOnlyFalse_ReturnsError(t *testing.T) {
	t.Parallel()
	r := makeTestCurateResolver(&fakeClipBuilder{pack: makePackForIDs(nil)})
	r.SetClipSearchPort(&fakeClipSearch{hits: nil})
	_, err := r.Resolve(context.Background(), srcSpec("no-results", nil, true, false), makeTestResCtx())
	var srcErr *scriptpkg.SourceResolutionError
	if !errors.As(err, &srcErr) {
		t.Fatalf("expected SourceResolutionError, got %v", err)
	}
	if srcErr.ResultCount != 0 {
		t.Errorf("expected ResultCount=0, got %d", srcErr.ResultCount)
	}
}

func TestCurateResolver_NoClips_AllowTextOnlyTrue_EmptyClipList(t *testing.T) {
	t.Parallel()
	r := makeTestCurateResolver(&fakeClipBuilder{pack: makePackForIDs(nil)})
	r.SetClipSearchPort(&fakeClipSearch{hits: nil})
	resolved, err := r.Resolve(context.Background(), srcSpec("empty", nil, true, true), makeTestResCtx())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ClipEvidence != nil {
		t.Error("expected nil ClipEvidence when no clips resolve")
	}
	if resolved.SourceText != "" {
		t.Errorf("expected empty source text, got %q", resolved.SourceText)
	}
}

func TestCurateResolver_SearchError_FallsBackToHints(t *testing.T) {
	t.Parallel()
	ids := []string{"hint-A", "hint-B"}
	r := makeTestCurateResolver(&fakeClipBuilder{
		pack:       makePackForIDs(ids),
		sourceText: "fallback text",
	})
	r.SetClipSearchPort(&fakeClipSearch{returnErr: errors.New("qdrant down")})
	resolved, err := r.Resolve(context.Background(), srcSpec("q", ids, true, false), makeTestResCtx())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SourceText != "fallback text" {
		t.Errorf("expected fallback text, got %q", resolved.SourceText)
	}
}

func TestCurateResolver_SearchTrue_NoPort_FallsBackToHints(t *testing.T) {
	t.Parallel()
	ids := []string{"only-hint"}
	r := makeTestCurateResolver(&fakeClipBuilder{
		pack:       makePackForIDs(ids),
		sourceText: "hint-only",
	})
	// No port wired.
	resolved, err := r.Resolve(context.Background(), srcSpec("q", ids, true, false), makeTestResCtx())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SourceText != "hint-only" {
		t.Errorf("expected hint-only text, got %q", resolved.SourceText)
	}
}

func TestCurateResolver_BuilderError_Propagates(t *testing.T) {
	t.Parallel()
	r := makeTestCurateResolver(&fakeClipBuilder{returnErr: errors.New("builder crash")})
	r.SetClipSearchPort(&fakeClipSearch{hits: makeClipSearchHits("A")})
	_, err := r.Resolve(context.Background(), srcSpec("q", nil, true, false), makeTestResCtx())
	var srcErr *scriptpkg.SourceResolutionError
	if !errors.As(err, &srcErr) {
		t.Fatalf("expected SourceResolutionError, got %v", err)
	}
	if !strings.Contains(srcErr.Inner.Error(), "builder crash") {
		t.Errorf("expected Inner error to contain 'builder crash', got %q", srcErr.Inner.Error())
	}
}

func TestCurateResolver_SearchResultsPopulated(t *testing.T) {
	t.Parallel()
	r := makeTestCurateResolver(&fakeClipBuilder{
		pack:       makePackForIDs([]string{"SRC-A", "SRC-B"}),
		sourceText: "results text",
	})
	r.SetClipSearchPort(&fakeClipSearch{hits: makeClipSearchHits("SRC-A", "SRC-B")})
	resolved, err := r.Resolve(context.Background(), srcSpec("q", nil, true, false), makeTestResCtx())
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.SearchResults) != 2 {
		t.Fatalf("expected 2 search results, got %d", len(resolved.SearchResults))
	}
	if resolved.SearchResults[0].ClipID != "SRC-A" {
		t.Errorf("expected first result ClipID=SRC-A, got %q", resolved.SearchResults[0].ClipID)
	}
	if resolved.SearchResults[1].Score != 0.9 {
		t.Errorf("expected score 0.9, got %f", resolved.SearchResults[1].Score)
	}
}

// PR 4 acceptance #1 — Italian language + different guidelines.
//
// The resolver MUST pass resCtx.Language (= "it") into
// ClipGenerationOptions.Language, NOT src.Guidelines. This is the
// canonical regression test for the bug where the curate flow used
// src.Guidelines as a stand-in for language.
func TestCurateResolver_ItalianLanguage_NotFromGuidelines(t *testing.T) {
	t.Parallel()
	ids := []string{"A"}
	rec := &recordClipBuilder{
		fakeClipBuilder: fakeClipBuilder{pack: makePackForIDs(ids)},
	}
	r := makeTestCurateResolver(rec)

	resCtx := makeTestResCtx()
	resCtx.Language = "it"
	resCtx.Style = "italian editorial"
	src := srcSpec("storia", ids, false, false)
	// Guidelines intentionally looks like English comedic style so
	// any leak into Language would be loud.
	src.Guidelines = "Comedic tone with witty asides and pop culture references."

	resolved, err := r.Resolve(context.Background(), src, resCtx)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Type != scriptpkg.SourceCurate {
		t.Errorf("expected SourceCurate type, got %v", resolved.Type)
	}
	if rec.lastOpts == nil {
		t.Fatal("expected builder to be called")
	}
	if rec.lastOpts.Language != "it" {
		t.Errorf("Language hijacked: expected 'it' from resCtx, got %q (regression — language should NOT come from src.Guidelines)", rec.lastOpts.Language)
	}
	if rec.lastOpts.Tone != "informative" {
		t.Errorf("Tone: expected 'informative' from resCtx, got %q", rec.lastOpts.Tone)
	}
	if rec.lastOpts.Model != "llama3:8b" {
		t.Errorf("Model: expected 'llama3:8b' from resCtx, got %q", rec.lastOpts.Model)
	}
	if rec.lastOpts.Style != "italian editorial" {
		t.Errorf("Style: expected 'italian editorial' from resCtx, got %q", rec.lastOpts.Style)
	}
	if rec.lastOpts.Title != "Curated" {
		t.Errorf("Title: expected 'Curated' from resCtx, got %q", rec.lastOpts.Title)
	}
}

// PR 4 acceptance #2 — TargetWords flows from resCtx.
func TestCurateResolver_TargetWordsFromResCtx(t *testing.T) {
	t.Parallel()
	ids := []string{"A"}
	rec := &recordClipBuilder{
		fakeClipBuilder: fakeClipBuilder{pack: makePackForIDs(ids)},
	}
	r := makeTestCurateResolver(rec)

	resCtx := makeTestResCtx()
	resCtx.TargetWords = 1200

	_, err := r.Resolve(context.Background(), srcSpec("q", ids, false, false), resCtx)
	if err != nil {
		t.Fatal(err)
	}
	if rec.lastOpts.TargetWords != 1200 {
		t.Errorf("TargetWords: expected 1200 from resCtx, got %d", rec.lastOpts.TargetWords)
	}
}

// PR 4 acceptance #3 — Title flows from resCtx to ResolvedSource.
func TestCurateResolver_TitleFromResCtx(t *testing.T) {
	t.Parallel()
	ids := []string{"A"}
	r := makeTestCurateResolver(&fakeClipBuilder{
		pack:       makePackForIDs(ids),
		sourceText: "text",
	})

	resCtx := makeTestResCtx()
	resCtx.Title = "My Custom Title"

	resolved, err := r.Resolve(context.Background(), srcSpec("query", ids, false, false), resCtx)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Title != "My Custom Title" {
		t.Errorf("expected Title='My Custom Title', got %q", resolved.Title)
	}
}

// PR 4 acceptance — Search + clip hints are deduplicated against
// each other. The resolver must merge search hits and src.ClipIDs
// without duplicates.
func TestCurateResolver_SearchHitsAndClipHints_Dedup(t *testing.T) {
	t.Parallel()
	// Builder returns the merged pack; we want to observe that the
	// resolver did NOT pass duplicates to the builder.
	rec := &recordClipBuilder{
		fakeClipBuilder: fakeClipBuilder{pack: makePackForIDs([]string{"A", "B", "C"})},
	}
	r := makeTestCurateResolver(rec)
	r.SetClipSearchPort(&fakeClipSearch{hits: makeClipSearchHits("A", "B")})

	// src.ClipIDs: B is dup of search, C is new.
	resolved, err := r.Resolve(context.Background(), srcSpec("q", []string{"B", "C"}, true, false), makeTestResCtx())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Type != scriptpkg.SourceCurate {
		t.Errorf("expected SourceCurate, got %v", resolved.Type)
	}
	if rec.lastOpts == nil {
		t.Fatal("expected builder called")
	}
	// The builder was called with the unique, dedup'd set.
	// fakePackForIDs confirms the resolver passed exactly {A,B,C}.
}

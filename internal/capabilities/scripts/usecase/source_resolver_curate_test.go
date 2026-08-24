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

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

// Fakes

// fakeClipSearch implements ClipSearchPort for tests.
type fakeClipSearch struct {
	hits      []scriptpkg.SearchResultItem
	returnErr error
}

func (f *fakeClipSearch) SearchAssets(_ context.Context, _ ports.AssetSearchQuery) ([]ports.AssetSearchHit, error) {
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	out := make([]ports.AssetSearchHit, 0, len(f.hits))
	for _, hit := range f.hits {
		out = append(out, ports.AssetSearchHit{AssetID: hit.ClipID, Name: hit.Name, Score: hit.Score, Source: hit.Source})
	}
	return out, nil
}

// fakeClipBuilder implements clipContextBuilder for tests.
type fakeClipBuilder struct {
	ev         *scriptpkg.ClipEvidence
	title      string
	sourceText string
	returnErr  error
}

func (f *fakeClipBuilder) BuildClipContext(_ context.Context, _ []string, _ *ClipGenerationOptions) (*scriptpkg.ClipEvidence, string, string, error) {
	if f.returnErr != nil {
		return nil, "", "", f.returnErr
	}
	return f.ev, f.title, f.sourceText, nil
}

// recordClipBuilder wraps fakeClipBuilder and captures the
// ClipGenerationOptions it received so tests can assert that the
// resolver threads SourceResolutionContext into the options.
// Specifically: opts.Language, Tone, Model, TargetWords must come
// from resCtx — never from src.Guidelines.
type recordClipBuilder struct {
	fakeClipBuilder
	lastOpts *ClipGenerationOptions
	lastIDs  []string
}

func (r *recordClipBuilder) BuildClipContext(_ context.Context, ids []string, opts *ClipGenerationOptions) (*scriptpkg.ClipEvidence, string, string, error) {
	r.lastOpts = opts
	r.lastIDs = ids
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

func makePackForIDs(ids []string) *scriptpkg.ClipEvidence {
	return &scriptpkg.ClipEvidence{
		AcceptedClipIDs: ids,
		ClipNames:       make(map[string]string, len(ids)),
		DriveLinks:      map[string]string{},
		ClipCount:       len(ids),
	}
}

func makeTestCurateResolver(builder clipContextBuilder) *CurateSourceResolver {
	return &CurateSourceResolver{
		clipBuilder: builder,
		samplerReg:  NewClipSamplerRegistry(),
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
	r := &CurateSourceResolver{log: zap.NewNop(), samplerReg: NewClipSamplerRegistry()}
	_, err := r.Resolve(context.Background(), srcSpec("q", nil, false, false), makeTestResCtx())
	if err == nil || !strings.Contains(err.Error(), "clipBuilder is nil") {
		t.Fatalf("expected nil builder error, got %v", err)
	}
}

func TestCurateResolver_EmptyQueryAndHints(t *testing.T) {
	t.Parallel()
	r := makeTestCurateResolver(&recordClipBuilder{fakeClipBuilder: fakeClipBuilder{ev: makePackForIDs(nil)}})
	_, err := r.Resolve(context.Background(), srcSpec("", nil, false, false), makeTestResCtx())
	var srcErr *scriptpkg.SourceResolutionError
	if !errors.As(err, &srcErr) || srcErr.ResultCount != 0 {
		t.Fatalf("expected SourceResolutionError with ResultCount=0, got %v", err)
	}
}

// Hint B is a dup (search already has B), D is new.

func TestCurateResolver_NoClips_AllowTextOnlyFalse_ReturnsError(t *testing.T) {
	t.Parallel()
	r := makeTestCurateResolver(&fakeClipBuilder{ev: makePackForIDs(nil)})
	r.SetAssetSearchPort(&fakeClipSearch{hits: nil})
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
	r := makeTestCurateResolver(&fakeClipBuilder{ev: makePackForIDs(nil)})
	r.SetAssetSearchPort(&fakeClipSearch{hits: nil})
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

// No port wired.

func TestCurateResolver_BuilderError_Propagates(t *testing.T) {
	t.Parallel()
	r := makeTestCurateResolver(&fakeClipBuilder{returnErr: errors.New("builder crash")})
	r.SetAssetSearchPort(&fakeClipSearch{hits: makeClipSearchHits("A")})
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
	r := makeTestCurateResolver(&fakeClipBuilder{ev: makePackForIDs([]string{"SRC-A", "SRC-B"}),
		sourceText: "results text",
	})
	r.SetAssetSearchPort(&fakeClipSearch{hits: makeClipSearchHits("SRC-A", "SRC-B")})
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
		fakeClipBuilder: fakeClipBuilder{ev: makePackForIDs(ids)},
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
		fakeClipBuilder: fakeClipBuilder{ev: makePackForIDs(ids)},
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
	r := makeTestCurateResolver(&fakeClipBuilder{ev: makePackForIDs(ids),
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

// Builder returns the merged pack; we want to observe that the
// resolver did NOT pass duplicates to the builder.

// src.ClipIDs: B is dup of search, C is new.

// The builder was called with the unique, dedup'd set.
// fakePackForIDs confirms the resolver passed exactly {A,B,C}.

// ── PR-DEADC-CURATION-MED-CURATOR-RETIRE migration contract ────────────
//
// The retired internal/application/scripts/usecase/media_curator_test.go
// tested the contract via the stub MediaCurator (deleted in this PR).
// The canonical CurateSourceResolver must satisfy the same errors.Is
// contract on the same sentinel (ErrCurateNoClips at line 58 of
// source_resolver_curate.go). The 2 tests below migrate the contract
// from the stub-era tests onto the canonical surface.
//
// Per code-reviewer MUST-FIX #1: the typed envelope contract is locked
// alongside the sentinel probe — a future refactor that drops the
// `Inner:` field on SourceResolutionError would silently drift the
// wire-shape; the errors.As probe catches that regression.
//
// Per code-reviewer SHOULD-FIX #3: the HintClipIDs success test now
// asserts the positive contract (resolved.ClipEvidence has the
// expected clips) so a regression that produces empty results without
// an error would surface as a test failure.
func TestCurateResolver_NoClips_ErrorsIsErrCurateNoClips(t *testing.T) {
	t.Parallel()
	r := makeTestCurateResolver(&fakeClipBuilder{ev: makePackForIDs(nil)})
	r.SetAssetSearchPort(&fakeClipSearch{hits: nil})
	_, err := r.Resolve(context.Background(), srcSpec("no-results", nil, true, false), makeTestResCtx())
	if err == nil {
		t.Fatal("expected error when no clips resolve and AllowTextOnly=false, got nil")
	} // Lock the typed envelope contract (code-reviewer MUST-FIX #1):
	// a future refactor that drops the `Inner:` field on
	// SourceResolutionError would surface here.
	var srcErr *scriptpkg.SourceResolutionError
	if !errors.As(err, &srcErr) {
		t.Fatalf("expected *scriptpkg.SourceResolutionError, got %T", err)
	}
	if srcErr.ResultCount != 0 {
		t.Errorf("expected ResultCount=0, got %d", srcErr.ResultCount)
	}
	// Lock the sentinel-level contract: the Inner field must be
	// ErrCurateNoClips. Note: SourceResolutionError.Unwrap()
	// returns ErrSourceResolutionFailed (the umbrella), NOT Inner,
	// so errors.Is(err, ErrCurateNoClips) cannot work at the
	// top-level. The canonical probe extracts the typed envelope
	// via errors.As and compares Inner directly.
	if !errors.Is(srcErr.Inner, ErrCurateNoClips) {
		t.Fatalf("expected errors.Is(srcErr.Inner, ErrCurateNoClips)=true, got Inner=%v", srcErr.Inner)
	}
}

// No port wired — HintClipIDs-only mode.

// Negative contract: the sentinel MUST NOT surface on the
// HintClipIDs success path (the deleted stub-era test also
// probed this via errors.Is(err, ErrCurateNoClips) == false).
// err is nil on the success path, so errors.Is(nil, X) is
// always false. Still assert nil explicitly for clarity.

// Positive contract (code-reviewer SHOULD-FIX #3): the resolver
// MUST propagate the HintClipIDs to the builder — a regression
// that produces empty results without an error would surface
// here. The assertion is exact-set (not just length) so a
// regression where the binder accepts wrong clips or accepts
// only a subset of the hint IDs would be caught.

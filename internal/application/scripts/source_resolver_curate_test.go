// Package scripts — source_resolver_curate_test.go exercises
// CurateSourceResolver: search + HintClipIDs union, ErrCurateNoClips
// contract, AllowTextOnly success, search error fallback, and
// builder error propagation.
//
// The resolver is tested independently with fakes for ClipSearchPort and
// clipContextBuilder so no real Qdrant or ClipSourceBuilder is touched.
package scripts

import (
	"context"
	"errors"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// ── Fakes ──────────────────────────────────────────────────────────

// fakeClipSearch implements ClipSearchPort for tests.
type fakeClipSearch struct {
	hits      []ClipSearchHit
	returnErr error
}

func (f *fakeClipSearch) SearchClips(_ context.Context, _ ClipSearchQuery) ([]ClipSearchHit, error) {
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	return f.hits, nil
}

// fakeClipBuilder implements clipContextBuilder for tests.
type fakeClipBuilder struct {
	pack         interface{}
	plan         *NarrativePlan
	sourceText   string
	returnErr    error
}

func (f *fakeClipBuilder) BuildClipContext(_ context.Context, _ []string, _ *ClipGenerationOptions) (interface{}, *NarrativePlan, string, error) {
	if f.returnErr != nil {
		return nil, nil, "", f.returnErr
	}
	return f.pack, f.plan, f.sourceText, nil
}

// ── Helpers ────────────────────────────────────────────────────────

func makeClipSearchHits(ids ...string) []ClipSearchHit {
	hits := make([]ClipSearchHit, len(ids))
	for i, id := range ids {
		hits[i] = ClipSearchHit{AssetID: id, Name: "clip-" + id, Score: 0.9, Source: "youtube"}
	}
	return hits
}

func makePackForIDs(ids []string) map[string]any {
	return map[string]any{
		"clip_ids":        ids,
		"clip_names":      ids,
		"clip_count":      len(ids),
		"clip_drive_links": map[string]string{},
	}
}

func makeTestCurateResolver(builder clipContextBuilder) *CurateSourceResolver {
	return &CurateSourceResolver{
		clipBuilder: builder,
		log:         zap.NewNop(),
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

// ── Tests ──────────────────────────────────────────────────────────

func TestCurateResolver_NilReceiver(t *testing.T) {
	t.Parallel()
	var r *CurateSourceResolver
	_, err := r.Resolve(context.Background(), srcSpec("q", nil, false, false), "item-1")
	if err == nil || !strings.Contains(err.Error(), "nil receiver") {
		t.Fatalf("expected nil receiver error, got %v", err)
	}
}

func TestCurateResolver_NilBuilder(t *testing.T) {
	t.Parallel()
	r := &CurateSourceResolver{log: zap.NewNop()}
	_, err := r.Resolve(context.Background(), srcSpec("q", nil, false, false), "item-1")
	if err == nil || !strings.Contains(err.Error(), "clipBuilder is nil") {
		t.Fatalf("expected nil builder error, got %v", err)
	}
}

func TestCurateResolver_EmptyQueryAndHints(t *testing.T) {
	t.Parallel()
	r := makeTestCurateResolver(&fakeClipBuilder{pack: makePackForIDs(nil)})
	_, err := r.Resolve(context.Background(), srcSpec("", nil, false, false), "item-1")
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
	resolved, err := r.Resolve(context.Background(), srcSpec("", ids, false, false), "item-1")
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
	resolved, err := r.Resolve(context.Background(), srcSpec("query", nil, true, false), "item-1")
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
	resolved, err := r.Resolve(context.Background(), srcSpec("q", []string{"B", "D"}, true, false), "item-1")
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
	_, err := r.Resolve(context.Background(), srcSpec("no-results", nil, true, false), "item-1")
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
	resolved, err := r.Resolve(context.Background(), srcSpec("empty", nil, true, true), "item-1")
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
	resolved, err := r.Resolve(context.Background(), srcSpec("q", ids, true, false), "item-1")
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
	resolved, err := r.Resolve(context.Background(), srcSpec("q", ids, true, false), "item-1")
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
	_, err := r.Resolve(context.Background(), srcSpec("q", nil, true, false), "item-1")
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
	resolved, err := r.Resolve(context.Background(), srcSpec("q", nil, true, false), "item-1")
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

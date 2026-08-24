// Package routing — search_resolver_test.go locks the FASE 6
// strong invariant: territory=retrieved returns ZERO rows with
// origin=generated (and vice versa).
//
// FASE 8 (July 2026): the fake backend now uses routing-local
// RetrievalSearchOptions/Result types (the canonical home after the
// cycle break). No more retrieved-package imports in routing tests.
package images

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type fakeRetrievalBackend struct {
	hits []RetrievalSearchResult
	err  error
}

func (f *fakeRetrievalBackend) SearchAll(_ context.Context, _ string, _ RetrievalSearchOptions) ([]RetrievalSearchResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.hits, nil
}

type fakeImageListRepository struct {
	rows       []ImageSearchResult
	err        error
	lastFilter ImageFilter
}

func (f *fakeImageListRepository) ListImages(_ context.Context, filter ImageFilter) ([]ImageSearchResult, error) {
	f.lastFilter = filter
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

func mustResolver(t *testing.T, retr RetrievalSearchBackend, repo ImageListRepository) ImageSearchResolver {
	t.Helper()
	r, err := NewImageSearchResolver(
		WithRetrievalBackend(retr),
		WithImageListRepository(repo),
	)
	if err != nil {
		t.Fatalf("NewImageSearchResolver failed: %v", err)
	}
	return r
}

func TestResolve_UnknownTerritory_FailsClosed(t *testing.T) {
	r, err := NewImageSearchResolver(
		WithRetrievalBackend(&fakeRetrievalBackend{}),
		WithImageListRepository(&fakeImageListRepository{}),
	)
	if err != nil {
		t.Fatalf("NewImageSearchResolver: %v", err)
	}
	got, err := r.Resolve(ImageSearchTerritory("garbage"))
	if !errors.Is(err, ErrUnknownTerritory) {
		t.Fatalf("expected ErrUnknownTerritory, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil searcher on unknown territory, got %#v", got)
	}
}

func TestNewImageSearchResolver_MissingBackend_FailsClosed(t *testing.T) {
	if _, err := NewImageSearchResolver(WithRetrievalBackend(&fakeRetrievalBackend{})); err == nil {
		t.Fatalf("expected error when repo is missing")
	}
	if _, err := NewImageSearchResolver(WithImageListRepository(&fakeImageListRepository{})); err == nil {
		t.Fatalf("expected error when backend is missing")
	}
	if _, err := NewImageSearchResolver(); err == nil {
		t.Fatalf("expected error when no options")
	}
}

// STRONG ASSERTION: territory=retrieved MUST NOT return rows with
// origin='generated'. Test uses a fake-filled backend that returns
// 3 results; the searcher must hard-set Origin=asset.ImageOriginRetrieved on
// every emitted row regardless of upstream domain.
func TestSearch_Retrieved_NoGeneratedLeak(t *testing.T) {
	backend := &fakeRetrievalBackend{
		hits: []RetrievalSearchResult{
			{Provider: asset.ProviderWikipedia, Title: "wiki-1", PreviewURL: "https://wiki/1"},
			{Provider: asset.ProviderDuckDuckGo, Title: "duck-1", PreviewURL: "https://duck/1"},
			{Provider: asset.ProviderSearXNG, Title: "searx-1", PreviewURL: "https://searx/1"},
		},
	}
	repo := &fakeImageListRepository{}
	resolver := mustResolver(t, backend, repo)
	s, err := resolver.Resolve(TerritoryRetrieved)
	if err != nil {
		t.Fatalf("Resolve(retrieved): %v", err)
	}
	got, err := s.Search(context.Background(), ImageFilter{Limit: 100})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}
	for i, r := range got {
		if r.Origin != asset.ImageOriginRetrieved {
			t.Fatalf("row %d: expected Origin=retrieved (territory invariant), got %q", i, r.Origin)
		}
		if r.Score < 0 || r.Score > 1 {
			t.Fatalf("row %d: Score out of [0,1] range (got=%v)", i, r.Score)
		}
	}
}

// STRONG ASSERTION (mirror): territory=generated MUST NOT return rows
// with origin='retrieved'. The fake repo returns 3 rows with mixed-or
// explicitly Origin=Retrieved values; the searcher must override
// Origin to asset.ImageOriginGenerated on every emitted row.
func TestSearch_Generated_NoRetrievedLeak(t *testing.T) {
	backend := &fakeRetrievalBackend{}
	repo := &fakeImageListRepository{
		rows: []ImageSearchResult{
			{AssetID: "a1", Origin: asset.ImageOriginRetrieved, Provider: "fake-storage-leak", Name: "leak-1", PreviewURL: "https://leak/1"},
			{AssetID: "a2", Origin: asset.ImageOriginGenerated, Provider: "Flux", Name: "flux-prompt", PreviewURL: "https://flux/2", StyleID: "photoreal"},
			{AssetID: "a3", Origin: asset.ImageOriginRetrieved, Provider: "another-fake-leak"},
		},
	}
	resolver := mustResolver(t, backend, repo)
	s, err := resolver.Resolve(TerritoryGenerated)
	if err != nil {
		t.Fatalf("Resolve(generated): %v", err)
	}
	got, err := s.Search(context.Background(), ImageFilter{Limit: 100})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}
	for i, r := range got {
		if r.Origin != asset.ImageOriginGenerated {
			t.Fatalf("row %d: expected Origin=generated (territory invariant), got %q", i, r.Origin)
		}
	}
}

// Territory=all merge covers BOTH retrieved and generated.
func TestSearch_All_ReturnsBothOrigins(t *testing.T) {
	backend := &fakeRetrievalBackend{
		hits: []RetrievalSearchResult{
			{Provider: asset.ProviderWikipedia, Title: "wiki-1", PreviewURL: "https://wiki/1"},
		},
	}
	repo := &fakeImageListRepository{
		rows: []ImageSearchResult{
			{AssetID: "a1", Origin: asset.ImageOriginGenerated, Provider: "Flux"},
		},
	}
	resolver := mustResolver(t, backend, repo)
	s, err := resolver.Resolve(TerritoryAll)
	if err != nil {
		t.Fatalf("Resolve(all): %v", err)
	}
	got, err := s.Search(context.Background(), ImageFilter{Limit: 100})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	countRetr, countGen := 0, 0
	for _, r := range got {
		switch r.Origin {
		case asset.ImageOriginRetrieved:
			countRetr++
		case asset.ImageOriginGenerated:
			countGen++
		default:
			t.Fatalf("unexpected Origin: %q", r.Origin)
		}
	}
	if countRetr != 1 {
		t.Fatalf("expected 1 retrieved row, got %d", countRetr)
	}
	if countGen != 1 {
		t.Fatalf("expected 1 generated row, got %d", countGen)
	}
}

func TestResolvedLimit_ClampsAndDefaults(t *testing.T) {
	cases := []struct{ in, want int }{
		{-5, DefaultLimit},
		{0, DefaultLimit},
		{1, 1},
		{DefaultLimit, DefaultLimit},
		{100, 100},
		{MaxListImagesLimit, MaxListImagesLimit},
		{MaxListImagesLimit + 1, MaxListImagesLimit},
		{99999, MaxListImagesLimit},
	}
	for _, c := range cases {
		got := ResolvedLimit(c.in)
		if got != c.want {
			t.Errorf("ResolvedLimit(%d): want %d, got %d", c.in, c.want, got)
		}
	}
}

func TestIntersectOrigins(t *testing.T) {
	cases := []struct{ a, b, want []ImageOrigin }{
		{nil, []ImageOrigin{asset.ImageOriginGenerated}, nil},
		{[]ImageOrigin{asset.ImageOriginGenerated}, nil, nil},
		{[]ImageOrigin{asset.ImageOriginGenerated}, []ImageOrigin{asset.ImageOriginGenerated}, []ImageOrigin{asset.ImageOriginGenerated}},
		{[]ImageOrigin{asset.ImageOriginRetrieved}, []ImageOrigin{asset.ImageOriginGenerated}, nil},
		{[]ImageOrigin{asset.ImageOriginRetrieved, asset.ImageOriginGenerated}, []ImageOrigin{asset.ImageOriginGenerated}, []ImageOrigin{asset.ImageOriginGenerated}},
		{[]ImageOrigin{}, []ImageOrigin{asset.ImageOriginGenerated}, nil},
	}
	for i, c := range cases {
		got := intersectOrigins(c.a, c.b)
		if len(got) != len(c.want) {
			t.Errorf("case %d: lengths differ (want %d, got %d)", i, len(c.want), len(got))
			continue
		}
		for j, w := range c.want {
			if got[j] != w {
				t.Errorf("case %d idx %d: want %q got %q", i, j, w, got[j])
			}
		}
	}
}

func TestResult_FieldsPerTerritory(t *testing.T) {
	backend := &fakeRetrievalBackend{
		hits: []RetrievalSearchResult{
			{Provider: asset.ProviderWikipedia, Title: "wiki", PreviewURL: "https://wiki/x"},
		},
	}
	repo := &fakeImageListRepository{
		rows: []ImageSearchResult{
			{AssetID: "gx", Origin: asset.ImageOriginGenerated, Provider: "Flux", StyleID: "photoreal", StyleVersion: "v1"},
		},
	}
	resolver := mustResolver(t, backend, repo)

	// Retrieved: Style*/License/Author must be zero.
	sRetr, _ := resolver.Resolve(TerritoryRetrieved)
	rows, err := sRetr.Search(context.Background(), ImageFilter{})
	if err != nil {
		t.Fatalf("retrieved: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("retrieved: expected 1 row, got %d", len(rows))
	}
	if rows[0].StyleID != "" || rows[0].StyleVersion != "" {
		t.Errorf("retrieved row leaked style metadata: StyleID=%q StyleVersion=%q", rows[0].StyleID, rows[0].StyleVersion)
	}

	// Generated: StyleID/StyleVersion preserved.
	sGen, _ := resolver.Resolve(TerritoryGenerated)
	genRows, err := sGen.Search(context.Background(), ImageFilter{})
	if err != nil {
		t.Fatalf("generated: %v", err)
	}
	if len(genRows) != 1 {
		t.Fatalf("generated: expected 1 row, got %d", len(genRows))
	}
	if genRows[0].StyleID != "photoreal" || genRows[0].StyleVersion != "v1" {
		t.Errorf("generated row style metadata lost: StyleID=%q StyleVersion=%q", genRows[0].StyleID, genRows[0].StyleVersion)
	}
}

func TestCompileTimeAssertions(t *testing.T) {
	var (
		_ ImageSearcher       = (*retrievedSearcher)(nil)
		_ ImageSearcher       = (*generatedSearcher)(nil)
		_ ImageSearcher       = (*compositeSearcher)(nil)
		_ ImageSearchResolver = (*ImageSearchResolverImpl)(nil)
	)
}

func TestExistingImagesUsesRetrievedSubjectRows(t *testing.T) {
	repo := &fakeImageListRepository{rows: []ImageSearchResult{
		{AssetID: "cached-ddg-1", Origin: asset.ImageOriginRetrieved, PreviewURL: "https://img.example/one.jpg"},
	}}
	resolver := mustResolver(t, &fakeRetrievalBackend{}, repo)
	impl := resolver.(*ImageSearchResolverImpl)
	rows, err := impl.ExistingImages(context.Background(), "Vintage Motorcycle", 10)
	if err != nil {
		t.Fatalf("ExistingImages returned error: %v", err)
	}
	if len(rows) != 1 || rows[0].AssetID != "cached-ddg-1" {
		t.Fatalf("ExistingImages rows = %+v, want only retrieved cached image", rows)
	}
	if repo.lastFilter.SubjectID != "vintage-motorcycle" {
		t.Fatalf("subject lookup = %q, want slug", repo.lastFilter.SubjectID)
	}
	if len(repo.lastFilter.Origins) != 1 || repo.lastFilter.Origins[0] != asset.ImageOriginRetrieved {
		t.Fatalf("origin filter = %v, want retrieved", repo.lastFilter.Origins)
	}
}

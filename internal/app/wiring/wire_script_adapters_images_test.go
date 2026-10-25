package wiring

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	scriptadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

type imageResolverFixture struct {
	cached          []images.ImageSearchResult
	lookupErr       error
	providerRows    []images.ImageSearchResult
	searchResponses [][]images.ImageSearchResult
	lookupCalls     int
	searchCalls     int
}

func (f *imageResolverFixture) Resolve(images.ImageSearchTerritory) (images.ImageSearcher, error) {
	return imageSearchFixture{owner: f}, nil
}

func (f *imageResolverFixture) ExistingImages(_ context.Context, _ string, _ int) ([]images.ImageSearchResult, error) {
	f.lookupCalls++
	return f.cached, f.lookupErr
}

func (f *imageResolverFixture) ResolveProvider(string) (images.ImageSearcher, error) {
	return imageSearchFixture{owner: f}, nil
}

type imageSearchFixture struct{ owner *imageResolverFixture }

func (s imageSearchFixture) Search(context.Context, images.ImageFilter) ([]images.ImageSearchResult, error) {
	s.owner.searchCalls++
	if len(s.owner.searchResponses) > 0 {
		index := s.owner.searchCalls - 1
		if index >= len(s.owner.searchResponses) {
			return nil, nil
		}
		return s.owner.searchResponses[index], nil
	}
	return s.owner.providerRows, nil
}

func TestInternetImageSearchAdapter_ReusesDurableDatabaseImageBeforeProvider(t *testing.T) {
	fixture := &imageResolverFixture{cached: []images.ImageSearchResult{{
		AssetID: "db-cena", Name: "John Cena", DriveLink: "https://drive.google.com/file/d/db-cena/view", ContentSHA256: "hash-cena",
		PreviewURL: "https://images.example/cena.jpg", License: "unknown",
	}}}
	adapter := newInternetImageSearchAdapter(fixture, zap.NewNop())

	got, err := adapter.SearchImages(context.Background(), scriptadapters.InternetImageSearchRequest{Query: "John Cena", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].AssetID != "db-cena" || got[0].DriveLink == "" {
		t.Fatalf("reused candidates = %+v, want durable DB image", got)
	}
	if fixture.lookupCalls != 1 || fixture.searchCalls != 0 {
		t.Fatalf("lookup_calls=%d search_calls=%d; DB-first must skip provider", fixture.lookupCalls, fixture.searchCalls)
	}
	if got[0].AcquisitionStatus != scriptpkg.VidRushStatusAcquired || got[0].PersistenceStatus != scriptpkg.VidRushStatusPersisted {
		t.Fatalf("reused candidate lifecycle = %+v; want durable statuses", got[0])
	}
}

// TestInternetImageSearchAdapter_ForceRefreshSkipsDurableDatabaseReuse pins the
// stale-hash self-heal at the search seam: with ForceRefresh the adapter must
// not serve recorded hashes from the durable image index (their bytes may have
// changed upstream), so the run re-downloads and re-hashes live provider rows.
// Regression for the Garlasco L3 poisoning where a re-encoded source kept
// surfacing its dead content address into overlay plans.
func TestInternetImageSearchAdapter_ForceRefreshSkipsDurableDatabaseReuse(t *testing.T) {
	fixture := &imageResolverFixture{
		cached: []images.ImageSearchResult{{
			AssetID: "db-stale", Name: "Chiara Poggi", DriveLink: "https://drive.google.com/file/d/db-stale/view", ContentSHA256: "stale-recorded-hash",
			PreviewURL: "https://images.example/stale.jpg", License: "unknown",
		}},
		providerRows: []images.ImageSearchResult{{AssetID: "web-fresh", Name: "Chiara Poggi", PreviewURL: "https://images.example/fresh.jpg"}},
	}
	adapter := newInternetImageSearchAdapter(fixture, zap.NewNop())

	got, err := adapter.SearchImages(context.Background(), scriptadapters.InternetImageSearchRequest{Query: "Chiara Poggi", Limit: 2, ForceRefresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].AssetID != "web-fresh" {
		t.Fatalf("refresh candidates = %+v, want live provider rows, never the durable index", got)
	}
	if fixture.lookupCalls != 0 || fixture.searchCalls != 1 {
		t.Fatalf("lookup_calls=%d search_calls=%d; force refresh must skip the DB index and hit the provider", fixture.lookupCalls, fixture.searchCalls)
	}
	if got[0].AcquisitionStatus == scriptpkg.VidRushStatusAcquired || got[0].PersistenceStatus == scriptpkg.VidRushStatusPersisted {
		t.Fatalf("refresh candidate lifecycle = %+v; live hits must not carry durable statuses", got[0])
	}
}

func TestInternetImageSearchAdapter_FallsBackWhenDatabaseHasNoDurableImage(t *testing.T) {
	fixture := &imageResolverFixture{
		cached:       []images.ImageSearchResult{{AssetID: "incomplete", Name: "John Cena", PreviewURL: "https://images.example/cena.jpg"}},
		providerRows: []images.ImageSearchResult{{AssetID: "web-cena", Name: "John Cena", PreviewURL: "https://images.example/web-cena.jpg"}},
	}
	adapter := newInternetImageSearchAdapter(fixture, zap.NewNop())

	got, err := adapter.SearchImages(context.Background(), scriptadapters.InternetImageSearchRequest{Query: "John Cena", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].AssetID != "web-cena" {
		t.Fatalf("fallback candidates = %+v, want provider image", got)
	}
	if fixture.lookupCalls != 1 || fixture.searchCalls != 1 {
		t.Fatalf("lookup_calls=%d search_calls=%d; incomplete DB rows must fall back once", fixture.lookupCalls, fixture.searchCalls)
	}
}

func TestInternetImageSearchAdapter_FallsBackToSearXNGWhenDuckDuckGoIsEmpty(t *testing.T) {
	fixture := &imageResolverFixture{searchResponses: [][]images.ImageSearchResult{
		{},
		{{AssetID: "searx-archive", Name: "Archivio giudiziario", PreviewURL: "https://images.example/archive.jpg", SourcePageURL: "https://example.org/archive"}},
	}}
	adapter := newInternetImageSearchAdapter(fixture, zap.NewNop())

	got, err := adapter.SearchImages(context.Background(), scriptadapters.InternetImageSearchRequest{Query: "archivio giudiziario faldoni", Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].AssetID != "searx-archive" || got[0].Provider != "internet_images" {
		t.Fatalf("fallback candidates = %+v, want SearXNG archive candidate", got)
	}
	if fixture.searchCalls != 2 {
		t.Fatalf("provider search calls = %d, want DDG then SearXNG", fixture.searchCalls)
	}
}

// providerAwareFixture routes each provider name to its own behaviour. The
// generic fixture above returns the same searcher for every name, so it cannot
// express the case that matters here: DuckDuckGo refusing the request while an
// independent licensed surface still answers.
type providerAwareFixture struct {
	ddgErr        error
	ddgRows       []images.ImageSearchResult
	commonsRows   []images.ImageSearchResult
	ddgCalls      int
	commonsCalls  int
	searxngCalls  int
	requestedName []string
}

func (f *providerAwareFixture) Resolve(images.ImageSearchTerritory) (images.ImageSearcher, error) {
	return providerAwareSearcher{owner: f, name: "duckduckgo"}, nil
}

func (f *providerAwareFixture) ResolveProvider(name string) (images.ImageSearcher, error) {
	f.requestedName = append(f.requestedName, name)
	return providerAwareSearcher{owner: f, name: name}, nil
}

type providerAwareSearcher struct {
	owner *providerAwareFixture
	name  string
}

func (s providerAwareSearcher) Search(context.Context, images.ImageFilter) ([]images.ImageSearchResult, error) {
	switch s.name {
	case "duckduckgo":
		s.owner.ddgCalls++
		return s.owner.ddgRows, s.owner.ddgErr
	case "wikimedia_commons":
		s.owner.commonsCalls++
		return s.owner.commonsRows, nil
	case "searxng":
		s.owner.searxngCalls++
		return nil, nil
	default:
		return nil, nil
	}
}

// TestInternetImageSearchAdapter_DuckDuckGoRefusalStillUsesLicensedFallback
// pins the fix for the blocked-IP failure mode: a challenged DuckDuckGo must
// not abort retrieval before Commons runs. Returning early is what made an
// entire run look like "the subject has no images" while a licensed provider
// was healthy.
func TestInternetImageSearchAdapter_DuckDuckGoRefusalStillUsesLicensedFallback(t *testing.T) {
	fixture := &providerAwareFixture{
		ddgErr: errors.New("duckduckgo: anti-bot challenge (HTTP 202)"),
		commonsRows: []images.ImageSearchResult{{
			AssetID: "commons-garlasco", Name: "Alberto Garlasco",
			PreviewURL: "https://upload.wikimedia.org/garlasco.jpg", SourcePageURL: "https://commons.wikimedia.org/wiki/File:Garlasco.jpg",
			License: "CC BY-SA 4.0", Author: "Archive",
		}},
	}
	adapter := newInternetImageSearchAdapter(fixture, zap.NewNop())

	got, err := adapter.SearchImages(context.Background(), scriptadapters.InternetImageSearchRequest{
		Query: "Alberto Garlasco", Entity: "Alberto Garlasco", Limit: 4,
	})
	if err != nil {
		t.Fatalf("DDG refusal must not fail the retrieval surface: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("no candidates returned although Commons answered; the DDG refusal aborted the fallback")
	}
	if got[0].SourceURL != "https://upload.wikimedia.org/garlasco.jpg" {
		t.Fatalf("first candidate = %+v, want the Commons row", got[0])
	}
	if fixture.ddgCalls == 0 {
		t.Fatal("DuckDuckGo was never attempted; the fallback test is not exercising the refusal")
	}
	if fixture.commonsCalls == 0 {
		t.Fatal("Commons was never attempted after the DDG refusal")
	}
}

func TestInternetImageSearchAdapter_SharesOneProviderAnswerAcrossEntityQueryVariants(t *testing.T) {
	fixture := &imageResolverFixture{providerRows: []images.ImageSearchResult{{
		AssetID: "web-cena", Name: "John Cena", PreviewURL: "https://images.example/cena.jpg",
	}}}
	adapter := newInternetImageSearchAdapter(fixture, zap.NewNop())

	first, err := adapter.SearchImages(context.Background(), scriptadapters.InternetImageSearchRequest{
		Query: "John Cena", Entity: "John Cena", Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	afterFirst := fixture.searchCalls
	second, err := adapter.SearchImages(context.Background(), scriptadapters.InternetImageSearchRequest{
		Query: "John Cena single-person portrait headshot photo", Entity: "John Cena", Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("cache variants = first:%+v second:%+v, want one candidate each", first, second)
	}
	if fixture.searchCalls != afterFirst {
		t.Fatalf("provider search calls = %d after first, %d after variant; want the entity variant served from cache", afterFirst, fixture.searchCalls)
	}
}

func TestInternetImageSearchAdapter_DoesNotCollapseDistinctContextQueries(t *testing.T) {
	fixture := &imageResolverFixture{providerRows: []images.ImageSearchResult{{
		AssetID: "pavia", Name: "Pavia", PreviewURL: "https://images.example/pavia.jpg",
	}}}
	adapter := newInternetImageSearchAdapter(fixture, zap.NewNop())

	if _, err := adapter.SearchImages(context.Background(), scriptadapters.InternetImageSearchRequest{Query: "Pavia", Entity: "Pavia", Limit: 2}); err != nil {
		t.Fatal(err)
	}
	afterFirst := fixture.searchCalls
	if _, err := adapter.SearchImages(context.Background(), scriptadapters.InternetImageSearchRequest{Query: "Pavia courthouse", Entity: "Pavia", Limit: 2}); err != nil {
		t.Fatal(err)
	}
	if fixture.searchCalls <= afterFirst {
		t.Fatalf("provider search calls = %d after first, %d after context query; want distinct context queries to search separately", afterFirst, fixture.searchCalls)
	}
}

func TestInternetImageSearchAdapter_ForceRefreshBypassesTheSharedCache(t *testing.T) {
	fixture := &imageResolverFixture{providerRows: []images.ImageSearchResult{{
		AssetID: "fresh", Name: "John Cena", PreviewURL: "https://images.example/fresh.jpg",
	}}}
	adapter := newInternetImageSearchAdapter(fixture, zap.NewNop())

	if _, err := adapter.SearchImages(context.Background(), scriptadapters.InternetImageSearchRequest{
		Query: "John Cena", Entity: "John Cena", Limit: 2, ForceRefresh: true,
	}); err != nil {
		t.Fatal(err)
	}
	afterFirst := fixture.searchCalls
	if _, err := adapter.SearchImages(context.Background(), scriptadapters.InternetImageSearchRequest{
		Query: "John Cena", Entity: "John Cena", Limit: 2, ForceRefresh: true,
	}); err != nil {
		t.Fatal(err)
	}
	if fixture.searchCalls <= afterFirst {
		t.Fatalf("provider search calls = %d after first, %d after refresh; want ForceRefresh to bypass the cache", afterFirst, fixture.searchCalls)
	}
}

func TestInternetImageSearchAdapter_DoesNotCacheEmptyProviderAnswers(t *testing.T) {
	fixture := &imageResolverFixture{}
	adapter := newInternetImageSearchAdapter(fixture, zap.NewNop())

	if _, err := adapter.SearchImages(context.Background(), scriptadapters.InternetImageSearchRequest{Query: "x", Limit: 2}); err != nil {
		t.Fatal(err)
	}
	afterFirst := fixture.searchCalls
	if _, err := adapter.SearchImages(context.Background(), scriptadapters.InternetImageSearchRequest{Query: "x", Limit: 2}); err != nil {
		t.Fatal(err)
	}
	if fixture.searchCalls <= afterFirst {
		t.Fatalf("provider search calls = %d after first, %d after retry; want empty answers to remain retryable", afterFirst, fixture.searchCalls)
	}
}

func TestInternetImageSearchAdapter_AddsSearXNGAlternativesForNonemptyContextSearch(t *testing.T) {
	fixture := &imageResolverFixture{searchResponses: [][]images.ImageSearchResult{
		{{AssetID: "ddg-thumb", PreviewURL: "https://images.example/thumb.jpg"}},
		{{AssetID: "searx-original", Name: "Courtroom interior", PreviewURL: "https://images.example/original.jpg", SourcePageURL: "https://example.org/courtroom"}},
	}}
	adapter := newInternetImageSearchAdapter(fixture, zap.NewNop())
	query := "Tribunale Pavia aula udienza vuota"
	got, err := adapter.SearchImages(context.Background(), scriptadapters.InternetImageSearchRequest{Query: query, Entity: query, Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].AssetID != "searx-original" || got[1].AssetID != "ddg-thumb" {
		t.Fatalf("merged context candidates = %+v, want SearXNG and DDG interleaved so both survive fanout limit", got)
	}
	if fixture.searchCalls != 2 {
		t.Fatalf("provider search calls = %d, want DDG plus SearXNG", fixture.searchCalls)
	}
}

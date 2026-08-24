package app

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/routing"
	scriptadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

type imageResolverFixture struct {
	cached       []routing.ImageSearchResult
	lookupErr    error
	providerRows []routing.ImageSearchResult
	lookupCalls  int
	searchCalls  int
}

func (f *imageResolverFixture) Resolve(routing.ImageSearchTerritory) (routing.ImageSearcher, error) {
	return imageSearchFixture{owner: f}, nil
}

func (f *imageResolverFixture) ExistingImages(_ context.Context, _ string, _ int) ([]routing.ImageSearchResult, error) {
	f.lookupCalls++
	return f.cached, f.lookupErr
}

func (f *imageResolverFixture) ResolveProvider(string) (routing.ImageSearcher, error) {
	return imageSearchFixture{owner: f}, nil
}

type imageSearchFixture struct{ owner *imageResolverFixture }

func (s imageSearchFixture) Search(context.Context, routing.ImageFilter) ([]routing.ImageSearchResult, error) {
	s.owner.searchCalls++
	return s.owner.providerRows, nil
}

func TestInternetImageSearchAdapter_ReusesDurableDatabaseImageBeforeProvider(t *testing.T) {
	fixture := &imageResolverFixture{cached: []routing.ImageSearchResult{{
		AssetID: "db-cena", Name: "John Cena", DriveLink: "https://drive.google.com/file/d/db-cena/view", LegacyFileMD5: "hash-cena",
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

func TestInternetImageSearchAdapter_FallsBackWhenDatabaseHasNoDurableImage(t *testing.T) {
	fixture := &imageResolverFixture{
		cached:       []routing.ImageSearchResult{{AssetID: "incomplete", Name: "John Cena", PreviewURL: "https://images.example/cena.jpg"}},
		providerRows: []routing.ImageSearchResult{{AssetID: "web-cena", Name: "John Cena", PreviewURL: "https://images.example/web-cena.jpg"}},
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

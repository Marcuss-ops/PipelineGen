package wiring

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	scriptadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

type imageResolverFixture struct {
	cached       []images.ImageSearchResult
	lookupErr    error
	providerRows []images.ImageSearchResult
	lookupCalls  int
	searchCalls  int
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
	return s.owner.providerRows, nil
}

func TestInternetImageSearchAdapter_ReusesDurableDatabaseImageBeforeProvider(t *testing.T) {
	fixture := &imageResolverFixture{cached: []images.ImageSearchResult{{
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

// TestInternetImageSearchAdapter_ForceRefreshSkipsDurableDatabaseReuse pins the
// stale-hash self-heal at the search seam: with ForceRefresh the adapter must
// not serve recorded hashes from the durable image index (their bytes may have
// changed upstream), so the run re-downloads and re-hashes live provider rows.
// Regression for the Garlasco L3 poisoning where a re-encoded source kept
// surfacing its dead content address into overlay plans.
func TestInternetImageSearchAdapter_ForceRefreshSkipsDurableDatabaseReuse(t *testing.T) {
	fixture := &imageResolverFixture{
		cached: []images.ImageSearchResult{{
			AssetID: "db-stale", Name: "Chiara Poggi", DriveLink: "https://drive.google.com/file/d/db-stale/view", LegacyFileMD5: "stale-recorded-hash",
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

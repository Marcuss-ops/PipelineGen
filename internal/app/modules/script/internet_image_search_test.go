package script

import (
	"context"
	"errors"
	"testing"
	"time"

	imagesrouting "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	adapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

func TestImageSearchCacheSeparatesQueryClasses(t *testing.T) {
	person := imageSearchCacheKey(adapters.InternetImageSearchRequest{Entity: "Jordan", Query: "Jordan", QueryClass: "person", Language: "en", Limit: 10})
	brand := imageSearchCacheKey(adapters.InternetImageSearchRequest{Entity: "Jordan", Query: "Jordan", QueryClass: "brand", Language: "en", Limit: 10})
	if person == brand {
		t.Fatal("same spelling with different semantic routes must not share provider results")
	}
	personAlias := imageSearchCacheKey(adapters.InternetImageSearchRequest{Entity: "Jordan", Query: "Jordan photo", QueryClass: "PERSON", Language: "en", Limit: 10})
	if person != personAlias {
		t.Fatal("the canonical person query and its retrieval-only photo suffix should share results")
	}
}

func TestRetrievedImageRightsPolicy(t *testing.T) {
	if got := retrievedImageRightsStatus("CC-BY-SA-4.0"); got != "verified" {
		t.Fatalf("licensed source status = %q, want verified", got)
	}
	if got := retrievedImageRightsStatus("Unknown"); got != "unknown_allowed" {
		t.Fatalf("unknown source status = %q, want unknown_allowed", got)
	}
	if got := retrievedImageRightsBasis("CC-BY-SA-4.0", "Wikipedia Contributors"); got == "" {
		t.Fatal("licensed source must preserve a rights basis")
	}
}

type fakeImageSearcher struct {
	results []imagesrouting.ImageSearchResult
}

func (f *fakeImageSearcher) Search(context.Context, imagesrouting.ImageFilter) ([]imagesrouting.ImageSearchResult, error) {
	return f.results, nil
}

type deadlineImageSearcher struct{}

func (deadlineImageSearcher) Search(ctx context.Context, _ imagesrouting.ImageFilter) ([]imagesrouting.ImageSearchResult, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestImageSearchProviderTimeoutLeavesBudgetForFallback(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := searchImageProvider(parent, 10*time.Millisecond, deadlineImageSearcher{}, imagesrouting.ImageFilter{SubjectID: "slow provider"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow provider error = %v, want its child deadline", err)
	}
	if err := parent.Err(); err != nil {
		t.Fatalf("provider timeout cancelled shared query context: %v", err)
	}

	want := []imagesrouting.ImageSearchResult{{AssetID: "fallback-hit"}}
	got, err := searchImageProvider(parent, 50*time.Millisecond, &fakeImageSearcher{results: want}, imagesrouting.ImageFilter{SubjectID: "fallback"})
	if err != nil {
		t.Fatalf("fallback search: %v", err)
	}
	if len(got) != 1 || got[0].AssetID != "fallback-hit" {
		t.Fatalf("fallback results = %#v, want %#v", got, want)
	}
}

type fakeImageSearchResolver struct {
	providers map[string]imagesrouting.ImageSearcher
}

func (r *fakeImageSearchResolver) Resolve(imagesrouting.ImageSearchTerritory) (imagesrouting.ImageSearcher, error) {
	return r.providers["duckduckgo"], nil
}

func (r *fakeImageSearchResolver) ResolveProvider(name string) (imagesrouting.ImageSearcher, error) {
	if p, ok := r.providers[name]; ok {
		return p, nil
	}
	return nil, errors.New("provider not configured")
}

func TestPersonRouteQueriesWikipediaAndKeepsDuckDuckGoFallback(t *testing.T) {
	resolver := &fakeImageSearchResolver{
		providers: map[string]imagesrouting.ImageSearcher{
			"wikipedia": &fakeImageSearcher{
				results: []imagesrouting.ImageSearchResult{{
					AssetID:    "wiki-tyson",
					Provider:   string(detail.ProviderWikipedia),
					PreviewURL: "https://upload.wikimedia.org/tyson.jpg",
					Width:      844,
					Height:     1126,
				}},
			},
			"duckduckgo": &fakeImageSearcher{
				results: []imagesrouting.ImageSearchResult{{
					AssetID:    "ddg-tyson",
					Provider:   string(detail.ProviderDuckDuckGo),
					PreviewURL: "https://images.example/tyson-ddg.jpg",
					Width:      1000,
					Height:     1000,
				}},
			},
		},
	}
	adapter := NewInternetImageSearchAdapter(resolver, nil)
	got, err := adapter.SearchImages(context.Background(), adapters.InternetImageSearchRequest{
		Entity:     "Mike Tyson",
		Query:      "Mike Tyson",
		QueryClass: "PERSON",
		Limit:      5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 {
		t.Fatalf("got %d candidates, want wikipedia + duckduckgo", len(got))
	}
	if got[0].Provider != "wikipedia" {
		t.Fatalf("first candidate provider = %q, want wikipedia", got[0].Provider)
	}
}

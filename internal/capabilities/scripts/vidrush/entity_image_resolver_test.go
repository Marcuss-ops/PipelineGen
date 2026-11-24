package vidrush

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
)

func TestSharedEntityImageResolverServesWarmCatalogWithoutProvider(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	seedCatalogPerson(t, repo, "Michael Jordan",
		"https://images.example/michael-jordan/one.jpg",
		"https://images.example/michael-jordan/two.jpg",
	)
	identity, err := entitycatalog.CanonicalizePersonName("Michael Jordan")
	if err != nil {
		t.Fatal(err)
	}
	plan := catalogPersonPlan()
	plan.ImagesPerScene = 1

	resolution, err := resolveEntityImageCatalogCache(context.Background(), entityImageResolverDeps{
		catalog: repo,
	}, plan, "Michael Jordan", "shared-resolver-cache-key", identity, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !resolution.FromCache {
		t.Fatal("warm catalog pool was not served from cache")
	}
	if resolution.CatalogRefreshRequired {
		t.Fatal("warm catalog pool still required a provider refresh")
	}
	if len(resolution.Candidates) == 0 || len(resolution.Candidates) > 2 {
		t.Fatalf("candidates = %d, want the bounded warm pool", len(resolution.Candidates))
	}
}

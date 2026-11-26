package vidrush

import (
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

func TestEntityImageLaneKeepsExplicitOnlyAndHonorsDisabled(t *testing.T) {
	if !entityImageLaneEnabled(true, mediadomain.ImageSearchModeExplicitOnly) {
		t.Fatal("entity_images.enabled must keep the identity lane active in explicit_only")
	}
	if !entityImageLaneEnabled(true, "") {
		t.Fatal("the default image-search mode must keep the identity lane active")
	}
	if entityImageLaneEnabled(true, mediadomain.ImageSearchModeDisabled) {
		t.Fatal("image_search_mode=disabled must suppress the identity lane")
	}
	if entityImageLaneEnabled(false, mediadomain.ImageSearchModeAutomatic) {
		t.Fatal("the lane must stay off when entity_images.enabled is false")
	}
}

func TestEntityImageDiscoveryKeepsIdentityWithContextPins(t *testing.T) {
	plan := planInternetImageDiscovery(
		nil,
		[]string{"Ana Rossi portrait"},
		[]string{"Garlasco piazza"},
		true,
		false,
		nil,
	)
	if plan.Mode != internetImageDiscoverySearch {
		t.Fatalf("discovery mode = %v, want a search", plan.Mode)
	}
	if len(plan.Queries) != 2 || plan.Queries[0] != "Ana Rossi portrait" || plan.Queries[1] != "Garlasco piazza" {
		t.Fatalf("discovery queries = %#v, want identity first and the context pin retained", plan.Queries)
	}
}

func TestEntityImageDiscoveryDoesNotReuseAWarmHitMissingAnIdentity(t *testing.T) {
	covers := func(query string) bool { return query == "Garlasco piazza" }
	plan := planInternetImageDiscovery(
		nil,
		[]string{"Ana Rossi portrait"},
		[]string{"Garlasco piazza"},
		true,
		true,
		covers,
	)
	if plan.Mode != internetImageDiscoverySearch {
		t.Fatalf("discovery mode = %v, want a search because the portrait is not covered", plan.Mode)
	}
}

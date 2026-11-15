package adapters

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestEntityImageDiscoveryCacheKeyIncludesProviderAndVerifier(t *testing.T) {
	base := entityImageDiscoveryCacheKey("person:michael-jordan", "person")
	if base != entityImageDiscoveryCacheKeyFor("person:michael-jordan", "PERSON", scriptpkg.VidRushProviderInternetImages, entitycatalog.CandidateVerifierVersion) {
		t.Fatal("the default key must bind the canonical provider and verifier")
	}
	if base == entityImageDiscoveryCacheKeyFor("person:michael-jordan", "person", "searxng", entitycatalog.CandidateVerifierVersion) {
		t.Fatal("changing the retrieval provider must invalidate the cache key")
	}
	if base == entityImageDiscoveryCacheKeyFor("person:michael-jordan", "person", scriptpkg.VidRushProviderInternetImages, "person-image-visual/v1") {
		t.Fatal("changing the verifier version must invalidate the cache key")
	}
	if base == entityImageDiscoveryCacheKey("person:michael-jordan", "organization") {
		t.Fatal("changing the semantic class must invalidate cached candidates and verdicts")
	}
}

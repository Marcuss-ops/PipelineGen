package adapters

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestEntityImageDiscoveryCacheKeyIncludesProviderVerifierIntentAndIndex(t *testing.T) {
	identity := entityImageCacheIdentity{
		CanonicalEntityID: "wd:Q42262",
		IntentKey:         "nike sportswear",
		EntityIndex:       "entity_index.v1",
	}
	base := entityImageDiscoveryCacheKey(identity, "organization")
	if base != entityImageDiscoveryCacheKeyFor(identity, "ORGANIZATION", scriptpkg.VidRushProviderInternetImages, entitycatalog.CandidateVerifierVersion) {
		t.Fatal("the default key must bind the canonical provider and verifier")
	}
	if base == entityImageDiscoveryCacheKeyFor(identity, "organization", "searxng", entitycatalog.CandidateVerifierVersion) {
		t.Fatal("changing the retrieval provider must invalidate the cache key")
	}
	if base == entityImageDiscoveryCacheKeyFor(identity, "organization", scriptpkg.VidRushProviderInternetImages, "person-image-visual/v1") {
		t.Fatal("changing the verifier version must invalidate the cache key")
	}
	if base == entityImageDiscoveryCacheKey(entityImageCacheIdentity{
		CanonicalEntityID: "wd:Q42262", IntentKey: "nike statue athens", EntityIndex: "entity_index.v1",
	}, "organization") {
		t.Fatal("changing the intent must invalidate the cache key")
	}
	if base == entityImageDiscoveryCacheKey(entityImageCacheIdentity{
		CanonicalEntityID: "wd:Q42262", IntentKey: "nike sportswear", EntityIndex: "entity_index.v2",
	}, "organization") {
		t.Fatal("changing the entity index must invalidate the cache key")
	}
	if base == entityImageDiscoveryCacheKey(identity, "person") {
		t.Fatal("changing the semantic class must invalidate cached candidates and verdicts")
	}
}

func TestEntityImageCacheIdentityKeepsUnlinkedEntitiesDocumentScoped(t *testing.T) {
	segment := scriptpkg.VidRushSegmentResult{Insights: scriptpkg.SegmentInsights{
		EntityIndex: "entity_index.v1",
		Entities: []scriptpkg.ExtractedEntity{{
			Value: "Nike", CanonicalID: "local:fp-123:ent_01", SearchIntents: []string{"Nike sportswear"},
		}},
	}}
	identity := newEntityImageCacheIdentity(segment, "Nike sportswear")
	if identity.CanonicalEntityID != "local:fp-123:ent_01" || identity.IntentKey != "nike sportswear" || identity.EntityIndex != "entity_index.v1" {
		t.Fatalf("identity = %+v", identity)
	}
	otherDocument := newEntityImageCacheIdentity(scriptpkg.VidRushSegmentResult{Insights: scriptpkg.SegmentInsights{
		Entities: []scriptpkg.ExtractedEntity{{Value: "Nike", CanonicalID: "local:fp-999:ent_01", SearchIntents: []string{"Nike sportswear"}}},
	}}, "Nike sportswear")
	if entityImageDiscoveryCacheKey(identity, "organization") == entityImageDiscoveryCacheKey(otherDocument, "organization") {
		t.Fatal("unlinked entities from different documents must not share a cache entry")
	}
	if otherDocument.EntityIndex != entityImageIndexVersionFallback {
		t.Fatalf("legacy segment fallback = %q", otherDocument.EntityIndex)
	}
}

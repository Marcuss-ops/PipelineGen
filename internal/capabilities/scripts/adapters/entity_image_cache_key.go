package adapters

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const (
	// v6 adds the intent and the entity-index version to the discovery key.
	// v5 reused one row for every intent of a canonical identity, which made
	// "Nike sportswear" and "Nike statue" share a cache entry.
	entityImageDiscoveryCacheSchema = "entity-image-discovery-v6"
	// entityImageIndexVersionFallback is used only for segments produced
	// before analyzer provenance was propagated. New segments carry
	// bundle.Analyzers.EntityIndex.
	entityImageIndexVersionFallback = "entity_index.v1"
)

// entityImageCacheIdentity is the complete discovery-cache identity of one
// entity-image intent. CanonicalID is empty for unlinked entities; in that
// case the adapter already emits a document-scoped "local:<fingerprint>:<id>"
// value, so the cache remains document-scoped instead of becoming a global
// surface cache.
type entityImageCacheIdentity struct {
	CanonicalEntityID string
	IntentKey         string
	EntityIndex       string
}

func newEntityImageCacheIdentity(segment scriptpkg.VidRushSegmentResult, query string) entityImageCacheIdentity {
	entityIndex := strings.TrimSpace(segment.Insights.EntityIndex)
	if entityIndex == "" {
		entityIndex = entityImageIndexVersionFallback
	}
	return entityImageCacheIdentity{
		CanonicalEntityID: canonicalImageSearchEntityID(segment, query),
		IntentKey:         normalizeEntityMatch(query),
		EntityIndex:       entityIndex,
	}
}

// entityImageDiscoveryCacheKey is identity-scoped discovery cache identity.
// Provider identity, verifier policy, the exact intent and the entity-index
// version are part of the key. Replaying a row produced by another intent or
// an older index is not a cache hit: it is evidence about different rules.
func entityImageDiscoveryCacheKey(identity entityImageCacheIdentity, queryClass string) string {
	return entityImageDiscoveryCacheKeyFor(identity, queryClass, scriptpkg.VidRushProviderInternetImages, entitycatalog.CandidateVerifierVersion)
}

func entityImageDiscoveryCacheKeyFor(identity entityImageCacheIdentity, queryClass, provider, verifierVersion string) string {
	return segmentCacheKey(
		entityImageDiscoveryCacheSchema,
		strings.ToLower(strings.TrimSpace(identity.CanonicalEntityID)),
		strings.ToLower(strings.TrimSpace(queryClass)),
		normalizeEntityMatch(identity.IntentKey),
		strings.ToLower(strings.TrimSpace(provider)),
		strings.TrimSpace(verifierVersion),
		strings.TrimSpace(identity.EntityIndex),
	)
}

// contextualImageDiscoveryCacheKey is the query-keyed cache identity for a
// scene query that has no canonical entity. Reuse is determined by the query,
// its class and the language — NOT by the plan topic. Including the topic
// fragmented the cache per video: two runs looking for the same place or
// organization ("RAI", "Via Teulada") missed each other because their titles
// differed, so the provider was queried again for identical bytes.
func contextualImageDiscoveryCacheKey(plan *scriptpkg.ResolvedGenerationPlan, query, queryClass string) string {
	language := ""
	if plan != nil {
		language = plan.Language
	}
	return segmentCacheKey(
		"internet-images-query-v2",
		strings.TrimSpace(language),
		strings.ToLower(strings.TrimSpace(queryClass)),
		normalizeEntityMatch(query),
		strings.ToLower(strings.TrimSpace(scriptpkg.VidRushProviderInternetImages)),
		strings.TrimSpace(entitycatalog.CandidateVerifierVersion),
	)
}

// canonicalImageSearchEntityID consumes only an identity ID emitted by
// BrainRust. Linked entities carry the canonical KB id; unlinked entities
// carry the document-scoped local id the adapter already prefixed with
// "local:<fingerprint>:". Unlinked strings do not become global identities.
func canonicalImageSearchEntityID(segment scriptpkg.VidRushSegmentResult, query string) string {
	key := normalizeEntityMatch(query)
	for _, entity := range segment.Insights.Entities {
		for _, intent := range entity.SearchIntents {
			if normalizeEntityMatch(intent) == key && strings.TrimSpace(entity.CanonicalID) != "" {
				return entity.CanonicalID
			}
		}
	}
	return ""
}

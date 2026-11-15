package adapters

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const entityImageDiscoveryCacheSchema = "entity-image-discovery-v5"

// entityImageDiscoveryCacheKey is identity-scoped discovery cache identity.
// Provider identity and verifier policy are part of the key: replaying a row
// produced by a different retriever or an older visual verifier is not a cache
// hit, it is evidence about different rules.
func entityImageDiscoveryCacheKey(canonicalEntityID, queryClass string) string {
	return entityImageDiscoveryCacheKeyFor(canonicalEntityID, queryClass, scriptpkg.VidRushProviderInternetImages, entitycatalog.CandidateVerifierVersion)
}

func entityImageDiscoveryCacheKeyFor(canonicalEntityID, queryClass, provider, verifierVersion string) string {
	return segmentCacheKey(
		entityImageDiscoveryCacheSchema,
		strings.ToLower(strings.TrimSpace(canonicalEntityID)),
		strings.ToLower(strings.TrimSpace(queryClass)),
		strings.ToLower(strings.TrimSpace(provider)),
		strings.TrimSpace(verifierVersion),
	)
}

// canonicalImageSearchEntityID consumes only an identity ID emitted by
// BrainRust. Unlinked strings do not become cache identities in Go.
func canonicalImageSearchEntityID(segment scriptpkg.VidRushSegmentResult, query, _ string) string {
	key := strings.ToLower(strings.Join(strings.Fields(query), " "))
	for _, entity := range segment.Insights.Entities {
		for _, intent := range entity.SearchIntents {
			if strings.ToLower(strings.Join(strings.Fields(intent), " ")) == key && strings.TrimSpace(entity.CanonicalID) != "" {
				return entity.CanonicalID
			}
		}
	}
	return ""
}

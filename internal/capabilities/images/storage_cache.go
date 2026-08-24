package images

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

const imageSearchCacheKeyVersion = "v2"

// imageSearchCacheKey returns the canonical cache / singleflight key for
// retrieved image searches. The key is intentionally query-aware so two
// different searches under the same slug do not collapse into the same
// cache entry.
func imageSearchCacheKey(query, lang, providerPolicy string) string {
	normalizedQuery := normalizeLookupTerm(query)
	normalizedLang := strings.ToLower(strings.TrimSpace(lang))
	if normalizedLang == "" {
		normalizedLang = "it"
	}
	normalizedPolicy := strings.ToLower(strings.TrimSpace(providerPolicy))
	if normalizedPolicy == "" {
		normalizedPolicy = "legacy"
	}
	return fmt.Sprintf("image-search:%s:%s:%s:%s", imageSearchCacheKeyVersion, normalizedQuery, normalizedLang, normalizedPolicy)
}

// retrievalPolicySignature returns a stable identifier for the wired
// provider policy. The fallback chain order is part of cache identity so
// two differently-wired registries do not share a cache entry.
func (s *ImageStorageService) retrievalPolicySignature() string {
	if s == nil || s.retrievalRegistry == nil {
		return "legacy"
	}
	providers := s.retrievalRegistry.Providers()
	if len(providers) == 0 {
		return "empty"
	}
	names := make([]string, 0, len(providers))
	for _, p := range providers {
		names = append(names, string(p.Name()))
	}
	return strings.Join(names, ",")
}

// selectBestCachedImageAsset picks the best semantic match among the
// already-cached images for a subject. The selector prefers a strong
// provenance-query match rather than the first row ordered by hash.
func selectBestCachedImageAsset(query string, images []asset.ImageAsset) (*asset.ImageAsset, int) {
	var best *asset.ImageAsset
	bestScore := 0

	for i := range images {
		score := scoreCachedImageAsset(query, images[i])
		if score < minCachedImageScore {
			continue
		}
		if best == nil || score > bestScore || (score == bestScore && betterCachedImageCandidate(images[i], *best)) {
			best = &images[i]
			bestScore = score
		}
	}

	return best, bestScore
}

// filterCachedImagesByProvider keeps an explicit-provider canary from
// accidentally reusing an asset produced by another retrieval source. The
// provider column is canonical; source_name is retained as a compatibility
// fallback for older rows written before provider was populated.
func filterCachedImagesByProvider(images []asset.ImageAsset, provider asset.ImageProvider) []asset.ImageAsset {
	if provider == "" {
		return images
	}
	out := make([]asset.ImageAsset, 0, len(images))
	for _, img := range images {
		if img.Provider == provider || img.ImageMetadata().SourceName == string(provider) {
			out = append(out, img)
		}
	}
	return out
}

const minCachedImageScore = 80

func scoreCachedImageAsset(query string, img asset.ImageAsset) int {
	meta := img.ImageMetadata()
	candidates := []string{
		meta.SourceQuery,
		meta.ResolvedQuery,
		strings.TrimSpace(img.Description),
		strings.Join(img.Tags, " "),
	}
	best := 0
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if score := scoreWikiCandidate(query, candidate); score > best {
			best = score
		}
	}
	return best
}

func betterCachedImageCandidate(a, b asset.ImageAsset) bool {
	if a.CreatedAt.After(b.CreatedAt) {
		return true
	}
	if b.CreatedAt.After(a.CreatedAt) {
		return false
	}
	if a.Hash != b.Hash {
		return a.Hash < b.Hash
	}
	return a.Provider < b.Provider
}

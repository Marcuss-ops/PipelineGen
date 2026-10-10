package adapters

import (
	"fmt"
	"sort"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func vidRushFanoutArtlistCacheKey(plan *vidRushFanoutPlan, generation *scriptpkg.ResolvedGenerationPlan) string {
	return artlistSegmentCacheKey(plan.segmentID, plan.textHash, plan.artlistIntentHash, generation.Language, generation.Model, generation.PromptVersion)
}

func vidRushFanoutImageCacheKey(plan *vidRushFanoutPlan, generation *scriptpkg.ResolvedGenerationPlan) string {
	return segmentCacheKey("internet-images-assets-v3", plan.segmentID, plan.textHash, generation.Language, generation.Model, generation.PromptVersion, fmt.Sprintf("%d", plan.perQueryLimit))
}

// artlistQuerySetPayload caches the RAW provider answer keyed by the query
// set — not by segment. Adjacent segments about the same entities (e.g. three
// scenes about Muhammad Ali) build identical query sets with different text
// hashes, so the segment cache never hits for them. Matches carry no segment
// identity (candidates do — stamped at conversion), so sharing them across
// segments is safe; the merge trust boundary re-stamps per owning segment.
type artlistQuerySetPayload struct {
	Matches []ArtlistClipMatch `json:"matches"`
}

// artlistQuerySetLoad reads shared matches (cloned: the LRU entry is owned
// by every segment of the fan-out, nobody may mutate it).
func artlistQuerySetLoad(key string) ([]ArtlistClipMatch, bool) {
	if cached, ok := cacheLoad(vidrushArtlistCache, key); ok {
		if payload, ok := cached.(artlistQuerySetPayload); ok {
			return cloneArtlistMatches(payload.Matches), true
		}
	}
	return nil, false
}

// artlistQuerySetStore shares fresh matches (cloned: the caller keeps
// mutating its own slice through conversion).
func artlistQuerySetStore(key string, matches []ArtlistClipMatch) {
	if key == "" {
		return
	}
	cacheStore(vidrushArtlistCache, key, artlistQuerySetPayload{Matches: cloneArtlistMatches(matches)})
}

// vidRushArtlistQuerySetKey is order-insensitive (sorted) and normalized
// (trimmed, lowercased): ["Ali", "ali "] and ["ali", "Ali"] share one
// entry instead of paying two identical provider searches.
func vidRushArtlistQuerySetKey(title, language string, queries []string) string {
	normalized := make([]string, 0, len(queries))
	for _, query := range queries {
		if trimmed := strings.ToLower(strings.TrimSpace(query)); trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	sort.Strings(normalized)
	return segmentCacheKey("artlist-queryset-v1", strings.TrimSpace(title), strings.TrimSpace(language), strings.Join(normalized, "\x01"))
}

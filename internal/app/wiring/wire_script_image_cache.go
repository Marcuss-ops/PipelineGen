package wiring

import (
	"fmt"
	"strings"
	"time"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/cacheutil"
	"golang.org/x/sync/singleflight"
)

const (
	// imageSearchCacheCapacity bounds the in-process dedupe layer. The durable
	// per-query caches remain the replay authority; this cache only prevents
	// the semantic planner, the VidRush fan-out and the internet_images
	// postprocessor from repeating the same provider call inside one worker.
	imageSearchCacheCapacity = 1024
	// imageSearchCacheTTL is intentionally short. Provider indexes and
	// anti-bot state change; a stale discovery result must not outlive the
	// run that produced it by more than a few minutes.
	imageSearchCacheTTL = 10 * time.Minute
)

type imageSearchCacheEntry struct {
	candidates []scriptpkg.SegmentAssetCandidate
	expiresAt  time.Time
}

// imageSearchCache is the shared, bounded in-process cache for internet image
// discovery. It is deliberately keyed on the retrieval subject (entity, or
// query when no entity is available), not on the caller's provenance label:
// the semantic planner, the fan-out and the postprocessor all ask for the same
// person/place and must share one provider answer.
type imageSearchCache struct {
	lru    *cacheutil.LRU
	flight singleflight.Group
	now    func() time.Time
	ttl    time.Duration
}

func newImageSearchCache() *imageSearchCache {
	return &imageSearchCache{
		lru: cacheutil.NewLRU(imageSearchCacheCapacity),
		now: time.Now,
		ttl: imageSearchCacheTTL,
	}
}

func (c *imageSearchCache) get(key string) ([]scriptpkg.SegmentAssetCandidate, bool) {
	if c == nil || c.lru == nil {
		return nil, false
	}
	raw, ok := c.lru.Get(key)
	if !ok {
		return nil, false
	}
	entry, ok := raw.(*imageSearchCacheEntry)
	if !ok || entry == nil {
		return nil, false
	}
	if c.now().After(entry.expiresAt) {
		return nil, false
	}
	return cloneImageSearchCandidates(entry.candidates), true
}

func (c *imageSearchCache) put(key string, candidates []scriptpkg.SegmentAssetCandidate) {
	if c == nil || c.lru == nil || len(candidates) == 0 {
		// Empty results are transient provider observations, not durable
		// facts. Leaving them out keeps a challenged/lagging provider from
		// poisoning every later scene for the rest of the TTL.
		return
	}
	c.lru.Put(key, &imageSearchCacheEntry{
		candidates: cloneImageSearchCandidates(candidates),
		expiresAt:  c.now().Add(c.ttl),
	})
}

func cloneImageSearchCandidates(in []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	if len(in) == 0 {
		return nil
	}
	return append([]scriptpkg.SegmentAssetCandidate(nil), in...)
}

// imageSearchCacheKey is the canonical identity of one retrieval subject.
// Provider and QueryClass are intentionally excluded: they describe how a
// caller labels the request, not which subject is being retrieved. Including
// them is what made the semantic planner and the per-segment portrait search
// miss each other's cache even though they ask for the same person.
func imageSearchCacheKey(req adapters.InternetImageSearchRequest) string {
	return fmt.Sprintf(
		"image-search:v1:%s|%s|%d",
		imageSearchSubject(req.Entity, req.Query),
		imageSearchLanguage(req.Language),
		imageSearchLimitBucket(req.Limit),
	)
}

func imageSearchSubject(entity, query string) string {
	entity = normalizeImageSearchText(entity)
	query = normalizeImageSearchText(query)
	if entity == "" {
		return query
	}
	if query == "" || query == entity {
		return entity
	}
	// The per-segment path appends a retrieval hint to the canonical entity
	// ("Chiara Poggi single-person portrait headshot photo"). Those variants
	// must collapse to the entity; a genuine context query ("Pavia
	// courthouse") must not.
	if strings.HasPrefix(query, entity+" ") {
		if suffix := strings.TrimSpace(strings.TrimPrefix(query, entity)); imageSearchHintOnly(suffix) {
			return entity
		}
	}
	return query
}

func imageSearchLanguage(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "" {
		return "default"
	}
	return lang
}

func imageSearchLimitBucket(limit int) int {
	switch {
	case limit <= 0:
		return 10
	case limit <= 10:
		return 10
	case limit <= 20:
		return 20
	default:
		return 50
	}
}

func normalizeImageSearchText(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

// imageSearchHintOnly reports whether the remainder is only a retrieval hint
// such as "portrait", "headshot photo" or the canonical fan-out suffix. Words
// are split on separators so "single-person" is treated as two hint tokens.
func imageSearchHintOnly(value string) bool {
	fields := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		switch r {
		case ' ', '-', '_', ',', ';', '/', '|':
			return true
		default:
			return false
		}
	})
	if len(fields) == 0 {
		return true
	}
	for _, field := range fields {
		switch field {
		case "portrait", "photo", "headshot", "single", "person", "image", "foto", "ritratto", "fotografia":
			continue
		default:
			return false
		}
	}
	return true
}

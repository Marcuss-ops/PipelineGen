// Package usecase — source_enrichment.go implements the topic source
// cache layer used by the prepare phase.
//
// The source cache is separate from the gemmamemory script output cache.
// It stores the resolved source_text for a topic keyed by language,
// source policy and research version so that repeated generation
// requests for the same topic can avoid source resolution.
package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// sourceCacheResult describes the outcome of a cache lookup.
type sourceCacheResult int

const (
	sourceCacheMiss sourceCacheResult = iota
	sourceCacheHit
	sourceCacheBypass
)

// sourceEnricher encapsulates the cache lookup/save logic. It is a
// thin helper owned by GenerationPreparer.
type sourceEnricher struct {
	cache scriptports.TopicSourceCache
	log   *zap.Logger
}

// newSourceEnricher returns a sourceEnricher. A nil cache is valid and
// causes every call to bypass the cache.
func newSourceEnricher(cache scriptports.TopicSourceCache, log *zap.Logger) *sourceEnricher {
	return &sourceEnricher{cache: cache, log: log}
}

// enrich runs before source resolution. It may populate
// item.Source.SourceText from the cache. The returned result tells the
// caller whether it can skip source resolution.
//
// Modes:
//   - disabled:       no cache read, no skip.
//   - cache_only:     read cache; on miss return an error.
//   - prefer_cache:   read cache; on miss resolve source normally.
//   - refresh_if_stale: alias of prefer_cache (TTL is handled by the repository).
//   - force_refresh:  do not read cache; always resolve source.
func (e *sourceEnricher) enrich(ctx context.Context, item *scriptpkg.GenerationItemV2) (sourceCacheResult, error) {
	if e.cache == nil || item == nil {
		return sourceCacheBypass, nil
	}

	mode := normalizeCacheMode(item.Source.CachePolicy.Mode)
	if mode == scriptpkg.SourceCacheModeDisabled || mode == scriptpkg.SourceCacheModeForceRefresh {
		return sourceCacheBypass, nil
	}

	key := computeSourceCacheKey(item)
	if key == "" {
		return sourceCacheBypass, nil
	}

	text, err := e.cache.GetResearchCache(ctx, key)
	if err != nil {
		return sourceCacheMiss, fmt.Errorf("source cache read failed: %w", err)
	}

	if text != "" {
		item.Source.SourceText = text
		if e.log != nil {
			e.log.Info("source cache hit",
				zap.String("item_id", item.ID),
				zap.String("cache_key", key))
		}
		return sourceCacheHit, nil
	}

	if mode == scriptpkg.SourceCacheModeCacheOnly {
		return sourceCacheMiss, fmt.Errorf("source cache miss with mode=%s", mode)
	}

	return sourceCacheMiss, nil
}

// save stores the resolved source text in the cache when the policy
// allows writes. It is called after source resolution succeeds.
func (e *sourceEnricher) save(ctx context.Context, item scriptpkg.GenerationItemV2, text string) error {
	if e.cache == nil || text == "" {
		return nil
	}

	mode := normalizeCacheMode(item.Source.CachePolicy.Mode)
	if mode == scriptpkg.SourceCacheModeDisabled || mode == scriptpkg.SourceCacheModeCacheOnly {
		return nil
	}

	key := computeSourceCacheKey(&item)
	if key == "" {
		return nil
	}

	topic := strings.TrimSpace(item.Source.Topic)
	if topic == "" {
		topic = strings.TrimSpace(item.Title)
	}

	ttlHours := item.Source.CachePolicy.TTLHours
	if ttlHours <= 0 {
		ttlHours = 7 * 24
	}

	topicFP := topicFingerprint(topic)
	rec := scriptpkg.ResearchCacheRecord{
		Key:               key,
		Topic:             topic,
		Language:          strings.ToLower(strings.TrimSpace(item.Language)),
		MaxSteps:          0,
		SourceText:        text,
		TopicFingerprint:  topicFP,
		SourceFingerprint: sourceFingerprint(item),
		ResolverVersion:   "source_enrichment_v1",
		ResearchVersion:   strings.TrimSpace(item.Source.CachePolicy.Version),
		HitCount:          0,
		ExpiresAt:         time.Now().UTC().Add(time.Duration(ttlHours) * time.Hour),
	}

	if err := e.cache.SaveResearchCache(ctx, rec); err != nil {
		return fmt.Errorf("source cache write failed: %w", err)
	}

	if e.log != nil {
		e.log.Info("source cache saved",
			zap.String("item_id", item.ID),
			zap.String("cache_key", key))
	}
	return nil
}

// computeSourceCacheKey returns the canonical cache key for the item's
// source. The key includes the normalized topic, language, source type,
// cache mode and research version so that policy changes invalidate
// previously cached entries.
func computeSourceCacheKey(item *scriptpkg.GenerationItemV2) string {
	if item == nil {
		return ""
	}
	topic := strings.TrimSpace(item.Source.Topic)
	if topic == "" {
		topic = strings.TrimSpace(item.Title)
	}
	if topic == "" {
		return ""
	}

	return scriptpkg.ComputeResearchCacheKey(
		topic,
		strings.ToLower(strings.TrimSpace(item.Language)),
		strings.TrimSpace(item.Source.CachePolicy.Version),
		string(item.Source.Type)+":"+normalizeCacheMode(item.Source.CachePolicy.Mode),
		0,
	)
}

// topicFingerprint returns a stable SHA-256 hex fingerprint for a
// normalized topic string. It is kept for record metadata; the cache
// key itself uses the canonical ComputeResearchCacheKey.
func topicFingerprint(topic string) string {
	t := strings.ToLower(strings.TrimSpace(topic))
	if t == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// sourceFingerprint returns the source fingerprint component used in
// the cache key and stored on the record.
func sourceFingerprint(item scriptpkg.GenerationItemV2) string {
	return string(item.Source.Type) + ":" + normalizeCacheMode(item.Source.CachePolicy.Mode)
}

// normalizeCacheMode returns a canonical mode value, treating empty or
// unknown modes as disabled.
func normalizeCacheMode(mode string) string {
	m := strings.ToLower(strings.TrimSpace(mode))
	switch m {
	case scriptpkg.SourceCacheModeCacheOnly,
		scriptpkg.SourceCacheModePreferCache,
		scriptpkg.SourceCacheModeRefreshIfStale,
		scriptpkg.SourceCacheModeForceRefresh:
		return m
	default:
		return scriptpkg.SourceCacheModeDisabled
	}
}

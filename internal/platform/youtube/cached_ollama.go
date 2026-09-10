package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	capcache "github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifactcache"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/metadata"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"go.uber.org/zap"
)

// CachedOllamaBuilder decorates an Ollama ClipMetadataBuilder with persistent
// capcache caching. The cache key is SHA256(transcript) + stable JSON of the
// semantic inputs + processor model version, so identical transcripts with the
// same model hit the cache without invoking Ollama (2-4s x N in fanout).
// On transient Ollama failure the builder already falls back deterministically;
// that fallback result is cached as well so a retry does not pay the LLM cost again.
type CachedOllamaBuilder struct {
	inner   metadata.ClipMetadataBuilder
	cache   capcache.Cache
	version string
	log     *zap.Logger
}

var _ metadata.ClipMetadataBuilder = (*CachedOllamaBuilder)(nil)

// NewCachedOllamaBuilder wraps an Ollama builder with capcache. version must be
// stable per model (e.g. ollama/<model>@<digest> or plain model name). Empty
// version falls back to "ollama/unknown" (still cacheable, just less precise).
func NewCachedOllamaBuilder(inner metadata.ClipMetadataBuilder, cache capcache.Cache, version string, log *zap.Logger) (*CachedOllamaBuilder, error) {
	if inner == nil {
		return nil, fmt.Errorf("cached ollama: inner builder is required")
	}
	if cache == nil {
		return nil, fmt.Errorf("cached ollama: cache is required")
	}
	if version == "" {
		version = "ollama/unknown"
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &CachedOllamaBuilder{inner: inner, cache: cache, version: version, log: log}, nil
}

func (c *CachedOllamaBuilder) Build(ctx context.Context, in youtubetypes.ClipMetadataInput) (youtubetypes.CanonicalClipMetadata, error) {
	if c == nil || c.inner == nil {
		return youtubetypes.CanonicalClipMetadata{}, fmt.Errorf("cached ollama: not wired")
	}
	key, ok := ollamaCacheKey(in, c.version)
	if !ok {
		return c.inner.Build(ctx, in)
	}
	if entry, hit, err := c.cache.Lookup(ctx, key, 5000); err == nil && hit {
		if result, readErr := c.readCached(ctx, entry); readErr == nil {
			c.log.Debug("ollama artifact cache hit", zap.String("source_sha256", key.SourceSHA256), zap.String("clip_id", in.ClipID))
			return result, nil
		}
		c.log.Warn("ollama artifact cache entry unreadable; recomputing", zap.String("cache_key", entry.CacheKey))
		_ = c.cache.Invalidate(ctx, key)
	} else if err != nil {
		c.log.Warn("ollama artifact cache lookup failed; recomputing", zap.Error(err))
	}
	result, err := c.inner.Build(ctx, in)
	if err != nil {
		return result, err
	}
	body, err := json.Marshal(result)
	if err != nil {
		c.log.Warn("ollama artifact cache serialization failed; returning computed result", zap.Error(err))
		return result, nil
	}
	if _, storeErr := c.cache.Store(ctx, key, bytes.NewReader(body), "application/json", 5000); storeErr != nil {
		c.log.Warn("ollama artifact cache store failed; returning computed result", zap.Error(storeErr))
	}
	return result, nil
}

func (c *CachedOllamaBuilder) readCached(ctx context.Context, entry *capcache.Entry) (youtubetypes.CanonicalClipMetadata, error) {
	r, err := c.cache.Open(ctx, entry)
	if err != nil {
		return youtubetypes.CanonicalClipMetadata{}, err
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		return youtubetypes.CanonicalClipMetadata{}, err
	}
	var out youtubetypes.CanonicalClipMetadata
	if err := json.Unmarshal(b, &out); err != nil {
		return youtubetypes.CanonicalClipMetadata{}, err
	}
	return out, nil
}

func ollamaCacheKey(in youtubetypes.ClipMetadataInput, version string) (capcache.Key, bool) {
	sourceSHA := digest.SHA256String(in.Transcript + "|title:" + in.Title)
	params := struct {
		Topics          []string `json:"topics"`
		Speakers        []string `json:"speakers"`
		MentionedPeople []string `json:"mentioned_people"`
		ClipDuration    int      `json:"clip_duration"`
		Group           string   `json:"group"`
	}{
		Topics: in.Topics, Speakers: in.Speakers, MentionedPeople: in.MentionedPeople, ClipDuration: in.ClipDuration, Group: in.Group,
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return capcache.Key{}, false
	}
	return capcache.Key{
		SourceSHA256:     sourceSHA,
		Operation:        "ollama_analyze_clip",
		ParametersJSON:   string(paramsJSON),
		ProcessorVersion: version,
	}, true
}

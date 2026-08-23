// Package transcripts provides application-owned transcript ports and policies.
package transcripts

import (
	"context"
	"sync"
	"time"

	captranscripts "github.com/Marcuss-ops/PipelineGen/internal/capabilities/transcripts"
	transcript "github.com/Marcuss-ops/PipelineGen/internal/kernel/transcript"
	urlutil "github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// SubtitleSource is retained as a compatibility alias while callers migrate
// from the legacy application package to capabilities/transcripts.
type SubtitleSource = captranscripts.SubtitleSource

// TranscriptFetcher is the compatibility alias for the canonical capability port.
type TranscriptFetcher = captranscripts.TranscriptFetcher

// DefaultTranscriptCacheTTL is the canonical 24-hour TTL.
const DefaultTranscriptCacheTTL = 24 * time.Hour

type cacheEntry struct {
	doc       transcript.Document
	expiresAt time.Time
}

// CachingTranscriptProvider implements SubtitleSource and delegates to Inner
// on cache miss or TTL expiry.
type CachingTranscriptProvider struct {
	Inner SubtitleSource
	TTL   time.Duration

	mu      sync.RWMutex
	entries map[string]cacheEntry
	now     func() time.Time
}

// NewCachingTranscriptProvider constructs a provider with the canonical TTL.
func NewCachingTranscriptProvider(inner SubtitleSource) *CachingTranscriptProvider {
	if inner == nil {
		panic("transcripts.NewCachingTranscriptProvider: inner SubtitleSource is nil")
	}
	return &CachingTranscriptProvider{
		Inner:   inner,
		TTL:     DefaultTranscriptCacheTTL,
		entries: make(map[string]cacheEntry),
		now:     time.Now,
	}
}

// Fetch satisfies SubtitleSource. Cache keys include video, language, and
// source so manual and ASR transcripts cannot silently collide.
func (c *CachingTranscriptProvider) Fetch(ctx context.Context, videoURL string) (transcript.Document, error) {
	if c == nil || c.Inner == nil {
		return transcript.Document{}, errNoInnerProvider
	}

	videoID, language, source := parseVideoURLComponents(videoURL)
	key := transcript.CacheKey(videoID, language, source)
	if entry, ok := c.lookup(key); ok {
		return entry.doc, nil
	}

	doc, err := c.Inner.Fetch(ctx, videoURL)
	if err != nil {
		return transcript.Document{}, err
	}
	c.store(key, doc)
	return doc, nil
}

func (c *CachingTranscriptProvider) lookup(key string) (cacheEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.entries == nil {
		return cacheEntry{}, false
	}
	entry, ok := c.entries[key]
	if !ok || c.now().After(entry.expiresAt) {
		return cacheEntry{}, false
	}
	return entry, true
}

func (c *CachingTranscriptProvider) store(key string, doc transcript.Document) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]cacheEntry)
	}
	c.entries[key] = cacheEntry{doc: doc, expiresAt: c.now().Add(c.TTL)}
}

// Invalidate removes one cache entry.
func (c *CachingTranscriptProvider) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

// Stats returns the number of cached entries.
func (c *CachingTranscriptProvider) Stats() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

var errNoInnerProvider = &NoInnerProviderError{}

// NoInnerProviderError identifies a cache without a backing source.
type NoInnerProviderError struct{}

func (e *NoInnerProviderError) Error() string {
	return "transcripts.CachingTranscriptProvider: no inner SubtitleSource wired"
}

func parseVideoURLComponents(videoURL string) (videoID, language, source string) {
	return extractVideoID(videoURL), "en", "asr"
}

func extractVideoID(rawURL string) string {
	if id, err := urlutil.ExtractVideoID(rawURL); err == nil && id != "" {
		return id
	}
	return rawURL
}

var _ SubtitleSource = (*CachingTranscriptProvider)(nil)

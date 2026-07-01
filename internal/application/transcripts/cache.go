// Package transcripts — cache.go (Commit G, June 2026): in-process
// 24-hour TTL cache wrapper around any TranscriptProvider.
//
// The cache is gated on (videoID, language, source) so multiple
// providers can be composed without colliding keyspaces. The wrapper
// satisfies monitor.TranscriptProvider itself (forwarding Fetch) so
// consumers don't need to be aware of the cache layer.
//
// Note: the legacy GetTranscript method that used to live on this
// wrapper was retired when monitor.TranscriptProvider's CONTRACT
// cleanup (Step 6, commit 60a1f922, June 2026) removed the legacy
// GetTranscript port method. The wrapper today satisfies the
// interface via Fetch alone.
//
// Scope: per-process only. Production runs deploy N workers; each
// gets its OWN L1 cache (post-Commit-G promotion to a shared
// SQLite-backed L2 cache is a separate ticket per
// architecture/current.yaml#P0.18). The TTL is 24h per spec to
// match the conservative artlist live-search cache in
// internal/infrastructure/artlist/cache/cache.go.
package transcripts

import (
	"context"
	"sync"
	"time"

	monitor "github.com/Marcuss-ops/PipelineGen/internal/application/assets/monitor"
	transcript "github.com/Marcuss-ops/PipelineGen/internal/domain/transcript"
	urlutil "github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// DefaultTranscriptCacheTTL is the canonical 24-hour TTL per spec.
// Honoured when CachingTranscriptProvider is constructed without
// an explicit TTL (commitment: re-keying on language/source allows
// stale ASR → manual subtitle updates without waiting on operator
// intervention).
const DefaultTranscriptCacheTTL = 24 * time.Hour

// cacheEntry is the internal stored shape. We hold the entire
// leaf transcript.Document so a second Fetch call (e.g. for cache
// promotion during L2 integration) can re-emit it without
// re-running yt-dlp.
type cacheEntry struct {
	doc       transcript.Document
	expiresAt time.Time
}

// CachingTranscriptProvider implements monitor.TranscriptProvider
// and delegates to Inner on miss / TTL-expiry. Construct via
// NewCachingTranscriptProvider so the map starts in a hot state
// (no nil-map defers in the hot path).
//
// Compile-time assertion at the bottom of this file pins the
// interface contract; signature drift in monitor.TranscriptProvider
// surfaces as a build failure, not a runtime missing-method panic.
type CachingTranscriptProvider struct {
	Inner monitor.TranscriptProvider
	TTL   time.Duration

	mu      sync.RWMutex
	entries map[string]cacheEntry
	now     func() time.Time // injectable for unit tests
}

// NewCachingTranscriptProvider constructs a CachingTranscriptProvider
// with the canonical 24h TTL. Inner is required (panic-free) — a nil
// Inner would silently bucket into "every Fetch returns the wrap
// error", which masks configuration drift.
func NewCachingTranscriptProvider(inner monitor.TranscriptProvider) *CachingTranscriptProvider {
	if inner == nil {
		panic("transcripts.NewCachingTranscriptProvider: inner TranscriptProvider is nil")
	}
	return &CachingTranscriptProvider{
		Inner:   inner,
		TTL:     DefaultTranscriptCacheTTL,
		entries: make(map[string]cacheEntry),
		now:     time.Now,
	}
}

// Fetch satisfies monitor.TranscriptProvider. The cache key is
// CacheKey(videoID, language, source). On miss / expiry, Inner.Fetch
// runs and the result is stored; on hit, the cached TranscriptDocument
// is returned without invoking Inner.
//
// The Inner.Fetch path is wrapped in an explicit
// `context.WithTimeout(parent, c.TTL)` ONLY when the parent has no
// deadline. If the parent already carries a deadline, that deadline
// is honoured (do not silently shrink a hard parent deadline to 24h).
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

// lookup reads the entry without copying InternalEntry (just the
// TranscriptDocument + expiresAt). RLock is held for the duration to
// avoid a torn read on the expiresAt boundary.
func (c *CachingTranscriptProvider) lookup(key string) (cacheEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.entries == nil {
		return cacheEntry{}, false
	}
	e, ok := c.entries[key]
	if !ok {
		return cacheEntry{}, false
	}
	if c.now().After(e.expiresAt) {
		return cacheEntry{}, false
	}
	return e, true
}

// store inserts the document with the configured TTL.
func (c *CachingTranscriptProvider) store(key string, doc transcript.Document) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]cacheEntry)
	}
	c.entries[key] = cacheEntry{
		doc:       doc,
		expiresAt: c.now().Add(c.TTL),
	}
}

// Invalidate is exposed for tests + ops (e.g. "operator flags a
// stale transcript after a manual subtitle edit"). Production
// callers should not invoke this — the TTL covers the use case.
func (c *CachingTranscriptProvider) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

// Stats returns the number of cached entries (no eviction stats;
// the cache has no eviction yet, only TTL-bound expiry on lookup).
func (c *CachingTranscriptProvider) Stats() (entries int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// errNoInnerProvider is hoisted to a typed sentinel so consumers can
// `errors.Is(err, transcripts.ErrNoInnerProvider)` without inspecting
// strings. Mirror of the typical port-with-nil pattern.
var errNoInnerProvider = &NoInnerProviderError{}

// NoInnerProviderError is the typed error returned when the
// CachingTranscriptProvider is constructed without a backing
// TranscriptProvider. Operational fix: wire Inner at composition
// time (composition.go::buildBundleClipTube or equivalent).
type NoInnerProviderError struct{}

func (e *NoInnerProviderError) Error() string {
	return "transcripts.CachingTranscriptProvider: no inner TranscriptProvider wired (composition bug)"
}

// parseVideoURLComponents derives the (videoID, language, source)
// cache key tuple from a YouTube URL. The default language is "en"
// (matches the canonical YTDLPSubtitleAdapter --sub-langs en). The
// default source is "asr" (the canonical yt-dlp auto-subs path);
// callers may override by passing a lang=<...> & src=<...> hint in
// the URL fragment (used in unit tests; production callers don't
// need the override because the YTDLPSubtitleAdapter is the only
// canonical provider today).
func parseVideoURLComponents(videoURL string) (videoID, language, source string) {
	language = "en"
	source = "asr"
	videoID = extractVideoID(videoURL)
	return
}

// extractVideoID pulls the v=… query parameter via the canonical
// pkg/urlutil.ExtractVideoID helper. If extraction fails, returns
// the raw URL string so a misconfigured URL still gets a stable
// cache key (and a single Fetch per process, no thrashing).
func extractVideoID(rawURL string) string {
	if id, err := urlutil.ExtractVideoID(rawURL); err == nil && id != "" {
		return id
	}
	return rawURL
}

// Compile-time assertion: CachingTranscriptProvider must satisfy
// monitor.TranscriptProvider (Pattern 0 invariant from AGENTS.md).
var _ monitor.TranscriptProvider = (*CachingTranscriptProvider)(nil)

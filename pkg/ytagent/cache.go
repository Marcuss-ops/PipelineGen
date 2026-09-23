// cache.go — the agent-side on-disk transcript cache (T1.4).
//
// The server already caches in-process (transcripts.CachingTranscriptProvider,
// in-memory, canonical 24h TTL). Remote agents outlive one server process and
// re-run the same reads across sessions, so their cache lives on DISK with the
// SAME key semantics: videoID + language + source, because ASR vs manual
// transcripts can yield materially different text — caching them as one entity
// would be a silent-fidelity regression (see internal/kernel/transcript).
package ytagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/veloxclient"
)

// DefaultTTL mirrors the canonical 24-hour transcript cache TTL
// (internal/capabilities/transcripts::DefaultTranscriptCacheTTL). Duplicated
// as a const because pkg/ must not import internal/ — the parity is pinned
// by TestDefaultTTLMirrorsCanonical24Hours' comment contract.
const DefaultTTL = 24 * time.Hour

// CacheEntry is the persisted form of one cached transcript. Language and
// Source are denormalized next to the payload so the provenance is readable
// from the file name AND the JSON body (greppable evidence honesty).
type CacheEntry struct {
	Transcript veloxclient.Transcript `json:"transcript"`
	FetchedAt  time.Time              `json:"fetched_at"`
	Language   string                 `json:"language"`
	Source     string                 `json:"source"`
	IsOriginal bool                   `json:"is_original"`
}

// EvidenceClass delegates to the fetcher's honesty tag (manual/subtitle/asr).
func (e *CacheEntry) EvidenceClass() string {
	if e == nil {
		return "unknown"
	}
	return EvidenceClass(e.Source)
}

// Cache is a directory of `<key>.json` transcript entries with a TTL.
// A zero Cache (or one with an empty dir) is unusable; construct with
// NewCache. Safe for concurrent use within one process only (one agent
// process per cache dir is the supported deployment).
type Cache struct {
	dir string
	ttl time.Duration
	now func() time.Time
}

// NewCache builds a cache rooted at dir (created lazily on first Put).
func NewCache(dir string) *Cache {
	return &Cache{dir: dir, ttl: DefaultTTL, now: time.Now}
}

// Key returns the canonical cache key for a resolved transcript: the LOWERCASE
// `videoID:language:source` join — a faithful mirror of
// internal/kernel/transcript::CacheKey (ASCII-only fast lower, trimmed
// segments, ":" separator). The source tag participates because ASR vs
// subtitles vs manual yield materially different text; the lowercasing
// matches the artlist cache's lowerKey convention so keys are stable across
// casings of the same provider codes.
func Key(videoID, language, source string) string {
	return lowerASCII(strings.Join([]string{
		strings.TrimSpace(videoID),
		strings.TrimSpace(language),
		strings.TrimSpace(source),
	}, ":"))
}

// lowerASCII fast-lower-cases ASCII A-Z bytes only (mirrors the canonical
// lowerKey: provider codes are ASCII; non-ASCII bytes pass through untouched
// rather than being locale-mangled by strings.ToLower).
func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// pathFor renders the on-disk path for a canonical key. Video IDs are
// [A-Za-z0-9_-] (glob-safe), language and source are provider codes without
// path separators — sanitised anyway, because a malformed upstream value must
// never escape the cache directory.
func (c *Cache) pathFor(key string) string {
	return filepath.Join(c.dir, sanitizeKey(key)+".json")
}

// sanitizeKey maps a canonical key onto a flat, glob-safe file stem: ASCII
// alphanumerics pass through, everything else (including path separators)
// becomes '_'.
func sanitizeKey(key string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_', r == '.', r == ':':
			return r
		default:
			return '_'
		}
	}, key)
}

// Get returns the freshest valid (non-expired) entry for videoID across
// languages/sources. Lookup is a prefix scan over the directory because the
// language/source of an answer are only known AFTER the first fetch — the
// caller asks "is this video cached?" and the entry itself carries which
// language/source it holds. Expired entries are ignored (and left on disk;
// the next Put overwrites them).
func (c *Cache) Get(videoID string) (*CacheEntry, bool) {
	if c == nil || c.dir == "" || strings.TrimSpace(videoID) == "" {
		return nil, false
	}
	// The canonical key is `videoID:language:source`, so every entry for
	// this video lives under the `<videoID>:` stem prefix (one colon, not
	// two: the empty-language join of Key(vid,"","") would over-constrain
	// the glob to `vid::` and miss every real entry).
	prefix := sanitizeKey(lowerASCII(strings.TrimSpace(videoID)) + ":")
	pattern := filepath.Join(c.dir, prefix+"*.json")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return nil, false
	}
	// Newest FetchedAt wins when several language/source variants exist.
	sort.Slice(matches, func(i, j int) bool { return matches[i] < matches[j] })
	var best *CacheEntry
	for _, m := range matches {
		raw, readErr := os.ReadFile(m)
		if readErr != nil {
			continue
		}
		var entry CacheEntry
		if jsonErr := json.Unmarshal(raw, &entry); jsonErr != nil {
			continue
		}
		if entry.Transcript.VideoID != videoID {
			continue // defensive: a collision or tampered file is never served
		}
		if c.now().Sub(entry.FetchedAt) > c.ttl || entry.FetchedAt.After(c.now().Add(time.Minute)) {
			continue // expired, or clock-skewed into the future
		}
		if best == nil || entry.FetchedAt.After(best.FetchedAt) {
			e := entry
			best = &e
		}
	}
	if best == nil {
		return nil, false
	}
	return best, true
}

// Put persists tr under its canonical key (videoID:language:source).
// The write is atomic (temp file + rename) so a crash never leaves a
// half-written entry that a later Get would reject forever.
func (c *Cache) Put(videoID string, tr *veloxclient.Transcript, fetchedAt time.Time) error {
	if c == nil || c.dir == "" {
		return nil // cache disabled: no-op, never fatal
	}
	if tr == nil || strings.TrimSpace(videoID) == "" {
		return nil
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	entry := CacheEntry{
		Transcript: *tr,
		FetchedAt:  fetchedAt.UTC(),
		Language:   strings.TrimSpace(tr.Language),
		Source:     strings.TrimSpace(tr.SourceType),
		IsOriginal: tr.IsOriginal,
	}
	raw, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	final := c.pathFor(Key(videoID, entry.Language, entry.Source))
	tmp, err := os.CreateTemp(c.dir, ".entry-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, final); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

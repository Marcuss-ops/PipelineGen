package ytagent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/veloxclient"
)

// TestDefaultTTLIs24Hours pins the canonical transcript cache TTL. The
// server-side constant (internal/capabilities/transcripts::
// DefaultTranscriptCacheTTL) is 24h; pkg/ cannot import it (internal),
// so the mirror value itself is the contract — changing either side
// without the other is a deliberate, reviewable act.
func TestDefaultTTLIs24Hours(t *testing.T) {
	if DefaultTTL != 24*time.Hour {
		t.Fatalf("DefaultTTL = %v, want 24h (canonical transcript cache TTL)", DefaultTTL)
	}
}

// TestCache_PersistsReadableJSONWithProvenance pins the on-disk shape: the
// entry is valid JSON carrying text, language, source and fetch time, so an
// operator (or another tool) can audit evidence honesty without Go.
func TestCache_PersistsReadableJSONWithProvenance(t *testing.T) {
	dir := t.TempDir()
	cache := NewCache(dir)
	at := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	tr := &veloxclient.Transcript{
		OK:         true,
		VideoID:    "vidJSON",
		Language:   "it",
		SourceType: "youtube_subtitle",
		IsOriginal: true,
		Text:       "ciao mondo",
		CueCount:   1,
	}
	if err := cache.Put("vidJSON", tr, at); err != nil {
		t.Fatalf("Put: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "vidJSON_*.json"))
	if err != nil || len(matches) != 1 {
		// video IDs keep their case; the key is lowercased as a whole.
		matches, err = filepath.Glob(filepath.Join(dir, "*.json"))
		if err != nil || len(matches) != 1 {
			t.Fatalf("expected exactly one cache file, got %v (err=%v)", matches, err)
		}
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read entry: %v", err)
	}
	for _, needle := range []string{`"ciao mondo"`, `"youtube_subtitle"`, `"it"`, `"fetched_at"`} {
		if !contains(raw, needle) {
			t.Errorf("cache file missing %s:\n%s", needle, raw)
		}
	}

	entry, ok := cache.Get("vidJSON")
	if !ok {
		t.Fatal("Get after Put must hit")
	}
	if !entry.FetchedAt.Equal(at) {
		t.Errorf("FetchedAt = %v, want %v", entry.FetchedAt, at)
	}
	if entry.EvidenceClass() != "subtitle" {
		t.Errorf("EvidenceClass = %q, want subtitle", entry.EvidenceClass())
	}
}

// TestCache_FutureDatedEntryRejected pins the clock-skew guard: an entry
// stamped in the future is never served (a bad local clock must not pin a
// stale transcript forever).
func TestCache_FutureDatedEntryRejected(t *testing.T) {
	dir := t.TempDir()
	cache := NewCache(dir)
	base := time.Now()
	cache.now = func() time.Time { return base }

	tr := &veloxclient.Transcript{VideoID: "vidSkew", Language: "en", SourceType: "manual", Text: "x"}
	if err := cache.Put("vidSkew", tr, base.Add(48*time.Hour)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, ok := cache.Get("vidSkew"); ok {
		t.Error("future-dated entry must be rejected")
	}
}

// TestCache_DisabledCacheIsANoop pins the optional-cache contract: a Cache
// without a dir never fails a fetch path (Put returns nil, Get misses).
func TestCache_DisabledCacheIsANoop(t *testing.T) {
	var disabled *Cache
	if err := disabled.Put("vid", &veloxclient.Transcript{}, time.Now()); err != nil {
		t.Errorf("nil cache Put: %v, want nil", err)
	}
	if _, ok := disabled.Get("vid"); ok {
		t.Error("nil cache Get must miss")
	}
	empty := NewCache("")
	if err := empty.Put("vid", &veloxclient.Transcript{}, time.Now()); err != nil {
		t.Errorf("empty-dir cache Put: %v, want nil", err)
	}
	if _, ok := empty.Get("vid"); ok {
		t.Error("empty-dir cache Get must miss")
	}
}

func contains(b []byte, s string) bool {
	return len(b) >= len(s) && (string(b) == s || len(s) == 0 || indexOf(string(b), s) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

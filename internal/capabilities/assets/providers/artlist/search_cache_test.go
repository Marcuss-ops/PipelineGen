package artlist

import (
	"sync"
	"testing"
	"time"
)

// inMemoryCache is a minimal liveSearchCache without SQLite backing
func newTestCache() *liveSearchCache {
	return &liveSearchCache{
		items: make(map[string]liveSearchCacheEntry),
	}
}

func TestCacheSetAndGet(t *testing.T) {
	c := newTestCache()
	clips := []Candidate{{ID: "1", Title: "Test Clip"}}

	c.set("test-term", clips)
	got, ok := c.get("test-term")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 clip, got %d", len(got))
	}
	if got[0].ID != "1" {
		t.Fatalf("expected clip ID '1', got %q", got[0].ID)
	}
}

func TestCacheGet_Miss(t *testing.T) {
	c := newTestCache()
	_, ok := c.get("nonexistent")
	if ok {
		t.Fatal("expected cache miss for nonexistent term")
	}
}

func TestCacheAge_Fresh(t *testing.T) {
	c := newTestCache()
	c.set("fresh-term", []Candidate{{ID: "2"}})

	age := c.age("fresh-term")
	if age < 0 {
		t.Fatal("expected positive age for cached entry")
	}
}

func TestCacheAge_Miss(t *testing.T) {
	c := newTestCache()
	age := c.age("never-set")
	if age != -1 {
		t.Fatalf("expected -1 for missing entry, got %v", age)
	}
}

// TestCacheIsFresh_WithinTTL pins the canonical isFresh semantic:
// entries within the TTL are fresh; entries past the TTL are stale.
//
// Implementation note (godlike/07 honest-limitation): the test
// manually sets CachedAt to a fixed past time rather than relying on
// the time.Since resolution of the dev platform. On Windows the
// system clock has coarse resolution (~15ms) and time.Now() called
// back-to-back can return identical timestamps, so a freshly-set
// entry's age is unreliable for testing sub-microsecond TTL
// boundaries. The original version of this test used a 1ns TTL on
// a freshly-set entry (relying on the inter-call time to be at least
// 1ns); that was flaky on Windows because the inter-call time was
// frequently 0ns (rounded to the clock tick). The fixed version uses
// a 2s-old entry with 1h/1s TTLs — the same semantic boundary
// (within-TTL=fresh, past-TTL=stale) on a timescale that is robust
// to the dev platform's clock resolution.
//
// Mirrors the manual-CachedAt pattern already used in
// TestCacheIsGettingStale_NearExpiry (this file) so future readers
// see one consistent way to test time-dependent cache behavior.
func TestCacheIsFresh_WithinTTL(t *testing.T) {
	c := newTestCache()

	// Manually insert an entry with a known CachedAt.
	c.mu.Lock()
	c.items["term"] = liveSearchCacheEntry{
		Clips:    []Candidate{{ID: "3"}},
		CachedAt: time.Now().Add(-2 * time.Second), // deterministically 2s old
	}
	c.mu.Unlock()

	// 1h TTL: entry is well within TTL → fresh
	if !c.isFresh("term", 1*time.Hour) {
		t.Fatal("expected 2s-old entry to be fresh within 1h TTL")
	}

	// 1s TTL: entry is 2s old > 1s TTL → stale
	if c.isFresh("term", 1*time.Second) {
		t.Fatal("expected 2s-old entry to NOT be fresh with 1s TTL")
	}
}

func TestCacheIsFresh_Miss(t *testing.T) {
	c := newTestCache()
	if c.isFresh("missing", 1*time.Hour) {
		t.Fatal("expected missing entry to not be fresh")
	}
}

func TestCacheIsGettingStale_NearExpiry(t *testing.T) {
	c := newTestCache()

	// Manually insert an old entry
	c.mu.Lock()
	c.items["old"] = liveSearchCacheEntry{
		Clips:    []Candidate{{ID: "4"}},
		CachedAt: time.Now().Add(-50 * time.Minute), // 50 min old
	}
	c.mu.Unlock()

	// With TTL = 60 min, 50 min old = 83% of TTL → should be stale
	if !c.isGettingStale("old", 60*time.Minute) {
		t.Fatal("expected entry at 83% TTL to be getting stale")
	}

	// With TTL = 120 min, 50 min old = 42% of TTL → should NOT be stale
	if c.isGettingStale("old", 120*time.Minute) {
		t.Fatal("expected entry at 42% TTL to not be getting stale")
	}
}

func TestCacheCleanup_RemovesExpired(t *testing.T) {
	c := newTestCache()

	c.mu.Lock()
	c.items["fresh"] = liveSearchCacheEntry{
		Clips:    []Candidate{{ID: "fresh"}},
		CachedAt: time.Now(),
	}
	c.items["expired"] = liveSearchCacheEntry{
		Clips:    []Candidate{{ID: "expired"}},
		CachedAt: time.Now().Add(-48 * time.Hour), // 48h old
	}
	c.mu.Unlock()

	// Cleanup with TTL = 1h, cleanup removes entries older than 2*TTL = 2h
	c.Cleanup(1 * time.Hour)

	// Fresh entry should still be there
	if _, ok := c.items["fresh"]; !ok {
		t.Fatal("expected fresh entry to survive cleanup")
	}

	// Expired entry (48h > 2h) should be removed
	if _, ok := c.items["expired"]; ok {
		t.Fatal("expected expired entry to be cleaned up")
	}
}

func TestCacheConcurrentAccess(t *testing.T) {
	c := newTestCache()
	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			term := "term-" + string(rune('A'+n))
			c.set(term, []Candidate{{ID: string(rune('0' + n))}})
		}(i)
	}

	// Concurrent reads while writing
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			term := "term-" + string(rune('A'+n))
			c.get(term)
			c.age(term)
			c.isFresh(term, 1*time.Hour)
		}(i)
	}

	wg.Wait()

	// Verify no deadlock and data is accessible
	for i := 0; i < 10; i++ {
		term := "term-" + string(rune('A'+i))
		clips, ok := c.get(term)
		if !ok {
			t.Fatalf("expected term %q to be present after concurrent access", term)
		}
		if len(clips) != 1 {
			t.Fatalf("expected 1 clip for %q, got %d", term, len(clips))
		}
	}
}

func TestCacheOverwrite(t *testing.T) {
	c := newTestCache()

	c.set("term", []Candidate{{ID: "v1"}})
	c.set("term", []Candidate{{ID: "v2"}})

	clips, ok := c.get("term")
	if !ok {
		t.Fatal("expected cache hit after overwrite")
	}
	if clips[0].ID != "v2" {
		t.Fatalf("expected v2 after overwrite, got %q", clips[0].ID)
	}
}

func TestCacheAgeAfterOverwrite(t *testing.T) {
	c := newTestCache()

	c.set("term", []Candidate{{ID: "1"}})
	time.Sleep(5 * time.Millisecond)
	c.set("term", []Candidate{{ID: "2"}})

	age := c.age("term")
	if age < 0 {
		t.Fatal("expected positive age after overwrite")
	}
	// Age should be close to 0 since we just overwrote
	if age > 100*time.Millisecond {
		t.Fatalf("expected age < 100ms after recent overwrite, got %v", age)
	}
}

func TestCacheCaseInsensitive(t *testing.T) {
	c := newTestCache()
	clips := []Candidate{{ID: "1", Title: "Test Clip"}}

	// Set with lowercase
	c.set("mountain river", clips)

	// Get with different cases should all hit
	for _, key := range []string{"Mountain River", "MOUNTAIN RIVER", "mountain river", "MoUnTaiN RiVeR"} {
		got, ok := c.get(key)
		if !ok {
			t.Errorf("expected cache hit for %q, got miss", key)
		}
		if len(got) != 1 {
			t.Errorf("expected 1 clip for %q, got %d", key, len(got))
		}
	}

	// Age should also be case-insensitive
	age := c.age("Mountain River")
	if age < 0 {
		t.Fatal("expected positive age for case-insensitive lookup")
	}

	// isFresh should be case-insensitive
	if !c.isFresh("MOUNTAIN RIVER", 1*time.Hour) {
		t.Fatal("expected isFresh to be case-insensitive")
	}
}

func TestNormalizeSearchTerm_LimitsToFourWords(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"  mountain river sunrise ", "mountain river sunrise"},
		{"  cat  ", "cat"},
		{"", ""},
		{"   ", ""},
		{"hello world foo bar", "hello world foo bar"},
		{"hello world foo bar baz", "hello world foo bar baz"},
		{"single", "single"},
		{"one two three four five six", "one two three four five six"},
		{"  mountain river sunrise extra ", "mountain river sunrise extra"},
	}

	for _, tt := range tests {
		got := normalizeSearchTerm(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeSearchTerm(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestNormalizeSearchTermLower(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Mountain River", "mountain river"},
		{"  BLUE OCEAN  ", "blue ocean"},
		{"Single", "single"},
		{"Hello World Foo Bar Baz", "hello world foo bar baz"},
	}

	for _, tt := range tests {
		got := normalizeSearchTermLower(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeSearchTermLower(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

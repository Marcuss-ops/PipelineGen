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
	clips := []ScraperClip{{ClipID: "1", Title: "Test Clip"}}

	c.set("test-term", clips)
	got, ok := c.get("test-term")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 clip, got %d", len(got))
	}
	if got[0].ClipID != "1" {
		t.Fatalf("expected clip ID '1', got %q", got[0].ClipID)
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
	c.set("fresh-term", []ScraperClip{{ClipID: "2"}})

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

func TestCacheIsFresh_WithinTTL(t *testing.T) {
	c := newTestCache()
	c.set("term", []ScraperClip{{ClipID: "3"}})

	if !c.isFresh("term", 1*time.Hour) {
		t.Fatal("expected entry to be fresh within 1h TTL")
	}

	if c.isFresh("term", 1*time.Nanosecond) {
		t.Fatal("expected entry to NOT be fresh with 1ns TTL")
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
		Clips:    []ScraperClip{{ClipID: "4"}},
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
		Clips:    []ScraperClip{{ClipID: "fresh"}},
		CachedAt: time.Now(),
	}
	c.items["expired"] = liveSearchCacheEntry{
		Clips:    []ScraperClip{{ClipID: "expired"}},
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
			c.set(term, []ScraperClip{{ClipID: string(rune('0' + n))}})
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

	c.set("term", []ScraperClip{{ClipID: "v1"}})
	c.set("term", []ScraperClip{{ClipID: "v2"}})

	clips, ok := c.get("term")
	if !ok {
		t.Fatal("expected cache hit after overwrite")
	}
	if clips[0].ClipID != "v2" {
		t.Fatalf("expected v2 after overwrite, got %q", clips[0].ClipID)
	}
}

func TestCacheAgeAfterOverwrite(t *testing.T) {
	c := newTestCache()

	c.set("term", []ScraperClip{{ClipID: "1"}})
	time.Sleep(5 * time.Millisecond)
	c.set("term", []ScraperClip{{ClipID: "2"}})

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
	clips := []ScraperClip{{ClipID: "1", Title: "Test Clip"}}

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
		{"hello world foo bar baz", "hello world foo bar"},
		{"single", "single"},
		{"one two three four five", "one two three four"},
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
		{"Hello World Foo Bar Baz", "hello world foo bar"},
	}

	for _, tt := range tests {
		got := normalizeSearchTermLower(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeSearchTermLower(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

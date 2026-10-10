package ollama

import (
	"testing"
	"time"
)

// TestBatchTranslationOutputBudget pins the P1-6 anti-muda contract: the
// chunk budget is the summed per-cue ceiling plus envelope slack, NOT the
// old flat 512 floor + 256/segment slack.
func TestBatchTranslationOutputBudget(t *testing.T) {
	short := make([]BatchTranslationSegment, 12)
	for i := range short {
		short[i] = BatchTranslationSegment{ID: string(rune('a' + i)), Text: "Hello world, this is a test cue."}
	}
	got := batchTranslationOutputBudget(short)
	// Old formula: 384 chars*2 + 256*12 = 3840. New must be well under it.
	if got >= 3840 {
		t.Fatalf("batch budget %d not below legacy formula 3840", got)
	}
	// Per-cue floor 96 * 12 + envelope 32*12 = 1536 for these short cues.
	if got != 12*96+12*32 {
		t.Fatalf("batch budget %d, want %d", got, 12*96+12*32)
	}

	single := []BatchTranslationSegment{{ID: "0", Text: "Hi."}}
	if got := batchTranslationOutputBudget(single); got != 96+32 {
		t.Fatalf("single short segment budget = %d, want %d", got, 96+32)
	}

	huge := make([]BatchTranslationSegment, 40)
	for i := range huge {
		huge[i] = BatchTranslationSegment{ID: string(rune(i)), Text: string(make([]byte, 2000))}
	}
	if got := batchTranslationOutputBudget(huge); got != 8192 {
		t.Fatalf("huge chunk budget = %d, want cap 8192", got)
	}
}

// TestWebContextCache_ReuseAndExpiry pins the P0-2 job fan-out sharing:
// store once, reuse byte-identical while fresh, miss after expiry.
func TestWebContextCache_ReuseAndExpiry(t *testing.T) {
	g := &Generator{}
	if _, ok := g.cachedWebContext("q"); ok {
		t.Fatal("empty cache must miss")
	}
	g.storeWebContext("q", "<web_context>facts</web_context>")
	got, ok := g.cachedWebContext("q")
	if !ok || got != "<web_context>facts</web_context>" {
		t.Fatalf("cache reuse = %q,%v, want hit with stored value", got, ok)
	}
	if _, ok := g.cachedWebContext("other"); ok {
		t.Fatal("different query must miss")
	}
	// Expired entry must miss.
	g.webMu.Lock()
	g.webCache["old"] = webContextEntry{context: "stale", expiresAt: time.Now().Add(-time.Minute)}
	g.webMu.Unlock()
	if _, ok := g.cachedWebContext("old"); ok {
		t.Fatal("expired entry must miss")
	}
	// Empty/blank stores are no-ops, never poison the cache.
	g.storeWebContext("", "x")
	g.storeWebContext("  ", "x")
	g.storeWebContext("k", "")
	if _, ok := g.cachedWebContext("k"); ok {
		t.Fatal("empty context must not be cached")
	}
}

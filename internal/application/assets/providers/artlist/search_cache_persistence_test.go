package artlist

import (
	"context"
	"errors"
	"testing"
	"time"
)

type failingSearchCachePort struct {
	err error
}

func (p failingSearchCachePort) Warm(context.Context) ([]CachedEntry, error) { return nil, nil }
func (p failingSearchCachePort) Get(context.Context, string) ([]Candidate, time.Time, bool, error) {
	return nil, time.Time{}, false, nil
}
func (p failingSearchCachePort) Set(context.Context, string, []Candidate) error { return p.err }
func (p failingSearchCachePort) Delete(context.Context, string) error           { return nil }
func (p failingSearchCachePort) CleanupExpired(context.Context, time.Duration) error {
	return nil
}

func TestLiveSearchCache_SetWithContext_RollsBackL1OnL2Failure(t *testing.T) {
	cache := newLiveSearchCache()
	cache.set("term", []Candidate{{ID: "old"}})
	cache.cache = failingSearchCachePort{err: errors.New("persistent cache unavailable")}

	err := cache.setWithContext(context.Background(), "term", []Candidate{{ID: "new"}})
	if err == nil {
		t.Fatal("expected persistent cache error")
	}
	got, ok := cache.get("term")
	if !ok || len(got) != 1 || got[0].ID != "old" {
		t.Fatalf("L1 value after failed L2 write = %#v (ok=%t), want previous value", got, ok)
	}
}

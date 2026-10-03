package adapters

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestArtlistSearchGateDefaultWidth(t *testing.T) {
	t.Setenv(EnvArtlistSearchConcurrency, "")
	got := artlistSearchGateWidthForTest()
	if got != defaultArtlistSearchConcurrency {
		t.Fatalf("gate width = %d, want %d (default)", got, defaultArtlistSearchConcurrency)
	}
}

func TestArtlistSearchGateEnvOverrideClamped(t *testing.T) {
	t.Setenv(EnvArtlistSearchConcurrency, "7")
	if got := artlistSearchGateWidthForTest(); got != 7 {
		t.Fatalf("gate width = %d, want 7", got)
	}
	t.Setenv(EnvArtlistSearchConcurrency, "99")
	if got := artlistSearchGateWidthForTest(); got != 8 {
		t.Fatalf("gate width = %d, want 8 (clamped)", got)
	}
	t.Setenv(EnvArtlistSearchConcurrency, "0")
	if got := artlistSearchGateWidthForTest(); got != 1 {
		t.Fatalf("gate width = %d, want 1 (never zero)", got)
	}
}

// TestArtlistSearchGateAllowsConcurrentQueries pins the actual behavioral fix:
// the measured production wall was gate queueing at width 1, so the gate must
// admit at least 2 concurrent holders (default width) instead of serializing.
func TestArtlistSearchGateAllowsConcurrentQueries(t *testing.T) {
	var inside int32
	var peak int32
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := acquireVidRushArtlistSearch(context.Background()); err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			now := atomic.AddInt32(&inside, 1)
			for {
				peakSnapshot := atomic.LoadInt32(&peak)
				if now <= peakSnapshot || atomic.CompareAndSwapInt32(&peak, peakSnapshot, now) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&inside, -1)
			releaseVidRushArtlistSearch()
		}()
	}
	wg.Wait()
	if peak < 2 {
		t.Fatalf("peak concurrent holders = %d, want >= 2 (gate must not serialize at 1)", peak)
	}
}

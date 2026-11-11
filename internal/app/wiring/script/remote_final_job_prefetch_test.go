package script

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestResolveOnceSingleFlightSharesOneBuild pins the property the prefetch
// depends on: a prefetch and the serial walk asking for the same identity at
// the same time cost ONE resolution, and both callers receive a reference they
// may mutate independently.
func TestResolveOnceSingleFlightSharesOneBuild(t *testing.T) {
	adapter := &RemoteFinalJobAdapter{}
	var builds atomic.Int32
	release := make(chan struct{})
	build := func() (map[string]any, error) {
		builds.Add(1)
		<-release
		return map[string]any{"asset_id": "drive-asset", "size_bytes": int64(20)}, nil
	}
	const callers = 6
	var wg sync.WaitGroup
	refs := make([]map[string]any, callers)
	errs := make([]error, callers)
	for index := 0; index < callers; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			refs[index], errs[index] = adapter.resolveOnce("drive:file-1", build)
		}(index)
	}
	// Let every caller reach the single-flight wait, then answer once.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := builds.Load(); got != 1 {
		t.Fatalf("builds = %d, want exactly one resolution per identity", got)
	}
	for index := range refs {
		if errs[index] != nil || refs[index]["asset_id"] != "drive-asset" {
			t.Fatalf("caller %d: ref=%v err=%v", index, refs[index], errs[index])
		}
	}
	// The reference is a copy: one caller stamping a role cannot corrupt the
	// cache entry another caller is about to read.
	refs[0]["asset_id"] = "mutated"
	again, err := adapter.resolveOnce("drive:file-1", func() (map[string]any, error) {
		t.Fatal("a resolved identity must be answered from the cache")
		return nil, nil
	})
	if err != nil || again["asset_id"] != "drive-asset" {
		t.Fatalf("cached ref = %v err=%v, want the untouched reference", again, err)
	}
	if adapter.resolveHits() != callers {
		t.Fatalf("hits = %d, want %d (five waiters plus the cache read)", adapter.resolveHits(), callers)
	}
}

// TestFinalJobSourcePrefetchBoundedAndDeduplicated pins the scheduling: every
// distinct identity is resolved exactly once, and no more than the configured
// number of resolutions run at the same time.
func TestFinalJobSourcePrefetchBoundedAndDeduplicated(t *testing.T) {
	t.Setenv("VELOX_FINAL_JOB_RESOLVE_CONCURRENCY", "3")
	adapter := &RemoteFinalJobAdapter{}
	var running, peak, calls atomic.Int32
	var mu sync.Mutex
	seen := make(map[string]int)
	resolve := func(_ context.Context, id string) (map[string]any, error) {
		now := running.Add(1)
		for {
			previous := peak.Load()
			if now <= previous || peak.CompareAndSwap(previous, now) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		running.Add(-1)
		calls.Add(1)
		mu.Lock()
		seen[id]++
		mu.Unlock()
		return map[string]any{"asset_id": id}, nil
	}
	ids := []string{"a", "b", "c", "d", "e", "f", "a", "  ", "b"}
	adapter.prefetchFinalJobSources(context.Background(), ids, resolve)

	if got := calls.Load(); got != 6 {
		t.Fatalf("resolutions = %d, want 6 distinct identities", got)
	}
	if got := peak.Load(); got < 2 || got > 3 {
		t.Fatalf("peak concurrency = %d, want 2..3 for a pool of 3", got)
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("identity %q resolved %d times, want once", id, count)
		}
	}
}

// TestFinalJobSourcePrefetchIsBestEffort pins that a warm-up failure can never
// fail the handoff: no error is returned, the failing identity is not cached,
// and the walk's own resolution still reports the real error.
func TestFinalJobSourcePrefetchIsBestEffort(t *testing.T) {
	t.Setenv("VELOX_FINAL_JOB_RESOLVE_CONCURRENCY", "2")
	adapter := &RemoteFinalJobAdapter{}
	broken := errors.New("drive quota exceeded")
	var okCalls atomic.Int32
	resolve := func(_ context.Context, id string) (map[string]any, error) {
		if id == "broken" {
			return nil, broken
		}
		okCalls.Add(1)
		return map[string]any{"asset_id": id}, nil
	}
	adapter.prefetchFinalJobSources(context.Background(), []string{"broken", "fine"}, resolve)
	if okCalls.Load() != 1 {
		t.Fatalf("healthy identity resolved %d times, want 1", okCalls.Load())
	}
	// The failure was not cached: the walk retries and sees the real error.
	if _, err := adapter.resolveOnce("drive:broken", func() (map[string]any, error) { return nil, broken }); !errors.Is(err, broken) {
		t.Fatalf("walk error = %v, want the resolver error", err)
	}
	adapter.resolvedMu.Lock()
	_, cached := adapter.resolved["drive:broken"]
	adapter.resolvedMu.Unlock()
	if cached {
		t.Fatal("a failed prefetch must never be cached as resolved")
	}
}

// TestFinalJobSourcePrefetchSerialModeDoesNothing pins the rollback knob: with
// concurrency 1 the handoff resolves exactly as it did before the prefetch
// existed, in the walk's own order.
func TestFinalJobSourcePrefetchSerialModeDoesNothing(t *testing.T) {
	t.Setenv("VELOX_FINAL_JOB_RESOLVE_CONCURRENCY", "1")
	adapter := &RemoteFinalJobAdapter{}
	calls := 0
	adapter.prefetchFinalJobSources(context.Background(), []string{"a", "b"}, func(context.Context, string) (map[string]any, error) {
		calls++
		return nil, nil
	})
	if calls != 0 {
		t.Fatalf("serial mode resolved %d sources ahead of the walk, want 0", calls)
	}
	if got := finalJobResolveConcurrency(); got != 1 {
		t.Fatalf("concurrency = %d, want 1", got)
	}
	t.Setenv("VELOX_FINAL_JOB_RESOLVE_CONCURRENCY", "banana")
	if got := finalJobResolveConcurrency(); got != 1 {
		t.Fatalf("malformed knob -> concurrency %d, want the serial fallback", got)
	}
	t.Setenv("VELOX_FINAL_JOB_RESOLVE_CONCURRENCY", "99")
	if got := finalJobResolveConcurrency(); got != maxFinalJobResolveConcurrency {
		t.Fatalf("oversized knob -> concurrency %d, want the %d ceiling", got, maxFinalJobResolveConcurrency)
	}
}

// TestFinalJobSourcePrefetchSkipsCachedAndCapsTheFolder pins the two bounds: an
// identity the cache already holds is not re-resolved, and one folder can never
// warm more than the window even when it offers hundreds of candidates.
func TestFinalJobSourcePrefetchSkipsCachedAndCapsTheFolder(t *testing.T) {
	t.Setenv("VELOX_FINAL_JOB_RESOLVE_CONCURRENCY", "2")
	adapter := &RemoteFinalJobAdapter{
		resolved: map[string]map[string]any{"drive:cached": {"asset_id": "cached"}},
	}
	var mu sync.Mutex
	resolvedIDs := make([]string, 0, finalJobPrefetchPerFolder+1)
	ids := []string{"cached"}
	for index := 0; index < finalJobPrefetchPerFolder+5; index++ {
		ids = append(ids, string(rune('a'+index)))
	}
	adapter.prefetchFinalJobSources(context.Background(), ids, func(_ context.Context, id string) (map[string]any, error) {
		mu.Lock()
		resolvedIDs = append(resolvedIDs, id)
		mu.Unlock()
		return map[string]any{"asset_id": id}, nil
	})
	if len(resolvedIDs) != finalJobPrefetchPerFolder {
		t.Fatalf("prefetch resolved %d identities, want the %d window", len(resolvedIDs), finalJobPrefetchPerFolder)
	}
	for _, id := range resolvedIDs {
		if id == "cached" {
			t.Fatal("a cached identity was resolved again")
		}
	}
}

// TestHandoffCacheSurvivesRepeatedBeginForTheSameRun pins the handoff boundary
// shared by prefetch and the handoff: both are calls for ONE run, so starting
// the handoff must not throw away what the prefetch already resolved.
//
// A DIFFERENT run must still start clean: the cache holds references into one
// run's handoff, and a retry that reuses the adapter must never inherit them.
func TestHandoffCacheSurvivesRepeatedBeginForTheSameRun(t *testing.T) {
	adapter := &RemoteFinalJobAdapter{}
	adapter.beginHandoffCache("run-1") // the prefetch's window opens
	adapter.resolved = map[string]map[string]any{"drive:file-1": {"asset_id": "file-1"}}
	adapter.hits = 3

	adapter.beginHandoffCache("run-1") // the handoff starts for the same run
	if _, ok := adapter.resolved["drive:file-1"]; !ok {
		t.Fatal("starting the handoff discarded the prefetched identities of the same run")
	}
	if adapter.hits != 3 {
		t.Fatalf("dedup hits = %d, want the prefetch's 3 kept", adapter.hits)
	}

	adapter.beginHandoffCache("run-2")
	if adapter.resolved != nil || adapter.hits != 0 {
		t.Fatalf("a new run inherited the previous handoff cache: resolved=%v hits=%d", adapter.resolved, adapter.hits)
	}
}

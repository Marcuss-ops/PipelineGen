package script

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestResolveOnceSingleFlightSharesOneBuild pins the property the handoff cache
// depends on: two callers asking for the same identity at the same time cost
// ONE resolution, and both receive a reference they may mutate independently.
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

// TestHandoffCacheSurvivesRepeatedBeginForTheSameRun pins the handoff boundary:
// a repeated attempt for ONE run keeps what the previous attempt resolved, while
// a DIFFERENT run starts clean and never inherits another run's identities.
func TestHandoffCacheSurvivesRepeatedBeginForTheSameRun(t *testing.T) {
	adapter := &RemoteFinalJobAdapter{}
	adapter.beginHandoffCache("run-1")
	adapter.resolved = map[string]map[string]any{"drive:file-1": {"asset_id": "file-1"}}
	adapter.hits = 3

	adapter.beginHandoffCache("run-1")
	if _, ok := adapter.resolved["drive:file-1"]; !ok {
		t.Fatal("starting the handoff discarded the identities the same run already resolved")
	}
	if adapter.hits != 3 {
		t.Fatalf("dedup hits = %d, want the previous attempt's 3 kept", adapter.hits)
	}

	adapter.beginHandoffCache("run-2")
	if adapter.resolved != nil || adapter.hits != 0 {
		t.Fatalf("a new run inherited the previous handoff cache: resolved=%v hits=%d", adapter.resolved, adapter.hits)
	}
}

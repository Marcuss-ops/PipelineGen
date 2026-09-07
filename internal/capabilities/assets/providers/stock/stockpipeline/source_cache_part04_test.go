package stockpipeline

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	"os"
	"sync"
	"testing"
	"time"
)

// T10 (PR-STOCK-SOURCE-CACHE-LEASE): 5 concurrent StageSource callers
// race the singleflight callback, then each defers Cleanup and fires
// it IN ORDER (job[0] first). After job[0]'s Cleanup, jobs[1..4] must
// STILL each have a usable file on disk AND each can Cleanup
// themselves without race errors. This pins:
//
//   - 1 download (singleflight collision contract from T7/T9).
//   - 5 distinct LocalPaths (copy-to-own-tmp isolation).
//   - Cleanup of one job does NOT remove another job's file
//     (verdict's "Job A termina prima e chiama Cleanup, può cancellare
//     la sorgente mentre Job B o Job C stanno ancora usando il file"
//     scenario, now mitigated by the refcount lease AND confirmed
//     here at the test surface).
//   - Each Cleanup yields nil error even though all 5 share the
//     same singleflight leader's tmpDir (lease guards it).
//
// The lease's primary effect is observable: under the lease, the
// leader's tmpDir survives the first 4 Cleanups until the LAST
// Cleanup fires. Without the lease, copying logic alone would still
// protect the followers' own files (their copies are independent),
// but a synchronous early leader Cleanup would unlink the leader's
// tmpDir while followers are still in the singleflight callback
// wait window. The test pre-conditions block all followers from
// reading the leader's file mid-copy by introducing a 50ms delay in
// the fake downloader, simulating the realistic timing pressure.
func TestStageSource_T10_FiveConcurrentJobs_LeaseGuardsCleanupOrdering(t *testing.T) {
	fd := newFakeDownloader([]byte("fake-mp4-bytes-t10"))
	fd.delay = 50 * time.Millisecond
	stager, cache, _ := setupTestEnv(t, fd)

	ref := assets.SourceRef{URL: "https://www.youtube.com/watch?v=QdSbtEo3x_Y"}
	const N = 5
	var wg sync.WaitGroup
	wg.Add(N)
	barrier := make(chan struct{})

	results := make([]*assets.StagedAsset, N)
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		go func(idx int) {
			defer wg.Done()
			<-barrier
			sa, err := stager.stageSource(context.Background(), ref)
			results[idx] = sa
			errs[idx] = err
		}(i)
	}
	close(barrier)
	wg.Wait()

	// Single download expected — singleflight collapse intact.
	if fd.Count() != 1 {
		t.Errorf("expected 1 download after %d concurrent callers, got %d (singleflight must collapse)", N, fd.Count())
	}
	// No goroutine should have errored.
	for i, sa := range results {
		if errs[i] != nil {
			t.Fatalf("goroutine %d StageSource err: %v", i, errs[i])
		}
		if sa == nil || sa.LocalPath == "" {
			t.Fatalf("goroutine %d nil/empty staged asset", i)
		}
	}

	// Distinct LocalPaths — copy-to-own-tmp isolation.
	seenPaths := make(map[string]struct{}, N)
	for i, sa := range results {
		if _, dup := seenPaths[sa.LocalPath]; dup {
			t.Errorf("goroutine %d LocalPath %q duplicated (no per-caller isolation)", i, sa.LocalPath)
		}
		seenPaths[sa.LocalPath] = struct{}{}
		if _, err := os.Stat(sa.LocalPath); err != nil {
			t.Errorf("goroutine %d stat LocalPath: %v (file MUST be on disk pre-Cleanup)", i, err)
		}
	}

	// Cleanup the job that owns the shared singleflight source first.
	// The leader is identified by the lease's shared source directory,
	// not by an exact file path: the downloader may resolve a different
	// filename extension than the caller's outputPath.
	leaderIdx := -1
	for i, sa := range results {
		if stager.isLeaseLeader(DeriveSourceCacheKey(ref.URL, "", "", false), sa.LocalPath) {
			leaderIdx = i
			break
		}
	}
	if leaderIdx < 0 {
		t.Fatal("expected one concurrent result to own the shared lease")
	}
	if err := stager.cleanup(context.Background(), results[leaderIdx]); err != nil {
		t.Fatalf("leader Cleanup err: %v", err)
	}
	cacheEntry, cacheErr := cache.GetByCacheKey(context.Background(), DeriveSourceCacheKey(ref.URL, "", "", false))
	if cacheErr != nil || cacheEntry == nil {
		t.Fatalf("expected shared source cache entry after staging: entry=%v err=%v", cacheEntry, cacheErr)
	}
	if _, err := os.Stat(cacheEntry.LocalPath); err != nil {
		t.Fatalf("leader cleanup removed shared source before the final lease release: %v", err)
	}
	// NOTE: we deliberately don't assert that results[0]'s file is
	// missing after its own Cleanup — under PR-STOCK-SOURCE-CACHE-LEASE
	// (B1), if goroutine[0] is the singleflight LEADER, its own
	// ownDir removal is deferred to releaseSharedLease==last-ref.
	// Instead we verify the load-bearing invariant below: AFTER all
	// 5 Cleanups complete, every LocalPath is gone (each follower
	// removed its own tmpDir, the lease removed the leader's tmpDir
	// when the LAST ref was released).
	//
	// goroutines[1..4] must STILL have their files intact regardless
	// of whether goroutine[0] is leader or follower:
	//   - leader Cleanup (B1): ownDir deferred to lease (refCount>0
	//     for 4 outstanding refs) → followers' files intact.
	//   - follower Cleanup: ownDir = filepath.Dir(results[0].LocalPath)
	//     is THIS caller's tmp, independent of followers' tmps.
	for i := 0; i < N; i++ {
		if i == leaderIdx {
			continue
		}
		if _, err := os.Stat(results[i].LocalPath); err != nil {
			t.Errorf("goroutine[%d] file was removed by leader Cleanup: %v (lease/copy must isolate)", i, err)
		}
	}

	// Cleanup the rest — no race errors, no leakage.
	for i := 0; i < N; i++ {
		if i == leaderIdx {
			continue
		}
		if err := stager.cleanup(context.Background(), results[i]); err != nil {
			t.Errorf("goroutine[%d] Cleanup err: %v", i, err)
		}
	}
	// After all 5 Cleanups, every LocalPath must be gone (each goroutine
	// removed its own tmpDir AND the lease removed the shared leader
	// tmpDir once the LAST ref was released).
	for i := 0; i < N; i++ {
		if _, err := os.Stat(results[i].LocalPath); !os.IsNotExist(err) {
			t.Errorf("goroutine[%d] file still on disk after Cleanup: stat err = %v", i, err)
		}
	}
}

// T8: cache.Invalidate removes entry (lookup after Invalidate returns
// nil). Companion to T5/T6; pins the public-cache contract that the
// production repository (stocksourcecache.Repository.Invalidate)
// upholds symmetrically with the in-memory fake.
func TestStageSource_T8_CacheInvalidationRemovesEntry(t *testing.T) {
	fd := newFakeDownloader([]byte("fake-mp4-bytes-t8"))
	_, cache, _ := setupTestEnv(t, fd)

	// Seed the cache directly (no need to stage a full download).
	cacheKey := DeriveSourceCacheKey("https://www.youtube.com/watch?v=QdSbtEo3x_Y", "", "", false)
	if err := cache.Upsert(context.Background(), &SourceCacheEntry{
		CacheKey:  cacheKey,
		LocalPath: "/tmp/synthetic.mp4",
		FileSize:  42,
	}); err != nil {
		t.Fatalf("seed upsert err: %v", err)
	}
	if entry, err := cache.GetByCacheKey(context.Background(), cacheKey); err != nil || entry == nil {
		t.Fatalf("expected seeded entry present, got entry=%v err=%v", entry, err)
	}

	if err := cache.Invalidate(context.Background(), cacheKey); err != nil {
		t.Fatalf("invalidate err: %v", err)
	}
	entry, err := cache.GetByCacheKey(context.Background(), cacheKey)
	if err != nil {
		t.Errorf("get after invalidate err: %v", err)
	}
	if entry != nil {
		t.Errorf("expected nil entry after invalidate, got %+v", entry)
	}
	if cache.Count() != 0 {
		t.Errorf("expected empty cache after invalidate, got %d entries", cache.Count())
	}
}

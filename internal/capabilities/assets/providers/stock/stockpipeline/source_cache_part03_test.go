package stockpipeline

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// T5: cache hit but cached file is missing on disk → entry invalidated,
// fall through to re-download (DoD §7 "se corrotto, scaricato di nuovo").
func TestStageSource_T5_CacheFileMissing_InvalidatesAndRedownloads(t *testing.T) {
	fd := newFakeDownloader([]byte("fake-mp4-bytes-t5"))
	stager, cache, _ := setupTestEnv(t, fd)

	ref := assets.SourceRef{URL: "https://www.youtube.com/watch?v=QdSbtEo3x_Y"}
	if _, err := stager.stageSource(context.Background(), ref); err != nil {
		t.Fatalf("first StageSource err: %v", err)
	}
	cacheKey := DeriveSourceCacheKey(ref.URL, "", "", false)

	// Corrupt the entry: point LocalPath at a non-existent file.
	corruptedPath := "/nonexistent/missing-file.mp4"
	cache.mu.Lock()
	cache.entries[cacheKey].LocalPath = corruptedPath
	cache.mu.Unlock()

	beforeCount := fd.Count()
	if _, err := stager.stageSource(context.Background(), ref); err != nil {
		t.Fatalf("second StageSource err: %v", err)
	}
	if fd.Count() != beforeCount+1 {
		t.Errorf("expected +1 download (missing-file triggers re-download), before=%d after=%d", beforeCount, fd.Count())
	}
	// After invalidate + re-download, populateCache writes a NEW entry
	// under the same cache key. The right assertion is that the
	// corrupted LocalPath was replaced by the fresh download's
	// LocalPath (the previous "entry != nil" assertion was wrong
	// because the entry is re-populated by the same cache key).
	entry, getErr := cache.GetByCacheKey(context.Background(), cacheKey)
	if getErr != nil {
		t.Errorf("get after invalidate+repopulate err: %v", getErr)
	}
	if entry == nil {
		t.Fatal("expected cache entry re-populated after invalidate+re-download, got nil")
	}
	if entry.LocalPath == corruptedPath {
		t.Errorf("expected LocalPath to differ from corrupted=%q after refresh, got same value", corruptedPath)
	}
}

// T6: cache hit but cached file size mismatch → entry invalidated →
// fall through to re-download.
func TestStageSource_T6_CacheFileSizeMismatch_InvalidatesAndRedownloads(t *testing.T) {
	fd := newFakeDownloader([]byte("fake-mp4-bytes-t6"))
	stager, cache, _ := setupTestEnv(t, fd)

	ref := assets.SourceRef{URL: "https://www.youtube.com/watch?v=QdSbtEo3x_Y"}
	if _, err := stager.stageSource(context.Background(), ref); err != nil {
		t.Fatalf("first StageSource err: %v", err)
	}
	cacheKey := DeriveSourceCacheKey(ref.URL, "", "", false)

	cache.mu.Lock()
	cache.entries[cacheKey].FileSize = 999999999 // way bigger than actual
	cache.mu.Unlock()

	beforeCount := fd.Count()
	if _, err := stager.stageSource(context.Background(), ref); err != nil {
		t.Fatalf("second StageSource err: %v", err)
	}
	if fd.Count() != beforeCount+1 {
		t.Errorf("expected +1 download on size mismatch (before=%d after=%d)", beforeCount, fd.Count())
	}
}

// T7: 5 concurrent calls on same URL collapse to 1 yt-dlp download
// (DoD §8 "2 richieste simultanee collassino a 1 download"). The
// fake downloader sleeps 100ms per call so the 5 goroutines all
// overlap inside the singleflight callback window — without
// singleflight this test would show downloadCount=5.
func TestStageSource_T7_ConcurrentCollapsesToOneDownload(t *testing.T) {
	fd := newFakeDownloader([]byte("fake-mp4-bytes-t7"))
	fd.delay = 100 * time.Millisecond
	stager, _, _ := setupTestEnv(t, fd)

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

	if fd.Count() != 1 {
		t.Errorf("expected 1 download after %d concurrent callers, got %d (singleflight must collapse)", N, fd.Count())
	}
	for i, sa := range results {
		if errs[i] != nil {
			t.Errorf("goroutine %d err: %v", i, errs[i])
			continue
		}
		if sa == nil {
			t.Errorf("goroutine %d returned nil staged asset", i)
		}
	}
}

// T9: concurrent callers that hit the same singleflight download must
// each receive an independent LocalPath. Cleanup of one job's temp
// directory must not remove the file used by another job (DoD §8 race
// condition guard).
func TestStageSource_T9_ConcurrentSingleflightReturnsIndependentPaths(t *testing.T) {
	fd := newFakeDownloader([]byte("fake-mp4-bytes-t9"))
	fd.delay = 100 * time.Millisecond
	stager, _, _ := setupTestEnv(t, fd)

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

	if fd.Count() != 1 {
		t.Errorf("expected 1 download after %d concurrent callers, got %d (singleflight must collapse)", N, fd.Count())
	}

	paths := make(map[string]struct{})
	for i, sa := range results {
		if errs[i] != nil {
			t.Fatalf("goroutine %d err: %v", i, errs[i])
		}
		if sa == nil || sa.LocalPath == "" {
			t.Fatalf("goroutine %d returned nil/empty staged asset", i)
		}
		if _, exists := paths[sa.LocalPath]; exists {
			t.Errorf("duplicate LocalPath returned: %s", sa.LocalPath)
		}
		paths[sa.LocalPath] = struct{}{}
		if _, err := os.Stat(sa.LocalPath); err != nil {
			t.Errorf("goroutine %d staged file missing: %v", i, err)
		}
	}

	// Simulate cleanup of the first job and verify the others still have
	// their files on disk.
	cleanupDir := filepath.Dir(results[0].LocalPath)
	if err := os.RemoveAll(cleanupDir); err != nil {
		t.Fatalf("cleanup first job dir: %v", err)
	}
	for i, sa := range results {
		if i == 0 {
			continue
		}
		if _, err := os.Stat(sa.LocalPath); err != nil {
			t.Errorf("goroutine %d file was removed by another job's cleanup: %v", i, err)
		}
	}
}

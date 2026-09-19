// Package stockpipeline — source_warmer_test.go
//
// PR-STOCK-PRECLAIM-SOURCE-WARM (September 2026).
//
// Covers the pre-claim source cache warmer (WarmSourceCache, owned by
// source_cache_port.go): the warmed URL must be a cache HIT for the in-run
// staging path (the whole point of the feature), and warming must stay
// bounded, deduped, and best-effort.
package stockpipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
)

// warmStager is an acquisition.SourceStager stub that materialises a real file
// per Prepare call (the canonical StockStager stats + copies it), tracks
// concurrency, and can be told to fail specific URLs.
type warmStager struct {
	mu       sync.Mutex
	calls    int
	byURL    map[string]int
	dir      string
	failURLs map[string]error

	active    atomic.Int64
	maxActive atomic.Int64
	delay     time.Duration
}

var _ acquisition.SourceStager = (*warmStager)(nil)

func newWarmStager(t *testing.T) *warmStager {
	t.Helper()
	return &warmStager{dir: t.TempDir(), byURL: map[string]int{}, failURLs: map[string]error{}}
}

func (w *warmStager) Prepare(_ context.Context, req acquisition.PrepareRequest) (*acquisition.PrepareContext, error) {
	active := w.active.Add(1)
	defer w.active.Add(-1)
	for {
		prev := w.maxActive.Load()
		if active <= prev || w.maxActive.CompareAndSwap(prev, active) {
			break
		}
	}
	if w.delay > 0 {
		time.Sleep(w.delay)
	}

	w.mu.Lock()
	w.calls++
	w.byURL[req.Source.URL]++
	n := w.calls
	failErr := w.failURLs[req.Source.URL]
	w.mu.Unlock()

	if failErr != nil {
		return nil, failErr
	}

	path := filepath.Join(w.dir, fmt.Sprintf("source_%d.mp4", n))
	payload := []byte("warm-staged-bytes")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return nil, err
	}
	return &acquisition.PrepareContext{
		ID:           fmt.Sprintf("stage-%d", n),
		SourceRef:    req.Source,
		LocalPath:    path,
		SizeBytes:    int64(len(payload)),
		CleanupToken: path,
		ExpiresAt:    time.Now().Add(time.Hour),
	}, nil
}

func (w *warmStager) Release(_ context.Context, _ string) error { return nil }

func (w *warmStager) Count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls
}

func (w *warmStager) CountFor(url string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.byURL[url]
}

// newWarmTestService wires the minimal Service surface the warmer consults:
// the acquisition stager (cold download), the cross-run cache, and the local FS
// port the cache validates against.
func newWarmTestService(t *testing.T, stager acquisition.SourceStager, cache *fakeSourceCache) *Service {
	t.Helper()
	return &Service{
		runtime: &RuntimeConfig{
			WorkDir:          t.TempDir(),
			ClipDurationSec:  5,
			ChunkDurationSec: 25,
			MaxResults:       25,
			PolicyVersion:    "test",
		},
		log:               zap.NewNop(),
		localFS:           testFS,
		sourceStager:      stager,
		sourceCacheReader: cache,
		sourceCacheWriter: cache,
	}
}

// warmTestURL returns a distinct non-Drive source URL per index.
func warmTestURL(i int) string {
	return fmt.Sprintf("https://www.youtube.com/watch?v=warm%07d", i)
}

// TestWarmSourceCache_MakesInRunStagingACacheHit is the feature's core
// contract: after warming, the canonical in-run staging path (stagerForRun →
// acquisition.SourceStager.Prepare, exactly what stock.stage_sources calls)
// must resolve from the cross-run cache WITHOUT invoking the underlying
// downloader again. That is what removes the yt-dlp download from the run's
// critical path.
func TestWarmSourceCache_MakesInRunStagingACacheHit(t *testing.T) {
	stager := newWarmStager(t)
	cache := newFakeSourceCache()
	svc := newWarmTestService(t, stager, cache)
	url := warmTestURL(1)

	// The invariant that makes the whole feature work: the key the warmer
	// populates and the key the run's stock.stage_sources derives from the
	// planned source URL must be identical.
	warmKey := DeriveSourceCacheKey(url, "", "", false)
	runKey := DeriveSourceCacheKey(stagingSourceURL(ClipPlan{SourceID: url}), "", "", false)
	if warmKey != runKey {
		t.Fatalf("warm cache key %q != in-run staging cache key %q for %s", warmKey, runKey, url)
	}

	report := svc.WarmSourceCache(context.Background(), []string{url})
	if report.Requested != 1 || report.Warmed != 1 || report.Failed != 0 {
		t.Fatalf("warm report = %+v, want Requested=1 Warmed=1 Failed=0", report)
	}
	if cache.Count() != 1 {
		t.Fatalf("cache entries = %d, want 1 (warm must populate the cross-run cache)", cache.Count())
	}
	if stager.Count() != 1 {
		t.Fatalf("underlying stager Prepare calls = %d, want 1", stager.Count())
	}

	// In-run staging path for the same URL.
	runStager := svc.stagerForRun()
	prepared, err := runStager.Prepare(context.Background(), acquisition.PrepareRequest{
		Source:         acquisition.SourceRef{URL: url, PolicyVersion: "v1"},
		IdempotencyKey: "stock.stage_sources.test",
		CallerRef:      "test",
	})
	if err != nil {
		t.Fatalf("in-run Prepare after warm returned err: %v", err)
	}
	if prepared == nil || prepared.LocalPath == "" {
		t.Fatalf("in-run Prepare returned %+v, want a staged local path", prepared)
	}
	if got := stager.Count(); got != 1 {
		t.Errorf("underlying stager Prepare calls = %d after in-run staging, want 1 (warm MUST make it a cache HIT — no second download)", got)
	}
	if got := stager.CountFor(url); got != 1 {
		t.Errorf("downloads for %s = %d, want 1", url, got)
	}
}

// TestWarmSourceCache_DeduplicatesURLs pins that duplicate/blank entries are
// collapsed before any download happens.
func TestWarmSourceCache_DeduplicatesURLs(t *testing.T) {
	stager := newWarmStager(t)
	cache := newFakeSourceCache()
	svc := newWarmTestService(t, stager, cache)
	url := warmTestURL(2)

	report := svc.WarmSourceCache(context.Background(), []string{"  " + url + "  ", url, ""})
	if report.Requested != 1 || report.Warmed != 1 {
		t.Fatalf("warm report = %+v, want Requested=1 Warmed=1", report)
	}
	if got := stager.Count(); got != 1 {
		t.Errorf("stager Prepare calls = %d, want 1 (duplicates must not re-download)", got)
	}
}

// TestWarmSourceCache_AlreadyCachedSkipsDownload pins the idempotency contract:
// warming an already-warm source is a no-op.
func TestWarmSourceCache_AlreadyCachedSkipsDownload(t *testing.T) {
	stager := newWarmStager(t)
	cache := newFakeSourceCache()
	svc := newWarmTestService(t, stager, cache)
	url := warmTestURL(3)

	first := svc.WarmSourceCache(context.Background(), []string{url})
	if first.Warmed != 1 {
		t.Fatalf("first warm = %+v, want Warmed=1", first)
	}
	second := svc.WarmSourceCache(context.Background(), []string{url})
	if second.AlreadyCached != 1 || second.Warmed != 0 || second.Failed != 0 {
		t.Fatalf("second warm = %+v, want AlreadyCached=1 Warmed=0 Failed=0", second)
	}
	if got := stager.Count(); got != 1 {
		t.Errorf("stager Prepare calls = %d, want 1 (already-warm source must not re-download)", got)
	}
}

// TestWarmSourceCache_BoundedConcurrency pins that multiple sources are warmed
// in parallel, but never above the configured cap.
func TestWarmSourceCache_BoundedConcurrency(t *testing.T) {
	stager := newWarmStager(t)
	stager.delay = 30 * time.Millisecond
	cache := newFakeSourceCache()
	svc := newWarmTestService(t, stager, cache)

	urls := make([]string, 0, 9)
	for i := 0; i < 9; i++ {
		urls = append(urls, warmTestURL(100+i))
	}

	report := svc.WarmSourceCache(context.Background(), urls)
	if report.Warmed != len(urls) || report.Failed != 0 {
		t.Fatalf("warm report = %+v, want Warmed=%d Failed=0", report, len(urls))
	}
	if got := int(stager.maxActive.Load()); got > maxSourceWarmWorkers {
		t.Errorf("max concurrent warm downloads = %d, want <= %d (pre-claim warm must stay bounded)", got, maxSourceWarmWorkers)
	}
	if got := int(stager.maxActive.Load()); got < 2 {
		t.Errorf("max concurrent warm downloads = %d, want >= 2 (sources must warm in parallel)", got)
	}
}

// TestWarmSourceCache_PerURLFailureIsBestEffort pins godlike/07
// no-fake-availability: a failing source is reported, never fatal, and the
// remaining sources still warm.
func TestWarmSourceCache_PerURLFailureIsBestEffort(t *testing.T) {
	stager := newWarmStager(t)
	badURL := warmTestURL(4)
	stager.failURLs[badURL] = errors.New("injected warm failure")
	cache := newFakeSourceCache()
	svc := newWarmTestService(t, stager, cache)
	goodURL := warmTestURL(5)

	report := svc.WarmSourceCache(context.Background(), []string{badURL, goodURL})
	if report.Requested != 2 || report.Warmed != 1 || report.Failed != 1 {
		t.Fatalf("warm report = %+v, want Requested=2 Warmed=1 Failed=1", report)
	}
	reason, ok := report.Errors[badURL]
	if !ok || !strings.Contains(reason, "injected warm failure") {
		t.Errorf("report.Errors[%s] = %q, want the injected failure reason", badURL, reason)
	}
	if _, unexpected := report.Errors[goodURL]; unexpected {
		t.Errorf("report.Errors must not contain the healthy source %s", goodURL)
	}
	if cache.Count() != 1 {
		t.Errorf("cache entries = %d, want 1 (healthy source must still be cached)", cache.Count())
	}
}

// TestWarmSourceCache_SkipsDriveURLs pins the deliberate Drive exclusion: the
// Drive branch caches a temp path that the stager's own release removes, so
// warming a Drive URL would download the whole file for nothing.
func TestWarmSourceCache_SkipsDriveURLs(t *testing.T) {
	stager := newWarmStager(t)
	cache := newFakeSourceCache()
	svc := newWarmTestService(t, stager, cache)

	report := svc.WarmSourceCache(context.Background(), []string{
		"https://drive.google.com/file/d/1AbCdEfGhIjKlMnOpQrStUvWxYz012345/view",
		warmTestURL(6),
	})
	if report.SkippedDrive != 1 {
		t.Errorf("SkippedDrive = %d, want 1", report.SkippedDrive)
	}
	if report.Requested != 1 || report.Warmed != 1 {
		t.Errorf("warm report = %+v, want Requested=1 Warmed=1 (Drive URL excluded)", report)
	}
	if got := stager.Count(); got != 1 {
		t.Errorf("stager Prepare calls = %d, want 1 (Drive URL must not be downloaded)", got)
	}
}

// TestWarmSourceCache_NilAndEmptyAreNoops pins the fail-safe surface: warming is
// an optimisation, so a nil service / empty input simply reports nothing to do.
func TestWarmSourceCache_NilAndEmptyAreNoops(t *testing.T) {
	var nilSvc *Service
	report := nilSvc.WarmSourceCache(context.Background(), []string{warmTestURL(7)})
	if report.Requested != 0 || report.Warmed != 0 || report.Failed != 0 {
		t.Errorf("nil service report = %+v, want an all-zero report", report)
	}
	if report.Errors == nil {
		t.Error("report.Errors must be non-nil so callers can read it unconditionally")
	}

	stager := newWarmStager(t)
	cache := newFakeSourceCache()
	svc := newWarmTestService(t, stager, cache)
	empty := svc.WarmSourceCache(context.Background(), []string{"", "   "})
	if empty.Requested != 0 || empty.Warmed != 0 {
		t.Errorf("blank-urls report = %+v, want an all-zero report", empty)
	}
	if got := stager.Count(); got != 0 {
		t.Errorf("stager Prepare calls = %d, want 0", got)
	}
}

// recordingWarmRunner is a submit-path double that satisfies BOTH ServiceRunner
// (so the optional-warmer type assert succeeds) and SourceCacheWarmer, and
// records the pre-claim warm call.
type recordingWarmRunner struct {
	mu        sync.Mutex
	warmCalls int
	urls      []string
	report    SourceWarmReport
}

var (
	_ ServiceRunner     = (*recordingWarmRunner)(nil)
	_ SourceCacheWarmer = (*recordingWarmRunner)(nil)
)

func (r *recordingWarmRunner) Run(context.Context, *RunInput) (*PipelineResult, error) {
	return nil, nil
}

func (r *recordingWarmRunner) WarmSourceCache(_ context.Context, urls []string) SourceWarmReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warmCalls++
	r.urls = append([]string(nil), urls...)
	return r.report
}

func (r *recordingWarmRunner) WarmCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.warmCalls
}

func (r *recordingWarmRunner) WarmedURLs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.urls...)
}

// runOnlyRunner satisfies ServiceRunner but NOT SourceCacheWarmer — the shape of
// a stub/test runner. The submit path must degrade, not panic.
type runOnlyRunner struct{}

var _ ServiceRunner = (*runOnlyRunner)(nil)

func (runOnlyRunner) Run(context.Context, *RunInput) (*PipelineResult, error) { return nil, nil }

// TestStockUseCase_SubmitAsync_WarmsDirectURLsBeforeClaim pins the wiring: the
// async submit path must hand the job's direct URLs to the warmer once the job
// is enqueued (i.e. before any worker can claim it).
func TestStockUseCase_SubmitAsync_WarmsDirectURLsBeforeClaim(t *testing.T) {
	runner := &recordingWarmRunner{report: SourceWarmReport{Requested: 2, Warmed: 2}}
	uc := NewStockUseCase(runner, &recordingJobsEnqueuer{}, zap.NewNop())
	urls := []string{warmTestURL(9), warmTestURL(10)}

	jobID, err := uc.Submit(context.Background(), &StockCommand{DirectURLs: urls, TotalMinutes: 5}, true)
	if err != nil {
		t.Fatalf("Submit returned unexpected error: %v", err)
	}
	if jobID == "" {
		t.Fatal("Submit returned an empty job id")
	}

	deadline := time.Now().Add(5 * time.Second)
	for runner.WarmCalls() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("pre-claim warm was never invoked on the async submit path")
		}
		time.Sleep(2 * time.Millisecond)
	}

	got := runner.WarmedURLs()
	if len(got) != len(urls) || got[0] != urls[0] || got[1] != urls[1] {
		t.Errorf("warmed URLs = %v, want %v", got, urls)
	}
}

// TestStockUseCase_SubmitAsync_SyncSubmitDoesNotWarm pins the scope: warming is a
// pre-claim optimisation, so the synchronous path (which stages the sources in
// its own process immediately) must not pay for it.
func TestStockUseCase_SubmitAsync_SyncSubmitDoesNotWarm(t *testing.T) {
	runner := &recordingWarmRunner{}
	uc := NewStockUseCase(runner, &recordingJobsEnqueuer{}, zap.NewNop())

	if _, err := uc.Submit(context.Background(), &StockCommand{DirectURLs: []string{warmTestURL(11)}}, false); err != nil {
		t.Fatalf("sync Submit returned unexpected error: %v", err)
	}
	if got := runner.WarmCalls(); got != 0 {
		t.Errorf("warm calls = %d on the sync path, want 0", got)
	}
}

// TestStockUseCase_SubmitAsync_RunnerWithoutWarmerIsNotFatal pins the optional
// surface: a ServiceRunner that cannot warm (stub, partial deploy) must not
// break or panic the enqueue path.
func TestStockUseCase_SubmitAsync_RunnerWithoutWarmerIsNotFatal(t *testing.T) {
	uc := NewStockUseCase(runOnlyRunner{}, &recordingJobsEnqueuer{}, zap.NewNop())
	jobID, err := uc.Submit(context.Background(), &StockCommand{DirectURLs: []string{warmTestURL(12)}}, true)
	if err != nil {
		t.Fatalf("Submit returned unexpected error: %v", err)
	}
	if jobID == "" {
		t.Fatal("Submit returned an empty job id")
	}
}

// TestWarmSourceCache_UnwiredStagerIsReportedNotFatal pins that a composition
// gap surfaces in the report instead of panicking or silently reporting a
// successful warm.
func TestWarmSourceCache_UnwiredStagerIsReportedNotFatal(t *testing.T) {
	cache := newFakeSourceCache()
	svc := newWarmTestService(t, nil, cache)
	url := warmTestURL(8)

	report := svc.WarmSourceCache(context.Background(), []string{url})
	if report.Failed != 1 || report.Warmed != 0 {
		t.Fatalf("warm report = %+v, want Failed=1 Warmed=0", report)
	}
	if !strings.Contains(report.Errors[url], "not wired") {
		t.Errorf("report.Errors[%s] = %q, want a 'not wired' wiring diagnostic", url, report.Errors[url])
	}
	if cache.Count() != 0 {
		t.Errorf("cache entries = %d, want 0 (nothing could be staged)", cache.Count())
	}
}

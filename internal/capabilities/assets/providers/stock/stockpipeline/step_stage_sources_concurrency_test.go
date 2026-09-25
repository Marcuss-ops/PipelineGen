// Package stockpipeline — step_stage_sources_concurrency_test.go
// (PR-STOCK-STAGE-SOURCES-CONCURRENCY, September 2026).
//
// Contract tests for the bounded stock.stage_sources fan-out. Before this
// change the step staged every unique source strictly in series, so an
// N-source request paid the SUM of N yt-dlp downloads and dominated the run
// wall (the pipeline's single largest cost). These tests pin the new contract:
//
//  1. independent sources stage CONCURRENTLY, bounded by
//     Cfg().MaxConcurrentDownloads;
//  2. MaxConcurrentDownloads=0 falls back to DefaultMaxConcurrentDownloads;
//     2b. the download bound is DECOUPLED from the CPU-bound cut bound
//     (Cfg().MaxConcurrentJobs): changing one must not change the other, or
//     staging pays ceil(N/3) waves of yt-dlp's fixed per-invocation cost again;
//  3. StagedAssets stay in canonical plan order regardless of completion order
//     (checkpoints + run fingerprints must not depend on goroutine scheduling);
//  4. per-source failures are graceful and still surface through
//     ErrStockStageSourcesIncomplete with the SourceErrors keyed by SourceID.
package stockpipeline

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
)

// boundedStagingStager is an acquisition.SourceStager stub that measures the
// peak number of concurrent Prepare calls and can inject per-URL failures.
type boundedStagingStager struct {
	mu        sync.Mutex
	active    int
	maxActive int
	delay     time.Duration
	failURLs  map[string]bool
}

var _ acquisition.SourceStager = (*boundedStagingStager)(nil)

func (s *boundedStagingStager) Prepare(ctx context.Context, req acquisition.PrepareRequest) (*acquisition.PrepareContext, error) {
	s.mu.Lock()
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.active--
		s.mu.Unlock()
	}()

	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if s.failURLs[req.Source.URL] {
		return nil, errors.New("injected staging failure")
	}
	return &acquisition.PrepareContext{
		ID:        req.Source.URL,
		LocalPath: "/staged/" + req.Source.URL,
		SizeBytes: 2048,
	}, nil
}

func (s *boundedStagingStager) Release(_ context.Context, _ string) error { return nil }

func (s *boundedStagingStager) maxConcurrency() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxActive
}

// stagingFakeRunner is the "stage-sources-isolated" fixture. It embeds the
// shared fakeStepRunner (only overriding SourceStager, which the base stub
// hardcodes to nil) so the step body sees a wired stager.
type stagingFakeRunner struct {
	*fakeStepRunner
	stager acquisition.SourceStager
}

func (r *stagingFakeRunner) SourceStager() acquisition.SourceStager { return r.stager }

func newStagingFakeRunner(plans []ClipPlan, stager acquisition.SourceStager, maxDownloads int) *stagingFakeRunner {
	return &stagingFakeRunner{
		fakeStepRunner: &fakeStepRunner{
			runInput: &RunInput{ClipDuration: 5, TotalMinutes: 1},
			cfg: OrchestratorConfig{
				PolicyVersion:          "test-policy-v1",
				MaxConcurrentDownloads: maxDownloads,
			},
			state: &RunState{Plan: plans},
		},
		stager: stager,
	}
}

func stageSourcePlans(urls ...string) []ClipPlan {
	plans := make([]ClipPlan, 0, len(urls))
	for _, url := range urls {
		plans = append(plans, ClipPlan{
			SourceID:      url,
			StartSec:      0,
			EndSec:        5,
			PolicyVersion: "test-policy-v1",
		})
	}
	return plans
}

// TestStockStageSources_BoundedConcurrencyPreservesPlanOrder pins both the
// concurrency win and the determinism guarantee: four sources stage two at a
// time, and StagedAssets come back in plan order even though goroutines may
// finish out of order.
func TestStockStageSources_BoundedConcurrencyPreservesPlanOrder(t *testing.T) {
	urls := []string{
		"https://example.com/a.mp4",
		"https://example.com/b.mp4",
		"https://example.com/c.mp4",
		"https://example.com/d.mp4",
	}

	stager := &boundedStagingStager{delay: 25 * time.Millisecond}
	runner := newStagingFakeRunner(stageSourcePlans(urls...), stager, 2)

	if err := (StockStageSourcesStep{}).Run(context.Background(), runner); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := stager.maxConcurrency(); got != 2 {
		t.Fatalf("max concurrent Prepare calls = %d, want 2 (bounded fan-out)", got)
	}

	staged := runner.State().StagedAssets
	if len(staged) != len(urls) {
		t.Fatalf("StagedAssets = %d, want %d", len(staged), len(urls))
	}
	for i, asset := range staged {
		if asset.SourceID != urls[i] {
			t.Errorf("StagedAssets[%d].SourceID = %q, want %q (order must follow the plan, not completion)", i, asset.SourceID, urls[i])
		}
	}
	if len(runner.State().SourceErrors) != 0 {
		t.Fatalf("SourceErrors = %v, want empty", runner.State().SourceErrors)
	}
}

// TestStockStageSources_UnsetParallelismFallsBackToDefault pins the fallback:
// MaxConcurrentDownloads=0 must use DefaultMaxConcurrentDownloads, never a
// zero-capacity pool (which would suspend every worker forever).
func TestStockStageSources_UnsetParallelismFallsBackToDefault(t *testing.T) {
	// More sources than the default bound, so the observed peak is the bound
	// itself and not the source count (which would make the assertion pass for
	// any fallback <= len(urls)).
	urls := []string{
		"https://example.com/a.mp4",
		"https://example.com/b.mp4",
		"https://example.com/c.mp4",
		"https://example.com/d.mp4",
		"https://example.com/e.mp4",
		"https://example.com/f.mp4",
		"https://example.com/g.mp4",
	}

	stager := &boundedStagingStager{delay: 20 * time.Millisecond}
	runner := newStagingFakeRunner(stageSourcePlans(urls...), stager, 0)

	if err := (StockStageSourcesStep{}).Run(context.Background(), runner); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := stager.maxConcurrency(); got != DefaultMaxConcurrentDownloads {
		t.Fatalf("max concurrent Prepare calls = %d, want %d (DefaultMaxConcurrentDownloads)", got, DefaultMaxConcurrentDownloads)
	}
	if got := len(runner.State().StagedAssets); got != len(urls) {
		t.Fatalf("StagedAssets = %d, want %d", got, len(urls))
	}
}

// TestStockStageSources_DownloadBoundIsIndependentOfTheCutBound is the
// decoupling guard: the download fan-out must follow
// Cfg().MaxConcurrentDownloads even when the CPU-bound cut bound
// (Cfg().MaxConcurrentJobs) is far smaller. If staging ever reads the cut knob
// again, a 15-source actor set silently collapses back to ceil(15/3) waves of
// yt-dlp's fixed ~15-20s per-invocation cost.
func TestStockStageSources_DownloadBoundIsIndependentOfTheCutBound(t *testing.T) {
	urls := []string{
		"https://example.com/a.mp4",
		"https://example.com/b.mp4",
		"https://example.com/c.mp4",
		"https://example.com/d.mp4",
	}

	stager := &boundedStagingStager{delay: 30 * time.Millisecond}
	runner := newStagingFakeRunner(stageSourcePlans(urls...), stager, 4)
	runner.cfg.MaxConcurrentJobs = 1

	if err := (StockStageSourcesStep{}).Run(context.Background(), runner); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := stager.maxConcurrency(); got != 4 {
		t.Fatalf("max concurrent Prepare calls = %d, want 4 (MaxConcurrentDownloads, not MaxConcurrentJobs=1)", got)
	}
	if got := len(runner.State().StagedAssets); got != len(urls) {
		t.Fatalf("StagedAssets = %d, want %d", got, len(urls))
	}
}

// TestStockStageSources_PartialFailureKeepsOrderAndReasons pins the graceful
// degradation contract under concurrency: one failing source does not cancel
// its siblings, the successes remain in plan order, and the fail-closed gate
// still surfaces ErrStockStageSourcesIncomplete with the failed SourceID.
func TestStockStageSources_PartialFailureKeepsOrderAndReasons(t *testing.T) {
	urls := []string{
		"https://example.com/a.mp4",
		"https://example.com/b.mp4",
		"https://example.com/c.mp4",
	}

	stager := &boundedStagingStager{
		delay:    10 * time.Millisecond,
		failURLs: map[string]bool{urls[1]: true},
	}
	runner := newStagingFakeRunner(stageSourcePlans(urls...), stager, 2)

	err := (StockStageSourcesStep{}).Run(context.Background(), runner)
	if !errors.Is(err, ErrStockStageSourcesIncomplete) {
		t.Fatalf("err = %v, want ErrStockStageSourcesIncomplete", err)
	}

	gotReason := runner.State().SourceErrors[urls[1]]
	if gotReason != "injected staging failure" {
		t.Fatalf("SourceErrors[%q] = %q, want %q", urls[1], gotReason, "injected staging failure")
	}

	staged := runner.State().StagedAssets
	if len(staged) != 2 {
		t.Fatalf("StagedAssets = %d, want 2 (only the successful sources)", len(staged))
	}
	if staged[0].SourceID != urls[0] || staged[1].SourceID != urls[2] {
		t.Fatalf("StagedAssets order = [%q, %q], want [%q, %q] in plan order",
			staged[0].SourceID, staged[1].SourceID, urls[0], urls[2])
	}
}

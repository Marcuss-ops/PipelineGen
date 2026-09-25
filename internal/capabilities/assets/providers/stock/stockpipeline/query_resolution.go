package stockpipeline

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"

	"go.uber.org/zap"
)

// maxSearchQueryWorkers bounds concurrent provider searches. Search calls are
// network/process heavy, so keep this independent from FFmpeg source-cut
// parallelism and deliberately conservative for the CPU-first worker.
const maxSearchQueryWorkers = 3

// searchQueryResolution holds one query's result at its original index. The
// indexed slices let workers write without locks while the caller performs
// deterministic ordered logging, URL deduplication, and error aggregation.
type searchQueryResolution struct {
	sources []VideoSource
	err     error
}

// resolveInputQueries converts text search queries in input.SearchQueries
// to resolved YouTube URLs via s.resolveQuery(), appending them to
// input.DirectURLs. Search calls run through a bounded worker pool, but
// aggregation remains in query order so retries and downstream planning are
// deterministic. URLs are deduplicated by their trimmed first appearance.
//
// A query-level failure logs a warning and does not cancel sibling queries;
// this preserves partial-success behavior. If every query fails, the existing
// typed ErrStockPipelineAllQueriesFailed is returned. Parent context
// cancellation is propagated as-is; provider errors retain the existing
// partial-success behavior and are only fatal when no usable URL remains.
func (s *Service) resolveInputQueries(ctx context.Context, input *RunInput) error {
	if s == nil || input == nil || len(input.SearchQueries) == 0 {
		return nil
	}

	queries := append([]string(nil), input.SearchQueries...)
	limits := append([]int(nil), input.SearchQueryLimits...)
	results := make([]searchQueryResolution, len(queries))
	workerCount := maxSearchQueryWorkers
	if workerCount > len(queries) {
		workerCount = len(queries)
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workerCount)
	for worker := 0; worker < workerCount; worker++ {
		concurrent.SafeGo("stock-query-resolution", func() {
			defer wg.Done()
			for {
				select {
				case <-workCtx.Done():
					return
				case index, ok := <-jobs:
					if !ok {
						return
					}
					if err := workCtx.Err(); err != nil {
						results[index].err = err
						continue
					}
					limit := 0
					if index < len(limits) {
						limit = limits[index]
					}
					sources, err := s.resolveQuery(workCtx, queries[index], limit)
					results[index] = searchQueryResolution{sources: sources, err: err}
				}
			}
		})
	}

	dispatching := true
	for index := range queries {
		if !dispatching {
			break
		}
		select {
		case jobs <- index:
		case <-workCtx.Done():
			dispatching = false
		}
	}
	close(jobs)
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return err
	}
	total := len(queries)
	failed := 0
	var lastErr error
	seen := make(map[string]struct{}, len(input.DirectURLs))
	directURLs := make([]string, 0, len(input.DirectURLs))
	for _, rawURL := range input.DirectURLs {
		url := strings.TrimSpace(rawURL)
		if url == "" {
			continue
		}
		if _, exists := seen[url]; exists {
			continue
		}
		seen[url] = struct{}{}
		directURLs = append(directURLs, url)
	}
	input.DirectURLs = directURLs
	for index, query := range queries {
		result := results[index]
		if result.err != nil {
			if s.log != nil {
				s.log.Warn("stock: failed to resolve search query, skipping",
					zap.String("query", query), zap.Error(result.err))
			}
			failed++
			lastErr = result.err
			continue
		}

		for _, src := range result.sources {
			url := strings.TrimSpace(src.URL)
			if url == "" {
				continue
			}
			if _, exists := seen[url]; exists {
				continue
			}
			seen[url] = struct{}{}
			input.DirectURLs = append(input.DirectURLs, url)
			// Carry the provider-known source duration into the plan
			// step so the deterministic planner distributes clip
			// windows within the REAL source length instead of the
			// budget*10 fallback. Without this, a short source
			// (e.g. 226s) planned with a 600s horizon fails closed at
			// extract time with ErrStockClipsOutOfRange.
			if src.DurationSec > 0 {
				if input.SourceDurations == nil {
					input.SourceDurations = make(map[string]float64)
				}
				input.SourceDurations[url] = src.DurationSec
			}
			// Carry the provider-known source title so the planned clips
			// inherit a searchable title instead of staying anonymous
			// clip_### rows. resolveQuery already resolved the real
			// YouTube title; dropping it here made a query such as
			// "mike tyson" unable to find its own clips.
			if title := strings.TrimSpace(src.Title); title != "" {
				if input.SourceTitles == nil {
					input.SourceTitles = make(map[string]string)
				}
				input.SourceTitles[url] = title
			}
		}
		if s.log != nil {
			if len(result.sources) > 0 {
				s.log.Info("stock: resolved search query to URLs",
					zap.String("query", query),
					zap.Int("urls", len(result.sources)))
			} else {
				s.log.Warn("stock: search query returned no results",
					zap.String("query", query))
			}
		}
	}
	// MaxVideos is the run-level cap for search-acquired sources. Apply it
	// after deterministic query-order aggregation and URL de-duplication so
	// multi-query runs can request a bounded, mixed set of sources without
	// violating the duration contract at planning time.
	if input.MaxVideos > 0 && len(input.DirectURLs) > input.MaxVideos {
		input.DirectURLs = input.DirectURLs[:input.MaxVideos]
		if s.log != nil {
			s.log.Info("stock: capped resolved search sources",
				zap.Int("max_videos", input.MaxVideos),
				zap.Int("urls", len(input.DirectURLs)))
		}
	}

	// Clear resolved queries so the orchestrator doesn't try to use
	// raw text as a URL (firstSource checks SearchQueries after
	// DirectURLs — the resolved URLs are already in DirectURLs).
	input.SearchQueries = nil
	// PR-STOCK-QUERY-RESOLUTION-FAIL-CLOSED (July 2026): when ALL
	// queries fail to resolve, return a typed error instead of
	// silently clearing SearchQueries. Without this, the
	// orchestrator hits the misleading "no sources to plan" error
	// in StockPlanStep.Run instead of surfacing the actual yt-dlp
	// failure (n-challenge, cookies, network).
	if failed > 0 && failed == total && len(input.DirectURLs) == 0 {
		return fmt.Errorf("%w: %d/%d queries failed, last error: %v",
			ErrStockPipelineAllQueriesFailed, failed, total, lastErr)
	}
	return nil
}

// directURLDurationProbeTimeout bounds each provider metadata probe so a
// slow or hung yt-dlp invocation cannot stall the run before its first
// planned step. The inner ListChannel call owns a longer 60s process
// timeout; this is the tighter outer bound for the planning phase.
const directURLDurationProbeTimeout = 20 * time.Second

// enrichDirectURLDurations resolves provider-known durations for direct
// source URLs that carry none, populating input.SourceDurations so the
// deterministic planner distributes clip windows inside the REAL source
// length instead of the budget*10 fallback.
//
// Why this exists: search-resolved sources arrive with a provider duration
// (resolveInputQueries propagates it), but a bare `direct_url` does not. A
// direct URL with no duration and no explicit clips therefore planned a
// budget*10 horizon — windows far past the end of any real video — and died
// at stock.extract_clips with ErrStockClipsOutOfRange. This reuses the
// already-wired ChannelLister (the same provider surface resolveQuery
// consumes) so no new port or composition-root plumbing is required.
//
// Scope and skip conditions:
//   - nil service/input, nil ChannelLister, no DirectURLs, or any explicit
//     Clips present → no-op (nothing to resolve / not needed: the explicit
//     planner consumes the operator windows verbatim and ignores the
//     deterministic horizon).
//   - Non-YouTube direct URLs are skipped (the duration-native metadata path
//     here is YouTube-only).
//   - URLs whose duration is already known are never overwritten. A caller
//     that already knows the durations (the curated `source_durations`
//     payload field) therefore pays ZERO provider probes on this path.
//
// Parallelism: the probes are independent per URL and run through a bounded
// pool (maxDurationProbeWorkers) with per-probe deadlines; the results are
// applied in DirectURLs order so logging and the resulting maps stay
// deterministic.
//
// godlike/07 contract: a failed or empty probe is NON-FATAL. It logs a Warn
// and leaves the plan on the planner's conservative fallback; it never
// fabricates a duration, so a genuinely out-of-range plan still fails closed
// at extract time.
func (s *Service) enrichDirectURLDurations(ctx context.Context, input *RunInput) {
	if s == nil || input == nil || s.channelLister == nil {
		return
	}
	if len(input.Clips) > 0 || len(input.DirectURLs) == 0 {
		return
	}

	// Canonical stage: the per-URL provider probe is a serial, network-bound
	// span that runs BEFORE planning. Recording it lets the timing breakdown
	// attribute it instead of folding it into unattributed wall time.
	probeMetric := startServiceStockPhase(ctx, "stock.duration_probe", "")
	defer func() {
		if probeMetric != nil {
			resolved := 0
			for _, raw := range input.DirectURLs {
				if input.SourceDurations[strings.TrimSpace(raw)] > 0 {
					resolved++
				}
			}
			probeMetric.SetItems(int64(len(input.DirectURLs)), int64(resolved))
		}
		finishServiceStockPhase(s.log, probeMetric, nil)
	}()

	// Deterministic work list: DirectURLs order, minus the URLs that already
	// carry a duration (operator-supplied source_durations or a duration the
	// search resolution propagated) and the non-YouTube sources this metadata
	// path cannot probe.
	targets := make([]string, 0, len(input.DirectURLs))
	for _, raw := range input.DirectURLs {
		url := strings.TrimSpace(raw)
		if url == "" {
			continue
		}
		if known := input.SourceDurations[url]; known > 0 {
			continue
		}
		if InferSourceProvider(url) != SourceProviderYouTube {
			continue
		}
		targets = append(targets, url)
	}
	if len(targets) == 0 {
		// Every source already carries a duration — the probe is skipped
		// entirely (the caller paid for the metadata once, upstream).
		return
	}

	// The probes are independent per URL, so they run through a bounded pool
	// instead of the previous serial loop: a 15-source run paid 15 sequential
	// provider round-trips (~90s measured) before planning could start. Writes
	// stay off the shared maps here — each worker fills its own slot and the
	// caller applies the results in DirectURLs order below, so log lines and
	// map contents remain deterministic regardless of completion order.
	results := make([]durationProbeResult, len(targets))
	workers := maxDurationProbeWorkers
	if workers > len(targets) {
		workers = len(targets)
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)
	for worker := 0; worker < workers; worker++ {
		concurrent.SafeGo("stock-duration-probe", func() {
			defer wg.Done()
			for {
				select {
				case <-workCtx.Done():
					return
				case index, ok := <-jobs:
					if !ok {
						return
					}
					if err := workCtx.Err(); err != nil {
						results[index] = durationProbeResult{err: err}
						continue
					}
					results[index] = s.probeDirectURLDuration(workCtx, targets[index])
				}
			}
		})
	}

	dispatching := true
	for index := range targets {
		if !dispatching {
			break
		}
		select {
		case jobs <- index:
		case <-workCtx.Done():
			dispatching = false
		}
	}
	close(jobs)
	wg.Wait()

	for index, url := range targets {
		if ctx.Err() != nil {
			// The run is being cancelled: stop applying results, the planner
			// fallback is irrelevant once the job is aborted.
			return
		}
		result := results[index]
		if result.err != nil {
			if s.log != nil {
				s.log.Warn("stock: direct URL duration probe failed — planner fallback applies",
					zap.String("source_url", url), zap.Error(result.err))
			}
			continue
		}
		if result.title != "" {
			if input.SourceTitles == nil {
				input.SourceTitles = make(map[string]string)
			}
			input.SourceTitles[url] = result.title
		}
		if result.duration <= 0 {
			if s.log != nil {
				s.log.Warn("stock: direct URL duration probe returned no duration — planner fallback applies",
					zap.String("source_url", url))
			}
			continue
		}
		if input.SourceDurations == nil {
			input.SourceDurations = make(map[string]float64)
		}
		input.SourceDurations[url] = result.duration
		if s.log != nil {
			s.log.Info("stock: enriched direct URL duration from provider metadata",
				zap.String("source_url", url), zap.Float64("duration_sec", result.duration))
		}
	}
}

// maxDurationProbeWorkers bounds the concurrent provider duration probes.
// Each probe is a provider metadata call (one yt-dlp invocation on the
// YouTube path), so the pool is deliberately small: large enough to remove
// the serial round-trip latency of a multi-source run, small enough not to
// starve the downloads that follow it on the same host.
const maxDurationProbeWorkers = 6

// durationProbeResult is one probe's outcome. Workers write it into their own
// slot so the shared RunInput maps are only touched by the caller, in
// DirectURLs order.
type durationProbeResult struct {
	duration float64
	title    string
	err      error
}

// probeDirectURLDuration runs one bounded provider metadata probe. The
// per-probe deadline is what keeps a hung provider from stalling the pool.
func (s *Service) probeDirectURLDuration(ctx context.Context, url string) durationProbeResult {
	probeCtx, cancel := context.WithTimeout(ctx, directURLDurationProbeTimeout)
	defer cancel()
	videos, err := s.channelLister.ListChannel(probeCtx, url, 1)
	if err != nil {
		return durationProbeResult{err: err}
	}
	result := durationProbeResult{}
	for _, video := range videos {
		if video.Duration > result.duration {
			result.duration = video.Duration
		}
		if result.title == "" {
			result.title = strings.TrimSpace(video.Title)
		}
	}
	return result
}

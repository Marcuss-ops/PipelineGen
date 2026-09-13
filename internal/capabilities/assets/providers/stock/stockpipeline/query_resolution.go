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
//   - URLs whose duration is already known are never overwritten.
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

		probeCtx, cancel := context.WithTimeout(ctx, directURLDurationProbeTimeout)
		videos, err := s.channelLister.ListChannel(probeCtx, url, 1)
		cancel()
		if err != nil {
			if s.log != nil {
				s.log.Warn("stock: direct URL duration probe failed — planner fallback applies",
					zap.String("source_url", url), zap.Error(err))
			}
			continue
		}
		duration := 0.0
		for _, video := range videos {
			if video.Duration > duration {
				duration = video.Duration
			}
		}
		if duration <= 0 {
			if s.log != nil {
				s.log.Warn("stock: direct URL duration probe returned no duration — planner fallback applies",
					zap.String("source_url", url))
			}
			continue
		}
		if input.SourceDurations == nil {
			input.SourceDurations = make(map[string]float64)
		}
		input.SourceDurations[url] = duration
		if s.log != nil {
			s.log.Info("stock: enriched direct URL duration from provider metadata",
				zap.String("source_url", url), zap.Float64("duration_sec", duration))
		}
	}
}

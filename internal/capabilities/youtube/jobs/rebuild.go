// Package jobs provides YouTube job handler implementations.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	jobtools "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	"go.uber.org/zap"
)

// RebuildDeps holds the dependencies for HandleRebuildSearchTextJob.
type RebuildDeps struct {
	Log      *zap.Logger
	Indexer  ClipIndexer
	Clips    YouTubeClipLister
	Enricher func(ctx context.Context, clipID string, meta any, force bool)
}

// ClipIndexer abstracts the clip embedding/indexing service.
type ClipIndexer interface {
	IsEnabled() bool
	IndexClip(ctx context.Context, clipID string) error
}

type YouTubeClipLister interface {
	ListYouTubeClipIDsForSearchText(ctx context.Context, limit, offset int) ([]string, error)
}

// HandleRebuildSearchTextJob rebuilds search_text for YouTube clips.
func HandleRebuildSearchTextJob(deps RebuildDeps, ctx context.Context, j *job.Job, tools *jobtools.JobTools) (map[string]any, error) {
	if deps.Clips == nil {
		return nil, fmt.Errorf("youtube clip lister not available")
	}

	type payload struct {
		Limit       int  `json:"limit"`
		Offset      int  `json:"offset"`
		ReIndex     bool `json:"reindex"`
		Concurrency int  `json:"concurrency"`
	}
	var p payload
	if len(j.Payload) > 0 {
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			return nil, fmt.Errorf("invalid payload: %w", err)
		}
	}

	if p.Concurrency <= 0 {
		p.Concurrency = 1
	}

	if tools.Progress != nil {
		tools.Progress(0, "Querying YouTube clips for search_text rebuild")
	}

	clipIDs, err := deps.Clips.ListYouTubeClipIDsForSearchText(ctx, p.Limit, p.Offset)
	if err != nil {
		return nil, fmt.Errorf("query youtube clips: %w", err)
	}

	if len(clipIDs) == 0 {
		deps.Log.Info("no YouTube clips found for search_text rebuild")
		if tools.Progress != nil {
			tools.Progress(100, "No YouTube clips found")
		}
		return map[string]any{"total": 0, "rebuilt": 0, "failed": 0}, nil
	}

	n := len(clipIDs)
	deps.Log.Info("starting search_text rebuild for YouTube clips", zap.Int("count", n))

	if tools.Progress != nil {
		tools.Progress(5, fmt.Sprintf("Rebuilding search_text for %d YouTube clips", n))
	}

	sem := make(chan struct{}, p.Concurrency)
	var (
		mu        sync.Mutex
		rebuilt   int
		failed    int
		reindexed int
		wg        sync.WaitGroup
	)

	for _, clipID := range clipIDs {
		// FASE 4(b) (July 2026): the tools.IsCancelled callback is
		// REMOVED from domain/job.JobExecutionTools (the pre-Fase-4
		// 2-second IsCancelled-poll goroutine is gone). Handlers
		// observe cancellation natively via ctx.Err() at their
		// next phase boundary. The break-on-cancel pattern below
		// uses ctx.Err() as the canonical cancellation probe.
		if ctx.Err() != nil {
			break
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer func() {
				if r := recover(); r != nil {
					deps.Log.Error("panic in rebuild worker goroutine", zap.String("clip_id", id), zap.Any("recover", r))
				}
			}()
			defer wg.Done()
			defer func() { <-sem }()

			clipCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()

			deps.Enricher(clipCtx, id, nil, true)

			mu.Lock()
			rebuilt++
			mu.Unlock()

			if p.ReIndex && deps.Indexer != nil && deps.Indexer.IsEnabled() {
				if err := deps.Indexer.IndexClip(clipCtx, id); err != nil {
					deps.Log.Warn("re-index failed for clip",
						zap.String("clip_id", id),
						zap.Error(err))
				} else {
					mu.Lock()
					reindexed++
					mu.Unlock()
				}
			}

			mu.Lock()
			done := rebuilt + failed
			pct := (done * 90 / n) + 5
			msg := fmt.Sprintf("Rebuilt %d/%d clips (%d failed)%s",
				done, n, failed,
				func() string {
					if reindexed > 0 {
						return fmt.Sprintf(", %d reindexed", reindexed)
					}
					return ""
				}())
			mu.Unlock()

			if tools.Progress != nil {
				tools.Progress(pct, msg)
			}
		}(clipID)
	}

	wg.Wait()

	result := map[string]any{
		"total":     n,
		"rebuilt":   rebuilt,
		"failed":    failed,
		"reindexed": reindexed,
	}

	if tools.Progress != nil {
		tools.Progress(100, fmt.Sprintf("Rebuilt search_text for %d/%d clips (%d reindexed)", rebuilt, n, reindexed))
	}

	deps.Log.Info("search_text rebuild complete",
		zap.Int("total", n),
		zap.Int("rebuilt", rebuilt),
		zap.Int("failed", failed),
		zap.Int("reindexed", reindexed))

	return result, nil
}

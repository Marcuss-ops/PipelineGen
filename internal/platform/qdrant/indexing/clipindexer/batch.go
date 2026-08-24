package clipindexer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	jobmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

// DefaultBatchConcurrency is the default number of parallel workers for batch reindex.
const DefaultBatchConcurrency = 3

type JobRegistrar interface {
	RegisterHandler(jobType string, h any) error
}

// RegisterJobHandler registers the batch reindex job handler with the jobs service.
func (s *Service) RegisterJobHandler(jobsSvc JobRegistrar) error {
	if jobsSvc == nil {
		return errors.New("clipindexer.Service.RegisterJobHandler: jobsSvc is nil")
	}
	if err := jobsSvc.RegisterHandler(jobmedia.TypeReindex, s.HandleJob); err != nil {
		return fmt.Errorf("clipindexer.Service.RegisterJobHandler: bind %q to dispatcher: %w", jobmedia.TypeReindex, err)
	}
	s.log.Info("registered media.reindex job handler")
	return nil
}

// HandleJob processes a batch reindex job from the job system.
// Payload: {"source": "artlist", "media_type": "video", "limit": 100}
// Reports progress via tools.Progress(pct, msg).
func (s *Service) HandleJob(ctx context.Context, j *job.Job, tools *job.JobExecutionTools) (map[string]any, error) {
	var req struct {
		Source    string `json:"source"`
		MediaType string `json:"media_type"`
		Limit     int    `json:"limit"`
	}
	if err := json.Unmarshal(j.Payload, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
	}

	if tools.Progress != nil {
		tools.Progress(5, "Querying assets missing embeddings")
	}

	// Query assets missing either semantic or transcript embeddings
	conditions := []string{
		`((embedding_json IS NULL OR embedding_json = '' OR embedding_json = '[]' OR embedding_json = '{}')
		  OR
		  (transcript_embedding IS NULL OR transcript_embedding = '' OR transcript_embedding = '[]' OR transcript_embedding = '{}'))`,
	}
	var queryParams []any
	if req.Source != "" {
		conditions = append(conditions, "source = ?")
		queryParams = append(queryParams, req.Source)
	}
	if req.MediaType != "" {
		conditions = append(conditions, "media_type = ?")
		queryParams = append(queryParams, req.MediaType)
	}

	where := strings.Join(conditions, " AND ")
	query := "SELECT id FROM media_assets WHERE " + where + " ORDER BY created_at DESC"
	if req.Limit > 0 {
		query += " LIMIT ?"
		queryParams = append(queryParams, req.Limit)
	} else {
		query += " LIMIT -1"
	}

	rows, err := s.db.QueryContext(ctx, query, queryParams...)
	if err != nil {
		return nil, fmt.Errorf("query assets: %w", err)
	}
	defer rows.Close()

	var clipIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		clipIDs = append(clipIDs, id)
	}

	if len(clipIDs) == 0 {
		if tools.Progress != nil {
			tools.Progress(100, "No assets missing embeddings")
		}
		return map[string]any{"total": 0, "indexed": 0, "failed": 0}, nil
	}

	// Process with worker pool + progress callbacks
	n := len(clipIDs)
	concurrency := DefaultBatchConcurrency
	if concurrency > n {
		concurrency = n
	}

	var (
		wg      sync.WaitGroup
		sem     = make(chan struct{}, concurrency)
		indexed atomic.Int64
		failed  atomic.Int64
	)

	for _, clipID := range clipIDs {
		// FASE 4(b) (July 2026): cancellation propagates via ctx.Err() —
		// the typed kerneljob.RenewLeaseResult.State (CancelRequested)
		// → renewLeaseLoopWith calls jobCancel(jobCtx) → ctx.Done().
		// The pre-Fase-4 IsCancelled callback (which polled a 2-second
		// cancel-watcher goroutine) was REMOVED from the JobTools struct
		// in FASE 4(b) as redundant with native context cancellation.
		if ctx.Err() != nil {
			break
		}

		clipID := clipID // capture
		wg.Add(1)
		sem <- struct{}{}
		concurrent.SafeGoFunc("clipindexer-batch", struct {
			ID  string
			Sem chan struct{}
		}{ID: clipID, Sem: sem}, func(arg struct {
			ID  string
			Sem chan struct{}
		}) {
			defer wg.Done()
			defer func() { <-arg.Sem }()

			jobCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()

			err := kernobs.MeasureOperation(jobCtx, kernobs.OperationInfo{
				Stage: kernobs.StageName("index"), Component: kernobs.ComponentName("clipindexer"),
				Operation: kernobs.OperationName("clip.index"), Items: 1,
			}, func(opCtx context.Context) error { return s.IndexClip(opCtx, arg.ID) })
			if err != nil {
				failed.Add(1)
			} else {
				indexed.Add(1)
			}

			// Report progress (10-100% range)
			done := int(indexed.Load() + failed.Load())
			pct := (done * 90 / n) + 10
			if tools.Progress != nil {
				tools.Progress(pct, fmt.Sprintf("Indexed %d/%d clips (%d failed)", done, n, int(failed.Load())))
			}
		})
	}

	wg.Wait()

	indexedCount := int(indexed.Load())
	failedCount := int(failed.Load())

	s.log.Info("batch reindex complete",
		zap.Int("indexed", indexedCount),
		zap.Int("failed", failedCount),
		zap.Int("total", n))

	return map[string]any{
		"total":   n,
		"indexed": indexedCount,
		"failed":  failedCount,
	}, nil
}

// IsEnabled returns whether the service is enabled

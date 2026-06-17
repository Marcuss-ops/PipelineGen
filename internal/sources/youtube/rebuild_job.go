package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"velox/go-master/internal/jobs"
	"velox/go-master/internal/media/models"

	"go.uber.org/zap"
)

// {"concurrency": 1}    – parallel workers (default 1, safe for yt-dlp rate limits)
func (s *Service) HandleRebuildSearchTextJob(ctx context.Context, job *models.Job, tools *jobs.JobTools) (map[string]any, error) {
	if s.clipsRepo == nil {
		return nil, fmt.Errorf("clips repository not available")
	}

	type payload struct {
		Limit       int  `json:"limit"`
		Offset      int  `json:"offset"`
		ReIndex     bool `json:"reindex"`
		Concurrency int  `json:"concurrency"`
	}
	var p payload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &p); err != nil {
			return nil, fmt.Errorf("invalid payload: %w", err)
		}
	}

	if p.Concurrency <= 0 {
		p.Concurrency = 1 // Default: sequential to avoid hammering yt-dlp
	}

	if tools.Progress != nil {
		tools.Progress(0, "Querying YouTube clips for search_text rebuild")
	}

	// Query all YouTube clips that have been enriched (have youtube_title).
	// We only target clips with youtube_title because enrichYouTubeClipWithMetadata
	// needs either pre-fetched metadata or a YouTube URL to fetch from.
	query := `SELECT id FROM media_assets WHERE source = 'youtube' AND json_extract(metadata_json, '$.youtube_title') != '' ORDER BY id`
	if p.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", p.Limit)
	}
	if p.Offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", p.Offset)
	}

	rows, err := s.clipsRepo.DB().QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query youtube clips: %w", err)
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
		s.log.Info("no YouTube clips found for search_text rebuild")
		if tools.Progress != nil {
			tools.Progress(100, "No YouTube clips found")
		}
		return map[string]any{"total": 0, "rebuilt": 0, "failed": 0}, nil
	}

	n := len(clipIDs)
	s.log.Info("starting search_text rebuild for YouTube clips", zap.Int("count", n))

	if tools.Progress != nil {
		tools.Progress(5, fmt.Sprintf("Rebuilding search_text for %d YouTube clips", n))
	}

	// Process sequentially with progress reporting (concurrency=1 by default,
	// because yt-dlp metadata fetches are rate-limited).
	sem := make(chan struct{}, p.Concurrency)
	var (
		mu        sync.Mutex
		rebuilt   int
		failed    int
		reindexed int
		wg        sync.WaitGroup
	)

	for _, clipID := range clipIDs {
		if tools.IsCancelled != nil && tools.IsCancelled() {
			break
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer func() {
				if r := recover(); r != nil {
					s.log.Error("panic in rebuild worker goroutine", zap.String("clip_id", id), zap.Any("recover", r))
				}
			}()
			defer wg.Done()
			defer func() { <-sem }()

			clipCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()

			// Re-enrich with force=true — bypasses the "already enriched" skip.
			// Uses the new field order: Transcript before Description.
			s.enrichYouTubeClipWithMetadata(clipCtx, id, nil, true)

			mu.Lock()
			rebuilt++
			mu.Unlock()

			// Optionally re-index embeddings and upsert to Qdrant
			if p.ReIndex && s.indexer != nil && s.indexer.IsEnabled() {
				if err := s.indexer.IndexClip(clipCtx, id); err != nil {
					s.log.Warn("re-index failed for clip",
						zap.String("clip_id", id),
						zap.Error(err))
				} else {
					mu.Lock()
					reindexed++
					mu.Unlock()
				}
			}

			// Report progress
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

	s.log.Info("search_text rebuild complete",
		zap.Int("total", n),
		zap.Int("rebuilt", rebuilt),
		zap.Int("failed", failed),
		zap.Int("reindexed", reindexed))

	return result, nil
}

// isTransientDownloadError returns true if the error is likely transient and worth retrying.
// Used with retry.Do's IsRetryable predicate for yt-dlp download operations.
func isTransientDownloadError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	// Never retry these permanent errors
	permanentPatterns := []string{
		"video unavailable",
		"private video",
		"sign in to confirm",
		"confirm your age",
		"requested format is not available",
		"invalid url",
		"unable to extract",
		"no video formats",
		"video is live",
	}
	for _, p := range permanentPatterns {
		if strings.Contains(errStr, p) {
			return false
		}
	}

	// Retry these transient errors
	transientPatterns := []string{
		"timeout",
		"connection reset",
		"connection refused",
		"temporary failure",
		"fragment download failed",
		"no route to host",
		"network is unreachable",
		"i/o timeout",
		"broken pipe",
	}
	for _, p := range transientPatterns {
		if strings.Contains(errStr, p) {
			return true
		}
	}

	// HTTP 429 (rate limit) and 5xx (server errors) are transient
	if strings.Contains(errStr, "http 429") || strings.Contains(errStr, "http 5") {
		return true
	}

	return false
}

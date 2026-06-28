package monitor

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"

	"go.uber.org/zap"
)

func (m *ChannelMonitor) searchQueriesLoop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Minute) // check every 15 min for queries due to run
	defer ticker.Stop()

	// Run immediately on startup
	m.processSearchQueries(ctx)

	for {
		select {
		case <-ticker.C:
			m.processSearchQueries(ctx)
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// processSearchQueries runs all active search queries that are due.
// For each query:
// 1. Calls SearchByTopicWithFilter with publishedAfter
// 2. Filters by min_score
// 3. Dedup against search_query_results and clipsRepo
// 4. Downloads clips for new videos
func (m *ChannelMonitor) processSearchQueries(ctx context.Context) {
	if m.searchQueriesRepo == nil || m.youtubeSvc == nil {
		return
	}

	queries, err := m.searchQueriesRepo.ListActive(ctx)
	if err != nil {
		m.log.Error("failed to list active search queries", zap.Error(err))
		return
	}

	now := time.Now().UTC()

	for _, q := range queries {
		q := q // capture

		// Check if this query is due to run
		if q.LastRunAt != "" {
			lastRun := timeutil.ParseRFC3339(q.LastRunAt)
			if !lastRun.IsZero() {
				interval, err := parseCheckInterval(q.CheckInterval)
				if err != nil {
					m.log.Warn("invalid check_interval for query",
						zap.String("query_id", q.ID),
						zap.String("interval", q.CheckInterval))
					continue
				}
				if now.Before(lastRun.Add(interval)) {
					continue // not due yet
				}
			}
		}

		// ── Rate limiter: check before each YouTube API call ──────────────
		if m.searchRateLimiter != nil {
			if !m.searchRateLimiter.Allow() {
				m.log.Warn("⏳ YouTube search rate limit reached, deferring query",
					zap.String("query_id", q.ID),
					zap.String("query", q.Query),
					zap.Int("remaining", m.searchRateLimiter.Remaining()),
					zap.Duration("reset_in", m.searchRateLimiter.ResetIn()))
				// Skip this query but keep others that follow
				// (they'll also be rate-limited but it's fine)
				continue
			}
		}

		m.log.Info("running scheduled search query",
			zap.String("query_id", q.ID),
			zap.String("query", q.Query),
			zap.String("category", q.Category))

		// Search YouTube with publishedAfter filter
		resp, err := m.youtubeSvc.SearchByTopicWithFilter(ctx, q.Query, q.MaxResults, "", q.LastVideoPublishedAt)
		if err != nil {
			m.log.Error("search query failed",
				zap.String("query_id", q.ID),
				zap.String("query", q.Query),
				zap.Error(err))
			continue
		}

		if len(resp.Results) == 0 {
			m.log.Debug("search query returned no results", zap.String("query", q.Query))
			// Still update last_run_at so we don't retry too often
			_ = m.searchQueriesRepo.UpdateLastRun(ctx, q.ID, timeutil.FormatRFC3339(now), q.LastVideoPublishedAt)
			continue
		}

		// Find the most recent publish date to update last_video_published_at
		newestPubDate := q.LastVideoPublishedAt

		for _, result := range resp.Results {
			// Track the newest publish date across all results
			if result.UploadDate != "" {
				pubDate, err := timeutil.ParseYouTubeUploadDate(result.UploadDate)
				if err == nil {
					pubStr := timeutil.FormatRFC3339(pubDate)
					if newestPubDate == "" || pubDate.After(parseDateOrZero(newestPubDate)) {
						newestPubDate = pubStr
					}
				}
			}

			// ── Filter: skip if below min_score ───────────────────────────
			score := result.SimilarityScore*70 + result.FormatMatchPercent*30
			if score/100 < q.MinScore {
				m.log.Debug("search result below min_score, skipping",
					zap.String("video_id", result.VideoID),
					zap.String("title", result.Title),
					zap.Int("score", score/100),
					zap.Int("min_score", q.MinScore))
				continue
			}

			// ── Cross-dedup: check clipsRepo AND search_query_results ─────
			if m.isVideoAlreadyProcessed(ctx, result.VideoID) {
				m.log.Debug("video already processed, skipping",
					zap.String("video_id", result.VideoID))
				continue
			}

			m.log.Info("✅ new video found by search query",
				zap.String("query_id", q.ID),
				zap.String("video_id", result.VideoID),
				zap.String("title", result.Title),
				zap.Int("score", score/100))

			// Download clip
			channel := ChannelConfig{
				URL:             result.DirectLink,
				Category:        q.Category,
				DriveFolderID:   q.DriveFolderID,
				MaxClipDuration: 60,
			}

			// Build a minimal MonitorConfig with defaults for the download
			monitorCfg := &MonitorConfig{
				YtdlpPath:       m.cfg.External.YtdlpPath,
				MaxClipDuration: 60,
				MaxFilesize:     "100M",
				OllamaURL:       m.cfg.External.OllamaURL,
			}

			m.downloadClip(ctx, result.VideoID, result.Title, channel, monitorCfg)

			// Record in search_query_results for dedup
			res := &asset.SearchQueryResult{
				QueryID:     q.ID,
				VideoID:     result.VideoID,
				VideoTitle:  result.Title,
				ChannelName: result.ChannelName,
				PublishedAt: result.UploadDate,
				Score:       score / 100,
			}
			_ = m.searchQueriesRepo.InsertResult(ctx, res)
		}

		// Update last_run_at and last_video_published_at
		_ = m.searchQueriesRepo.UpdateLastRun(ctx, q.ID, timeutil.FormatRFC3339(now), newestPubDate)
	}
}

// isVideoAlreadyProcessed checks both clipsRepo and search_query_results for dedup.
func (m *ChannelMonitor) isVideoAlreadyProcessed(ctx context.Context, videoID string) bool {
	// Check clipsRepo first (channel-sourced videos)
	if m.clipsRepo != nil {
		existing, err := m.clipsRepo.GetClipFolderByVideoID(ctx, videoID)
		if err == nil && existing != nil {
			return true
		}
	}
	// Check search_query_results (query-sourced videos)
	if m.searchQueriesRepo != nil {
		processed, err := m.searchQueriesRepo.IsVideoProcessed(ctx, videoID)
		if err == nil && processed {
			return true
		}
	}
	return false
}


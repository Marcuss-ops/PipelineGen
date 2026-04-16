package artlistpipeline

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"velox/go-master/pkg/logger"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// VideoStats tracks per-video statistics for measuring cache effectiveness.
type VideoStats struct {
	VideoID       string    `json:"video_id"`
	Topic         string    `json:"topic"`
	CreatedAt     string    `json:"created_at"`
	TotalRequests int       `json:"total_requests"` // total clip requests
	CacheHits     int       `json:"cache_hits"`     // clips found in cache/DB
	NewDownloads  int       `json:"new_downloads"`  // clips newly downloaded from Artlist
	TotalClips    int       `json:"total_clips"`    // total clips used
	Duration      string    `json:"duration"`       // video duration
}

// StatsStore manages video statistics.
type StatsStore struct {
	path string
	data *StatsData
	mu   sync.RWMutex
}

// StatsData holds all statistics data.
type StatsData struct {
	Videos []VideoStats `json:"videos"`
}

// NewStatsStore creates or loads the stats store.
func NewStatsStore(path string) (*StatsStore, error) {
	ss := &StatsStore{
		path: path,
		data: &StatsData{
			Videos: []VideoStats{},
		},
	}

	if _, err := os.Stat(path); err == nil {
		if err := ss.load(); err != nil {
			logger.Warn("Failed to load stats, starting fresh", zap.Error(err))
		}
	}

	return ss, nil
}

// load reads stats from disk.
func (ss *StatsStore) load() error {
	data, err := os.ReadFile(ss.path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, ss.data)
}

// save writes stats to disk.
func (ss *StatsStore) save() error {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	dir := filepath.Dir(ss.path)
	os.MkdirAll(dir, 0755)

	data, err := json.MarshalIndent(ss.data, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(ss.path, data, 0644)
}

// RecordVideo records stats for a new video.
func (ss *StatsStore) RecordVideo(videoID, topic string, totalRequests, cacheHits, newDownloads, totalClips int, duration string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	ss.data.Videos = append(ss.data.Videos, VideoStats{
		VideoID:       videoID,
		Topic:         topic,
		CreatedAt:     time.Now().Format(time.RFC3339),
		TotalRequests: totalRequests,
		CacheHits:     cacheHits,
		NewDownloads:  newDownloads,
		TotalClips:    totalClips,
		Duration:      duration,
	})

	ss.save()
}

// GetStats returns all video stats.
func (ss *StatsStore) GetStats() []VideoStats {
	ss.mu.RLock()
	defer ss.mu.RUnlock()

	return ss.data.Videos
}

// StatsSummary holds typed aggregate statistics.
type StatsSummary struct {
	TotalVideos      int     `json:"total_videos"`
	TotalRequests    int     `json:"total_requests"`
	TotalCacheHits   int     `json:"total_cache_hits"`
	TotalNewDownloads int    `json:"total_new_downloads"`
	TotalClips       int     `json:"total_clips"`
	HitRatePct       float64 `json:"hit_rate_pct"`
	NewDownloadsPct  float64 `json:"new_downloads_pct"`
}

// GetSummary returns aggregate stats.
func (ss *StatsStore) GetSummary() StatsSummary {
	ss.mu.RLock()
	defer ss.mu.RUnlock()

	totalVideos := len(ss.data.Videos)
	totalRequests := 0
	totalCacheHits := 0
	totalNewDownloads := 0
	totalClips := 0

	for _, v := range ss.data.Videos {
		totalRequests += v.TotalRequests
		totalCacheHits += v.CacheHits
		totalNewDownloads += v.NewDownloads
		totalClips += v.TotalClips
	}

	hitRate := 0.0
	if totalRequests > 0 {
		hitRate = float64(totalCacheHits) / float64(totalRequests) * 100
	}

	newPct := 0.0
	if totalRequests > 0 {
		newPct = float64(totalNewDownloads) / float64(totalRequests) * 100
	}

	return StatsSummary{
		TotalVideos:       totalVideos,
		TotalRequests:     totalRequests,
		TotalCacheHits:    totalCacheHits,
		TotalNewDownloads: totalNewDownloads,
		TotalClips:        totalClips,
		HitRatePct:        hitRate,
		NewDownloadsPct:   newPct,
	}
}

// HandleStatus returns the current status of the Artlist pipeline.
func (h *Handler) HandleStatus(c *gin.Context) {
	stats := h.getPipelineStatus()

	c.JSON(http.StatusOK, gin.H{
		"ok":    true,
		"stats": stats,
	})
}

// HandleVideoStats returns per-video statistics.
func (h *Handler) HandleVideoStats(c *gin.Context) {
	if h.statsStore == nil {
		c.JSON(http.StatusOK, gin.H{
			"ok":     true,
			"videos": []VideoStats{},
		})
		return
	}

	videos := h.statsStore.GetStats()
	summary := h.statsStore.GetSummary()

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"videos":  videos,
		"summary": summary,
	})
}

// getPipelineStatus gathers current pipeline status.
func (h *Handler) getPipelineStatus() map[string]interface{} {
	status := map[string]interface{}{}

	// Artlist DB stats
	if h.artlistDB != nil {
		status["artlist_db"] = h.artlistDB.GetStats()
	}

	// Clip cache stats
	if h.clipCache != nil {
		status["clip_cache"] = h.clipCache.GetStats()
	}

	// Keyword pool stats
	if h.keywordPool != nil {
		status["keyword_pool"] = map[string]interface{}{
			"total_seeds": len(h.keywordPool.data.Seeds),
			"total_clusters": len(h.keywordPool.data.Clusters),
			"last_warm": h.keywordPool.data.LastWarm,
		}
	}

	// Video stats summary
	if h.statsStore != nil {
		status["video_stats_summary"] = h.statsStore.GetSummary()
	}

	return status
}

// HandlePreWarm triggers the pre-warm job manually.
func (h *Handler) HandlePreWarm(c *gin.Context) {
	if h.keywordPool == nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "keyword pool not initialized"})
		return
	}
	if h.artlistSrc == nil || h.artlistDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "artlist source or DB not initialized"})
		return
	}

	go func() {
		if err := h.RunPreWarmV2(context.Background()); err != nil {
			logger.Error("Pre-warm failed", zap.Error(err))
		}
	}()

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "Pre-warm job started in background",
	})
}

// RecordVideoStats records stats for a completed video.
func (h *Handler) RecordVideoStats(videoID, topic string, totalRequests, cacheHits, newDownloads, totalClips int, duration string) {
	if h.statsStore == nil {
		return
	}

	h.statsStore.RecordVideo(videoID, topic, totalRequests, cacheHits, newDownloads, totalClips, duration)
}

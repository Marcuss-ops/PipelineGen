package harvester

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"velox/go-master/pkg/logger"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type Handler struct {
	harvester *Harvester
}

func NewHandler(h *Harvester) *Handler {
	return &Handler{harvester: h}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/stats", h.GetStats)
	r.GET("/queries", h.GetQueries)
	r.POST("/queries", h.AddQuery)
	r.DELETE("/queries/:query", h.RemoveQuery)
	r.GET("/channels", h.GetChannels)
	r.POST("/channels", h.AddChannel)
	r.DELETE("/channels/:channel", h.RemoveChannel)
	r.GET("/blacklist", h.GetBlacklist)
	r.POST("/blacklist", h.BlacklistVideo)
	r.DELETE("/blacklist/:videoID", h.UnblacklistVideo)
	r.POST("/run", h.RunNow)
	r.GET("/results", h.GetResults)
}

func (h *Handler) GetStats(c *gin.Context) {
	stats := h.harvester.GetStats()
	c.JSON(http.StatusOK, gin.H{"ok": true, "stats": stats})
}

func (h *Handler) GetQueries(c *gin.Context) {
	queries := h.harvester.GetQueries()
	c.JSON(http.StatusOK, gin.H{"ok": true, "queries": queries})
}

func (h *Handler) AddQuery(c *gin.Context) {
	var req struct {
		Query string `json:"query"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	h.harvester.AddQuery(req.Query)
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Query added"})
}

func (h *Handler) RemoveQuery(c *gin.Context) {
	query := c.Param("query")
	h.harvester.RemoveQuery(query)
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Query removed"})
}

func (h *Handler) GetChannels(c *gin.Context) {
	channels := h.harvester.GetChannels()
	c.JSON(http.StatusOK, gin.H{"ok": true, "channels": channels})
}

func (h *Handler) AddChannel(c *gin.Context) {
	var req struct {
		Channel string `json:"channel"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	h.harvester.AddChannel(req.Channel)
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Channel added"})
}

func (h *Handler) RemoveChannel(c *gin.Context) {
	channel := c.Param("channel")
	h.harvester.RemoveChannel(channel)
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Channel removed"})
}

func (h *Handler) GetBlacklist(c *gin.Context) {
	blacklist := h.harvester.GetBlacklist()
	c.JSON(http.StatusOK, gin.H{"ok": true, "blacklist": blacklist})
}

func (h *Handler) BlacklistVideo(c *gin.Context) {
	var req struct {
		VideoID string  `json:"video_id"`
		Reason  string  `json:"reason"`
		Score   float64 `json:"score"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	h.harvester.BlacklistVideo(req.VideoID, req.Reason, req.Score)
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Video blacklisted"})
}

func (h *Handler) UnblacklistVideo(c *gin.Context) {
	videoID := c.Param("videoID")
	h.harvester.UnblacklistVideo(videoID)
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Video unblacklisted"})
}

func (h *Handler) RunNow(c *gin.Context) {
	h.harvester.RunNow(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Harvest cycle started"})
}

func (h *Handler) GetResults(c *gin.Context) {
	results := []HarvestResult{}
	timeout := time.After(5 * time.Second)

	for {
		select {
		case <-timeout:
			c.JSON(http.StatusOK, gin.H{"ok": true, "results": results})
			return
		case result, ok := <-h.harvester.GetResults():
			if !ok {
				c.JSON(http.StatusOK, gin.H{"ok": true, "results": results})
				return
			}
			results = append(results, result)
			if len(results) >= 100 {
				c.JSON(http.StatusOK, gin.H{"ok": true, "results": results})
				return
			}
		}
	}
}

type CronJob struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Query     string    `json:"query"`
	Channel   string    `json:"channel"`
	Interval  string    `json:"interval"` // "hourly", "daily", "weekly"
	LastRun   time.Time `json:"last_run"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

type CronManager struct {
	jobs       map[string]*CronJob
	harvester  *Harvester
	intervalCh chan *CronJob
	stopCh     chan struct{}
}

func NewCronManager(h *Harvester) *CronManager {
	return &CronManager{
		jobs:       make(map[string]*CronJob),
		harvester:  h,
		intervalCh: make(chan *CronJob, 10),
		stopCh:     make(chan struct{}),
	}
}

func (m *CronManager) Start(ctx context.Context) {
	go m.run(ctx)
	logger.Info("Cron manager started")
}

func (m *CronManager) Stop() {
	close(m.stopCh)
	logger.Info("Cron manager stopped")
}

func (m *CronManager) run(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.checkAndRun(ctx)
		}
	}
}

func (m *CronManager) checkAndRun(ctx context.Context) {
	now := time.Now()

	for _, job := range m.jobs {
		if !job.Enabled {
			continue
		}

		shouldRun := false
		lastRunDiff := now.Sub(job.LastRun)

		switch job.Interval {
		case "hourly":
			if lastRunDiff >= 1*time.Hour {
				shouldRun = true
			}
		case "daily":
			if lastRunDiff >= 24*time.Hour {
				shouldRun = true
			}
		case "weekly":
			if lastRunDiff >= 7*24*time.Hour {
				shouldRun = true
			}
		}

		if shouldRun {
			logger.Info("Running cron job", zap.String("name", job.Name))

			if job.Query != "" {
				m.harvester.AddQuery(job.Query)
			}
			if job.Channel != "" {
				m.harvester.AddChannel(job.Channel)
			}

			m.harvester.RunNow(ctx)

			job.LastRun = time.Now()
		}
	}
}

func (m *CronManager) AddJob(name, query, channel, interval string) *CronJob {
	job := &CronJob{
		ID:        fmt.Sprintf("job_%d", len(m.jobs)+1),
		Name:      name,
		Query:     query,
		Channel:   channel,
		Interval:  interval,
		LastRun:   time.Now().Add(-24 * time.Hour),
		Enabled:   true,
		CreatedAt: time.Now(),
	}
	m.jobs[job.ID] = job
	logger.Info("Cron job added", zap.String("name", name), zap.String("interval", interval))
	return job
}

func (m *CronManager) RemoveJob(id string) {
	delete(m.jobs, id)
	logger.Info("Cron job removed", zap.String("id", id))
}

func (m *CronManager) GetJobs() []*CronJob {
	jobs := make([]*CronJob, 0, len(m.jobs))
	for _, job := range m.jobs {
		jobs = append(jobs, job)
	}
	return jobs
}

func (m *CronManager) ToggleJob(id string, enabled bool) {
	if job, ok := m.jobs[id]; ok {
		job.Enabled = enabled
		logger.Info("Cron job toggled", zap.String("id", id), zap.Bool("enabled", enabled))
	}
}

type CronHandler struct {
	manager *CronManager
}

func NewCronHandler(m *CronManager) *CronHandler {
	return &CronHandler{manager: m}
}

func (h *CronHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/jobs", h.GetJobs)
	r.POST("/jobs", h.AddJob)
	r.DELETE("/jobs/:id", h.RemoveJob)
	r.PUT("/jobs/:id/toggle", h.ToggleJob)
}

func (h *CronHandler) GetJobs(c *gin.Context) {
	jobs := h.manager.GetJobs()
	c.JSON(http.StatusOK, gin.H{"ok": true, "jobs": jobs})
}

func (h *CronHandler) AddJob(c *gin.Context) {
	var req struct {
		Name     string `json:"name"`
		Query    string `json:"query"`
		Channel  string `json:"channel"`
		Interval string `json:"interval"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	job := h.manager.AddJob(req.Name, req.Query, req.Channel, req.Interval)
	c.JSON(http.StatusOK, gin.H{"ok": true, "job": job})
}

func (h *CronHandler) RemoveJob(c *gin.Context) {
	id := c.Param("id")
	h.manager.RemoveJob(id)
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Job removed"})
}

func (h *CronHandler) ToggleJob(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	h.manager.ToggleJob(id, req.Enabled)
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Job toggled"})
}

type CronJobConfig struct {
	Name       string `json:"name"`
	Query      string `json:"query"`
	Channel    string `json:"channel"`
	Interval   string `json:"interval"` // hourly, daily, weekly
	MaxResults int    `json:"max_results"`
	MinViews   int64  `json:"min_views"`
	Timeframe  string `json:"timeframe"`
}

func LoadCronJobsFromConfig(configPath string) ([]CronJobConfig, error) {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return []CronJobConfig{}, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var configs []CronJobConfig
	if err := json.Unmarshal(data, &configs); err != nil {
		return nil, err
	}

	return configs, nil
}

func SaveCronJobsToConfig(configs []CronJobConfig, configPath string) error {
	data, err := json.MarshalIndent(configs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0644)
}

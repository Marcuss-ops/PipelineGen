package main

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func handleDashboardPage(apiClient *APIClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		summary := &SummaryResponse{
			BySource:    map[string]int64{},
			ByMediaType: map[string]int64{},
		}

		if diag, err := apiClient.GetIndexHealth(ctx); err == nil && diag.AssetStats != nil {
			if total, ok := diag.AssetStats["total"].(float64); ok {
				summary.TotalAssets = int64(total)
			}
			if indexed, ok := diag.AssetStats["indexed"].(float64); ok {
				summary.Indexed = int64(indexed)
			}
			if local, ok := diag.AssetStats["local"].(float64); ok {
				summary.LocalCount = int64(local)
			}
			if drive, ok := diag.AssetStats["drive"].(float64); ok {
				summary.DriveCount = int64(drive)
			}
		}

		if stats, err := apiClient.GetJobStats(ctx); err == nil && stats != nil {
			for k, v := range stats.Stats {
				switch k {
				case "running":
					summary.JobsRunning = v
				case "failed":
					summary.JobsFailed = v
				case "succeeded":
					summary.JobsCompleted = v
				}
			}
		}

		if ob, err := apiClient.GetOutboxStatus(ctx); err == nil && ob.Counts != nil {
			summary.OutboxPending = ob.Counts["pending"]
			summary.OutboxFailed = ob.Counts["dead_letter"]
			summary.OutboxRetry = ob.Counts["processing"]
		}

		if assets, err := apiClient.ListAssets(ctx, AssetFilter{Limit: 10}); err == nil {
			summary.LatestAssets = assets.Assets
		}

		if jobs, err := apiClient.ListJobs(ctx, JobFilter{Status: "failed", Limit: 5}); err == nil {
			summary.LatestErrors = jobs.Jobs
		}

		if summary.TotalAssets == 0 {
			if assets, err := apiClient.ListAssets(ctx, AssetFilter{Limit: 200}); err == nil {
				summary.TotalAssets = int64(assets.Count)
				for _, a := range assets.Assets {
					summary.BySource[a.Source]++
					summary.ByMediaType[a.MediaType]++
				}
			}
		}

		c.HTML(http.StatusOK, "layout", gin.H{
			"Title":     "PipelineGen Operator Console",
			"NavActive": "dashboard",
			"Summary":   summary,
		})
	}
}

func handleAssetsPage(apiClient *APIClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		filter := AssetFilter{
			Source:         c.Query("source"),
			MediaType:      c.Query("media_type"),
			LifecycleState: c.Query("lifecycle_state"),
			Category:       c.Query("category"),
			Q:              c.Query("q"),
			Cursor:         c.Query("cursor"),
		}
		limit := 50
		if l := c.Query("limit"); l != "" {
			if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
				limit = parsed
			}
		}
		filter.Limit = limit

		result, err := apiClient.ListAssets(ctx, filter)
		assets := []AssetSummary{}
		hasMore := false
		nextCursor := ""
		if err == nil {
			assets = result.Assets
			hasMore = result.HasMore
			nextCursor = result.NextCursor
		}

		c.HTML(http.StatusOK, "layout", gin.H{
			"Title":      "Media Explorer",
			"NavActive":  "assets",
			"Assets":     assets,
			"Filter":     filter,
			"HasMore":    hasMore,
			"NextCursor": nextCursor,
		})
	}
}

func handleSoundEffectsPage(apiClient *APIClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		filter := AssetFilter{
			MediaType: "sound_effect",
			Q:         c.Query("q"),
			Category:  c.Query("category"),
			Limit:     100,
		}

		result, err := apiClient.ListAssets(ctx, filter)
		assets := []AssetSummary{}
		if err == nil {
			assets = result.Assets
		}

		c.HTML(http.StatusOK, "layout", gin.H{
			"Title":     "Sound Effects",
			"NavActive": "sound-effects",
			"Assets":    assets,
			"Filter":    filter,
		})
	}
}

func handleJobsPage(apiClient *APIClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		filter := JobFilter{
			Status: c.Query("status"),
			Type:   c.Query("type"),
			Limit:  50,
		}

		result, err := apiClient.ListJobs(ctx, filter)
		jobs := []JobSummary{}
		if err == nil {
			jobs = result.Jobs
		}

		stats, _ := apiClient.GetJobStats(ctx)

		c.HTML(http.StatusOK, "layout", gin.H{
			"Title":     "Jobs",
			"NavActive": "jobs",
			"Jobs":      jobs,
			"Filter":    filter,
			"Stats":     stats,
		})
	}
}

func handleOutboxPage(apiClient *APIClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		status, _ := apiClient.GetOutboxStatus(ctx)
		eventsResp, _ := apiClient.GetOutboxEvents(ctx)

		events := []OutboxEvent{}
		if eventsResp != nil {
			events = eventsResp.Events
		}

		if status == nil {
			status = &OutboxStatusResponse{Counts: map[string]int64{}}
		}

		c.HTML(http.StatusOK, "layout", gin.H{
			"Title":     "Outbox",
			"NavActive": "outbox",
			"Status":    status,
			"Events":    events,
		})
	}
}

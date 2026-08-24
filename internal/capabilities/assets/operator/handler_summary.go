// Package operator — handler_summary.go (RESOURCE: SUMMARY, July 2026
// split by resource).
//
// Split rationale (resource/handler), see handler.go header.
//
// This file owns the SUMMARY resource (dashboard aggregation). 1 route:
//
//   - GET /summary → handleSummary
//
// registers via the private registerSummaryRoutes method, called from
// handler.go::RegisterRoutes. The summary resource is the only
// "umbrella" route (it reads across assetService + jobService +
// jobStats + outboxPort to build the dashboard payload).
//
// Cross-resource sharing: jobsToJSON lives here (used only by summary).
// summariesToJSON, isAllowedPath, maskPath live in handler_assets.go.
package assets

import (
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// registerSummaryRoutes mounts the dashboard summary endpoint on the
// shared /api/assets/operator/* prefix. The path "/summary" is RELATIVE
// to the parent router group (the registry_operator_console.go caller
// owns the "/api/assets/operator" prefix).
func (h *Handler) registerSummaryRoutes(rg *gin.RouterGroup) {
	rg.GET("/summary", h.handleSummary)
}

// handleSummary returns aggregated dashboard data.
func (h *Handler) handleSummary(c *gin.Context) {
	ctx := c.Request.Context()

	summary := gin.H{
		"ok":            true,
		"total_assets":  int64(0),
		"by_source":     map[string]int64{},
		"by_media_type": map[string]int64{},
		"indexed":       int64(0),
		"non_indexed":   int64(0),
		"local_count":   int64(0),
		"drive_count":   int64(0),
	}

	// Count total assets
	sources := []string{"", "artlist", "youtube_clip", "stock", "image", "generated", "sound_effect", "ai_generated"}
	mediaTypes := []string{"", "stock", "clip", "image", "audio", "document", "image_video", "sound_effect", "script"}

	bySource := map[string]int64{}
	byMediaType := map[string]int64{}
	var total int64

	for _, src := range sources {
		filter := asset.Filter{Limit: 1}
		if src != "" {
			filter.Source = src
		}
		count, err := h.assetService.Repository().Count(ctx, filter)
		if err != nil {
			h.log.Warn("failed to count assets by source", zap.String("source", src), zap.Error(err))
			continue
		}
		if src == "" && count == 0 {
			continue
		}
		if src != "" {
			bySource[src] = count
			total += count
		}
	}

	for _, mt := range mediaTypes {
		filter := asset.Filter{Limit: 1}
		if mt != "" {
			filter.MediaType = mt
		}
		count, err := h.assetService.Repository().Count(ctx, filter)
		if err != nil {
			h.log.Warn("failed to count assets by media type", zap.String("media_type", mt), zap.Error(err))
			continue
		}
		if mt != "" {
			byMediaType[mt] = count
		}
	}

	// If total is 0 but we have per-source counts, sum them
	if total == 0 {
		for _, v := range bySource {
			total += v
		}
	}

	summary["total_assets"] = total
	summary["by_source"] = bySource
	summary["by_media_type"] = byMediaType

	// Latest 10 assets
	latest, err := h.assetService.List(ctx, asset.Filter{Limit: 10})
	if err == nil {
		summary["latest_assets"] = h.summariesToJSON(latest)
	}

	// Job stats
	if h.jobStats != nil {
		stats, err := h.jobStats.GetStats(ctx)
		if err == nil && stats != nil {
			summary["jobs_running"] = stats.ByStatus["running"]
			summary["jobs_failed"] = stats.ByStatus["failed"]
			summary["jobs_completed"] = stats.ByStatus["succeeded"]
		}
	}

	// Latest failed jobs
	if h.jobService != nil {
		failedStatus := job.StatusFailed
		failedJobs, err := h.jobService.List(ctx, job.Filter{Status: &failedStatus, Limit: 5})
		if err == nil {
			summary["latest_errors"] = h.jobsToJSON(failedJobs)
		}
	}

	// Outbox stats
	if h.outboxPort != nil {
		for _, status := range []string{"pending", "processing", "dead_letter"} {
			count, err := h.outboxPort.CountByStatus(ctx, status)
			if err != nil {
				continue
			}
			switch status {
			case "pending":
				summary["outbox_pending"] = count
			case "dead_letter":
				summary["outbox_failed"] = count
			}
		}
	}

	apiutil.OK(c, summary)
}

// jobsToJSON converts domain Job values to JSON-friendly maps. Used only
// by handleSummary (the failed-jobs listing inside the dashboard payload).
//
// Lives on the *Handler receiver for naming-convention symmetry with
// summariesToJSON (in handler_assets.go). Does NOT mutate h.
func (h *Handler) jobsToJSON(js []job.Job) []gin.H {
	result := make([]gin.H, 0, len(js))
	for _, j := range js {
		result = append(result, gin.H{
			"id":         j.ID,
			"type":       j.Type,
			"status":     string(j.Status),
			"progress":   j.Progress,
			"error":      j.Error,
			"created_at": j.CreatedAt,
			"updated_at": j.UpdatedAt,
		})
	}
	return result
}

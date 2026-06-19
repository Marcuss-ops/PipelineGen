package channels

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

// BulkUpsertRequest is the JSON body for bulk creating/updating channels.
type BulkUpsertRequest struct {
	Channels []BulkChannelRequest `json:"channels" binding:"required,min=1"`
}

// BulkChannelRequest is a single channel in a bulk upsert.
type BulkChannelRequest struct {
	ID               string `json:"id" binding:"required"`
	Category         string `json:"category" binding:"required"`
	ChannelURL       string `json:"channel_url" binding:"required"`
	ChannelName      string `json:"channel_name,omitempty"`
	Keywords         string `json:"keywords,omitempty"`
	MinViews         int    `json:"min_views,omitempty"`
	MaxClipDuration  int    `json:"max_clip_duration,omitempty"`
	DriveFolderID    string `json:"drive_folder_id,omitempty"`
	SemanticKeywords string `json:"semantic_keywords,omitempty"`
	MinSemanticScore int    `json:"min_semantic_score,omitempty"`
	PlaylistEnd      int    `json:"playlist_end,omitempty"`
	CheckInterval    string `json:"check_interval,omitempty"`
	MaxVideosPerRun  int    `json:"max_videos_per_run,omitempty"`
	Priority         int    `json:"priority,omitempty"`
	LookbackDays     int    `json:"lookback_days,omitempty"`
	MaxSegments      int    `json:"max_segments,omitempty"`
	SegmentPrompt    string `json:"segment_prompt,omitempty"`
}

// BulkUpsertResponse reports the result of a bulk upsert.
type BulkUpsertResponse struct {
	Created []string `json:"created"`
	Updated []string `json:"updated"`
	Errors  []string `json:"errors"`
}

// BulkUpsert creates or updates multiple channels in a single request.
// New channels are inserted; existing channels (by ID) are updated.
func (h *ChannelsHandler) BulkUpsert(c *gin.Context) {
	var req BulkUpsertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, err.Error())
		return
	}

	ctx := c.Request.Context()
	var created, updated, errs []string

	for _, ch := range req.Channels {
		// Set defaults
		if ch.MaxClipDuration == 0 {
			ch.MaxClipDuration = 60
		}
		if ch.Priority == 0 {
			ch.Priority = 2
		}
		if ch.MaxSegments == 0 {
			ch.MaxSegments = 2
		}
		if ch.MaxVideosPerRun == 0 {
			ch.MaxVideosPerRun = 3
		}
		if ch.CheckInterval == "" {
			ch.CheckInterval = "7d"
		}

		model := &media.CategoryChannel{
			ID:               ch.ID,
			Category:         ch.Category,
			ChannelURL:       ch.ChannelURL,
			ChannelName:      ch.ChannelName,
			Keywords:         ch.Keywords,
			MinViews:         ch.MinViews,
			MaxClipDuration:  ch.MaxClipDuration,
			DriveFolderID:    ch.DriveFolderID,
			SemanticKeywords: ch.SemanticKeywords,
			MinSemanticScore: ch.MinSemanticScore,
			PlaylistEnd:      ch.PlaylistEnd,
			CheckInterval:    ch.CheckInterval,
			MaxVideosPerRun:  ch.MaxVideosPerRun,
			Priority:         ch.Priority,
			LookbackDays:     ch.LookbackDays,
			MaxSegments:      ch.MaxSegments,
			SegmentPrompt:    ch.SegmentPrompt,
		}

		// Check if exists
		existing, _ := h.repo.GetByID(ctx, ch.ID)
		isUpdate := existing != nil

		if err := h.repo.Upsert(ctx, model); err != nil {
			h.log.Error("failed to upsert channel", zap.String("id", ch.ID), zap.Error(err))
			errs = append(errs, ch.ID+": "+err.Error())
			continue
		}

		if isUpdate {
			updated = append(updated, ch.ID)
		} else {
			created = append(created, ch.ID)
		}
	}

	c.JSON(http.StatusOK, BulkUpsertResponse{
		Created: created,
		Updated: updated,
		Errors:  errs,
	})
}

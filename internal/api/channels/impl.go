// Package channels provides HTTP handlers for managing category↔channel associations.
package channels

import (
	"net/http"

	"github.com/Marcuss-ops/PipelineGen/internal/api"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	sqlite "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite"
)

// ChannelsHandler handles CRUD operations for category_channels (channel subscriptions per Drive folder).
type ChannelsHandler struct {
	repo *sqlite.ChannelsRepository
	log  *zap.Logger
}

// NewHandler creates a new channels API handler.
func NewChannelsHandler(repo *sqlite.ChannelsRepository, log *zap.Logger) *ChannelsHandler {
	return &ChannelsHandler{repo: repo, log: log}
}

// RegisterRoutes registers the channels CRUD routes under the given router group.
func (h *ChannelsHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("", h.ListAll)
	r.GET("/categories", h.ListCategories)
	r.GET("/:id", h.GetByID)
	r.POST("", h.Upsert)
	r.POST("/bulk-upsert", h.BulkUpsert)
	r.DELETE("/:id", h.Delete)
}

// ListAll returns all category↔channel associations.
func (h *ChannelsHandler) ListAll(c *gin.Context) {
	channels, err := h.repo.ListAll(c.Request.Context())
	if err != nil {
		h.log.Error("failed to list channels", zap.Error(err))
		api.InternalError(c, err)
		return
	}
	api.OK(c, gin.H{"channels": channels})
}

// ListCategories returns all distinct categories that have channels assigned.
func (h *ChannelsHandler) ListCategories(c *gin.Context) {
	categories, err := h.repo.ListCategories(c.Request.Context())
	if err != nil {
		h.log.Error("failed to list categories", zap.Error(err))
		api.InternalError(c, err)
		return
	}
	api.OK(c, gin.H{"categories": categories})
}

// GetByID returns a single channel association by ID.
func (h *ChannelsHandler) GetByID(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		api.BadRequest(c, "id parameter is required")
		return
	}

	ch, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		h.log.Error("failed to get channel", zap.String("id", id), zap.Error(err))
		api.NotFound(c, "channel not found")
		return
	}
	api.OK(c, ch)
}

// UpsertRequest is the JSON body for creating or updating a channel association.
type UpsertRequest struct {
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
}

// Upsert creates or updates a category↔channel association.
func (h *ChannelsHandler) Upsert(c *gin.Context) {
	var req UpsertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, err.Error())
		return
	}

	ch := &media.CategoryChannel{
		ID:               req.ID,
		Category:         req.Category,
		ChannelURL:       req.ChannelURL,
		ChannelName:      req.ChannelName,
		Keywords:         req.Keywords,
		MinViews:         req.MinViews,
		MaxClipDuration:  req.MaxClipDuration,
		DriveFolderID:    req.DriveFolderID,
		SemanticKeywords: req.SemanticKeywords,
		MinSemanticScore: req.MinSemanticScore,
		PlaylistEnd:      req.PlaylistEnd,
	}

	if err := h.repo.Upsert(c.Request.Context(), ch); err != nil {
		h.log.Error("failed to upsert channel", zap.Error(err))
		api.InternalError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"channel": ch,
	})
}

// Delete removes a channel association by ID.
func (h *ChannelsHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		api.BadRequest(c, "id parameter is required")
		return
	}

	// Check if channel exists before deleting
	existing, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		api.NotFound(c, "channel not found")
		return
	}

	if err := h.repo.Delete(c.Request.Context(), id); err != nil {
		h.log.Error("failed to delete channel", zap.String("id", id), zap.Error(err))
		api.InternalError(c, err)
		return
	}

	api.OK(c, gin.H{
		"ok":              true,
		"deleted_channel": existing,
	})
}

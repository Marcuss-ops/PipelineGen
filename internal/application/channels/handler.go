// Package channels — handler.go: thin HTTP transport for the channels
// capability. Capability Standard rule:
//
//   "Handlers may bind input, validate transport syntax, translate
//    to a command/query, invoke one use case, map typed errors,
//    and serialize output."
//
// i.e. no SQL, no asset.CategoryChannel construction, no default-policy
// logic — those all live in Service (service.go) and the SQLite
// adapter (adapters.go).
//
// This file previously lived at internal/api/channels/{impl,bulk}.go;
// it moved to the application package as part of the Capability
// Standard migration so the application layer owns its full
// vertical and the import direction becomes one-way
// (registry → channels.Build → channels.Handler → channels.Service).
// The legacy internal/api/channels/ package was deleted in the same
// PR.
package channels

import (
	"encoding/json"
	"net/http"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Handler is the thin HTTP transport for the channels capability.
type Handler struct {
	svc *Service
	log *zap.Logger
}

// NewHandler creates a new channels HTTP handler. Used by Build
// (composition root path) and direct tests.
func NewHandler(svc *Service, log *zap.Logger) *Handler {
	if svc == nil {
		panic("channels.NewHandler: Service is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes registers the channels CRUD routes under the given router group.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("", h.ListAll)
	r.GET("/categories", h.ListCategories)
	r.GET("/:id", h.GetByID)
	r.POST("", h.Upsert)
	r.POST("/bulk-upsert", h.BulkUpsert)
	r.DELETE("/:id", h.Delete)
}

// ListAll returns all category↔channel associations.
func (h *Handler) ListAll(c *gin.Context) {
	out, err := h.svc.ListAll(c.Request.Context())
	if err != nil {
		h.log.Error("failed to list channels", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"channels": out.Channels})
}

// ListCategories returns all distinct categories that have channels assigned.
func (h *Handler) ListCategories(c *gin.Context) {
	out, err := h.svc.ListCategories(c.Request.Context())
	if err != nil {
		h.log.Error("failed to list categories", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"categories": out.Categories})
}

// GetByID returns a single channel association by ID.
func (h *Handler) GetByID(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		apiutil.BadRequest(c, "id parameter is required")
		return
	}
	out, err := h.svc.GetByID(c.Request.Context(), id)
	if err != nil {
		h.log.Error("failed to get channel", zap.String("id", id), zap.Error(err))
		apiutil.NotFound(c, "channel not found")
		return
	}
	apiutil.OK(c, out)
}

// UpsertRequest is the JSON body for creating or updating a channel
// association. Transport-side mirror of the persistence model — the
// handler translates this to channels.UpsertChannelCommand before
// calling Service.Upsert. Keep this struct's JSON shape byte-for-byte
// compatible with the previous asset.CategoryChannel output.
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

// Upsert creates or updates a channel association. The handler
// validates transport syntax then translates to UpsertChannelCommand;
// Service applies default policy, derives an ID if missing, and
// returns the persisted Channel as the body.
func (h *Handler) Upsert(c *gin.Context) {
	var req UpsertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}
	keywords := splitJSONArray(req.Keywords)
	semanticKeywords := splitJSONArray(req.SemanticKeywords)
	out, err := h.svc.Upsert(c.Request.Context(), UpsertChannelCommand{
		ID:               req.ID,
		Category:         req.Category,
		ChannelURL:       req.ChannelURL,
		ChannelName:      req.ChannelName,
		Keywords:         keywords,
		MinViews:         req.MinViews,
		MaxClipDuration:  req.MaxClipDuration,
		DriveFolderID:    req.DriveFolderID,
		SemanticKeywords: semanticKeywords,
		MinSemanticScore: req.MinSemanticScore,
		PlaylistEnd:      req.PlaylistEnd,
	})
	if err != nil {
		h.log.Error("failed to upsert channel", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"channel": out,
	})
}

// Delete removes a channel association by ID. The handler looks up
// the channel before delete (via Service.Delete which returns the
// pre-delete state) so the response body can echo back the row
// without re-querying.
func (h *Handler) Delete(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		apiutil.BadRequest(c, "id parameter is required")
		return
	}
	res, err := h.svc.Delete(c.Request.Context(), id)
	if err != nil {
		h.log.Error("failed to delete channel", zap.String("id", id), zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":              true,
		"deleted_channel": res.Deleted,
	})
}

// ── Bulk upsert ────────────────────────────────────────────────

// BulkUpsertRequest is the JSON body for bulk creating/updating channels.
type BulkUpsertRequest struct {
	Channels []BulkChannelRequest `json:"channels" binding:"required,min=1"`
}

// BulkChannelRequest is a single channel in a bulk upsert. Mirrors
// the persistence model field-for-field; the handler converts to
// UpsertChannelCommand below.
type BulkChannelRequest struct {
	ID               string `json:"id"`
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

// BulkUpsert creates or updates multiple channels in a single request.
// New channels are inserted; existing channels (by ID) are updated.
// Service applies Default policy per row.
func (h *Handler) BulkUpsert(c *gin.Context) {
	var req BulkUpsertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	cmds := make([]UpsertChannelCommand, 0, len(req.Channels))
	for _, ch := range req.Channels {
		cmds = append(cmds, UpsertChannelCommand{
			ID:               ch.ID,
			Category:         ch.Category,
			ChannelURL:       ch.ChannelURL,
			ChannelName:      ch.ChannelName,
			Keywords:         splitJSONArray(ch.Keywords),
			MinViews:         ch.MinViews,
			MaxClipDuration:  ch.MaxClipDuration,
			DriveFolderID:    ch.DriveFolderID,
			SemanticKeywords: splitJSONArray(ch.SemanticKeywords),
			MinSemanticScore: ch.MinSemanticScore,
			PlaylistEnd:      ch.PlaylistEnd,
			CheckInterval:    ch.CheckInterval,
			MaxVideosPerRun:  ch.MaxVideosPerRun,
			Priority:         ch.Priority,
			LookbackDays:     ch.LookbackDays,
			MaxSegments:      ch.MaxSegments,
			SegmentPrompt:    ch.SegmentPrompt,
		})
	}

	res, err := h.svc.UpsertBulk(c.Request.Context(), BulkUpsertChannelsCommand{
		Channels: cmds,
	})
	if err != nil {
		h.log.Error("bulk upsert failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"created": res.Created,
		"updated": res.Updated,
		"errors":  res.Errors,
	})
}

// ── Internal helper ────────────────────────────────────────────

// splitJSONArray decodes a JSON-encoded string array (e.g. "[\"a\",\"b\"]")
// into a []string. Returns an empty slice when raw is empty or
// malformed; the channel domain never needs anything beyond "valid
// array or empty". The handler uses this to translate the wire shape
// (string) into the typed []string the Service command expects.
func splitJSONArray(raw string) []string {
	if raw == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []string{}
	}
	if out == nil {
		return []string{}
	}
	return out
}

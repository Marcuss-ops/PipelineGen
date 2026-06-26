// Package voiceover provides thin HTTP handlers for voiceover operations.
package voiceover

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	voiceoversync "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/sync"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// Handler is the unified handler for all voiceover operations:
//   - /generate: Generate a single voiceover (sync or async)
//   - /batch: Generate multiple voiceovers (always async via job queue)
//   - /sync: Sync voiceovers from Google Drive
//   - /generate-with-group: Generate a single voiceover routed via voiceover_group topic
//     (reads the topic→folder_id mapping from asset_tree_nodes via GroupsResolver).
//   - /groups (GET): List the canonical topic→folder_id mapping for a given parent
//     (defaults to the configured voiceover root).
type Handler struct {
	service              *voiceover.Service
	syncService          *voiceoversync.Service
	jobsSvc              jobservice.Service
	groupsResolver       *voiceover.GroupsResolver // optional; nil-safe — falls back to no-routing
	defaultVoiceoverRoot string                    // folder ID under which groups live
	log                  *zap.Logger
}

// NewHandler builds the handler. groupsResolver is REQUIRED to keep the
// API surface consistent (nil resolver would mean DB-backed routing is dead code).
// Pass nil only if you also accept that GET /groups + voiceover_group routing
// will return 501 Not Implemented.
func NewHandler(
	service *voiceover.Service,
	syncService *voiceoversync.Service,
	jobsSvc jobservice.Service,
	groupsResolver *voiceover.GroupsResolver,
	defaultVoiceoverRoot string,
	log *zap.Logger,
) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{
		service:              service,
		syncService:          syncService,
		jobsSvc:              jobsSvc,
		groupsResolver:       groupsResolver,
		defaultVoiceoverRoot: strings.TrimSpace(defaultVoiceoverRoot),
		log:                  log,
	}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/generate", h.Generate)
	r.POST("/generate-with-group", h.GenerateWithGroup)
	r.POST("/batch", h.Batch)
	r.POST("/promo", h.Promo)
	r.POST("/sync", h.Sync)
	r.GET("/groups", h.ListGroups)
}

// Generate processes a single voiceover request (sync or async)
func (h *Handler) Generate(c *gin.Context) {
	if h.service == nil {
		apiutil.BadRequest(c, "voiceover service not initialized")
		return
	}

	var req struct {
		Text     string `json:"text" binding:"required"`
		Language string `json:"language"`
		Filename string `json:"filename"`
		Async    bool   `json:"async"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	if req.Language == "" {
		req.Language = "it"
	}

	// If async is requested, enqueue as a batch job with 1 item
	if req.Async && h.jobsSvc != nil {
		h.log.Info("enqueuing voiceover generation (async)",
			zap.String("language", req.Language),
			zap.Bool("async", req.Async))

		batchReq := voiceover.BatchRequest{
			Text:      req.Text,
			Languages: []string{req.Language},
		}
		if req.Filename != "" {
			batchReq.FilenameTemplate = req.Filename
		}

		job, err := h.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
			Type:    "voiceover.batch",
			Payload: batchReq.PayloadMap(),
		})
		if err != nil {
			apiutil.InternalError(c, err)
			return
		}

		apiutil.OK(c, gin.H{
			"job_id":  job.ID,
			"message": "Voiceover generation enqueued",
		})
		return
	}

	// Default to sync processing
	if req.Filename == "" {
		req.Filename = "manual vo " + strings.ReplaceAll(req.Language, "-", " ") + ".mp3"
	}

	h.log.Info("generating voiceover (sync)",
		zap.String("language", req.Language),
		zap.String("filename", req.Filename))

	result, err := h.service.Generate(c.Request.Context(), req.Text, req.Language, req.Filename)
	if err != nil {
		h.log.Error("voiceover generation failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	h.log.Info("voiceover generated successfully", zap.String("path", result.Path))
	apiutil.OK(c, gin.H{"result": result})
}

// Batch processes multiple voiceover requests (always async)
func (h *Handler) Batch(c *gin.Context) {
	if h.service == nil {
		apiutil.BadRequest(c, "voiceover service not initialized")
		return
	}

	req, ok := apiutil.BindJSON[voiceover.BatchRequest](c)
	if !ok {
		return
	}

	h.log.Info("enqueuing voiceover batch",
		zap.Int("languages", len(req.Languages)),
		zap.Strings("languages", req.Languages))

	if h.jobsSvc != nil {
		job, err := h.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
			Type:    "voiceover.batch",
			Payload: req.PayloadMap(),
		})
		if err != nil {
			apiutil.InternalError(c, err)
			return
		}

		apiutil.OK(c, gin.H{
			"job_id":  job.ID,
			"message": "Voiceover batch enqueued",
		})
		return
	}

	// Fallback to sync if jobs service not available
	h.log.Info("jobs service unavailable, falling back to sync batch processing")

	resp, err := h.service.GenerateBatch(c.Request.Context(), &req)
	if err != nil {
		h.log.Error("voiceover batch generation failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, resp)
}

// Promo generates promotional voiceovers in multiple languages.
// Translates the source text via Ollama, then generates a voiceover per language.
// Runs async via goroutine — returns immediately with translation preview.
func (h *Handler) Promo(c *gin.Context) {
	if h.service == nil {
		apiutil.BadRequest(c, "voiceover service not initialized")
		return
	}

	var req voiceover.PromoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	// Default drive folder if not provided
	if req.DriveFolderID == "" {
		req.DriveFolderID = h.service.Cfg().Drive.VoiceoverFolder()
	}

	ctx := c.Request.Context()

	if req.DryRun {
		resp, err := h.service.GeneratePromo(ctx, &req)
		if err != nil {
			apiutil.InternalError(c, err)
			return
		}
		apiutil.OK(c, resp)
		return
	}

	// Async: fire-and-forget goroutine, return immediately
	concurrent.SafeGo("promo-voiceover", func() {
		promoCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
		defer cancel()
		resp, err := h.service.GeneratePromo(promoCtx, &req)
		if err != nil {
			h.log.Error("promo voiceover generation failed", zap.Error(err))
			return
		}
		h.log.Info("promo voiceover generation complete",
			zap.Int("success", resp.Success),
			zap.Int("failed", resp.Failed),
			zap.Int("total", resp.Total))
	})

	langCount := len(req.Languages)
	if langCount == 0 {
		langCount = len(voiceover.DefaultPromoLanguages())
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"action":  "promo_started",
		"message": fmt.Sprintf("Translating to %d languages and generating voiceovers (async)", langCount),
	})
}

// ListGroups lists the canonical topic→folder_id mapping under the voiceover root.
func (h *Handler) ListGroups(c *gin.Context) {
	if h.groupsResolver == nil {
		apiutil.InternalError(c, fmt.Errorf("groups resolver not configured"))
		return
	}

	parentID := strings.TrimSpace(c.Query("parent_id"))
	if parentID == "" {
		parentID = h.defaultVoiceoverRoot
	}
	if parentID == "" {
		apiutil.BadRequest(c, "parent_id is required (or configure a default voiceover root on the handler)")
		return
	}

	entries, err := h.groupsResolver.ListGroups(c.Request.Context(), parentID)
	if err != nil {
		h.log.Error("list groups failed", zap.String("parent_id", parentID), zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":        true,
		"parent_id": parentID,
		"count":     len(entries),
		"groups":    entries,
	})
}

// GenerateWithGroup accepts a voiceover_group string (the topic name) and resolves
// it to a Drive folder ID via asset_tree_nodes before delegating to the regular
// /generate flow.
func (h *Handler) GenerateWithGroup(c *gin.Context) {
	if h.groupsResolver == nil {
		apiutil.InternalError(c, fmt.Errorf("groups resolver not configured"))
		return
	}
	if h.service == nil {
		apiutil.BadRequest(c, "voiceover service not initialized")
		return
	}

	var req struct {
		Text           string `json:"text" binding:"required"`
		Language       string `json:"language"`
		Filename       string `json:"filename"`
		Async          bool   `json:"async"`
		VoiceoverGroup string `json:"voiceover_group" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	if req.Language == "" {
		req.Language = "it"
	}

	parentID := h.defaultVoiceoverRoot
	if parentID == "" {
		apiutil.BadRequest(c, "no default voiceover root configured on the handler")
		return
	}

	group, err := h.groupsResolver.ResolveByName(c.Request.Context(), parentID, req.VoiceoverGroup)
	if err != nil {
		if errors.Is(err, voiceover.ErrGroupNotFound) {
			available, _ := h.groupsResolver.ListGroups(c.Request.Context(), parentID)
			names := make([]string, 0, len(available))
			for _, e := range available {
				names = append(names, e.Name)
			}
			apiutil.Error(c, http.StatusNotFound, fmt.Sprintf("voiceover_group %q not found under root %s; available: [%s]",
				req.VoiceoverGroup, parentID, strings.Join(names, ", ")))
			return
		}
		apiutil.InternalError(c, err)
		return
	}

	h.log.Info("routing voiceover via groups_resolver",
		zap.String("voiceover_group", req.VoiceoverGroup),
		zap.String("folder_id", group.FolderID),
		zap.String("parent_id", parentID))

	req.Filename = strings.TrimSpace(req.Filename)
	if req.Filename == "" {
		req.Filename = group.Name + " " + strings.ReplaceAll(req.Language, "-", " ") + ".mp3"
	}

	if req.Async && h.jobsSvc != nil {
		batchReq := voiceover.BatchRequest{
			Text:      req.Text,
			Languages: []string{req.Language},
		}
		if req.Filename != "" {
			batchReq.FilenameTemplate = req.Filename
		}
		payload := batchReq.PayloadMap()
		payload["folder_id"] = group.FolderID
		payload["voiceover_group"] = req.VoiceoverGroup

		job, jobErr := h.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
			Type:    "voiceover.batch",
			Payload: payload,
		})
		if jobErr != nil {
			apiutil.InternalError(c, jobErr)
			return
		}
		apiutil.OK(c, gin.H{
			"ok":              true,
			"job_id":          job.ID,
			"voiceover_group": req.VoiceoverGroup,
			"folder_id":       group.FolderID,
			"message":         "Voiceover generation enqueued",
		})
		return
	}

	result, genErr := h.service.GenerateWithDestination(c.Request.Context(), req.Text, req.Language, req.Filename, &voiceover.DestinationRequest{
		FolderID:      group.FolderID,
		SubfolderName: "",
	})
	if genErr != nil {
		h.log.Error("voiceover generation failed (generate-with-group)", zap.Error(genErr))
		apiutil.InternalError(c, genErr)
		return
	}
	apiutil.OK(c, gin.H{
		"ok":              true,
		"voiceover_group": req.VoiceoverGroup,
		"folder_id":       group.FolderID,
		"result":          result,
	})
}

// Sync triggers synchronization of voiceovers from Google Drive.
func (h *Handler) Sync(c *gin.Context) {
	if h.syncService == nil {
		apiutil.InternalError(c, fmt.Errorf("voiceover sync service not configured"))
		return
	}

	h.log.Info("starting voiceover sync")

	summary, err := h.syncService.Sync(c.Request.Context())
	if err != nil {
		h.log.Error("voiceover sync failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, summary)
}

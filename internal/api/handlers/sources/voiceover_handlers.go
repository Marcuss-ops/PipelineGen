package sources

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/media/voiceover"
	voiceoversync "velox/go-master/internal/media/voiceoversync"
	"velox/go-master/internal/pkg/apiutil"
)

// VoiceoverHandler is the unified handler for all voiceover operations:
// - /generate: Generate a single voiceover (sync or async)
// - /batch: Generate multiple voiceovers (always async via job queue)
// - /sync: Sync voiceovers from Google Drive
type VoiceoverHandler struct {
	service     *voiceover.Service
	syncService *voiceoversync.Service
	jobsSvc     *jobservice.Service
	log         *zap.Logger
}

func NewVoiceoverHandler(service *voiceover.Service, syncService *voiceoversync.Service, jobsSvc *jobservice.Service, log *zap.Logger) *VoiceoverHandler {
	return &VoiceoverHandler{
		service:     service,
		syncService: syncService,
		jobsSvc:     jobsSvc,
		log:         log,
	}
}

func (h *VoiceoverHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/generate", h.Generate)
	r.POST("/batch", h.Batch)
	r.POST("/sync", h.Sync)
}

// Generate processes a single voiceover request (sync or async)
func (h *VoiceoverHandler) Generate(c *gin.Context) {
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
			Type:    models.JobTypeVoiceoverBatch,
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
func (h *VoiceoverHandler) Batch(c *gin.Context) {
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
			Type:    models.JobTypeVoiceoverBatch,
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

// Sync triggers synchronization of voiceovers from Google Drive.
func (h *VoiceoverHandler) Sync(c *gin.Context) {
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

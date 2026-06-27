package stock

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api/common"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// stockPayloadToMap converts a StockRunPayload to map[string]any for job enqueue.
func stockPayloadToMap(p *stockpipeline.StockRunPayload) map[string]any {
	data, _ := json.Marshal(p)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}

type Handler struct {
	service *stockpipeline.Service
	jobsSvc jobservice.Service
	log     *zap.Logger
}

func NewHandler(service *stockpipeline.Service, jobsSvc jobservice.Service, log *zap.Logger) *Handler {
	return &Handler{
		service: service,
		jobsSvc: jobsSvc,
		log:     log,
	}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	h.log.Info("Registering Stock Pipeline routes")

	r.POST("/run", h.RunStockPipeline)
	r.POST("/search-and-run", h.SearchAndRun)
}

type SearchQuery struct {
	Q     string `json:"q"`
	Limit int    `json:"limit"`
}

type StockSearchAndRunRequest struct {
	Queries       []SearchQuery                     `json:"queries"`
	TotalMinutes  int                               `json:"total_minutes"`
	ChunkDuration int                               `json:"chunk_duration,omitempty"`
	ClipDuration  int                               `json:"clip_duration,omitempty"`
	NoAudio       bool                              `json:"no_audio,omitempty"`
	NoEffects     bool                              `json:"no_effects,omitempty"`
	NoTransitions bool                              `json:"no_transitions,omitempty"`
	MaxVideos     int                               `json:"max_videos,omitempty"`
	Subfolder     string                            `json:"subfolder"`
	FolderName    string                            `json:"folder_name"`
	FolderID      string                            `json:"folder_id,omitempty"`
	Metadata      *stockpipeline.ChunkMetadataInput `json:"metadata,omitempty"`
}

type StockPipelineResponse struct {
	Status      string                      `json:"status"`
	TotalClips  int                         `json:"total_clips"`
	TotalChunks int                         `json:"total_chunks"`
	Chunks      []stockpipeline.ChunkResult `json:"chunks"`
	Error       string                      `json:"error,omitempty"`
	JobID       string                      `json:"job_id,omitempty"`
	StatusURL   string                      `json:"status_url,omitempty"`
}

func (h *Handler) SearchAndRun(c *gin.Context) {
	var req StockSearchAndRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	h.log.Info("stock search-and-run request received",
		zap.Int("queries", len(req.Queries)),
		zap.Int("total_minutes", req.TotalMinutes),
		zap.Int("chunk_duration", req.ChunkDuration),
		zap.Int("clip_duration", req.ClipDuration),
		zap.Bool("no_audio", req.NoAudio),
		zap.Bool("no_effects", req.NoEffects),
		zap.Bool("no_transitions", req.NoTransitions),
		zap.Int("max_videos", req.MaxVideos),
		zap.String("subfolder", req.Subfolder),
		zap.String("folder_name", req.FolderName),
		zap.String("folder_id", req.FolderID),
	)

	if len(req.Queries) == 0 {
		apiutil.BadRequest(c, "queries required")
		return
	}
	if req.TotalMinutes <= 0 {
		req.TotalMinutes = 5
	}
	if req.ClipDuration < 0 {
		apiutil.BadRequest(c, "clip_duration must be >= 0")
		return
	}
	if req.ClipDuration > 0 && (req.ClipDuration < 3 || req.ClipDuration > 30) {
		apiutil.BadRequest(c, "clip_duration must be between 3 and 30 seconds")
		return
	}

	// Extract query strings from the request
	searchQueries := make([]string, len(req.Queries))
	for i, q := range req.Queries {
		searchQueries[i] = q.Q
	}

	payload := &stockpipeline.StockRunPayload{
		SearchQueries: searchQueries,
		TotalMinutes:  req.TotalMinutes,
		ChunkDuration: req.ChunkDuration,
		ClipDuration:  req.ClipDuration,
		NoAudio:       req.NoAudio,
		NoEffects:     req.NoEffects,
		NoTransitions: req.NoTransitions,
		MaxVideos:     req.MaxVideos,
		Subfolder:     req.Subfolder,
		FolderName:    req.FolderName,
		FolderID:      req.FolderID,
	}
	if req.Metadata != nil {
		payload.Metadata = &stockpipeline.StockRunPayloadMetadata{
			Title:       req.Metadata.Title,
			Description: req.Metadata.Description,
			Tags:        req.Metadata.Tags,
			Category:    req.Metadata.Category,
			Author:      req.Metadata.Author,
			Extra:       req.Metadata.Extra,
		}
	}

	if ok := common.EnqueueAsync(c, h.jobsSvc, &common.EnqueueInput{
		Type:    "media.stock",
		Payload: stockPayloadToMap(payload),
	}, "Stock search-and-run job enqueued."); ok {
		return
	}
	// EnqueueAsync returns false if jobsSvc is nil (503) or on error.
}

func (h *Handler) RunStockPipeline(c *gin.Context) {
	var req stockpipeline.StockRunPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.log.Info("stock run request received",
		zap.Int("search_queries", len(req.SearchQueries)),
		zap.Int("direct_urls", len(req.DirectURLs)),
		zap.Int("total_minutes", req.TotalMinutes),
		zap.Int("chunk_duration", req.ChunkDuration),
		zap.Int("clip_duration", req.ClipDuration),
		zap.Bool("no_audio", req.NoAudio),
		zap.Bool("no_effects", req.NoEffects),
		zap.Bool("no_transitions", req.NoTransitions),
		zap.Int("max_videos", req.MaxVideos),
		zap.String("subfolder", req.Subfolder),
		zap.String("folder_name", req.FolderName),
		zap.String("folder_id", req.FolderID),
	)

	if len(req.SearchQueries) == 0 && len(req.DirectURLs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "search_queries or direct_urls required"})
		return
	}
	if req.TotalMinutes <= 0 {
		req.TotalMinutes = 5
	}
	if req.ClipDuration < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "clip_duration must be >= 0"})
		return
	}
	if req.ClipDuration > 0 && (req.ClipDuration < 3 || req.ClipDuration > 30) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "clip_duration must be between 3 and 30 seconds"})
		return
	}

	if ok := common.EnqueueAsync(c, h.jobsSvc, &common.EnqueueInput{
		Type:    "media.stock",
		Payload: stockPayloadToMap(&req),
	}, "Stock pipeline job enqueued."); ok {
		return
	}
	// EnqueueAsync returns false if jobsSvc is nil (503) or on error.
}

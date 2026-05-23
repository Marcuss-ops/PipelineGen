package sources

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	corejobs "velox/go-master/internal/core/jobs"
	"velox/go-master/internal/pkg/apiutil"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/media/stockpipeline"
)

type StockHandler struct {
	service *stockpipeline.Service
	jobsSvc *jobservice.Service
	log     *zap.Logger
}

func NewStockHandler(service *stockpipeline.Service, jobsSvc *jobservice.Service, log *zap.Logger) *StockHandler {
	return &StockHandler{
		service: service,
		jobsSvc: jobsSvc,
		log:     log,
	}
}

func (h *StockHandler) RegisterRoutes(r *gin.RouterGroup) {
	h.log.Info("Registering Stock Pipeline routes")

	r.POST("/run", h.RunStockPipeline)
}

type StockPipelineResponse struct {
	Status      string                       `json:"status"`
	TotalClips  int                          `json:"total_clips"`
	TotalChunks int                          `json:"total_chunks"`
	Chunks      []stockpipeline.ChunkResult  `json:"chunks"`
	Error       string                       `json:"error,omitempty"`
	JobID       string                       `json:"job_id,omitempty"`
	StatusURL   string                       `json:"status_url,omitempty"`
}

func (h *StockHandler) RunStockPipeline(c *gin.Context) {
	var req corejobs.StockRunPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if len(req.SearchQueries) == 0 && len(req.DirectURLs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "search_queries or direct_urls required"})
		return
	}
	if req.TotalMinutes <= 0 {
		req.TotalMinutes = 5
	}

	if h.jobsSvc != nil {
		job, err := h.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
			Type:    models.JobTypeMediaStock,
			Payload: req.ToMap(),
		})
		if err != nil {
			h.log.Error("failed to enqueue stock pipeline job", zap.Error(err))
			apiutil.InternalError(c, fmt.Errorf("failed to enqueue job: %w", err))
			return
		}

		apiutil.Accepted(c, gin.H{
			"job_id":     job.ID,
			"message":    "Stock pipeline job enqueued",
			"status_url": "/api/jobs/" + job.ID + "/full",
		})
		return
	}

	input := &stockpipeline.RunInput{
		SearchQueries: req.SearchQueries,
		DirectURLs:    req.DirectURLs,
		TotalMinutes:  req.TotalMinutes,
		Subfolder:     req.Subfolder,
		FolderName:    req.FolderName,
	}

	result, err := h.service.Run(c.Request.Context(), input)
	if err != nil {
		h.log.Error("stock pipeline failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, StockPipelineResponse{
			Status: "failed",
			Error:  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, StockPipelineResponse{
		Status:      "completed",
		TotalClips:  result.TotalClips,
		TotalChunks: result.TotalChunks,
		Chunks:      result.Chunks,
	})
}

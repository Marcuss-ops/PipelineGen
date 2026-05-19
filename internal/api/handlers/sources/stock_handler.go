package sources

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

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

type StockPipelineRequest struct {
	SearchQueries []string `json:"search_queries"`
	DirectURLs    []string `json:"direct_urls"`
	TotalMinutes  int      `json:"total_minutes"`
	Subfolder     string   `json:"subfolder"`
	FolderName    string   `json:"folder_name"`
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
	var req StockPipelineRequest
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
		payloadBytes, err := json.Marshal(req)
		if err != nil {
			apiutil.InternalError(c, fmt.Errorf("failed to marshal request: %w", err))
			return
		}
		var payload map[string]any
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			apiutil.InternalError(c, fmt.Errorf("failed to unmarshal request: %w", err))
			return
		}

		job, err := h.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
			Type:    models.JobTypeMediaStock,
			Payload: payload,
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

package sources

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/media/stockpipeline"
)

type StockHandler struct {
	service *stockpipeline.Service
	log     *zap.Logger
}

func NewStockHandler(service *stockpipeline.Service, log *zap.Logger) *StockHandler {
	return &StockHandler{
		service: service,
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
	ChunkDuration int      `json:"chunk_duration"`
	Subfolder     string   `json:"subfolder"`
	FolderName    string   `json:"folder_name"`
}

type StockPipelineResponse struct {
	Status      string                       `json:"status"`
	TotalClips  int                          `json:"total_clips"`
	TotalChunks int                          `json:"total_chunks"`
	Chunks      []stockpipeline.ChunkResult  `json:"chunks"`
	Error       string                       `json:"error,omitempty"`
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

	input := &stockpipeline.RunInput{
		SearchQueries: req.SearchQueries,
		DirectURLs:    req.DirectURLs,
		TotalMinutes:  req.TotalMinutes,
		ChunkDuration: req.ChunkDuration,
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

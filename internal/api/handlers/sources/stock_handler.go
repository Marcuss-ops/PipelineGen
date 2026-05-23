package sources

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	corejobs "velox/go-master/internal/core/jobs"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/media/stockpipeline"
	"velox/go-master/internal/pkg/apiutil"
	"velox/go-master/internal/sources/youtube"
)

type StockHandler struct {
	service        *stockpipeline.Service
	jobsSvc        *jobservice.Service
	log            *zap.Logger
	youtubeService *youtube.Service
}

func NewStockHandler(service *stockpipeline.Service, jobsSvc *jobservice.Service, log *zap.Logger) *StockHandler {
	return &StockHandler{
		service: service,
		jobsSvc: jobsSvc,
		log:     log,
	}
}

func (h *StockHandler) SetYoutubeService(svc *youtube.Service) {
	h.youtubeService = svc
}

func (h *StockHandler) RegisterRoutes(r *gin.RouterGroup) {
	h.log.Info("Registering Stock Pipeline routes")

	r.POST("/run", h.RunStockPipeline)
	r.POST("/search-and-run", h.SearchAndRun)
}

type SearchQuery struct {
	Q     string `json:"q"`
	Limit int    `json:"limit"`
}

type StockSearchAndRunRequest struct {
	Queries       []SearchQuery `json:"queries"`
	TotalMinutes  int           `json:"total_minutes"`
	ChunkDuration int           `json:"chunk_duration,omitempty"`
	MaxVideos     int           `json:"max_videos,omitempty"`
	Subfolder     string        `json:"subfolder"`
	FolderName    string        `json:"folder_name"`
	FolderID      string        `json:"folder_id,omitempty"`
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

func (h *StockHandler) SearchAndRun(c *gin.Context) {
	var req StockSearchAndRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	h.log.Info("stock search-and-run request received",
		zap.Int("queries", len(req.Queries)),
		zap.Int("total_minutes", req.TotalMinutes),
		zap.Int("chunk_duration", req.ChunkDuration),
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

	if h.youtubeService == nil {
		apiutil.InternalError(c, fmt.Errorf("youtube service not available"))
		return
	}

	// Search all queries in parallel
	type searchResult struct {
		query string
		resp  *youtube.TopicSearchResponse
		err   error
	}

	ch := make(chan searchResult, len(req.Queries))
	var wg sync.WaitGroup

	for _, q := range req.Queries {
		wg.Add(1)
		q := q
		go func() {
			defer wg.Done()
			limit := q.Limit
			if limit <= 0 {
				limit = 10
			}
			resp, err := h.youtubeService.SearchTopicVideos(context.Background(), q.Q, limit, "")
			ch <- searchResult{query: q.Q, resp: resp, err: err}
		}()
	}

	wg.Wait()
	close(ch)

	// Collect and deduplicate URLs
	seen := make(map[string]bool)
	var directURLs []string
	var searchErrors []string

	for r := range ch {
		if r.err != nil {
			searchErrors = append(searchErrors, fmt.Sprintf("%s: %v", r.query, r.err))
			continue
		}
		if r.resp == nil {
			continue
		}
		for _, res := range r.resp.Results {
			if !seen[res.VideoID] {
				seen[res.VideoID] = true
				directURLs = append(directURLs, res.DirectLink)
			}
		}
	}

	if len(directURLs) == 0 {
		apiutil.BadRequest(c, fmt.Sprintf("no videos found for any query: %v", searchErrors))
		return
	}

	h.log.Info("search-and-run found videos",
		zap.Int("total_urls", len(directURLs)),
		zap.Strings("errors", searchErrors),
	)

	// Enqueue or run the stock pipeline
	if h.jobsSvc != nil {
		payload := &corejobs.StockRunPayload{
			DirectURLs:    directURLs,
			TotalMinutes:  req.TotalMinutes,
			ChunkDuration: req.ChunkDuration,
			MaxVideos:     req.MaxVideos,
			Subfolder:     req.Subfolder,
			FolderName:    req.FolderName,
			FolderID:      req.FolderID,
		}

		job, err := h.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
			Type:    models.JobTypeMediaStock,
			Payload: payload.ToMap(),
		})
		if err != nil {
			h.log.Error("failed to enqueue stock pipeline job", zap.Error(err))
			apiutil.InternalError(c, fmt.Errorf("failed to enqueue job: %w", err))
			return
		}

		apiutil.Accepted(c, gin.H{
			"job_id":        job.ID,
			"message":       "Stock pipeline job enqueued",
			"status_url":    "/api/jobs/" + job.ID + "/full",
			"total_videos":  len(directURLs),
			"search_errors": searchErrors,
		})
		return
	}

	// Fallback: run synchronously
	input := &stockpipeline.RunInput{
		DirectURLs:    directURLs,
		TotalMinutes:  req.TotalMinutes,
		ChunkDuration: req.ChunkDuration,
		MaxVideos:     req.MaxVideos,
		Subfolder:     req.Subfolder,
		FolderName:    req.FolderName,
		FolderID:      req.FolderID,
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

func (h *StockHandler) RunStockPipeline(c *gin.Context) {
	var req corejobs.StockRunPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.log.Info("stock run request received",
		zap.Int("search_queries", len(req.SearchQueries)),
		zap.Int("direct_urls", len(req.DirectURLs)),
		zap.Int("total_minutes", req.TotalMinutes),
		zap.Int("chunk_duration", req.ChunkDuration),
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
		ChunkDuration: req.ChunkDuration,
		MaxVideos:     req.MaxVideos,
		Subfolder:     req.Subfolder,
		FolderName:    req.FolderName,
		FolderID:      req.FolderID,
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

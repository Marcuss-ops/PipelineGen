// Package handlers gestisce endpoint per pipeline asincrona
package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/service/asyncpipeline"
	"velox/go-master/internal/service/scriptclips"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// AsyncPipelineHandler gestisce richieste HTTP per pipeline asincrona
type AsyncPipelineHandler struct {
	service *asyncpipeline.AsyncPipelineService
}

// NewAsyncPipelineHandler crea un nuovo handler
func NewAsyncPipelineHandler(service *asyncpipeline.AsyncPipelineService) *AsyncPipelineHandler {
	return &AsyncPipelineHandler{
		service: service,
	}
}

// RegisterRoutes registra le rotte per pipeline asincrona
func (h *AsyncPipelineHandler) RegisterRoutes(rg *gin.RouterGroup) {
	pipeline := rg.Group("/pipeline")
	{
		pipeline.POST("/start", h.StartPipeline)
		pipeline.GET("/status/:job_id", h.GetJobStatus)
		pipeline.GET("/jobs", h.ListJobs)
		pipeline.POST("/cancel/:job_id", h.CancelJob)
	}
}

// StartPipelineRequest rappresenta la richiesta per avviare una pipeline
type StartPipelineRequest struct {
	SourceText            string `json:"source_text" binding:"required"`
	Title                 string `json:"title" binding:"required"`
	Language              string `json:"language" default:"italian"`
	Duration              int    `json:"duration" default:"60"`
	EntityCountPerSegment int   `json:"entity_count_per_segment" default:"5"`
	Model                 string `json:"model" default:"gemma3:4b"`
}

// StartPipelineResponse rappresenta la risposta per l'avvio pipeline
type StartPipelineResponse struct {
	OK     bool   `json:"ok"`
	JobID  string `json:"job_id"`
	Status string `json:"status"`
	Title  string `json:"title"`
}

// StartPipeline avvia una pipeline asincrona
// @Summary Avvia pipeline asincrona
// @Description Avvia la generazione script + clip in background e restituisce un job ID
// @Tags pipeline
// @Accept json
// @Produce json
// @Param request body StartPipelineRequest true "Richiesta pipeline"
// @Success 200 {object} StartPipelineResponse
// @Router /api/pipeline/start [post]
func (h *AsyncPipelineHandler) StartPipeline(c *gin.Context) {
	var req StartPipelineRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Valida campi richiesti
	if req.SourceText == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "source_text is required"})
		return
	}
	if req.Title == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "title is required"})
		return
	}

	// Valida durata
	if req.Duration <= 0 {
		req.Duration = 60
	} else if req.Duration < 10 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "duration must be at least 10 seconds"})
		return
	} else if req.Duration > 3600 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "duration cannot exceed 60 minutes"})
		return
	}

	if req.EntityCountPerSegment <= 0 {
		req.EntityCountPerSegment = 5
	}

	// Converti in richiesta del servizio
	serviceReq := &scriptclips.ScriptClipsRequest{
		SourceText:            req.SourceText,
		Title:                 req.Title,
		Language:              req.Language,
		Duration:              req.Duration,
		EntityCountPerSegment: req.EntityCountPerSegment,
		Model:                 req.Model,
	}

	// Avvia pipeline asincrona
	jobID, err := h.service.StartPipeline(serviceReq)
	if err != nil {
		logger.Error("Failed to start pipeline", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, StartPipelineResponse{
		OK:     true,
		JobID:  jobID,
		Status: "pending",
		Title:  req.Title,
	})
}

// GetJobStatus restituisce lo stato di un job
// @Summary Stato job pipeline
// @Description Restituisce lo stato di un job di pipeline
// @Tags pipeline
// @Produce json
// @Param job_id path string true "Job ID"
// @Success 200 {object} asyncpipeline.PipelineJob
// @Router /api/pipeline/status/{job_id} [get]
func (h *AsyncPipelineHandler) GetJobStatus(c *gin.Context) {
	jobID := c.Param("job_id")
	if jobID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "job_id is required"})
		return
	}

	job, err := h.service.GetJobStatus(jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":  true,
		"job": job,
	})
}

// ListJobsResponse rappresenta la risposta per la lista job
type ListJobsResponse struct {
	OK    bool                          `json:"ok"`
	Jobs  []asyncpipeline.PipelineJob   `json:"jobs"`
	Count int                           `json:"count"`
}

// ListJobs restituisce la lista dei job
// @Summary Lista job pipeline
// @Description Restituisce la lista dei job di pipeline con filtro opzionale
// @Tags pipeline
// @Produce json
// @Param status query string false "Filtra per status (pending, running, completed, failed)"
// @Param limit query int false "Limite risultati (default 20)"
// @Success 200 {object} ListJobsResponse
// @Router /api/pipeline/jobs [get]
func (h *AsyncPipelineHandler) ListJobs(c *gin.Context) {
	status := c.Query("status")
	limitStr := c.DefaultQuery("limit", "20")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	jobs, err := h.service.ListJobs(status, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, ListJobsResponse{
		OK:    true,
		Jobs:  jobs,
		Count: len(jobs),
	})
}

// CancelJob cancella un job in esecuzione
// @Summary Cancella job pipeline
// @Description Cancella un job di pipeline in esecuzione
// @Tags pipeline
// @Produce json
// @Param job_id path string true "Job ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/pipeline/cancel/{job_id} [post]
func (h *AsyncPipelineHandler) CancelJob(c *gin.Context) {
	jobID := c.Param("job_id")
	if jobID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "job_id is required"})
		return
	}

	err := h.service.CancelJob(jobID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"job_id": jobID,
		"status": "cancelled",
	})
}

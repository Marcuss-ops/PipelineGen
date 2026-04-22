package common

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/core/job"
	"velox/go-master/internal/core/worker"
	"velox/go-master/pkg/config"
)

// HealthHandler handles health check requests
type HealthHandler struct {
	cfg         *config.Config
	jobService  *job.Service
	workerService *worker.Service
}

// NewHealthHandler creates a new health handler
func NewHealthHandler(cfg *config.Config, jobService *job.Service, workerService *worker.Service) *HealthHandler {
	return &HealthHandler{
		cfg:         cfg,
		jobService:  jobService,
		workerService: workerService,
	}
}

// RegisterRoutes registers health routes
func (h *HealthHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/health", h.Health)
	rg.GET("/status", h.Status)
	rg.GET("/metrics", h.Metrics)
}

// Health godoc
// @Summary Health check
// @Description Check if the server is healthy
// @Tags health
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /health [get]
func (h *HealthHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "healthy",
		"ok":     true,
	})
}

// Status godoc
// @Summary Server status
// @Description Get detailed server status
// @Tags health
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /status [get]
func (h *HealthHandler) Status(c *gin.Context) {
	workerStats := h.workerService.GetWorkerStats()
	jobs := h.jobService.GetAllJobs()

	// Count jobs by status
	jobCounts := make(map[string]int)
	for _, job := range jobs {
		jobCounts[string(job.Status)]++
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"status": "running",
		"workers": workerStats,
		"jobs": gin.H{
			"total":  len(jobs),
			"counts": jobCounts,
		},
		"config": gin.H{
			"new_jobs_paused": h.jobService.IsNewJobsPaused(),
		},
	})
}

// Metrics godoc
// @Summary Server metrics
// @Description Get server metrics
// @Tags health
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /metrics [get]
func (h *HealthHandler) Metrics(c *gin.Context) {
	workers := h.workerService.ListWorkers()
	jobs := h.jobService.GetAllJobs()

	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"metrics": gin.H{
			"workers_total": len(workers),
			"jobs_total":    len(jobs),
		},
	})
}
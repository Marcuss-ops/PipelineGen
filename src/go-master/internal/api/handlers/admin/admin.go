// Package handlers provides HTTP handlers for the VeloxEditing API.
package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/core/job"
	"velox/go-master/internal/core/worker"
	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/models"
	"go.uber.org/zap"
)

// AdminHandler handles admin-related HTTP requests
type AdminHandler struct {
	jobService    *job.Service
	workerService *worker.Service
}

// NewAdminHandler creates a new admin handler
func NewAdminHandler(jobService *job.Service, workerService *worker.Service) *AdminHandler {
	return &AdminHandler{
		jobService:    jobService,
		workerService: workerService,
	}
}

// RegisterRoutes registers admin routes
func (h *AdminHandler) RegisterRoutes(rg *gin.RouterGroup) {
	admin := rg.Group("/admin")
	{
		// Job admin endpoints
		admin.POST("/jobs/pause", h.PauseAllJobs)
		admin.POST("/jobs/resume", h.ResumeAllJobs)
		admin.POST("/jobs/:id/pause", h.PauseJob)
		admin.POST("/jobs/:id/resume", h.ResumeJob)

		// Worker admin endpoints
		admin.POST("/workers/:id/restart", h.RestartWorker)
		admin.POST("/workers/:id/update", h.UpdateWorker)
	}
}

// PauseAllJobs godoc
// @Summary Pause all new jobs
// @Description Pause accepting new jobs
// @Tags admin
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /admin/jobs/pause [post]
func (h *AdminHandler) PauseAllJobs(c *gin.Context) {
	h.jobService.SetNewJobsPaused(true)
	logger.Info("Admin paused all new jobs")
	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"status": "paused",
		"message": "New job creation is now paused",
	})
}

// ResumeAllJobs godoc
// @Summary Resume accepting new jobs
// @Description Resume accepting new jobs
// @Tags admin
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /admin/jobs/resume [post]
func (h *AdminHandler) ResumeAllJobs(c *gin.Context) {
	h.jobService.SetNewJobsPaused(false)
	logger.Info("Admin resumed all new jobs")
	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"status": "resumed",
		"message": "New job creation is now resumed",
	})
}

// PauseJob godoc
// @Summary Pause a specific job
// @Description Pause a specific job by ID (prevents execution)
// @Tags admin
// @Accept json
// @Produce json
// @Param id path string true "Job ID"
// @Success 200 {object} map[string]interface{}
// @Router /admin/jobs/{id}/pause [post]
func (h *AdminHandler) PauseJob(c *gin.Context) {
	ctx := c.Request.Context()
	jobID := c.Param("id")

	// Get the job first
	j, err := h.jobService.GetJob(ctx, jobID)
	if err != nil {
		if err == job.ErrJobNotFound {
			c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Job not found"})
			return
		}
		logger.Error("Failed to get job", zap.String("job_id", jobID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Update job status to paused
	if err := h.jobService.UpdateJobStatus(ctx, jobID, models.JobStatusPaused, j.Progress, nil, ""); err != nil {
		logger.Error("Failed to pause job", zap.String("job_id", jobID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	logger.Info("Admin paused job", zap.String("job_id", jobID))
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"job_id":  jobID,
		"status":  "paused",
		"message": "Job paused successfully",
	})
}

// ResumeJob godoc
// @Summary Resume a specific job
// @Description Resume a paused job by ID
// @Tags admin
// @Accept json
// @Produce json
// @Param id path string true "Job ID"
// @Success 200 {object} map[string]interface{}
// @Router /admin/jobs/{id}/resume [post]
func (h *AdminHandler) ResumeJob(c *gin.Context) {
	ctx := c.Request.Context()
	jobID := c.Param("id")

	// Get the job first
	j, err := h.jobService.GetJob(ctx, jobID)
	if err != nil {
		if err == job.ErrJobNotFound {
			c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Job not found"})
			return
		}
		logger.Error("Failed to get job", zap.String("job_id", jobID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Update job status back to pending
	if err := h.jobService.UpdateJobStatus(ctx, jobID, models.JobStatusPending, j.Progress, nil, ""); err != nil {
		logger.Error("Failed to resume job", zap.String("job_id", jobID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	logger.Info("Admin resumed job", zap.String("job_id", jobID))
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"job_id":  jobID,
		"status":  "pending",
		"message": "Job resumed successfully",
	})
}

// RestartWorker godoc
// @Summary Restart a worker
// @Description Send restart command to a worker
// @Tags admin
// @Accept json
// @Produce json
// @Param id path string true "Worker ID"
// @Success 200 {object} map[string]interface{}
// @Router /admin/workers/{id}/restart [post]
func (h *AdminHandler) RestartWorker(c *gin.Context) {
	ctx := c.Request.Context()
	workerID := c.Param("id")

	cmd, err := h.workerService.SendCommand(ctx, workerID, "restart", nil)
	if err != nil {
		if err == worker.ErrWorkerNotFound {
			c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Worker not found"})
			return
		}
		logger.Error("Failed to send restart command", zap.String("worker_id", workerID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	logger.Info("Admin sent restart command to worker", zap.String("worker_id", workerID))
	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"worker_id":   workerID,
		"command_id":  cmd.ID,
		"message":     "Restart command sent to worker",
	})
}

// UpdateWorker godoc
// @Summary Update worker code
// @Description Send update command to a worker to download new code
// @Tags admin
// @Accept json
// @Produce json
// @Param id path string true "Worker ID"
// @Success 200 {object} map[string]interface{}
// @Router /admin/workers/{id}/update [post]
func (h *AdminHandler) UpdateWorker(c *gin.Context) {
	ctx := c.Request.Context()
	workerID := c.Param("id")

	cmd, err := h.workerService.SendCommand(ctx, workerID, "update", nil)
	if err != nil {
		if err == worker.ErrWorkerNotFound {
			c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Worker not found"})
			return
		}
		logger.Error("Failed to send update command", zap.String("worker_id", workerID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	logger.Info("Admin sent update command to worker", zap.String("worker_id", workerID))
	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"worker_id":   workerID,
		"command_id":  cmd.ID,
		"message":     "Update command sent to worker",
	})
}

package jobs

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/internal/service/jobs"
	"velox/go-master/pkg/models"
)

type Handler struct {
	service *jobs.Service
	log     *zap.Logger
}

func NewHandler(service *jobs.Service, log *zap.Logger) *Handler {
	return &Handler{service: service, log: log}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("", h.Enqueue)
	r.GET("", h.List)
	r.GET("/:id", h.Get)
	r.POST("/:id/cancel", h.Cancel)
	r.POST("/:id/retry", h.Retry)
	r.GET("/:id/events", h.Events)
}

func (h *Handler) Enqueue(c *gin.Context) {
	var req jobs.EnqueueRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	job, err := h.service.Enqueue(c.Request.Context(), &req)
	if err != nil {
		h.log.Error("failed to enqueue job", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"ok":     true,
		"job_id": job.ID,
		"job": gin.H{
			"id":      job.ID,
			"type":    job.Type,
			"status":  job.Status,
			"project": job.Project,
			"progress": job.Progress,
		},
	})
}

func (h *Handler) Get(c *gin.Context) {
	id := c.Param("id")

	job, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "job not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":  true,
		"job": job,
	})
}

func (h *Handler) List(c *gin.Context) {
	var filter models.JobFilter

	if status := c.Query("status"); status != "" {
		s := models.JobStatus(status)
		filter.Status = &s
	}
	if jobType := c.Query("type"); jobType != "" {
		t := models.JobType(jobType)
		filter.Type = &t
	}
	if workerID := c.Query("worker_id"); workerID != "" {
		filter.WorkerID = workerID
	}
	if limit := c.Query("limit"); limit != "" {
		filter.Limit, _ = strconv.Atoi(limit)
	}
	if offset := c.Query("offset"); offset != "" {
		filter.Offset, _ = strconv.Atoi(offset)
	}

	jobs, err := h.service.List(c.Request.Context(), filter)
	if err != nil {
		h.log.Error("failed to list jobs", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":   true,
		"jobs": jobs,
		"count": len(jobs),
	})
}

func (h *Handler) Cancel(c *gin.Context) {
	id := c.Param("id")

	if err := h.service.Cancel(c.Request.Context(), id); err != nil {
		h.log.Error("failed to cancel job", zap.String("job_id", id), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "job cancelled"})
}

func (h *Handler) Retry(c *gin.Context) {
	id := c.Param("id")

	job, err := h.service.Retry(c.Request.Context(), id)
	if err != nil {
		h.log.Error("failed to retry job", zap.String("job_id", id), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":  true,
		"job": job,
	})
}

func (h *Handler) Events(c *gin.Context) {
	id := c.Param("id")

	events, err := h.service.ListEvents(c.Request.Context(), id)
	if err != nil {
		h.log.Error("failed to list job events", zap.String("job_id", id), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"events": events,
		"count":  len(events),
	})
}

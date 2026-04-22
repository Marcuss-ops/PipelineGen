package common

import (
"net/http"

"github.com/gin-gonic/gin"
corejob "velox/go-master/internal/core/job"
"velox/go-master/pkg/models"
)

type JobHandler struct {
service *corejob.Service
}

func NewJobHandler(service *corejob.Service) *JobHandler {
return &JobHandler{service: service}
}

func (h *JobHandler) RegisterRoutes(rg *gin.RouterGroup) {
jobs := rg.Group("/jobs")
{
jobs.GET("", h.ListJobs)
jobs.POST("", h.CreateJob)
jobs.GET(":id", h.GetJob)
jobs.PUT(":id/status", h.UpdateJobStatus)
jobs.DELETE(":id", h.DeleteJob)
jobs.POST(":id/assign", h.AssignJob)
jobs.POST(":id/lease", h.RenewLease)
}
}

func (h *JobHandler) ListJobs(c *gin.Context) {
filter := models.JobFilter{}
if status := c.Query("status"); status != "" {
s := models.JobStatus(status)
filter.Status = &s
}
if jobType := c.Query("type"); jobType != "" {
t := models.JobType(jobType)
filter.Type = &t
}
filter.WorkerID = c.Query("worker_id")
jobs, err := h.service.ListJobs(c.Request.Context(), filter)
if err != nil {
c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
return
}
c.JSON(http.StatusOK, gin.H{"ok": true, "jobs": jobs, "count": len(jobs)})
}

func (h *JobHandler) CreateJob(c *gin.Context) {
var req models.CreateJobRequest
if err := c.ShouldBindJSON(&req); err != nil {
c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
return
}
created, err := h.service.CreateJob(c.Request.Context(), req)
if err != nil {
c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
return
}
c.JSON(http.StatusCreated, gin.H{"ok": true, "job_id": created.ID, "job": created})
}

func (h *JobHandler) GetJob(c *gin.Context) {
item, err := h.service.GetJob(c.Request.Context(), c.Param("id"))
if err != nil {
status := http.StatusInternalServerError
if err == corejob.ErrJobNotFound {
status = http.StatusNotFound
}
c.JSON(status, gin.H{"ok": false, "error": err.Error()})
return
}
c.JSON(http.StatusOK, gin.H{"ok": true, "job": item})
}

func (h *JobHandler) UpdateJobStatus(c *gin.Context) {
var req struct {
Status   models.JobStatus         `json:"status" binding:"required"`
Progress int                      `json:"progress"`
Result   map[string]interface{}   `json:"result"`
Error    string                   `json:"error"`
}
if err := c.ShouldBindJSON(&req); err != nil {
c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
return
}
if err := h.service.UpdateJobStatus(c.Request.Context(), c.Param("id"), req.Status, req.Progress, req.Result, req.Error); err != nil {
c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
return
}
c.JSON(http.StatusOK, gin.H{"ok": true, "job_id": c.Param("id"), "status": req.Status})
}

func (h *JobHandler) DeleteJob(c *gin.Context) {
if err := h.service.DeleteJob(c.Request.Context(), c.Param("id")); err != nil {
status := http.StatusInternalServerError
if err == corejob.ErrJobNotFound {
status = http.StatusNotFound
}
c.JSON(status, gin.H{"ok": false, "error": err.Error()})
return
}
c.JSON(http.StatusOK, gin.H{"ok": true, "job_id": c.Param("id")})
}

func (h *JobHandler) AssignJob(c *gin.Context) {
var req struct {
WorkerID string `json:"worker_id" binding:"required"`
}
if err := c.ShouldBindJSON(&req); err != nil {
c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
return
}
if err := h.service.AssignJobToWorker(c.Request.Context(), c.Param("id"), req.WorkerID); err != nil {
c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
return
}
c.JSON(http.StatusOK, gin.H{"ok": true, "job_id": c.Param("id"), "worker_id": req.WorkerID})
}

func (h *JobHandler) RenewLease(c *gin.Context) {
if err := h.service.RenewJobLease(c.Request.Context(), c.Param("id")); err != nil {
c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
return
}
c.JSON(http.StatusOK, gin.H{"ok": true, "job_id": c.Param("id")})
}

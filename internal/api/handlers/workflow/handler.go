package workflow

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"velox/go-master/internal/core/jobs"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/internal/service/workflowrunner"
	"velox/go-master/pkg/models"

	"github.com/gin-gonic/gin"
)

// Handler handles workflow API requests
type Handler struct {
	service      *workflowrunner.Service
	log          *zap.Logger
	workflowsDir string
	jobsService *jobservice.Service
}

// NewHandler creates a new workflow handler
func NewHandler(svc *workflowrunner.Service, log *zap.Logger, workflowsDir string, jobsService *jobservice.Service) *Handler {
	return &Handler{
		service:      svc,
		log:          log,
		workflowsDir: workflowsDir,
		jobsService: jobsService,
	}
}

// resolveWorkflowPath resolves a workflow name to a file path within workflowsDir.
// Only allows names with .yaml or .yml extension, or adds .yaml.
func (h *Handler) resolveWorkflowPath(name string) (string, error) {
	// Clean the name to prevent path traversal
	cleanName := filepath.Clean(name)
	// Ensure the name does not contain directory separators
	if strings.ContainsAny(cleanName, `/\`) {
		return "", fmt.Errorf("workflow name must not contain path separators")
	}
	// Check extension
	ext := filepath.Ext(cleanName)
	if ext == "" {
		cleanName += ".yaml"
	} else if ext != ".yaml" && ext != ".yml" {
		return "", fmt.Errorf("workflow file must have .yaml or .yml extension")
	}
	// Join with workflowsDir
	fullPath := filepath.Join(h.workflowsDir, cleanName)
	// Ensure the full path is within workflowsDir (double-check)
	if !strings.HasPrefix(fullPath, filepath.Clean(h.workflowsDir)) {
		return "", fmt.Errorf("workflow path is outside allowed directory")
	}
	return fullPath, nil
}

// RegisterRoutes registers workflow routes
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/run", h.runWorkflow)
	r.POST("/run-file", h.runWorkflowFile)
	r.GET("/runs/:id", h.getRunStatus)
	r.GET("/list", h.listWorkflows)
	r.POST("/load", h.loadWorkflow)
	r.POST("/content-package", h.contentPackage)
}

// ContentPackageRequest is the request for creating a content package
type ContentPackageRequest struct {
	Title  string `json:"title" binding:"required"`
	Style  string `json:"style"`
	Assets string `json:"assets"`
	Output string `json:"output"`
}

// contentPackage creates a content package (script + clips + upload)
func (h *Handler) contentPackage(c *gin.Context) {
	var req ContentPackageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Enqueue a job in the job system using request context
	payload := map[string]any{
		"title":  req.Title,
		"style":  req.Style,
		"assets": req.Assets,
		"output": req.Output,
	}
	job, err := h.jobsService.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
		Type:    models.JobTypeContentPackage,
		Payload: payload,
	})
	if err != nil {
		h.log.Error("failed to enqueue content package job", zap.String("title", req.Title), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	h.log.Info("enqueued content package job", zap.String("job_id", job.ID), zap.String("title", req.Title))
	c.JSON(http.StatusAccepted, gin.H{
		"ok":        true,
		"message":   "content package job enqueued",
		"job_id":    job.ID,
		"status_url": "/api/jobs/" + job.ID + "/full",
	})
}

// runWorkflowRequest is the request body for running a workflow
type runWorkflowRequest struct {
	Workflow string `json:"workflow"`
}

// runWorkflow runs a loaded workflow by name
// This enqueues a job in the job system for async execution.
func (h *Handler) runWorkflow(c *gin.Context) {
	var req runWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	if req.Workflow == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "workflow name required"})
		return
	}

	// Enqueue a job in the job system using request context
	payload := map[string]any{"workflow": req.Workflow}
	job, err := h.jobsService.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
		Type:    models.JobType(jobs.JobTypeWorkflowRun),
		Payload: payload,
	})
	if err != nil {
		h.log.Error("failed to enqueue workflow job", zap.String("workflow", req.Workflow), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	h.log.Info("enqueued workflow job", zap.String("job_id", job.ID), zap.String("workflow", req.Workflow))
	c.JSON(http.StatusAccepted, gin.H{"ok": true, "message": "workflow job enqueued", "job_id": job.ID})
}

// runWorkflowFileRequest is the request body for running a workflow from file
type runWorkflowFileRequest struct {
	Path string `json:"path"`
}

// runWorkflowFile runs a workflow from a YAML file (async via job system)
func (h *Handler) runWorkflowFile(c *gin.Context) {
	var req runWorkflowFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	if req.Path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "workflow path required"})
		return
	}

	// Resolve the workflow name to a file path within the configured directory
	path, err := h.resolveWorkflowPath(req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Enqueue a job in the job system using request context
	payload := map[string]any{"path": path}
	job, err := h.jobsService.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
		Type:    models.JobType(jobs.JobTypeWorkflowRun),
		Payload: payload,
	})
	if err != nil {
		h.log.Error("failed to enqueue workflow file job", zap.String("path", path), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	h.log.Info("enqueued workflow file job", zap.String("job_id", job.ID), zap.String("path", path))
	c.JSON(http.StatusAccepted, gin.H{
		"ok":         true,
		"message":    "workflow file job enqueued",
		"job_id":     job.ID,
		"status_url": "/api/jobs/" + job.ID + "/full",
	})
}

// getRunStatus returns the status of a workflow run
func (h *Handler) getRunStatus(c *gin.Context) {
	jobID := c.Param("id")
	if jobID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "job id required"})
		return
	}

	job, err := h.jobsService.Get(c.Request.Context(), jobID)
	if err != nil {
		h.log.Error("failed to get job", zap.String("job_id", jobID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if job == nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "workflow run not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"job_id": job.ID,
		"type":   job.Type,
		"status": job.Status,
		"result": job.Result,
		"error":  job.Error,
	})
}

// listWorkflows lists loaded workflows
func (h *Handler) listWorkflows(c *gin.Context) {
	names := h.service.ListWorkflows()
	c.JSON(http.StatusOK, gin.H{"ok": true, "workflows": names})
}

// loadWorkflow loads a workflow from file
func (h *Handler) loadWorkflow(c *gin.Context) {
	var req runWorkflowFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	if req.Path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "path required"})
		return
	}

	wf, err := h.service.LoadWorkflow(req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "workflow": wf.Name})
}

package workflow

import (
	"context"
	"net/http"
	"path/filepath"

	"go.uber.org/zap"
	"velox/go-master/internal/service/workflowrunner"

	"github.com/gin-gonic/gin"
)

// Handler handles workflow API requests
type Handler struct {
	service *workflowrunner.Service
	log     *zap.Logger
}

// NewHandler creates a new workflow handler
func NewHandler(svc *workflowrunner.Service, log *zap.Logger) *Handler {
	return &Handler{
		service: svc,
		log:     log,
	}
}

// RegisterRoutes registers workflow routes
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/run", h.runWorkflow)
	r.POST("/run-file", h.runWorkflowFile)
	r.GET("/runs/:id", h.getRunStatus)
	r.GET("/list", h.listWorkflows)
	r.POST("/load", h.loadWorkflow)
}

// runWorkflowRequest is the request body for running a workflow
type runWorkflowRequest struct {
	Workflow string `json:"workflow"`
}

// runWorkflow runs a loaded workflow by name
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

	// Run workflow asynchronously with background context
	go func() {
		ctx := context.Background()
		result, err := h.service.RunWorkflow(ctx, req.Workflow)
		if err != nil {
			h.log.Error("workflow run failed", zap.String("workflow", req.Workflow), zap.Error(err))
			return
		}
		h.log.Info("workflow completed", zap.String("workflow_id", result.WorkflowID), zap.String("status", result.Status))
	}()

	c.JSON(http.StatusAccepted, gin.H{"ok": true, "message": "workflow started"})
}

// runWorkflowFileRequest is the request body for running a workflow from file
type runWorkflowFileRequest struct {
	Path string `json:"path"`
}

// runWorkflowFile runs a workflow from a YAML file
func (h *Handler) runWorkflowFile(c *gin.Context) {
	var req runWorkflowFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	if req.Path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "path required"})
		return
	}

	// Resolve path
	path := req.Path
	if !filepath.IsAbs(path) {
		// Try relative to working directory
	}

	// Run workflow asynchronously with background context
	go func() {
		ctx := context.Background()
		result, err := h.service.RunWorkflowFromFile(ctx, path)
		if err != nil {
			h.log.Error("workflow file run failed", zap.String("path", path), zap.Error(err))
			return
		}
		h.log.Info("workflow file completed", zap.String("workflow_id", result.WorkflowID), zap.String("status", result.Status))
	}()

	c.JSON(http.StatusAccepted, gin.H{"ok": true, "message": "workflow file started"})
}

// getRunStatus returns the status of a workflow run
func (h *Handler) getRunStatus(c *gin.Context) {
	workflowID := c.Param("id")
	if workflowID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "workflow id required"})
		return
	}

	result, ok := h.service.GetResult(workflowID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "workflow run not found"})
		return
	}

	c.JSON(http.StatusOK, result)
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

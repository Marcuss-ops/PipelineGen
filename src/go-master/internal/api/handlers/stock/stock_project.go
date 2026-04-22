// Package handlers provides HTTP handlers for stock video endpoints.
package stock

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/stock"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// StockProjectHandler handles stock project management endpoints
type StockProjectHandler struct {
	manager *stock.StockManager
}

// NewStockProjectHandler creates a new stock project handler
func NewStockProjectHandler(manager *stock.StockManager) *StockProjectHandler {
	return &StockProjectHandler{manager: manager}
}

// RegisterRoutes registers stock project routes
func (h *StockProjectHandler) RegisterRoutes(rg *gin.RouterGroup) {
	s := rg.Group("/stock")
	{
		s.GET("/projects", h.ListProjects)
		s.POST("/project", h.CreateProject)
		s.GET("/project/:name", h.GetProject)
		s.DELETE("/project/:name", h.DeleteProject)
		s.POST("/project/:name/video", h.AddVideo)
		s.GET("/project/:name/videos", h.ListVideos)
		s.DELETE("/project/:name/video/:id", h.DeleteVideo)
	}
}

// ListProjects lists all stock projects
func (h *StockProjectHandler) ListProjects(c *gin.Context) {
	projects, err := h.manager.ListProjects(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Failed to list projects: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"projects": projects,
		"count":    len(projects),
	})
}

// CreateProject creates a new stock project
func (h *StockProjectHandler) CreateProject(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	project, err := h.manager.CreateProject(c.Request.Context(), req.Name, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Failed to create project: " + err.Error(),
		})
		return
	}

	logger.Info("Stock project created",
		zap.String("name", project.Name),
	)

	c.JSON(http.StatusCreated, gin.H{
		"ok":      true,
		"project": project,
	})
}

// GetProject gets a stock project by name
func (h *StockProjectHandler) GetProject(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Project name is required",
		})
		return
	}

	project, err := h.manager.GetProject(c.Request.Context(), name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"ok":    false,
			"error": "Project not found: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"project": project,
	})
}

// DeleteProject deletes a stock project
func (h *StockProjectHandler) DeleteProject(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Project name is required",
		})
		return
	}

	if err := h.manager.DeleteProject(c.Request.Context(), name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Failed to delete project: " + err.Error(),
		})
		return
	}

	logger.Info("Stock project deleted",
		zap.String("name", name),
	)

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "Project deleted",
	})
}

// AddVideo adds a video to a project
func (h *StockProjectHandler) AddVideo(c *gin.Context) {
	projectName := c.Param("name")
	
	var req struct {
		VideoPath  string `json:"video_path" binding:"required"`
		SourceURL  string `json:"source_url"`
		Title      string `json:"title"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	// Verify project exists
	_, err := h.manager.GetProject(c.Request.Context(), projectName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"ok":    false,
			"error": "Project not found",
		})
		return
	}

	// Verify video file exists
	if _, err := os.Stat(req.VideoPath); os.IsNotExist(err) {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Video file not found: " + req.VideoPath,
		})
		return
	}

	logger.Info("Video added to project",
		zap.String("project", projectName),
		zap.String("video", req.VideoPath),
	)

	c.JSON(http.StatusCreated, gin.H{
		"ok":         true,
		"video_path": req.VideoPath,
		"source_url": req.SourceURL,
	})
}

// ListVideos lists videos in a project
func (h *StockProjectHandler) ListVideos(c *gin.Context) {
	projectName := c.Param("name")
	
	project, err := h.manager.GetProject(c.Request.Context(), projectName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"ok":    false,
			"error": "Project not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":         true,
		"project":    project.Name,
		"video_count": project.VideoCount,
		"status":     project.Status,
	})
}

// DeleteVideo deletes a video from a project
func (h *StockProjectHandler) DeleteVideo(c *gin.Context) {
	projectName := c.Param("name")
	videoID := c.Param("id")

	_, err := h.manager.GetProject(c.Request.Context(), projectName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"ok":    false,
			"error": "Project not found",
		})
		return
	}

	logger.Info("Video deleted from project",
		zap.String("project", projectName),
		zap.String("video_id", videoID),
	)

	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"message":  "Video deleted",
		"video_id": videoID,
	})
}

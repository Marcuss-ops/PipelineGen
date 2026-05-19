package google_accounting

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"velox/go-master/internal/config"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Handler handles requests to the Google Accounting service
type Handler struct {
	cfg *config.Config
	log *zap.Logger
}

// NewHandler creates a new Google Accounting handler
func NewHandler(cfg *config.Config, log *zap.Logger) *Handler {
	return &Handler{
		cfg: cfg,
		log: log.Named("google_accounting"),
	}
}

// ListProjects lists available Google Vids projects
func (h *Handler) ListProjects(c *gin.Context) {
	resp, err := http.Get(fmt.Sprintf("%s/list", h.cfg.GoogleAccounting.ServerURL))
	if err != nil {
		h.log.Error("failed to call google accounting service", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to call google accounting service"})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var data interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		c.Data(resp.StatusCode, "application/json", body)
		return
	}
	c.JSON(resp.StatusCode, data)
}

// SyncProject triggers export and download for a project
func (h *Handler) SyncProject(c *gin.Context) {
	videoID := c.Query("video_id")
	if videoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "video_id is required"})
		return
	}

	resp, err := http.Post(fmt.Sprintf("%s/sync?video_id=%s", h.cfg.GoogleAccounting.ServerURL, videoID), "application/json", nil)
	if err != nil {
		h.log.Error("failed to call google accounting service", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to call google accounting service"})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var data interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		c.Data(resp.StatusCode, "application/json", body)
		return
	}
	c.JSON(resp.StatusCode, data)
}

// RegisterRoutes registers the handler routes
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/google-accounting")
	{
		group.GET("/list", h.ListProjects)
		group.POST("/sync", h.SyncProject)
	}
}

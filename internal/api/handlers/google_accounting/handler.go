package google_accounting

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"velox/go-master/internal/config"
	"velox/go-master/internal/pkg/googleaccounting"
	"velox/go-master/internal/pkg/mediascan"

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
	resp, err := http.Get(fmt.Sprintf("%s/list", strings.TrimRight(h.cfg.GoogleAccounting.ServerURL, "/")))
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

	payload, err := json.Marshal(googleaccounting.DownloadRequest{
		VideoID:  videoID,
		FileType: c.DefaultQuery("file_type", "all"),
		Headless: c.DefaultQuery("headless", "true") != "false",
		Account:  c.Query("account"),
	})
	if err != nil {
		h.log.Error("failed to marshal google accounting request", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare request"})
		return
	}

	req, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("%s/download", strings.TrimRight(h.cfg.GoogleAccounting.ServerURL, "/")),
		bytes.NewReader(payload),
	)
	if err != nil {
		h.log.Error("failed to call google accounting service", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to call google accounting service"})
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
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

// GenerateVideo triggers Google Vids AI video generation for a real project.
func (h *Handler) GenerateVideo(c *gin.Context) {
	var reqBody googleaccounting.GenerateRequest
	if err := c.ShouldBindJSON(&reqBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}
	if reqBody.VideoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "video_id is required"})
		return
	}
	if reqBody.Prompt == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "prompt is required"})
		return
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		h.log.Error("failed to marshal google accounting request", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare request"})
		return
	}

	req, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("%s/generate-vids-video", strings.TrimRight(h.cfg.GoogleAccounting.ServerURL, "/")),
		bytes.NewReader(payload),
	)
	if err != nil {
		h.log.Error("failed to call google accounting service", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to call google accounting service"})
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
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

// GenerateFlowImages triggers Google Labs Flow image generation.
func (h *Handler) GenerateFlowImages(c *gin.Context) {
	var reqBody googleaccounting.FlowImageRequest
	if err := c.ShouldBindJSON(&reqBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}
	if reqBody.Prompt == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "prompt is required"})
		return
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		h.log.Error("failed to marshal google accounting request", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare request"})
		return
	}

	req, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("%s/generate-flow-images", strings.TrimRight(h.cfg.GoogleAccounting.ServerURL, "/")),
		bytes.NewReader(payload),
	)
	if err != nil {
		h.log.Error("failed to call google accounting service", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to call google accounting service"})
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
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

// JobStatus proxies the status of a Google Accounting background job.
func (h *Handler) JobStatus(c *gin.Context) {
	jobID := c.Param("job_id")
	if jobID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "job_id is required"})
		return
	}

	resp, err := http.Get(fmt.Sprintf("%s/status/%s", strings.TrimRight(h.cfg.GoogleAccounting.ServerURL, "/"), jobID))
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

// ListMedia exposes generated Google media files with browser-openable URLs.
func (h *Handler) ListMedia(c *gin.Context) {
	root := h.cfg.GoogleAccounting.DownloadDir
	if root == "" {
		c.JSON(http.StatusOK, gin.H{"count": 0, "files": []mediascan.MediaFile{}})
		return
	}

	files, err := mediascan.ScanDirectory(root, "/media/google-accounting/")
	if err != nil {
		h.log.Error("failed to scan media directory", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to scan media directory"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"count": len(files),
		"files": files,
	})
}

// RegisterRoutes registers the handler routes
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/google-accounting")
	{
		group.GET("/list", h.ListProjects)
		group.POST("/sync", h.SyncProject)
		group.POST("/download", h.SyncProject)
		group.POST("/generate-video", h.GenerateVideo)
		group.POST("/generate-flow-images", h.GenerateFlowImages)
		group.GET("/media", h.ListMedia)
		group.GET("/status/:job_id", h.JobStatus)
	}
}

package google_accounting

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"velox/go-master/internal/config"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Handler handles requests to the Google Accounting service
type Handler struct {
	cfg *config.Config
	log *zap.Logger
}

type downloadRequest struct {
	VideoID  string `json:"video_id"`
	FileType string `json:"file_type"`
	Headless bool   `json:"headless"`
	Account  string `json:"account,omitempty"`
}

type generateVideoRequest struct {
	VideoID  string `json:"video_id"`
	Prompt   string `json:"prompt"`
	Headless bool   `json:"headless"`
	Account  string `json:"account,omitempty"`
}

type generateFlowImagesRequest struct {
	Prompt    string `json:"prompt"`
	ProjectID string `json:"project_id,omitempty"`
	Style     string `json:"style,omitempty"`
	Headless  bool   `json:"headless"`
	Account   string `json:"account,omitempty"`
}

type mediaFileResponse struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	URL     string    `json:"url"`
	Kind    string    `json:"kind"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
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

	payload, err := json.Marshal(downloadRequest{
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
	var reqBody generateVideoRequest
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
	var reqBody generateFlowImagesRequest
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
		c.JSON(http.StatusOK, gin.H{"count": 0, "files": []mediaFileResponse{}})
		return
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve media directory"})
		return
	}

	var files []mediaFileResponse
	allowedExt := map[string]string{
		".mp4":  "video",
		".mov":  "video",
		".mkv":  "video",
		".webm": "video",
		".jpg":  "image",
		".jpeg": "image",
		".png":  "image",
		".webp": "image",
	}

	_ = filepath.WalkDir(absRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d == nil || d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		kind, ok := allowedExt[ext]
		if !ok {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}

		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return nil
		}
		urlPath := "/media/google-accounting/" + filepath.ToSlash(rel)
		files = append(files, mediaFileResponse{
			Name:    d.Name(),
			Path:    path,
			URL:     urlPath,
			Kind:    kind,
			Size:    info.Size(),
			ModTime: info.ModTime().UTC(),
		})
		return nil
	})

	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime.After(files[j].ModTime)
	})

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

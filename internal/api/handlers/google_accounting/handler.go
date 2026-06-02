package google_accounting

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"velox/go-master/internal/config"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/images"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/pkg/googleaccounting"
	"velox/go-master/internal/pkg/mediascan"
	"velox/go-master/internal/pkg/ptrutil"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// VideoGenJobResponse is the JSON response returned by the status endpoint.
type VideoGenJobResponse struct {
	JobID    string `json:"job_id"`
	Status   string `json:"status"`
	Prompt   string `json:"prompt"`
	Style    string `json:"style,omitempty"`
	FilePath string `json:"file_path,omitempty"`
	Error    string `json:"error,omitempty"`
	Created  string `json:"created_at"`
	Updated  string `json:"updated_at"`
}

// Handler handles requests to the Google Accounting service
type Handler struct {
	cfg         *config.Config
	log         *zap.Logger
	imgService  *images.Service
	jobsService *jobservice.Service
	veloxDB     *sql.DB
}

// NewHandler creates a new Google Accounting handler
func NewHandler(cfg *config.Config, log *zap.Logger, imgService *images.Service, jobsService *jobservice.Service, veloxDB *sql.DB) *Handler {
	return &Handler{
		cfg:         cfg,
		log:         log.Named("google_accounting"),
		imgService:  imgService,
		jobsService: jobsService,
		veloxDB:     veloxDB,
	}
}

// jobToResponse maps a models.Job to the external VideoGenJobResponse.
func jobToResponse(job *models.Job) *VideoGenJobResponse {
	resp := &VideoGenJobResponse{
		JobID:   job.ID,
		Status:  string(job.Status),
		Created: job.CreatedAt.Format(time.RFC3339),
		Updated: job.UpdatedAt.Format(time.RFC3339),
	}

	var payload struct {
		Prompt string `json:"prompt"`
		Style  string `json:"style"`
	}
	if len(job.Payload) > 0 {
		json.Unmarshal(job.Payload, &payload)
	}
	resp.Prompt = payload.Prompt
	resp.Style = payload.Style

	if job.Result != nil {
		if fp, ok := job.Result["file_path"].(string); ok {
			resp.FilePath = fp
		}
	}
	if job.Error != "" {
		resp.Error = job.Error
	}
	return resp
}

// ListProjects lists available Google Vids projects
func (h *Handler) ListProjects(c *gin.Context) {
	h.proxyGoogleAccounting(c, http.MethodGet, "/list", nil)
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
		Headless: ptrutil.Bool(c.DefaultQuery("headless", "true") != "false"),
		Account:  c.Query("account"),
	})
	if err != nil {
		h.log.Error("failed to marshal google accounting request", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare request"})
		return
	}

	h.proxyGoogleAccounting(c, http.MethodPost, "/download", payload)
}

// GenerateVideo avvia la generazione video in background, crea un job persistente su SQLite e ritorna subito.
// Usa GET /api/google-accounting/generate-video/status/:job_id per il risultato.
func (h *Handler) GenerateVideo(c *gin.Context) {
	var reqBody googleaccounting.GenerateRequest
	if err := c.ShouldBindJSON(&reqBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}
	if reqBody.Prompt == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "prompt is required"})
		return
	}

	if h.imgService == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "image service not available"})
		return
	}

	payload := map[string]any{
		"prompt": reqBody.Prompt,
		"style":  reqBody.Style,
	}

	job, err := h.jobsService.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
		Type:    models.JobTypeVideoGenerate,
		Payload: payload,
	})
	if err != nil {
		h.log.Error("failed to create video job", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create job"})
		return
	}

	// Background pipeline: Python → wait → Drive upload → DB registration
	// context.Background() is intentional here: this is a long-running background job
	// (minutes) with DB persistence. Cancelling it when the HTTP request ends would
	// silently abort video generation. The client polls /status/:job_id separately.
	go func(ctx context.Context, svc *images.Service, prompt, style, jid string) {
		if err := h.jobsService.SetRunning(ctx, jid); err != nil {
			h.log.Error("failed to mark job running", zap.String("job_id", jid), zap.Error(err))
		}

		filePath, err := svc.GenerateVideoAI(ctx, prompt, style)
		if err != nil {
			h.log.Error("background video generation failed", zap.String("job_id", jid), zap.Error(err))
			h.jobsService.Fail(ctx, jid, err)
			return
		}

		h.jobsService.Complete(ctx, jid, map[string]any{
			"file_path": filePath,
		})
		h.log.Info("background video generation completed", zap.String("job_id", jid), zap.String("file_path", filePath))
	}(context.Background(), h.imgService, reqBody.Prompt, reqBody.Style, job.ID)

	c.JSON(http.StatusAccepted, gin.H{
		"job_id":  job.ID,
		"status":  "pending",
		"message": "Video generation started. Poll /api/google-accounting/generate-video/status/" + job.ID + " for result.",
	})
}

// GenerateVideoStatus returns the status of an async video generation job from the persistent jobs table.
func (h *Handler) GenerateVideoStatus(c *gin.Context) {
	jobID := c.Param("job_id")
	if jobID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "job_id is required"})
		return
	}

	job, err := h.jobsService.Get(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}

	c.JSON(http.StatusOK, jobToResponse(job))
}

// GenerateAvatar avvia la generazione avatar in background, crea un job persistente su SQLite e ritorna subito.
func (h *Handler) GenerateAvatar(c *gin.Context) {
	var reqBody googleaccounting.AvatarRequest
	if err := c.ShouldBindJSON(&reqBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}
	if reqBody.Script == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "script is required"})
		return
	}

	if h.imgService == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "image service not available"})
		return
	}

	avatarID := reqBody.AvatarID
	if avatarID == "" {
		avatarID = "James"
	}

	payload := map[string]any{
		"script":    reqBody.Script,
		"avatar_id": avatarID,
	}

	job, err := h.jobsService.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
		Type:    models.JobTypeVideoGenerate,
		Payload: payload,
	})
	if err != nil {
		h.log.Error("failed to create avatar job", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create job"})
		return
	}

	// context.Background() is intentional: avatar generation is a long-running job
	// persisted in the DB. Cancelling it on HTTP request end would silently abort work.
	go func(ctx context.Context, svc *images.Service, script, avatarID, jid string) {
		if err := h.jobsService.SetRunning(ctx, jid); err != nil {
			h.log.Error("failed to mark job running", zap.String("job_id", jid), zap.Error(err))
		}

		filePath, err := svc.GenerateAvatarVideo(ctx, script, avatarID)
		if err != nil {
			h.log.Error("background avatar generation failed", zap.String("job_id", jid), zap.Error(err))
			h.jobsService.Fail(ctx, jid, err)
			return
		}

		h.jobsService.Complete(ctx, jid, map[string]any{
			"file_path": filePath,
		})
		h.log.Info("background avatar generation completed", zap.String("job_id", jid), zap.String("file_path", filePath))
	}(context.Background(), h.imgService, reqBody.Script, avatarID, job.ID)

	c.JSON(http.StatusAccepted, gin.H{
		"job_id":  job.ID,
		"status":  "pending",
		"message": "Avatar generation started. Poll /api/google-accounting/avatar/status/" + job.ID + " for result.",
	})
}

// ListVideos returns all generated videos from media_assets with google-vids sources.
func (h *Handler) ListVideos(c *gin.Context) {
	rows, err := h.veloxDB.QueryContext(c.Request.Context(),
		`SELECT id, name, source, drive_link, drive_file_id, metadata_json, created_at
		 FROM media_assets
		 WHERE source IN ('google-vids', 'google-vids-avatar')
		 ORDER BY created_at DESC`)
	if err != nil {
		h.log.Error("failed to query video assets", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list videos"})
		return
	}
	defer rows.Close()

	type VideoItem struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Source    string `json:"source"`
		DriveLink string `json:"drive_link"`
		Prompt    string `json:"prompt"`
		Style     string `json:"style,omitempty"`
		CreatedAt string `json:"created_at"`
	}

	var videos []VideoItem
	for rows.Next() {
		var id, name, source, driveLink, driveFileID, metaJSON, createdAt string
		if err := rows.Scan(&id, &name, &source, &driveLink, &driveFileID, &metaJSON, &createdAt); err != nil {
			h.log.Warn("failed to scan video row", zap.Error(err))
			continue
		}

		item := VideoItem{
			ID:        id,
			Name:      name,
			Source:    source,
			DriveLink: driveLink,
			CreatedAt: createdAt,
		}

		if metaJSON != "" && metaJSON != "{}" {
			var meta map[string]any
			if err := json.Unmarshal([]byte(metaJSON), &meta); err == nil {
				if p, ok := meta["prompt"].(string); ok {
					item.Prompt = p
				}
				if s, ok := meta["style"].(string); ok {
					item.Style = s
				}
			}
		}

		videos = append(videos, item)
	}

	if videos == nil {
		videos = []VideoItem{}
	}

	c.JSON(http.StatusOK, gin.H{
		"count":  len(videos),
		"videos": videos,
	})
}

// GenerateFlowImages is kept for compatibility, but the current Python backend
// exposes the Vids image synthesis endpoint, so we remap the request there.
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

	driveFolderID := h.cfg.Drive.ImagesFolder()

	vidsPayload, err := json.Marshal(googleaccounting.VidsImageRequest{
		VideoID:       reqBody.ProjectID,
		Prompt:        reqBody.Prompt,
		Style:         reqBody.Style,
		Headless:      reqBody.Headless,
		Account:       reqBody.Account,
		DriveFolderID: driveFolderID,
	})
	if err != nil {
		h.log.Error("failed to marshal vids image request", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare request"})
		return
	}

	h.proxyGoogleAccounting(c, http.MethodPost, "/generate-vids-images", vidsPayload)
}

// JobStatus proxies the status of a Google Accounting background job.
func (h *Handler) JobStatus(c *gin.Context) {
	jobID := c.Param("job_id")
	if jobID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "job_id is required"})
		return
	}

	h.proxyGoogleAccounting(c, http.MethodGet, "/status/"+jobID, nil)
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

// GenerateVidsImage generates an image via Google Vids Image Synthesis
func (h *Handler) GenerateVidsImage(c *gin.Context) {
	var reqBody googleaccounting.VidsImageRequest
	if err := c.ShouldBindJSON(&reqBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}
	if reqBody.Prompt == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "prompt is required"})
		return
	}

	if strings.TrimSpace(reqBody.DriveFolderID) == "" {
		reqBody.DriveFolderID = h.cfg.Drive.ImagesFolder()
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		h.log.Error("failed to marshal vids image request", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare request"})
		return
	}

	h.proxyGoogleAccounting(c, http.MethodPost, "/generate-vids-images", payload)
}

// RegisterRoutes registers the handler routes
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg
	{
		group.GET("/list", h.ListProjects)
		group.POST("/sync", h.SyncProject)
		group.POST("/download", h.SyncProject)
		group.POST("/generate-video", h.GenerateVideo)
		group.GET("/generate-video/status/:job_id", h.GenerateVideoStatus)
		group.POST("/generate-avatar-video", h.GenerateAvatar)
		group.GET("/avatar/status/:job_id", h.GenerateVideoStatus)
		group.GET("/videos", h.ListVideos)
		group.POST("/generate-flow-images", h.GenerateFlowImages)
		group.POST("/generate-vids-images", h.GenerateVidsImage)
		group.GET("/media", h.ListMedia)
		group.GET("/status/:job_id", h.JobStatus)
	}
}

func (h *Handler) proxyGoogleAccounting(c *gin.Context, method, path string, body []byte) {
	contentType := ""
	if len(body) > 0 {
		contentType = "application/json"
	}

	resp, err := h.doGoogleAccountingRequest(c, method, path, body, contentType)
	if err != nil {
		h.log.Error("failed to call google accounting service", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to call google accounting service"})
		return
	}
	defer resp.Body.Close()

	writeGoogleAccountingResponse(c, resp)
}

func (h *Handler) doGoogleAccountingRequest(c *gin.Context, method, path string, body []byte, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(c.Request.Context(), method, fmt.Sprintf("%s%s", strings.TrimRight(h.cfg.GoogleAccounting.ServerURL, "/"), path), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return http.DefaultClient.Do(req)
}

func writeGoogleAccountingResponse(c *gin.Context, resp *http.Response) {
	body, _ := io.ReadAll(resp.Body)
	var data interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		c.Data(resp.StatusCode, "application/json", body)
		return
	}
	c.JSON(resp.StatusCode, data)
}

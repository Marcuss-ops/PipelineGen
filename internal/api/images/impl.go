package images

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/api"

	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/media/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type ImagesHandler struct {
	service   *imgservice.Service
	ingestSvc *ingest.Service
}

func NewImagesHandler(service *imgservice.Service) *ImagesHandler {
	return &ImagesHandler{service: service}
}

func (h *ImagesHandler) SetIngestService(svc *ingest.Service) {
	h.ingestSvc = svc
}

func (h *ImagesHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/search", h.Search)
	r.GET("/diagnostics", h.Diagnostics)
	r.POST("/upload", h.Upload)
	r.POST("/sync", h.Sync)
	r.POST("/generate", h.Generate)
	r.POST("/animate", h.Animate)
	r.POST("/webhook/remote", h.ReceiveRemoteWebhook) // Webhook per immagini da remote Google Flow
}

type UploadRequest struct {
	Subject string   `json:"subject" binding:"required"`
	Name    string   `json:"name"`
	URL     string   `json:"image_url" binding:"required"`
	Lang    string   `json:"lang"`
	Tags    []string `json:"tags"`
}

type GenerateNvidiaRequest struct {
	Prompt string   `json:"prompt" binding:"required"`
	Model  string   `json:"model"`
	Width  int      `json:"width"`
	Height int      `json:"height"`
	Style  string   `json:"style" example:"medievale"`
	Tags   []string `json:"tags"`
}

type AnimateRequest struct {
	ImageHash string `json:"image_hash" binding:"required"`
	Duration  int    `json:"duration"`
}

// Upload permette di aggiungere manualmente un'immagine tramite URL
func (h *ImagesHandler) Upload(c *gin.Context) {
	var req UploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, err.Error())
		return
	}

	if req.Name == "" {
		req.Name = req.Subject
	}

	slug := strings.ReplaceAll(strings.ToLower(req.Subject), " ", "-")

	if h.ingestSvc != nil && req.URL != "" {
		res, err := h.ingestSvc.Ingest(c.Request.Context(), &ingest.Request{
			Kind:   string(ingest.KindImage),
			URL:    req.URL,
			Name:   req.Name,
			Group:  slug,
			Tags:   req.Tags,
			Source: "upload",
		})
		if err != nil {
			api.InternalError(c, err)
			return
		}
		api.OK(c, res)
		return
	}

	// Fallback
	asset, err := h.service.SearchAndDownload(c.Request.Context(), slug, req.Name, req.URL, req.Lang, req.Tags)
	if err != nil {
		api.InternalError(c, err)
		return
	}

	api.OK(c, asset)
}

// Search cerca un'immagine per un soggetto, scaricandola se non esiste
func (h *ImagesHandler) Search(c *gin.Context) {
	query := c.Query("q")
	lang := c.DefaultQuery("lang", "it")
	if query == "" {
		api.BadRequest(c, "missing query parameter 'q'")
		return
	}

	// Proviamo a cercare/scaricare
	slug := strings.ReplaceAll(strings.ToLower(query), " ", "-")
	asset, err := h.service.SearchAndDownload(c.Request.Context(), slug, query, query, lang, nil)
	if err != nil {
		api.InternalError(c, err)
		return
	}

	api.OK(c, gin.H{
		"subject": query,
		"image": gin.H{
			"hash":       asset.Hash,
			"path_rel":   asset.PathRel,
			"source_url": asset.SourceURL,
			"url_full":   "/assets/" + asset.PathRel,
			"desc":       asset.Description,
			"tags":       asset.Tags,
		},
	})
}

// Sync avvia la sincronizzazione manuale del file system e di Drive
func (h *ImagesHandler) Sync(c *gin.Context) {
	ctx := c.Request.Context()

	// 1. Local Sync
	if err := h.service.SyncAssets(); err != nil {
		api.InternalError(c, err)
		return
	}

	// 2. Drive Sync
	if err := h.service.SyncFromDrive(ctx); err != nil {
		api.InternalError(c, err)
		return
	}

	api.OK(c, gin.H{"message": "Synchronization complete (Local + Drive)"})
}

// Generate genera un'immagine AI: prova Google Flow (primario), fallback NVIDIA.
func (h *ImagesHandler) Generate(c *gin.Context) {
	var req GenerateNvidiaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, err.Error())
		return
	}

	// Create a long-lived context for AI generation
	ctx, cancel := context.WithTimeout(c.Request.Context(), 6*time.Minute)
	defer cancel()

	// Default to 1920x1080 for YouTube format if not specified
	if req.Width == 0 {
		req.Width = 1920
	}
	if req.Height == 0 {
		req.Height = 1080
	}

	// Always upload to Drive via the common pipeline
	skipDrive := false
	asset, err := h.service.GenerateSmartImage(
		ctx,
		req.Prompt,           // subject
		"",                   // topic (vuoto, usiamo solo il prompt)
		req.Style,            // style
		[]string{req.Prompt}, // prompts
		req.Tags,
		req.Width,
		req.Height,
		req.Model,
		skipDrive,
	)
	if err != nil {
		api.InternalError(c, err)
		return
	}

	api.OK(c, gin.H{
		"prompt": req.Prompt,
		"model":  req.Model,
		"style":  req.Style,
		"image": gin.H{
			"hash":          asset.Hash,
			"path_rel":      asset.PathRel,
			"source_url":    asset.SourceURL,
			"url_full":      "/assets/" + asset.PathRel,
			"desc":          asset.Description,
			"tags":          asset.Tags,
			"drive_link":    drive.FileURLFromID(asset.DriveFileID),
			"drive_file_id": asset.DriveFileID,
		},
	})
}

// Animate crea un video zoom-out da un'immagine esistente
func (h *ImagesHandler) Animate(c *gin.Context) {
	var req AnimateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, err.Error())
		return
	}

	if req.Duration <= 0 {
		req.Duration = 7
	}

	outputPath, err := h.service.AnimateImage(c.Request.Context(), req.ImageHash, req.Duration)
	if err != nil {
		api.InternalError(c, err)
		return
	}

	api.OK(c, gin.H{
		"image_hash":  req.ImageHash,
		"duration":    req.Duration,
		"output_path": outputPath,
		"message":     "Animation created successfully",
	})
}

// Diagnostics reports the local state of the image generation and animation wiring.
func (h *ImagesHandler) Diagnostics(c *gin.Context) {
	if h.service == nil {
		api.InternalError(c, fmt.Errorf("image service not configured"))
		return
	}

	api.OK(c, h.service.Diagnostics())
}

// RemoteWebhookJobJSON is the JSON payload sent by the remote worker alongside image files.
type RemoteWebhookJobJSON struct {
	JobID            string   `json:"job_id"`
	Status           string   `json:"status"`
	Prompt           string   `json:"prompt"`
	ProjectID        string   `json:"project_id"`
	Model            string   `json:"model,omitempty"`
	Error            string   `json:"error,omitempty"`
	DownloadedImages []string `json:"downloaded_images,omitempty"`
}

// ReceiveRemoteWebhook handles POST /api/images/webhook/remote
// The remote worker sends image files as multipart form data with a job_json field.
// This endpoint receives the images, saves them locally, and triggers the ingest pipeline.
func (h *ImagesHandler) ReceiveRemoteWebhook(c *gin.Context) {
	ctx := c.Request.Context()

	// Parse multipart form (max 500MB per image, allow many files)
	if err := c.Request.ParseMultipartForm(500 << 20); err != nil {
		api.BadRequest(c, fmt.Sprintf("failed to parse multipart form: %v", err))
		return
	}

	// Extract job metadata from job_json field
	jobJSONStr := c.PostForm("job_json")
	if jobJSONStr == "" {
		api.BadRequest(c, "missing job_json field")
		return
	}

	var jobData RemoteWebhookJobJSON
	if err := json.Unmarshal([]byte(jobJSONStr), &jobData); err != nil {
		api.BadRequest(c, fmt.Sprintf("failed to parse job_json: %v", err))
		return
	}

	if jobData.JobID == "" {
		api.BadRequest(c, "job_json missing job_id")
		return
	}

	// Extract style from project_id (format: "velox-{style}" or UUID for no-style)
	style := ""
	if strings.HasPrefix(jobData.ProjectID, "velox-") {
		style = strings.TrimPrefix(jobData.ProjectID, "velox-")
		// If style looks like a UUID (4+ dashes), treat as no-style
		if strings.Count(style, "-") >= 4 {
			style = ""
		}
	}

	// Collect image files from multipart
	form := c.Request.MultipartForm
	var imageFiles []*multipart.FileHeader
	if form != nil && form.File != nil {
		for filename := range form.File {
			lower := strings.ToLower(filename)
			if strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg") ||
				strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".webp") {
				if fhs, ok := form.File[filename]; ok && len(fhs) > 0 {
					imageFiles = append(imageFiles, fhs[0])
				}
			}
		}
	}

	if len(imageFiles) == 0 {
		api.BadRequest(c, "no image files found in webhook request")
		return
	}

	h.service.Log().Info("ReceiveRemoteWebhook: received images from remote",
		zap.String("job_id", jobData.JobID),
		zap.Int("image_count", len(imageFiles)),
		zap.String("prompt", jobData.Prompt),
		zap.String("style", style),
	)

	var ingestedAssets []map[string]any

	// Process each image file through the ingest pipeline
	for i, fh := range imageFiles {
		src, err := fh.Open()
		if err != nil {
			h.service.Log().Warn("ReceiveRemoteWebhook: failed to open uploaded file",
				zap.String("filename", fh.Filename),
				zap.Error(err),
			)
			continue
		}

		content, err := io.ReadAll(src)
		src.Close()
		if err != nil {
			h.service.Log().Warn("ReceiveRemoteWebhook: failed to read uploaded file",
				zap.String("filename", fh.Filename),
				zap.Error(err),
			)
			continue
		}

		// Determine slug from prompt
		slug := textutil.Slugify(jobData.Prompt)
		if len(slug) > 50 {
			slug = slug[:50]
		}

		// Use the original filename from the remote worker
		filename := fh.Filename
		if filename == "" {
			filename = fmt.Sprintf("remote_%s_%d.jpg", jobData.JobID[:8], i)
		}

		description := fmt.Sprintf("AI generated image via Google Flow for prompt: %s", jobData.Prompt)
		generator := "google-flow"
		tags := []string{"remote", "google-flow", "ai-generated"}

		// Ingest through the full pipeline: local storage + metadata + Drive upload + Qdrant
		asset, ingestErr := h.service.IngestImage(
			ctx,
			slug,
			style,
			jobData.JobID,
			bytes.NewReader(content),
			filename,
			generator,
			description,
			tags,
			false, // skipDrive = false → upload to Drive
			false, // isURL = false, we have the content directly
		)

		if ingestErr != nil {
			h.service.Log().Error("ReceiveRemoteWebhook: ingest failed for file",
				zap.String("filename", fh.Filename),
				zap.Error(ingestErr),
			)
			continue
		}

		ingestedAssets = append(ingestedAssets, map[string]any{
			"hash":          asset.Hash,
			"path_rel":      asset.PathRel,
			"filename":      filename,
			"drive_file_id": asset.DriveFileID,
			"description":   description,
		})

		h.service.Log().Info("ReceiveRemoteWebhook: successfully ingested image",
			zap.String("filename", filename),
			zap.String("hash", asset.Hash),
			zap.String("drive_file_id", asset.DriveFileID),
		)
	}

	if len(ingestedAssets) == 0 {
		api.InternalError(c, fmt.Errorf("failed to ingest any of the %d received images", len(imageFiles)))
		return
	}

	api.OK(c, gin.H{
		"job_id":  jobData.JobID,
		"status":  jobData.Status,
		"prompt":  jobData.Prompt,
		"images":  ingestedAssets,
		"count":   len(ingestedAssets),
		"message": fmt.Sprintf("Received and ingested %d image(s) from remote Google Flow", len(ingestedAssets)),
	})
}

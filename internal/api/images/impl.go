package images

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/generated"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	domainjob "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type ImagesHandler struct {
	service   *imgservice.Service
	ingestSvc *ingest.Service
	jobsSvc   domainjob.Service
}

func NewImagesHandler(service *imgservice.Service, ingestSvc *ingest.Service, jobsSvc domainjob.Service) *ImagesHandler {
	return &ImagesHandler{service: service, ingestSvc: ingestSvc, jobsSvc: jobsSvc}
}

func (h *ImagesHandler) RegisterRoutes(r *gin.RouterGroup) {
	// Step 10 (territory-separated search + generate endpoints).
	// /search is REPLACED by TerritorySearch (defaults to
	// territory=retrieved for back-compat with callers that used
	// /search?q=X).
	r.GET("/search", h.TerritorySearch)
	r.GET("/retrieved/search", h.RetrievedSearch)
	r.GET("/generated/search", h.GeneratedSearch)
	r.POST("/generated/generate", h.GeneratedGenerate)
	r.GET("/generated/styles", h.GeneratedStyles)

	// Existing endpoints (unchanged).
	r.GET("/diagnostics", h.Diagnostics)
	r.POST("/upload", h.Upload)
	r.POST("/sync", h.Sync)
	r.POST("/generate", h.Generate)
	r.POST("/batch-generate", h.GenerateBatch)
	r.POST("/animate", h.Animate)
	r.POST("/webhook/remote", h.ReceiveRemoteWebhook)
}

type UploadRequest struct {
	Subject string   `json:"subject" binding:"required"`
	Name    string   `json:"name"`
	URL     string   `json:"image_url" binding:"required"`
	Lang    string   `json:"lang"`
	Tags    []string `json:"tags"`
}

// GenerateImageRequest is the request type for POST /api/images/generate.
type GenerateImageRequest struct {
	Prompt    string   `json:"prompt" binding:"required"`
	Width     int      `json:"width"`
	Height    int      `json:"height"`
	Style     string   `json:"style" example:"medievale"`
	Tags      []string `json:"tags"`
	Account   string   `json:"account,omitempty"`
	ProjectID string   `json:"project_id,omitempty"`
}

// GenerateBatchRequest is the async batch image generation request (FASE 3, June 2026).
// Each item becomes an independent image.generate.google job; concurrency is
// controlled server-side by the worker pool, not by the client.
type GenerateBatchRequest struct {
	// RequestID is an optional caller-supplied identifier for correlation.
	RequestID string `json:"request_id,omitempty"`
	// Items is the list of images to generate (required, min 1).
	Items []GenerateBatchItem `json:"items" binding:"required,min=1"`
}

// GenerateBatchItem describes a single image to generate in a batch.
type GenerateBatchItem struct {
	// Prompt is the natural-language description of the desired image.
	Prompt string `json:"prompt" binding:"required"`
	// Style is the visual style (e.g. "cinematic", "anime").
	Style string `json:"style,omitempty"`
	// Width and Height are the desired output dimensions (default 1920x1080).
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
	// Tags are metadata labels to attach to the generated asset.
	Tags []string `json:"tags,omitempty"`
}

type AnimateRequest struct {
	ImageHash string `json:"image_hash" binding:"required"`
	Duration  int    `json:"duration"`
}

// generateBatchID creates a short unique batch identifier.
func generateBatchID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("imgbatch_%d", time.Now().UnixNano())
	}
	return "imgbatch_" + hex.EncodeToString(b)
}

// batchJobResponse is the per-job entry in the 202 response.
type batchJobResponse struct {
	JobID    string `json:"job_id"`
	Position int    `json:"position"`
	Status   string `json:"status"`
}

// Upload permette di aggiungere manualmente un'immagine tramite URL
func (h *ImagesHandler) Upload(c *gin.Context) {
	var req UploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
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
			apiutil.InternalError(c, err)
			return
		}
		apiutil.OK(c, res)
		return
	}

	// Fallback
	asset, err := h.service.SearchAndDownload(c.Request.Context(), slug, req.Name, req.URL, req.Lang, req.Tags)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, asset)
}

// Sync avvia la sincronizzazione manuale del file system e di Drive
func (h *ImagesHandler) Sync(c *gin.Context) {
	ctx := c.Request.Context()

	// 1. Local Sync
	if err := h.service.SyncAssets(); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	// 2. Drive Sync
	if err := h.service.SyncFromDrive(ctx); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{"message": "Synchronization complete (Local + Drive)"})
}

// Generate genera un'immagine AI tramite Chrome/Playwright (Google Slides).
func (h *ImagesHandler) Generate(c *gin.Context) {
	var req GenerateImageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	asset, err := h.service.GenerateSmartImageWithAccount(
		c.Request.Context(),
		"", // subject
		"", // topic
		req.Style,
		[]string{req.Prompt},
		req.Tags,
		req.Width,
		req.Height,
		generated.CanonicalGoogleSlidesModel,
		false, // skipDrive = false
		req.Account,
		req.ProjectID,
	)
	if err != nil {
		if errors.Is(err, imgservice.ErrImageGenNotImplemented) {
			c.AbortWithStatusJSON(http.StatusNotImplemented, gin.H{
				"error":   "image generation endpoint has been removed",
				"message": err.Error(),
			})
			return
		}
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, asset)
}

// GenerateBatch accetta un batch di prompt e li trasforma in job asincroni.
// Ogni item diventa un job image.generate.google indipendente.
// La risposta è 202 Accepted con batch_id e lista dei job creati.
//
// FASE 3 (June 2026): la vecchia implementazione sincrona (Google Slides API)
// è stata rimossa. Questo endpoint ora orchestra il sistema di job asincroni.
func (h *ImagesHandler) GenerateBatch(c *gin.Context) {
	var req GenerateBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	if h.jobsSvc == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
			"error": "job service not wired — image generation requires the async job system",
		})
		return
	}

	batchID := generateBatchID()
	if req.RequestID != "" {
		batchID = req.RequestID + "_" + batchID
	}

	// Apply defaults
	for i := range req.Items {
		if req.Items[i].Width == 0 {
			req.Items[i].Width = 1920
		}
		if req.Items[i].Height == 0 {
			req.Items[i].Height = 1080
		}
	}

	jobs := make([]batchJobResponse, len(req.Items))
	for i, item := range req.Items {
		position := i
		correlationID := fmt.Sprintf("%s_%d", batchID, position)

		payload := map[string]any{
			"batch_id": batchID,
			"position": position,
			"prompt":   item.Prompt,
			"style":    item.Style,
			"width":    item.Width,
			"height":   item.Height,
			"tags":     item.Tags,
		}

		enqueued, err := h.jobsSvc.Enqueue(c.Request.Context(), &domainjob.EnqueueRequest{
			Type:          appjobs.TypeImageGenerateGoogle,
			CorrelationID: correlationID,
			Payload:       payload,
			MaxRetries:    2,
		})
		if err != nil {
			apiutil.InternalError(c, fmt.Errorf("failed to enqueue job %d/%d: %w", i+1, len(req.Items), err))
			return
		}

		jobs[i] = batchJobResponse{
			JobID:    enqueued.ID,
			Position: position,
			Status:   string(enqueued.Status),
		}
	}

	c.JSON(http.StatusAccepted, gin.H{
		"batch_id": batchID,
		"accepted": len(jobs),
		"jobs":     jobs,
	})
}

// Animate crea un video zoom-out da un'immagine esistente (NVIDIA capability removed)
func (h *ImagesHandler) Animate(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusNotImplemented, gin.H{
		"error": "image animation capability not implemented (NVIDIA capability removed)",
	})
}

// Diagnostics reports the local state of the image generation and animation wiring.
func (h *ImagesHandler) Diagnostics(c *gin.Context) {
	if h.service == nil {
		apiutil.InternalError(c, fmt.Errorf("image service not configured"))
		return
	}

	apiutil.OK(c, h.service.Diagnostics())
}

// RemoteWebhookJobJSON is the JSON payload sent by the remote worker alongside image files.
type RemoteWebhookJobJSON struct {
	JobID            string   `json:"job_id"`
	Status           string   `json:"status"`
	Prompt           string   `json:"prompt"`
	ProjectID        string   `json:"project_id"`
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
		apiutil.BadRequest(c, fmt.Sprintf("failed to parse multipart form: %v", err))
		return
	}

	// Extract job metadata from job_json field
	jobJSONStr := c.PostForm("job_json")
	if jobJSONStr == "" {
		apiutil.BadRequest(c, "missing job_json field")
		return
	}

	var jobData RemoteWebhookJobJSON
	if err := json.Unmarshal([]byte(jobJSONStr), &jobData); err != nil {
		apiutil.BadRequest(c, fmt.Sprintf("failed to parse job_json: %v", err))
		return
	}

	if jobData.JobID == "" {
		apiutil.BadRequest(c, "job_json missing job_id")
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
		apiutil.BadRequest(c, "no image files found in webhook request")
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
		apiutil.InternalError(c, fmt.Errorf("failed to ingest any of the %d received images", len(imageFiles)))
		return
	}

	apiutil.OK(c, gin.H{
		"job_id":  jobData.JobID,
		"status":  jobData.Status,
		"prompt":  jobData.Prompt,
		"images":  ingestedAssets,
		"count":   len(ingestedAssets),
		"message": fmt.Sprintf("Received and ingested %d image(s) from remote Google Flow", len(ingestedAssets)),
	})
}

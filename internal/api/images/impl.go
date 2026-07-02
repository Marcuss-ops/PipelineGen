package images

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/generated"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	domainjob "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
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
	// surface-2 (July 2026): POST /webhook/remote retired. The remote
	// worker ingest pipeline collapsed into the canonical async job
	// system (job type image.generate.google) post-NVIDIA-cutover; the
	// legacy webhook handler that bypassed the workers and went
	// straight to ingest.Service.IngestImage is gone. See
	// middleware_auth_test.go::TestAuth_RetiredWebhookPathReturns404
	// for the audit-pin test that locks the retirement.
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

// surface-2 (July 2026): ReceiveRemoteWebhook + RemoteWebhookJobJSON
// retired. The legacy POST /api/images/webhook/remote route that
// bypassed the worker pool and fed images straight into the local
// ingest pipeline via multipart upload (max 500MB) is gone. The
// remote-worker ingest pipeline collapsed into the canonical async
// job system (job type image.generate.google + job results →
// ingest.Service.IngestImage on the worker side). The retirement
// audit-pin lives in
// middleware/middleware_auth_test.go::TestAuth_RetiredWebhookPathReturns404
// which asserts 404 for all POSTs to the path regardless of credentials.

package images

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	imgservice "velox/go-master/internal/media/images"
	"velox/go-master/internal/media/ingest"
	"velox/go-master/internal/pkg/apiutil"
)

type Handler struct {
	service   *imgservice.Service
	ingestSvc *ingest.Service
}

func NewHandler(service *imgservice.Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) SetIngestService(svc *ingest.Service) {
	h.ingestSvc = svc
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/search", h.Search)
	r.GET("/diagnostics", h.Diagnostics)
	r.POST("/upload", h.Upload) // Nuovo endpoint
	r.POST("/sync", h.Sync)
	r.POST("/generate/nvidia", h.GenerateNvidia)
	r.POST("/animate", h.Animate)
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
	Tags   []string `json:"tags"`
}

type AnimateRequest struct {
	ImageHash string `json:"image_hash" binding:"required"`
	Duration  int    `json:"duration"`
}

// Upload permette di aggiungere manualmente un'immagine tramite URL
func (h *Handler) Upload(c *gin.Context) {
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
	asset, err := h.service.SearchAndDownload(slug, req.Name, req.URL, req.Lang, req.Tags)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, asset)
}

// Search cerca un'immagine per un soggetto, scaricandola se non esiste
func (h *Handler) Search(c *gin.Context) {
	query := c.Query("q")
	lang := c.DefaultQuery("lang", "it")
	if query == "" {
		apiutil.BadRequest(c, "missing query parameter 'q'")
		return
	}

	// Proviamo a cercare/scaricare
	slug := strings.ReplaceAll(strings.ToLower(query), " ", "-")
	asset, err := h.service.SearchAndDownload(slug, query, query, lang, nil)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
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
func (h *Handler) Sync(c *gin.Context) {
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

// GenerateNvidia genera un'immagine AI tramite NVIDIA NIM
func (h *Handler) GenerateNvidia(c *gin.Context) {
	var req GenerateNvidiaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	asset, err := h.service.GenerateAImage(req.Prompt, req.Model, req.Width, req.Height, req.Tags)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"prompt": req.Prompt,
		"model":  req.Model,
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

// Animate crea un video zoom-out da un'immagine esistente
func (h *Handler) Animate(c *gin.Context) {
	var req AnimateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	if req.Duration <= 0 {
		req.Duration = 7
	}

	outputPath, err := h.service.AnimateImage(c.Request.Context(), req.ImageHash, req.Duration)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"image_hash":  req.ImageHash,
		"duration":    req.Duration,
		"output_path": outputPath,
		"message":     "Animation created successfully",
	})
}

// Diagnostics reports the local state of the image generation and animation wiring.
func (h *Handler) Diagnostics(c *gin.Context) {
	if h.service == nil {
		apiutil.InternalError(c, fmt.Errorf("image service not configured"))
		return
	}

	apiutil.OK(c, h.service.Diagnostics())
}

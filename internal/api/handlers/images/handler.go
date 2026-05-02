package images

import (
	"strings"

	"github.com/gin-gonic/gin"
	imgservice "velox/go-master/internal/service/images"
	"velox/go-master/pkg/apiutil"
)

type Handler struct {
	service *imgservice.Service
}

func NewHandler(service *imgservice.Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/search", h.Search)
	r.POST("/sync", h.Sync)
}

// Search cerca un'immagine per un soggetto, scaricandola se non esiste
func (h *Handler) Search(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		apiutil.BadRequest(c, "missing query parameter 'q'")
		return
	}

	// Proviamo a cercare/scaricare
	slug := strings.ReplaceAll(strings.ToLower(query), " ", "-")
	asset, err := h.service.SearchAndDownload(slug, query, query)
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
		},
	})
}

// Sync avvia la sincronizzazione manuale del file system
func (h *Handler) Sync(c *gin.Context) {
	if err := h.service.SyncAssets(); err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"message": "Synchronization complete"})
}

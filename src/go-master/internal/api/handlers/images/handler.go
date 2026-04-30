package images

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	imgservice "velox/go-master/internal/service/images"
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
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "missing query parameter 'q'"})
		return
	}

	// Proviamo a cercare/scaricare
	slug := strings.ReplaceAll(strings.ToLower(query), " ", "-")
	asset, err := h.service.SearchAndDownload(slug, query, query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
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
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Synchronization complete"})
}

package adminui

import (
	"io/fs"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Handler serves the Admin UI API routes and knows the static filesystem
// so the composition root can also wire static serving.
type Handler struct {
	staticFS fs.FS
	log      *zap.Logger
}

// NewHandler constructs a new Admin UI handler.
func NewHandler(staticFS fs.FS, log *zap.Logger) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{staticFS: staticFS, log: log}
}

// StaticFS returns the configured static filesystem.
func (h *Handler) StaticFS() fs.FS { return h.staticFS }

// RegisterRoutes registers the admin UI API routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/health", h.Health)
}

// Health returns a simple health check for the admin UI.
func (h *Handler) Health(c *gin.Context) {
	WriteHealth(c)
}

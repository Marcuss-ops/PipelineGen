package assets

import (
	"strings"

	"github.com/gin-gonic/gin"

	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/application/ingest"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// MediaingestHandler exposes the media ingest endpoint. It is a
// sub-handler of the consolidated assets/ package; routes remain
// mounted under /api/media/* for backward compatibility.
type MediaingestHandler struct {
	service *ingest.Service
}

// NewMediaingestHandler creates a new media ingest handler.
func NewMediaingestHandler(service *ingest.Service) *MediaingestHandler {
	return &MediaingestHandler{service: service}
}

// RegisterRoutes registers /api/media routes.
//
//	POST /api/media/ingest
func (h *MediaingestHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/ingest", h.Ingest)
}

// Ingest handles POST /api/media/ingest. The handler dispatches to
// either an image, voiceover, clip, or stock ingest pipeline based
// on the request kind.
func (h *MediaingestHandler) Ingest(c *gin.Context) {
	req, ok := api.BindJSON[ingest.Request](c)
	if !ok {
		return
	}
	if strings.TrimSpace(req.Kind) == "" {
		api.BadRequest(c, "kind is required")
		return
	}

	result, err := h.service.Ingest(c.Request.Context(), &req)
	if err != nil {
		if textutil.ContainsCI(err.Error(), "required") {
			api.BadRequest(c, err.Error())
			return
		}
		api.InternalError(c, err)
		return
	}

	api.OK(c, result)
}

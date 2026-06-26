package assets

import (
	"strings"

	"github.com/gin-gonic/gin"

	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// MediaingestHandler exposes the media ingest endpoint. It is a
// sub-handler of the consolidated assets/ package; routes remain
// mounted under /api/media/* for backward compatibility.
//
// PR8 (June 2026): added Idempotency middleware installation on
// the POST /ingest route. The Idempotency-Key header triggers
// 24h replay caching; conflicting same-key+different-body returns
// 422; concurrent same-key returns 409.
type MediaingestHandler struct {
	service     *ingest.Service
	Idempotency gin.HandlerFunc
}

// NewMediaingestHandler creates a new media ingest handler.
//
// PR8 (June 2026): idempotencyMiddleware is the reusable Gin
// idempotency middleware instance from middleware.NewIdempotency
// (constructed in WireMediaIngest). nil disables idempotency for
// test fixtures.
func NewMediaingestHandler(service *ingest.Service, idempotencyMiddleware gin.HandlerFunc) *MediaingestHandler {
	var idem gin.HandlerFunc = func(c *gin.Context) { c.Next() }
	if idempotencyMiddleware != nil {
		idem = idempotencyMiddleware
	}
	return &MediaingestHandler{service: service, Idempotency: idem}
}

// RegisterRoutes registers /api/media routes.
//
//	POST /api/media/ingest  (Idempotency-Key optional)
func (h *MediaingestHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/ingest", h.Idempotency, h.Ingest)
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

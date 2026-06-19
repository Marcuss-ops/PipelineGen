package api

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/internal/media/ingest"
	textutil "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

type MediaingestHandler struct {
	service *ingest.Service
}

func NewMediaingestHandler(service *ingest.Service) *MediaingestHandler {
	return &MediaingestHandler{service: service}
}

func (h *MediaingestHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/ingest", h.Ingest)
}

func (h *MediaingestHandler) Ingest(c *gin.Context) {
	req, ok := BindJSON[ingest.Request](c)
	if !ok {
		return
	}
	if strings.TrimSpace(req.Kind) == "" {
		apiutil.BadRequest(c, "kind is required")
		return
	}

	result, err := h.service.Ingest(c.Request.Context(), &req)
	if err != nil {
		if textutil.ContainsCI(err.Error(), "required") {
			apiutil.BadRequest(c, err.Error())
			return
		}
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, result)
}

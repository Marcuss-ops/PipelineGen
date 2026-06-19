package mediaingest

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/internal/media/ingest"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
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

package mediaingest

import (
	"strings"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/media/ingest"
	"velox/go-master/internal/pkg/apiutil"
)

type Handler struct {
	service *ingest.Service
}

func NewHandler(service *ingest.Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/ingest", h.Ingest)
}

func (h *Handler) Ingest(c *gin.Context) {
	req, ok := apiutil.BindJSON[ingest.Request](c)
	if !ok {
		return
	}
	if strings.TrimSpace(req.Kind) == "" {
		apiutil.BadRequest(c, "kind is required")
		return
	}

	result, err := h.service.Ingest(c.Request.Context(), &req)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "required") {
			apiutil.BadRequest(c, err.Error())
			return
		}
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, result)
}

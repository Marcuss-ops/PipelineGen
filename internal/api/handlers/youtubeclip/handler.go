package youtubeclip

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/service/youtubeclip"
)

type Handler struct {
	service *youtubeclip.Service
	log     *zap.Logger
}

func NewHandler(service *youtubeclip.Service, log *zap.Logger) *Handler {
	return &Handler{
		service: service,
		log:     log,
	}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/extract", h.Extract)
}

func (h *Handler) Extract(c *gin.Context) {
	var req youtubeclip.ExtractRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "invalid request: " + err.Error(),
		})
		return
	}

	resp, err := h.service.Extract(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusBadRequest, resp)
		return
	}

	c.JSON(http.StatusOK, resp)
}

package voiceover

import (
	"net/http"
	"velox/go-master/internal/service/voiceover"
	"github.com/gin-gonic/gin"
	"strings"
)

type Handler struct {
	service *voiceover.Service
}

func NewHandler(service *voiceover.Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/generate", h.Generate)
}

type GenerateRequest struct {
	Text     string `json:"text" binding:"required"`
	Language string `json:"language"`
	Filename string `json:"filename"`
}

func (h *Handler) Generate(c *gin.Context) {
	var req GenerateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	if req.Language == "" {
		req.Language = "it"
	}
	if req.Filename == "" {
		req.Filename = "manual_vo_" + strings.ReplaceAll(req.Language, "-", "_") + ".mp3"
	}

	result, err := h.service.Generate(c.Request.Context(), req.Text, req.Language, req.Filename)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"result": result,
	})
}

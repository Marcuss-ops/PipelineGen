package voiceover

import (
	"strings"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/service/voiceover"
	"velox/go-master/pkg/apiutil"
)

type Handler struct {
	service *voiceover.Service
}

func NewHandler(service *voiceover.Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/generate", h.Generate)
	r.POST("/batch", h.Batch)
}

type GenerateRequest struct {
	Text     string `json:"text" binding:"required"`
	Language string `json:"language"`
	Filename string `json:"filename"`
}

func (h *Handler) Generate(c *gin.Context) {
	req, ok := apiutil.BindJSON[GenerateRequest](c)
	if !ok {
		return
	}

	if req.Language == "" {
		req.Language = "it"
	}
	if req.Filename == "" {
		req.Filename = "manual vo " + strings.ReplaceAll(req.Language, "-", " ") + ".mp3"
	}

	result, err := h.service.Generate(c.Request.Context(), req.Text, req.Language, req.Filename)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{"result": result})
}

func (h *Handler) Batch(c *gin.Context) {
	req, ok := apiutil.BindJSON[voiceover.BatchRequest](c)
	if !ok {
		return
	}

	resp, err := h.service.GenerateBatch(c.Request.Context(), &req)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, resp)
}

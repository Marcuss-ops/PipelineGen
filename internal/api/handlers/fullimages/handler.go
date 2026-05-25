package handlers

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/internal/media/fullimages"
	"velox/go-master/internal/pkg/apiutil"
)

// FullImagesHandler exposes the FullImages endpoint under /images/video/generate.
type FullImagesHandler struct {
	service *fullimages.Service
}

// NewFullImagesHandler creates a FullImages HTTP handler.
func NewFullImagesHandler(svc *fullimages.Service) *FullImagesHandler {
	return &FullImagesHandler{service: svc}
}

// RegisterRoutes registers the route on the provided RouterGroup.
func (h *FullImagesHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/video/generate", h.GenerateFullImages)
}

// GenerateFullImagesRequest is the JSON body for the endpoint.
type GenerateFullImagesRequest struct {
	Sections []fullimages.Section `json:"sections" binding:"required,min=1"`
	Topic    string               `json:"topic" example:"Medieval Europe"`
	Language string               `json:"language" example:"en"`
	// DefaultStyle is applied to every section that doesn't specify its own style.
	DefaultStyle string `json:"default_style" example:"medievale"`
}

// GenerateFullImagesResponse is returned on success.
type GenerateFullImagesResponse struct {
	OK     bool                     `json:"ok"`
	Videos []fullimages.SectionVideo `json:"videos"`
}

// GenerateFullImages handles POST /images/generate/fullimages.
// It generates one image per section — no entity extraction, no asset
// association. Pure image generation per section using Google/NVIDIA AI.
func (h *FullImagesHandler) GenerateFullImages(c *gin.Context) {
	req, ok := apiutil.BindJSON[GenerateFullImagesRequest](c)
	if !ok {
		return
	}

	// Apply default style to sections that don't specify their own.
	if req.DefaultStyle != "" {
		for i := range req.Sections {
			if req.Sections[i].Style == "" {
				req.Sections[i].Style = req.DefaultStyle
			}
		}
	}

	zap.L().Info("fullimages: request received",
		zap.Int("sections", len(req.Sections)),
		zap.String("topic", req.Topic),
		zap.String("language", req.Language),
		zap.String("default_style", req.DefaultStyle),
	)

	result, err := h.service.GenerateForSections(c.Request.Context(), req.Sections, req.Topic, req.Language)
	if err != nil {
		zap.L().Error("fullimages: generation failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	zap.L().Info("fullimages: response sent",
		zap.Int("total", len(result.Videos)),
	)

	apiutil.OK(c, GenerateFullImagesResponse{
		OK:     true,
		Videos: result.Videos,
	})
}

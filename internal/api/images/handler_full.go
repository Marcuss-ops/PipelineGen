// Package images (api/images) — handler_full.go holds the FullImagesHandler.
// Wave 14 close (June 2026): this receiver was absorbed from the standalone
// internal/api/fullimages/ package.
//
// The full-images endpoint (/images/video/generate) lives alongside the
// canonical image endpoints (search/diagnostics/upload/sync/generate/animate)
// inside the same package. Two siblings coexist:
//   - ImagesHandler    (ImagesHandler.RegisterRoutes mounts /images/{*, /search/diagnostics/upload/sync/generate/animate})
//     surface-2 (July 2026): /webhook/remote entry was retired; see
//     middleware/middleware_auth_test.go::TestAuth_RetiredWebhookPathReturns404.
//   - FullImagesHandler (FullImagesHandler.RegisterRoutes mounts /images/video/*)
//
// The router module (registry.go) registers both through the same /images
// prefix module so the resulting public URLs are /api/images/...
package images

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	mediafullimages "github.com/Marcuss-ops/PipelineGen/internal/application/images/fullimages"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// FullImagesHandler exposes the FullImages endpoint under /images/video/generate.
// Generates one image per section — no entity extraction, no asset
// association. Pure image generation per section using Google/NVIDIA AI.
type FullImagesHandler struct {
	service *mediafullimages.Service
}

// NewFullImagesHandler creates a FullImages HTTP handler.
func NewFullImagesHandler(svc *mediafullimages.Service) *FullImagesHandler {
	return &FullImagesHandler{service: svc}
}

// RegisterRoutes registers the route on the provided RouterGroup.
// The System module mounts this handler on the same /images prefix
// group as ImagesHandler (sibling routes). Sub-path /video/generate
// is intentionally distinct from ImagesHandler's /generate so the two
// coexist without collision in the gin router.
func (h *FullImagesHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/video/generate", h.GenerateFullImages)
}

// GenerateFullImagesRequest is the JSON body for the endpoint.
type GenerateFullImagesRequest struct {
	Sections []mediafullimages.Section `json:"sections" binding:"required,min=1"`
	Topic    string                    `json:"topic" example:"Medieval Europe"`
	Language string                    `json:"language" example:"en"`
	// DefaultStyle is applied to every section that doesn't specify its own style.
	DefaultStyle string `json:"default_style" example:"medievale"`
}

// GenerateFullImagesResponse is returned on success.
type GenerateFullImagesResponse struct {
	OK     bool                           `json:"ok"`
	Videos []mediafullimages.SectionVideo `json:"videos"`
}

// GenerateFullImages handles POST /images/video/generate.
// It generates one image per section — no entity extraction, no asset
// association. Pure image generation per section using Google/NVIDIA AI.
func (h *FullImagesHandler) GenerateFullImages(c *gin.Context) {
	var req GenerateFullImagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
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

	// Google Vids engine: the video_ai capability was removed (PR June 2026).
	// Sections requesting engine="google-vids" will fall through to the
	// ken-burns path inside fullimages.Service.generateOneVideo.

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

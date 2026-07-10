// Package fullimages (api/fullimages) — FullImagesHandler lives here.
//
// PR-IMG-LEGACY-6 (IMAGES-LEGACY-CLEANUP-2026-07-06 wave, 2026-07-06,
// CUTOVER phase, deadline 2026-08-22): this file was MOVED from
// `internal/api/images/handler_full.go` to its canonical dedicated
// package. The pre-PR location was a temporary absorption from the
// original `internal/api/fullimages/` package (PR3 Wave 14, June 2026)
// — that absorption is now reversed; the package has its own home
// again so the public REST URL stays at /api/fullimages/video/generate
// (per gen_api_docs.go entry) and the api/images package no longer
// carries a public fullimages surface.
//
// Architecture (post-move):
//   - api/fullimages/Handler    (this file, public surface)
//   - api/images/Handler        (sibling, NO fullimages surface)
//
// The two handlers coexist as sibling route modules registered on
// different /api/* prefixes. The system router mounts api/fullimages
// at /api/fullimages (handler.RegisterRoutes does r.POST("/video/generate",
// h.GenerateFullImages) on a /api/fullimages prefix group).
package fullimages

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	mediafullimages "github.com/Marcuss-ops/PipelineGen/internal/application/images/fullimages"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// FullImagesHandler exposes the FullImages endpoint under /fullimages/video/generate.
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
// The System module mounts this handler on a dedicated /api/fullimages
// prefix group (sibling of the /api/images prefix group used by
// ImagesHandler). The route path is intentionally /video/generate so
// the resulting public URL is /api/fullimages/video/generate.
func (h *FullImagesHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/video/generate", h.GenerateFullImages)
}

// FullImagesRequest is the JSON body for POST /api/fullimages/video/generate.
type FullImagesRequest struct {
	Topic    string                    `json:"topic"    binding:"required"`
	Language string                    `json:"language"  binding:"required"`
	Sections []mediafullimages.Section `json:"sections" binding:"required,min=1"`
}

// FullImagesResponse wraps the canonical mediafullimages.Result so
// the handler layer doesn't re-expose the application-layer type.
type FullImagesResponse struct {
	Videos []mediafullimages.SectionVideo `json:"videos"`
}

// GenerateFullImages handles POST /api/fullimages/video/generate.
//
// godlike/07 typed-error contract: the application-layer errors flow
// back via errors.Is probes (e.g. mediafullimages.ErrNoImageGenerated)
// and surface as 5xx envelopes with the typed sentinel diagnostic
// (no string-fragment matching anywhere on the wire path).
func (h *FullImagesHandler) GenerateFullImages(c *gin.Context) {
	req, ok := apiutil.BindJSON[FullImagesRequest](c)
	if !ok {
		return
	}

	// nil-tolerance: fullimages.Service not wired → 503. Pre-PR
	// godlike/07 fake-availability surface (silent-success on nil
	// service) was retired in FASE 2.1.
	if h.service == nil {
		apiutil.Error(c, 503, "fullimages service not wired")
		return
	}

	ctx := c.Request.Context()
	result, err := h.service.GenerateForSections(ctx, req.Sections, req.Topic, req.Language)
	if err != nil {
		zap.L().Error("fullimages: generation failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	zap.L().Info("fullimages: response sent",
		zap.String("topic", req.Topic),
		zap.String("language", req.Language),
		zap.Int("section_count", len(result.Videos)),
	)
	apiutil.OK(c, FullImagesResponse{Videos: result.Videos})
}

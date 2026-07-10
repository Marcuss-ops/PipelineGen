// Package fullimages (api/fullimages) — FullImagesHandler lives here.
//
// PR-IMAGES-FULLIMAGES-IMAGE-ONLY (2026-07-10, IMAGES-LEGACY-CLEANUP
// wave, CUTOVER phase, deadline 2026-09-01): the public REST URL is
// RENAMED from /api/fullimages/video/generate to
// /api/fullimages/image/generate, and the FullImagesResponse wire
// shape is RENAMED from {videos: [...]} to {images: [...]}. This is
// a hard breaking change per Option B (consumer awareness note in
// commit message; no 410-Gone literal shim — clients reading
// `response.videos[...]` will break).
//
// Architecture (post-CUTOVER):
//   - api/fullimages/Handler   (this file, public surface, /image/generate)
//   - api/images/Handler       (sibling, NO fullimages surface)
//
// The two handlers coexist as sibling route modules registered on
// different /api/* prefixes. The system router mounts api/fullimages
// at /api/fullimages (handler.RegisterRoutes does r.POST("/image/generate",
// h.GenerateFullImages) on a /api/fullimages prefix group).
package fullimages

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	mediafullimages "github.com/Marcuss-ops/PipelineGen/internal/application/images/fullimages"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// FullImagesHandler exposes the FullImages endpoint under /fullimages/image/generate.
//
// PR-IMAGES-FULLIMAGES-IMAGE-ONLY (2026-07-10, CUTOVER phase): the
// pre-CUTOVER godoc referenced `/fullimages/video/generate`; the
// route is RENAMED to `/image/generate`. Generates one image per
// section — no entity extraction, no asset association. Pure image
// generation per section using Google/NVIDIA AI.
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
// ImagesHandler). The route path is intentionally /image/generate so
// the resulting public URL is /api/fullimages/image/generate
// (post-PR-IMAGES-FULLIMAGES-IMAGE-ONLY CUTOVER phase).
func (h *FullImagesHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/image/generate", h.GenerateFullImages)
}

// FullImagesRequest is the JSON body for POST /api/fullimages/image/generate.
//
// PR-IMAGES-FULLIMAGES-IMAGE-ONLY (2026-07-10, CUTOVER phase): the
// pre-CUTOVER godoc referenced /api/fullimages/video/generate; the
// route is RENAMED to /api/fullimages/image/generate.
type FullImagesRequest struct {
	Topic    string                    `json:"topic"    binding:"required"`
	Language string                    `json:"language"  binding:"required"`
	Sections []mediafullimages.Section `json:"sections" binding:"required,min=1"`
}

// FullImagesResponse wraps the canonical mediafullimages.Result so
// the handler layer doesn't re-expose the application-layer type.
//
// PR-IMAGES-FULLIMAGES-IMAGE-ONLY (2026-07-10, CUTOVER phase): the
// pre-CUTOVER {videos: [...]} wire shape is RENAMED to
// {images: [...]} (canonical SectionImage slice, json tag "images").
// Wire-shape breaking change per Option B.
type FullImagesResponse struct {
	Images []mediafullimages.SectionImage `json:"images"`
}

// GenerateFullImages handles POST /api/fullimages/image/generate.
//
// PR-IMAGES-FULLIMAGES-IMAGE-ONLY (2026-07-10, CUTOVER phase): the
// pre-CUTOVER route was /api/fullimages/video/generate; the route is
// RENAMED to /api/fullimages/image/generate.
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
		zap.Int("section_count", len(result.Images)),
	)
	apiutil.OK(c, FullImagesResponse{Images: result.Images})
}

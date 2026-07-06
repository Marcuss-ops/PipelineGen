// Package images (api/images) — handler_full.go holds the FullImagesHandler.
// Wave 14 close (June 2026): this receiver was absorbed from the standalone
// internal/api/fullimages/ package.
//
// The full-images endpoint (/images/video/generate) lives alongside the
// canonical image endpoints (search/diagnostics/upload/sync/generate/animate)
// inside the same package. Two siblings coexist:
//   - ImagesHandler     (ImagesHandler.RegisterRoutes mounts /images/{*, /search/diagnostics/upload/sync/generate/animate})
//   - FullImagesHandler (FullImagesHandler.RegisterRoutes mounts /images/video/*)
//
// The router module (registry.go) registers both through the same /images
// prefix module so the resulting public URLs are /api/images/...
// /webhook/remote retired; see docs/archive/image-legacy.md §1
package images

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	mediafullimages "github.com/Marcuss-ops/PipelineGen/internal/application/images/fullimages"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// ErrEngineRetired is the canonical typed sentinel returned when a
// caller asks for a retired generation engine on a section (the only
// known retired engine today is "google-vids"; the video_ai capability
// was removed per FASE_2.1 in June 2026).
//
// PR-IMG-LEGACY-4 (IMAGES-LEGACY-CLEANUP-2026-07-06 wave, 2026-07-06,
// EXPAND phase, deadline 2026-08-15): the pre-PR silent-fall-through
// to the ken-burns path was a godlike/07 fake-availability surface —
// a /images/video/generate caller asking engine="google-vids" was
// silently coerced to engine="ken-burns" with no error envelope.
// The canonical 400 BadRequest surfaces an actionable hint naming
// the two valid replacement engines (ken-burns + ai-image-N) so an
// operator can migrate without guessing.
//
// godlike/06 SSOT: ErrEngineRetired lives ONLY in this package
// (where the public fullimages surface is). godlike/07 typed-error
// contract: callers probe via errors.Is(err, ErrEngineRetired).
// The diagnostic surfaced in the 400 body is ErrEngineRetired.Error()
// — there is no separate const; one canonical owner per fact.
var ErrEngineRetired = errors.New("engine=google-vids retired; use ken-burns or ai-image-N explicitly")

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

	// PR-IMG-LEGACY-4 — engine retirement gate. The video_ai capability
	// (FASE_2.1, June 2026) was removed and "google-vids" is RETIRED.
	// Case-insensitive match (strings.EqualFold + TrimSpace) so
	// "google-vids" / "Google-Vids" / " GOOGLE-VIDS " all surface
	// the same canonical rejection.
	//
	// godlike/06 SSOT: the engine contract lives in
	// mediafullimages.Section.Engine; this handler is the SOLE gater
	// of retired-engine callers on the /images/video/generate route.
	// (Composition-root path is the next gate layer, out of scope for
	// this PR per godlike/07 minimum-blast-radius — forward-pointer
	// PR-INGEST-SOURCE-VALIDATE-SSOT.)
	for _, sec := range req.Sections {
		if strings.EqualFold(strings.TrimSpace(sec.Engine), "google-vids") {
			apiutil.BadRequest(c, ErrEngineRetired.Error())
			return
		}
	}

	// Sections requesting engine values other than "google-vids" pass
	// through to the canonical ken-burns path inside
	// fullimages.Service.generateOneVideo (Section.Engine marked
	// Deprecated: generation always uses Google Slides + Ken Burns).

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

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
//
// ErrEngineRetired is the typed sentinel that gates the retired
// google-vids engine. It lives ONLY in this package — the canonical
// public fullimages surface per godlike/06 SSOT. godlike/07 typed-
// error contract: callers probe via errors.Is(err, ErrEngineRetired).
package fullimages

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
// a /fullimages/video/generate caller asking engine="google-vids" was
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
// PR-IMG-LEGACY-4: each section's Engine field is gated by the
// canonical ErrEngineRetired sentinel (see handler-level goddoc).
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
// It validates each section's engine via the canonical
// ErrEngineRetired sentinel (case-insensitive + whitespace-trimmed)
// BEFORE dispatching to the application-layer service. Any retired
// engine returns HTTP 400 with an actionable hint body.
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

	// PR-IMG-LEGACY-4 engine gate. Runs BEFORE the service dispatch so
	// a retired engine never reaches the application layer. Iterates
	// each section (the request allows multiple sections in one call).
	for i, sec := range req.Sections {
		engine := strings.TrimSpace(sec.Engine)
		if strings.EqualFold(engine, "google-vids") {
			apiutil.Error(c, 400, ErrEngineRetired.Error())
			zap.L().Warn("fullimages: retired engine rejected",
				zap.Int("section_index", i),
				zap.String("requested_engine", sec.Engine),
			)
			return
		}
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

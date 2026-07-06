// Package images (api/images) — legacy_generate_handler.go holds
// the legacy synchronous AI image generation handler
// (POST /api/images/generate). Per the golden rule: this is LEGACY
// compatibility. The canonical generated-territory generation
// endpoint is POST /api/images/generated/generate
// (generated_generate_handler.go).
//
// This handler wraps GenerateSmartImage with legacy defaults
// (empty subject/topic, skipDrive=false). PR-IMAGES-SHIM-REMOVAL
// (2026-07-04) retired the Account/ProjectID fake-availability
// fields from the request type.
package images

import (
	"errors"
	"net/http"

	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/generated"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

// Generate handles POST /api/images/generate — legacy synchronous
// AI image generation via Chrome/Playwright (Google Slides). Routes
// through GenerateSmartImage with empty subject/topic defaults.
//
// PR-IMG-LEGACY-5 (IMAGES-LEGACY-CLEANUP-2026-07-06 wave, 2026-07-06,
// CUTOVER phase, deadline 2026-08-22): the handler now binds the
// canonical ImageGenerationRequest (unified with the /generated/generate
// route) instead of the pre-PR local-literal of the same shape. The
// wire-shape is unchanged; only the type identity collapsed per
// godlike/06 SSOT (one canonical owner per fact).
func (h *ImagesHandler) Generate(c *gin.Context) {
	var req ImageGenerationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	asset, err := h.service.GenerateSmartImage(
		c.Request.Context(),
		"", // subject
		"", // topic
		req.Style,
		[]string{req.Prompt},
		req.Tags,
		req.Width,
		req.Height,
		generated.CanonicalGoogleSlidesModel,
		false, // skipDrive = false
	)
	if err != nil {
		if errors.Is(err, imgservice.ErrImageGenNotImplemented) {
			c.AbortWithStatusJSON(http.StatusNotImplemented, gin.H{
				"error":   "image generation endpoint has been removed",
				"message": err.Error(),
			})
			return
		}
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, asset)
}

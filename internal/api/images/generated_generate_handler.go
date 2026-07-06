// Package images (api/images) — generated_generate_handler.go holds
// the POST /api/images/generated/generate handler and its request
// DTO. This is the AI image-generation entry point scoped to the
// generated territory — distinct from search (read) and from the
// legacy /generate endpoint (kept for backward compat).
//
// Per the golden rule: generated = AI-created images. This handler
// delegates to Service.GenerateSmartImage with the canonical
// GoogleSlidesModel.
package images

import (
	"errors"
	"net/http"

	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/generated"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
) // GeneratedGenerate handles POST /api/images/generated/generate.
// Equivalent to the legacy POST /api/images/generate — same
// service method, same payload shape. Territory=matters for
// the URL: callers using /generated/* opt into the Step-10
// territory scope explicitly.
//
// PR-IMG-LEGACY-5 (IMAGES-LEGACY-CLEANUP-2026-07-06 wave, 2026-07-06,
// CUTOVER phase, deadline 2026-08-22): the handler now binds the
// canonical ImageGenerationRequest (declared in request_types.go)
// instead of the pre-PR local duplicate of the same shape. The
// wire-shape is unchanged; only the type identity collapsed per
// godlike/06 SSOT (one canonical owner per fact).
func (h *ImagesHandler) GeneratedGenerate(c *gin.Context) {
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

	// DoD #8 (July 2026): response includes drive block + indexed
	// alongside the unified search-result shape. ImageAsset carries
	// DriveFileID and PathRel; DriveLink is derived.
	res := assetToResult(asset)
	apiutil.OK(c, gin.H{
		"asset_id":    res.AssetID,
		"origin":      res.Origin,
		"provider":    res.Provider,
		"preview_url": res.PreviewURL,
		"style_id":    res.StyleID,
		"license":     res.License,
		"author":      res.Author,
		"drive":       imageDriveBlock(asset),
		"indexed":     false,
	})
}

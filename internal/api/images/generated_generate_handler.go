// Package images (api/images) — generated_generate_handler.go holds
// the canonical POST /api/images/generated/generate handler. This is
// the single AI image-generation entry point on the /api/images
// surface, distinct from generated search and style discovery.
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
)

// GeneratedGenerate handles POST /api/images/generated/generate.
// It binds the canonical ImageGenerationRequest declared in
// request_types.go and dispatches exactly one generated-territory
// image-generation request.
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
		"location": gin.H{
			"category": "",
			"subject":  asset.SubjectID,
			"provider": string(asset.Provider),
			"style":    req.Style,
		},
	})
}

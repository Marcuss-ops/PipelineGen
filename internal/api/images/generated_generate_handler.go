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
)

// GeneratedGenerateRequest is the body for POST
// /api/images/generated/generate. Mirrors the existing
// GenerateImageRequest shape but is mounted under /generated/*
// to emphasise territory separation.
type GeneratedGenerateRequest struct {
	Prompt string   `json:"prompt" binding:"required"`
	Width  int      `json:"width"`
	Height int      `json:"height"`
	Style  string   `json:"style" example:"medievale"`
	Tags   []string `json:"tags"`
}

// GeneratedGenerate handles POST /api/images/generated/generate.
// Equivalent to the legacy POST /api/images/generate — same
// service method, same payload shape. Territory=matters for
// the URL: callers using /generated/* opt into the Step-10
// territory scope explicitly.
func (h *ImagesHandler) GeneratedGenerate(c *gin.Context) {
	var req GeneratedGenerateRequest
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

	res := assetToResult(asset)
	apiutil.OK(c, res)
}

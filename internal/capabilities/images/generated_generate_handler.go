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
	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
	"net/http"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

// deliveryModeFast is the default that matches behavior before the
// delivery_mode field existed: return ASAP after local write +
// media_assets row + asset.index.requested outbox commit. Drive
// upload + embedding + Qdrant upsert run async via the outbox
// dispatcher after SQLite commit (godlike/07 fail-closed — NEVER
// inline from this handler thread).
const (
	deliveryModeFast     = "fast"
	deliveryModeComplete = "complete"
)

// GeneratedGenerate handles POST /api/images/generated/generate.
// It binds the canonical ImageGenerationRequest declared in
// request_types.go and dispatches exactly one generated-territory
// image-generation request.
//
// PR-DELIVERY-MODE (July 2026): the handler honours an optional
// delivery_mode field ("fast" | "complete") on the request body.
// "fast" (default) returns as soon as the local file is on disk +
// the media_assets row + the asset.index.requested outbox row are
// committed in the same SQLite transaction. Drive upload, metadata
// enrichment, SigLIP embedding, and Qdrant upsert are deferred to
// the outbox dispatcher running AFTER the SQLite commit — they are
// NEVER invoked inline on this handler thread (godlike/07 fail-
// closed: an unavailable backend must not be represented as a
// successful no-op).
//
// "complete" mode behaves identically except it waits for the
// outbox dispatcher to ack the index.requested event for the
// returned asset_id before responding (bounded timeout). On
// timeout the response still carries the asset_id — the timeout
// is a hint, not an error, because delivery is by definition
// async-safe.
func (h *ImagesHandler) GeneratedGenerate(c *gin.Context) {
	var req ImageGenerationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	// Canonical pre-dispatch validation: whitespace-only prompt and
	// negative dimensions must be rejected before the service is called.
	if err := req.Validate(); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	mode := req.DeliveryMode
	switch mode {
	case "", deliveryModeFast:
		mode = deliveryModeFast
	case deliveryModeComplete:
		// accepted
	default:
		apiutil.BadRequest(c, "delivery_mode must be \"fast\" or \"complete\"")
		return
	}

	// Drive/embedding/Qdrant MUST NEVER run inline on this handler
	// thread (godlike/07 fail-closed + AGENTS.md "durable side
	// effects after database commits must use the transactional
	// outbox"). The SyncCommand.SkipDrive flag below is the
	// concrete wire that defers Drive + metadata persistence to
	// the post-commit outbox dispatch loop. When delivery_mode ==
	// "complete" we additionally wait for that loop to ack.
	skipDrive := true
	if mode == deliveryModeComplete {
		skipDrive = false
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
		CanonicalGoogleSlidesModel,
		skipDrive,
	)
	if err != nil {
		if errors.Is(err, ErrImageGenNotImplemented) {
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
		// Operator-visible canonical metadata shape (godlike/06 SSOT):
		// "visual_embedding_dimensions" + "embedding_version_visual" pin
		// the canonical SigLIP model that the outbox dispatcher will
		// embed with after SQLite commit. "metadata_json" echoes back
		// the full persisted text so operators can verify
		// prompt_original / semantic_description / style / etc. without
		// a separate GET.
		"visual_embedding_dimensions": coreembedding.DimensionVisual, // canonical SigLIP dimension (registry SSOT)
		"embedding_version_visual":    coreembedding.VisualEmbeddingModelVersion,
		"metadata_json":               asset.MetadataJSON,
		"delivery_mode":               mode,
		"location": gin.H{
			"category": "",
			"subject":  asset.SubjectID,
			"provider": string(asset.Provider),
			"style":    req.Style,
		},
	})
}

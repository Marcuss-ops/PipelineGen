// Package operator — handler_assets.go (RESOURCE: ASSETS, July 2026
// split by resource).
//
// Split rationale (resource/handler), see handler.go header.
//
// This file owns the ASSETS resource (list + get + preview). 6 routes:
//
//   - GET  /assets               → handleListAssets
//   - GET  /assets/:id           → handleGetAsset
//   - GET  /assets/:id/preview   → handlePreview
//   - GET  /facets               → handleFacets
//   - POST /assets/:id/verify-index → handleVerifyIndex
//   - POST /assets/:id/reindex   → handleReindex
//
// registers via the private registerAssetsRoutes method, called from
// handler.go::RegisterRoutes.
//
// Cross-resource sharing: this file owns summariesToJSON (used by
// BOTH summary and assets; it stays in the assets file because its
// semantic owner is asset.Summary → JSON conversion). It also owns
// isAllowedPath + maskPath (only used by preview).
package operator

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/operator"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// registerAssetsRoutes mounts assets transports on the shared
// /api/assets/operator/* prefix. The paths below are RELATIVE to the
// parent router group.
func (h *Handler) registerAssetsRoutes(rg *gin.RouterGroup) {
	rg.GET("/assets", h.handleListAssets)
	rg.GET("/assets/:id", h.handleGetAsset)
	rg.GET("/assets/:id/preview", h.handlePreview)
	rg.GET("/facets", h.handleFacets)
	rg.POST("/assets/:id/verify-index", h.handleVerifyIndex)
	rg.POST("/assets/:id/reindex", h.handleReindex)
}

// handleListAssets returns a filtered, cursor-paginated asset list using
// the canonical operator read model.
func (h *Handler) handleListAssets(c *gin.Context) {
	if h.readModel == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "asset inventory read model not available")
		return
	}

	ctx := c.Request.Context()
	query := operator.AssetInventoryQuery{
		Source:         c.Query("source"),
		Provider:       c.Query("provider"),
		MediaType:      c.Query("media_type"),
		LifecycleState: c.Query("lifecycle_state"),
		AssetState:     c.Query("asset_state"),
		IndexState:     c.Query("index_state"),
		Search:         c.Query("search"),
	}

	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			query.Limit = parsed
		}
	}

	if cursor := c.Query("cursor"); cursor != "" {
		if offset, err := strconv.Atoi(cursor); err == nil {
			query.Offset = offset
		}
	}

	page, err := h.readModel.List(ctx, query)
	if err != nil {
		h.log.Error("failed to list assets", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, page)
}

// handleGetAsset returns the full operator inspection view for a single asset.
func (h *Handler) handleGetAsset(c *gin.Context) {
	if h.readModel == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "asset inventory read model not available")
		return
	}

	id := c.Param("id")
	ctx := c.Request.Context()

	inspection, err := h.readModel.Get(ctx, id)
	if err != nil {
		h.log.Error("failed to get asset", zap.String("id", id), zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	if inspection == nil {
		apiutil.NotFound(c, "asset not found")
		return
	}

	apiutil.OK(c, inspection)
}

// handlePreview streams an asset file securely.
func (h *Handler) handlePreview(c *gin.Context) {
	if h.assetService == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "asset service not available")
		return
	}

	id := c.Param("id")
	ctx := c.Request.Context()

	details, err := h.assetService.Get(ctx, id)
	if err != nil || details == nil || details.Asset == nil {
		apiutil.NotFound(c, "asset not found")
		return
	}

	// Find the local location
	loc := details.LocalLocation()
	if loc == nil {
		apiutil.Error(c, http.StatusNotFound, "no local file available")
		return
	}

	// Validate the file is in an allowed directory
	if !h.isAllowedPath(loc.URI) {
		h.log.Warn("preview denied: path not in allowed roots",
			zap.String("id", id), zap.String("path", loc.URI))
		apiutil.Error(c, http.StatusForbidden, "access denied")
		return
	}

	// Check file exists
	info, err := os.Stat(loc.URI)
	if err != nil {
		apiutil.Error(c, http.StatusNotFound, "file not found on disk")
		return
	}

	// Set content type
	contentType := loc.MimeType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Header("Content-Type", contentType)
	c.Header("Content-Length", strconv.FormatInt(info.Size(), 10))
	c.Header("Accept-Ranges", "bytes")
	c.Header("Cache-Control", "private, max-age=300")

	// Support range requests for audio/video
	if c.GetHeader("Range") != "" {
		http.ServeFile(c.Writer, c.Request, loc.URI)
		return
	}

	// Stream the file
	file, err := os.Open(loc.URI)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("open file: %w", err))
		return
	}
	defer file.Close()

	c.Status(http.StatusOK)
	io.Copy(c.Writer, file)
}

// handleFacets returns server-driven facet counts derived from canonical
// enum values and live DB rows.
func (h *Handler) handleFacets(c *gin.Context) {
	if h.readModel == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "asset inventory read model not available")
		return
	}

	ctx := c.Request.Context()
	facets, err := h.readModel.Facets(ctx)
	if err != nil {
		h.log.Error("failed to list facets", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, facets)
}

// handleVerifyIndex performs a live Qdrant check for the asset and
// compares it with the persisted SQLite read-model projection.
func (h *Handler) handleVerifyIndex(c *gin.Context) {
	if h.readModel == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "asset inventory read model not available")
		return
	}
	if h.indexVerifier == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "index verifier not available")
		return
	}

	id := c.Param("id")
	ctx := c.Request.Context()

	inspection, err := h.readModel.Get(ctx, id)
	if err != nil {
		h.log.Error("failed to get asset for verify", zap.String("id", id), zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	if inspection == nil {
		apiutil.NotFound(c, "asset not found")
		return
	}

	collection := inspection.CollectionVersion
	if collection == "" {
		collection = "media_assets_current"
	}

	qdrantInfo, err := h.indexVerifier.Verify(ctx, id, collection)
	if err != nil {
		h.log.Error("failed to verify asset index", zap.String("id", id), zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	consistent := true
	if qdrantInfo.Present {
		if qdrantInfo.PayloadAssetID != "" && qdrantInfo.PayloadAssetID != id {
			consistent = false
		}
		if inspection.ContentHash != "" && inspection.IndexedContentHash != "" &&
			inspection.ContentHash != inspection.IndexedContentHash {
			consistent = false
		}
	} else {
		consistent = false
	}

	apiutil.OK(c, gin.H{
		"asset_id": id,
		"sqlite": gin.H{
			"index_state":          string(inspection.IndexState),
			"embedding_present":    inspection.HasEmbedding,
			"content_hash":         inspection.ContentHash,
			"indexed_content_hash": inspection.IndexedContentHash,
		},
		"outbox": gin.H{
			"pending": inspection.PendingOutboxEvents,
		},
		"qdrant": gin.H{
			"checked":                 qdrantInfo.Checked,
			"point_present":           qdrantInfo.Present,
			"collection":              qdrantInfo.Collection,
			"vector_dimensions":       qdrantInfo.VectorDimensions,
			"payload_lifecycle_state": qdrantInfo.PayloadLifecycleState,
		},
		"consistent": consistent,
	})
}

// handleReindex enqueues a re-index outbox event for the asset.
func (h *Handler) handleReindex(c *gin.Context) {
	if h.mutator == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "reindex unavailable: mutation dispatcher not wired")
		return
	}
	if h.assetService == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "reindex unavailable: asset service not wired")
		return
	}

	id := c.Param("id")
	ctx := c.Request.Context()

	details, err := h.assetService.Get(ctx, id)
	if err != nil {
		h.log.Error("failed to get asset for reindex", zap.String("id", id), zap.Error(err))
		apiutil.NotFound(c, "asset not found")
		return
	}
	if details == nil || details.Asset == nil {
		apiutil.NotFound(c, "asset not found")
		return
	}

	a := details.Asset
	contentHash := a.LegacyFileMD5()
	if contentHash == "" {
		contentHash = a.ID
	}

	if err := h.mutator.EnqueueAndIndex(ctx, a, contentHash); err != nil {
		h.log.Error("failed to enqueue reindex", zap.String("id", id), zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"asset_id": id,
		"queued":   true,
	})
}

// isAllowedPath checks if a file path is under one of the allowed roots.
func (h *Handler) isAllowedPath(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	for _, root := range h.allowedRoots {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if strings.HasPrefix(abs, absRoot+string(filepath.Separator)) || abs == absRoot {
			return true
		}
	}
	return false
}

// summariesToJSON converts domain Summary pointers to JSON-friendly
// maps. Used by handleSummary (the dashboard payload).
func (h *Handler) summariesToJSON(items []*asset.Summary) []gin.H {
	result := make([]gin.H, 0, len(items))
	for _, s := range items {
		if s == nil {
			continue
		}
		result = append(result, gin.H{
			"id":              s.ID,
			"source":          string(s.Source),
			"name":            s.Name,
			"filename":        s.Filename,
			"media_type":      string(s.MediaType),
			"category":        s.Category,
			"lifecycle_state": string(s.LifecycleState),
			"created_at":      s.CreatedAt,
			"updated_at":      s.UpdatedAt,
		})
	}
	return result
}

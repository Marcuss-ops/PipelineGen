// Package operator — handler_assets.go (RESOURCE: ASSETS, July 2026
// split by resource).
//
// Split rationale (resource/handler), see handler.go header.
//
// This file owns the ASSETS resource (list + get + preview). 3 routes:
//
//   - GET /assets               → handleListAssets
//   - GET /assets/:id           → handleGetAsset
//   - GET /assets/:id/preview   → handlePreview
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

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// registerAssetsRoutes mounts assets transports on the shared
// /api/assets/operator/* prefix. The paths "/assets", "/assets/:id",
// "/assets/:id/preview" are RELATIVE to the parent router group.
func (h *Handler) registerAssetsRoutes(rg *gin.RouterGroup) {
	rg.GET("/assets", h.handleListAssets)
	rg.GET("/assets/:id", h.handleGetAsset)
	rg.GET("/assets/:id/preview", h.handlePreview)
}

// handleListAssets returns a filtered, cursor-paginated asset list.
func (h *Handler) handleListAssets(c *gin.Context) {
	ctx := c.Request.Context()

	filter := asset.Filter{}

	if src := c.Query("source"); src != "" {
		filter.Source = src
	}
	if mt := c.Query("media_type"); mt != "" {
		filter.MediaType = mt
	}
	if ls := c.Query("lifecycle_state"); ls != "" {
		filter.States = []string{ls}
	}
	if cat := c.Query("category"); cat != "" {
		filter.Category = cat
	}

	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	filter.Limit = limit + 1 // fetch one extra for cursor detection

	// Offset-based pagination (cursor would require schema changes)
	if cursor := c.Query("cursor"); cursor != "" {
		if offset, err := strconv.Atoi(cursor); err == nil {
			filter.Offset = offset
		}
	}

	assets, err := h.assetService.List(ctx, filter)
	if err != nil {
		h.log.Error("failed to list assets", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	hasMore := len(assets) > limit
	if hasMore {
		assets = assets[:limit]
	}

	nextCursor := ""
	if hasMore {
		offset := filter.Offset + limit
		nextCursor = strconv.Itoa(offset)
	}

	apiutil.OK(c, gin.H{
		"assets":      h.summariesToJSON(assets),
		"count":       len(assets),
		"next_cursor": nextCursor,
		"has_more":    hasMore,
	})
}

// handleGetAsset returns full asset details.
func (h *Handler) handleGetAsset(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()

	details, err := h.assetService.Get(ctx, id)
	if err != nil {
		h.log.Error("failed to get asset", zap.String("id", id), zap.Error(err))
		apiutil.NotFound(c, "asset not found")
		return
	}
	if details == nil || details.Asset == nil {
		apiutil.NotFound(c, "asset not found")
		return
	}

	a := details.Asset
	resp := gin.H{
		"id":              a.ID,
		"source":          string(a.Source),
		"name":            a.Name,
		"filename":        a.Filename,
		"media_type":      string(a.MediaType),
		"category":        a.Category,
		"group":           a.Group,
		"source_url":      a.SourceURL,
		"clip_page_url":   a.ClipPageURL,
		"thumbnail_url":   a.ThumbnailURL,
		"duration":        a.Duration.String(),
		"duration_secs":   a.Duration.Seconds(),
		"tags":            a.Tags,
		"search_terms":    a.SearchTerms,
		"search_text":     a.SearchText,
		"lifecycle_state": string(a.LifecycleState),
		"metadata":        a.Metadata,
		"created_at":      a.CreatedAt,
		"updated_at":      a.UpdatedAt,
		"license_basis":   a.LicenseBasis,
		"review_status":   string(a.ReviewStatus),
	}

	// Locations
	locs := make([]gin.H, 0, len(details.Locations))
	for _, loc := range details.Locations {
		if loc == nil {
			continue
		}
		displayURI := loc.URI
		// Mask local paths for security
		if loc.LocationKind == asset.LocationKindLocal {
			displayURI = maskPath(loc.URI)
		}
		locs = append(locs, gin.H{
			"kind":            string(loc.LocationKind),
			"uri":             displayURI,
			"external_id":     loc.ExternalID,
			"is_primary":      loc.IsPrimary,
			"mime_type":       loc.MimeType,
			"file_size_bytes": loc.FileSizeBytes,
			"file_hash":       loc.FileHash,
		})
	}
	resp["locations"] = locs

	// Processing
	procs := make([]gin.H, 0, len(details.Processing))
	for _, p := range details.Processing {
		if p == nil {
			continue
		}
		procs = append(procs, gin.H{
			"step":          p.Step,
			"status":        string(p.Status),
			"error":         p.ErrorMessage,
			"started_at":    p.StartedAt,
			"completed_at":  p.CompletedAt,
			"attempt_count": p.AttemptCount,
		})
	}
	resp["processing"] = procs

	// Versions (file version history — no raw vectors exposed)
	vers := make([]gin.H, 0, len(details.Versions))
	for _, v := range details.Versions {
		vers = append(vers, gin.H{
			"version_number": v.VersionNumber,
			"source_uri":     v.SourceURI,
			"file_hash":      v.FileHash,
			"file_size":      v.FileSizeBytes,
			"mime_type":      v.MimeType,
			"created_at":     v.CreatedAt,
		})
	}
	resp["versions"] = vers

	// Embedding info from metadata (safe subset — no raw vectors)
	embeddingInfo := gin.H{"present": false, "dimensions": 0, "version": ""}
	if a.Metadata != nil {
		if dim, ok := a.Metadata["visual_embedding_dimensions"]; ok {
			embeddingInfo["present"] = true
			embeddingInfo["dimensions"] = dim
		}
		if ver, ok := a.Metadata["embedding_version_visual"]; ok {
			embeddingInfo["version"] = ver
		}
	}
	resp["embedding_info"] = embeddingInfo

	apiutil.OK(c, resp)
}

// handlePreview streams an asset file securely.
func (h *Handler) handlePreview(c *gin.Context) {
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

// isAllowedPath checks if a file path is under one of the allowed roots.
// Used only by handlePreview (path-safety guard for arbitrary file
// resolution). Lives on *Handler receiver for naming-convention
// symmetry with the other path/handler helpers in this file.
//
// godlike/07 NO-FAKE-AVAILABILITY: filepath.Abs failure short-circuits
// to "not allowed" (no panic, no false-positive).
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

// maskPath hides the real filesystem path, showing only the filename.
// Used only by handleGetAsset for location.URI when LocationKindLocal
// (preventing the admin UI from leaking server-internal paths).
func maskPath(p string) string {
	return filepath.Base(p)
}

// summariesToJSON converts domain Summary pointers to JSON-friendly
// maps. Used by both summary (handleSummary inside the dashboard
// payload) and assets (handleListAssets response). Lives in handler_
// assets.go because its semantic owner is asset.Summary → JSON.
//
// On *Handler receiver for naming-convention symmetry with
// jobsToJSON (in handler_summary.go). Does NOT mutate h.
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

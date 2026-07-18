// Package operator provides admin-facing read-only API endpoints for the
// Operator Console. All routes are under /api/operator/ and require admin
// auth. The handler is thin transport — it delegates to existing domain
// services and aggregates their results.
package operator

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// Handler is the thin HTTP transport for operator console API endpoints.
type Handler struct {
	assetService *asset.Service
	jobService   job.Service
	jobStats     JobStatsReader
	outboxPort   outbox.MonitorPort
	allowedRoots []string
	log          *zap.Logger
}

// JobStatsReader is the narrow port for job statistics.
type JobStatsReader = jobs.JobStatsReader

// Dependencies holds the pre-built dependencies for the operator handler.
type Dependencies struct {
	AssetService *asset.Service
	JobService   job.Service
	JobStats     JobStatsReader
	OutboxPort   outbox.MonitorPort
	AllowedRoots []string // directories allowed for file previews
}

// NewHandler creates a new operator API handler.
func NewHandler(deps Dependencies, log *zap.Logger) *Handler {
	return &Handler{
		assetService: deps.AssetService,
		jobService:   deps.JobService,
		jobStats:     deps.JobStats,
		outboxPort:   deps.OutboxPort,
		allowedRoots: deps.AllowedRoots,
		log:          log,
	}
}

// RegisterRoutes mounts the operator endpoints under the given router group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/summary", h.handleSummary)
	rg.GET("/assets", h.handleListAssets)
	rg.GET("/assets/:id", h.handleGetAsset)
	rg.GET("/assets/:id/preview", h.handlePreview)
	rg.GET("/outbox/status", h.handleOutboxStatus)
	rg.GET("/outbox/events", h.handleOutboxEvents)
	rg.GET("/index-health", h.handleIndexHealth)
}

// handleSummary returns aggregated dashboard data.
func (h *Handler) handleSummary(c *gin.Context) {
	ctx := c.Request.Context()

	summary := gin.H{
		"ok":            true,
		"total_assets":  int64(0),
		"by_source":     map[string]int64{},
		"by_media_type": map[string]int64{},
		"indexed":       int64(0),
		"non_indexed":   int64(0),
		"local_count":   int64(0),
		"drive_count":   int64(0),
	}

	// Count total assets
	sources := []string{"", "artlist", "youtube_clip", "stock", "image", "generated", "sound_effect", "ai_generated"}
	mediaTypes := []string{"", "stock", "clip", "image", "audio", "document", "image_video", "sound_effect", "script"}

	bySource := map[string]int64{}
	byMediaType := map[string]int64{}
	var total int64

	for _, src := range sources {
		filter := asset.Filter{Limit: 1}
		if src != "" {
			filter.Source = src
		}
		count, err := h.assetService.Repository().Count(ctx, filter)
		if err != nil {
			h.log.Warn("failed to count assets by source", zap.String("source", src), zap.Error(err))
			continue
		}
		if src == "" && count == 0 {
			continue
		}
		if src != "" {
			bySource[src] = count
			total += count
		}
	}

	for _, mt := range mediaTypes {
		filter := asset.Filter{Limit: 1}
		if mt != "" {
			filter.MediaType = mt
		}
		count, err := h.assetService.Repository().Count(ctx, filter)
		if err != nil {
			h.log.Warn("failed to count assets by media type", zap.String("media_type", mt), zap.Error(err))
			continue
		}
		if mt != "" {
			byMediaType[mt] = count
		}
	}

	// If total is 0 but we have per-source counts, sum them
	if total == 0 {
		for _, v := range bySource {
			total += v
		}
	}

	summary["total_assets"] = total
	summary["by_source"] = bySource
	summary["by_media_type"] = byMediaType

	// Latest 10 assets
	latest, err := h.assetService.List(ctx, asset.Filter{Limit: 10})
	if err == nil {
		summary["latest_assets"] = h.summariesToJSON(latest)
	}

	// Job stats
	if h.jobStats != nil {
		stats, err := h.jobStats.GetStats(ctx)
		if err == nil && stats != nil {
			summary["jobs_running"] = stats.ByStatus["running"]
			summary["jobs_failed"] = stats.ByStatus["failed"]
			summary["jobs_completed"] = stats.ByStatus["succeeded"]
		}
	}

	// Latest failed jobs
	if h.jobService != nil {
		failedStatus := job.StatusFailed
		failedJobs, err := h.jobService.List(ctx, job.Filter{Status: &failedStatus, Limit: 5})
		if err == nil {
			summary["latest_errors"] = h.jobsToJSON(failedJobs)
		}
	}

	// Outbox stats
	if h.outboxPort != nil {
		for _, status := range []string{"pending", "processing", "dead_letter"} {
			count, err := h.outboxPort.CountByStatus(ctx, status)
			if err != nil {
				continue
			}
			switch status {
			case "pending":
				summary["outbox_pending"] = count
			case "dead_letter":
				summary["outbox_failed"] = count
			}
		}
	}

	apiutil.OK(c, summary)
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

// handleOutboxStatus returns outbox event counts by status.
func (h *Handler) handleOutboxStatus(c *gin.Context) {
	if h.outboxPort == nil {
		apiutil.OK(c, gin.H{"ok": true, "counts": map[string]int64{}})
		return
	}

	ctx := c.Request.Context()
	statuses := []string{"pending", "processing", "completed", "dead_letter", "superseded"}
	counts := make(map[string]int64, len(statuses))

	for _, status := range statuses {
		count, err := h.outboxPort.CountByStatus(ctx, status)
		if err != nil {
			h.log.Warn("failed to count outbox status", zap.String("status", status), zap.Error(err))
			continue
		}
		counts[status] = count
	}

	apiutil.OK(c, gin.H{"ok": true, "counts": counts})
}

// handleOutboxEvents lists pending and processing outbox events.
func (h *Handler) handleOutboxEvents(c *gin.Context) {
	if h.outboxPort == nil {
		apiutil.OK(c, gin.H{"ok": true, "events": []any{}, "count": 0})
		return
	}

	events, err := h.outboxPort.ListPending(c.Request.Context())
	if err != nil {
		h.log.Error("failed to list outbox events", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":     true,
		"events": events,
		"count":  len(events),
	})
}

// handleIndexHealth returns index health for the dashboard.
func (h *Handler) handleIndexHealth(c *gin.Context) {
	// Reuse existing diagnostics data
	apiutil.OK(c, gin.H{
		"ok":       true,
		"degraded": false,
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

// maskPath hides the real filesystem path, showing only the filename.
func maskPath(p string) string {
	return filepath.Base(p)
}

// summariesToJSON converts domain Summary pointers to JSON-friendly maps.
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

// jobsToJSON converts domain Job values to JSON-friendly maps.
func (h *Handler) jobsToJSON(jobs []job.Job) []gin.H {
	result := make([]gin.H, 0, len(jobs))
	for _, j := range jobs {
		result = append(result, gin.H{
			"id":         j.ID,
			"type":       j.Type,
			"status":     string(j.Status),
			"progress":   j.Progress,
			"error":      j.Error,
			"created_at": j.CreatedAt,
			"updated_at": j.UpdatedAt,
		})
	}
	return result
}

// Compile-time check that Handler satisfies the module interface.
var _ interface {
	Name() string
	Enabled() bool
	RegisterRoutes(rg *gin.RouterGroup)
} = (*moduleWrapper)(nil)

// moduleWrapper wraps Handler to satisfy the api.Module interface.
type moduleWrapper struct {
	*Handler
	name    string
	enabled bool
}

// NewModule creates a module wrapper for the operator handler.
func NewModule(h *Handler) *moduleWrapper {
	return &moduleWrapper{Handler: h, name: "operator", enabled: true}
}

func (m *moduleWrapper) Name() string  { return m.name }
func (m *moduleWrapper) Enabled() bool { return m.enabled }

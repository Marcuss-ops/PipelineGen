package stock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// StockAssetLookup is the narrow port for looking up a single media
// asset by ID. Satisfied by *assets.ClipsRepository.Get (returns
// *asset.Asset). Pattern 0 typed port — the stock package stays
// free of infrastructure imports.
type StockAssetLookup interface {
	Get(ctx context.Context, id string) (*asset.Asset, error)
}

// StockDriveReader is the narrow port for streaming files from Google
// Drive. Satisfied by drive.Reader (DownloadFile + GetFileMeta).
// Pattern 0 typed port.
type StockDriveReader interface {
	DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error)
	GetFileMeta(ctx context.Context, fileID string) (*DriveFileMeta, error)
}

// DriveFileMeta is a minimal mirror of drive.FileMeta so the stock
// package stays free of infrastructure/drive imports. Exported so the
// composition-root adapter can construct it.
type DriveFileMeta struct {
	MimeType string
}

// Handler is the api-layer adapter for the stock pipeline endpoints.
// After S2b it holds the use case + logger + optional download deps.
// All dispatch logic lives in stockpipeline.StockUseCase.
type Handler struct {
	useCase   *stockpipeline.StockUseCase
	log       *zap.Logger
	assetRepo StockAssetLookup // optional (nil → 503 on /clips/:id/download)
	driveRead StockDriveReader // optional (nil → 503 on /clips/:id/download)
}

// NewHandler constructs the api handler. Production wire-up builds a
// *stockpipeline.StockUseCase first (composition root, module_sources.go)
// and passes it in; test fixtures may pass nil for either dependency.
func NewHandler(useCase *stockpipeline.StockUseCase, log *zap.Logger, assetRepo StockAssetLookup, driveRead StockDriveReader) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{
		useCase:   useCase,
		log:       log,
		assetRepo: assetRepo,
		driveRead: driveRead,
	}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	h.log.Info("Registering Stock Pipeline routes")

	r.POST("/run", h.RunStockPipeline)
	r.POST("/search-and-run", h.SearchAndRun)
	r.POST("/clips/:id/download", h.DownloadStockClip)
}

// ── 200-vs-202 SEMANTIC DECISION (S2c spec, applies to /run AND /search-and-run) ──
//
// Both endpoints return HTTP 200 OK (apiutil.OK) on success — NOT 202
// Accepted (apiutil.Accepted) — even though the dispatch path routes
// through an async job broker. The semantic distinction:
//
//   - 202 Accepted: fire-and-forget. Response acknowledges RECEIPT
//     but does NOT carry resolved identifiers. This is the contract
//     POST /api/jobs uses (handler enqueues into the broker and the
//     broker itself resolves the job_id asynchronously).
//
//   - 200 OK: synchronous acknowledgement. Response carries the
//     resolved values (job_id + status_url) inline. Used here because
//     by the time the handler returns, the orchestrator has already
//     completed the work needed to surface those identifiers (broker
//     accepted the enqueue, broker resolved a job_id, status URL
//     resolvable). The downstream async pipeline remains observable
//     via `status_url` `/api/jobs/<id>/full`, but THIS API call has
//     fully resolved.
//
// Drift trap: do NOT switch these endpoints back to apiutil.Accepted
// without a product-side review against the S2c spec. Endpoints that
// return only an unresolved placeholder belong on 202; these two
// anchor the 200 contract because they return the RESOLVED values
// inline.

// ── POST /api/stock/search-and-run ──────────────────────────────────────
//
// Body binds directly to the canonical stockpipeline.StockSearchAndRunRequest
// rather than a local mirror — that way the api request type and the
// application command type stay in lockstep (renames propagate via Go
// compile errors rather than via drift in two json-tag sets).

func (h *Handler) SearchAndRun(c *gin.Context) {
	// Default Async=true so existing clients (no "async" field in payload)
	// preserve the canonical jobs-broker path. Operators that want
	// in-process sync set "async": false on the wire. Sync mode also
	// flips Persist=true so the runner uses the resilient path and
	// completes upload + finalization + indexing instead of stopping
	// at the legacy manifest-only flow.
	req := stockpipeline.StockSearchAndRunRequest{Async: true}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	h.log.Info("stock search-and-run request received",
		zap.Int("queries", len(req.Queries)),
		zap.Int("direct_urls", len(req.DirectURLs)),
		zap.Int("drive_urls", len(req.DriveURLs)),
		zap.Int("clips", len(req.Clips)),
		zap.Int("total_minutes", req.TotalMinutes),
		zap.Int("chunk_duration", req.ChunkDuration),
		zap.Int("clip_duration", req.ClipDuration),
		zap.Bool("no_audio", req.NoAudio),
		zap.Bool("no_effects", req.NoEffects),
		zap.Bool("no_transitions", req.NoTransitions),
		zap.Int("max_videos", req.MaxVideos),
		zap.String("subfolder", req.Subfolder),
		zap.String("folder_name", req.FolderName),
		zap.String("folder_id", req.FolderID),
	)

	// HTTP validation — must run before FromSearchAndRunRequest so the
	// converter sees a valid shape (per the S2b design: validation in
	// the api layer, defaulting in the api layer).
	if len(req.Queries) == 0 && len(req.DirectURLs) == 0 && len(req.DriveURLs) == 0 && len(req.Clips) == 0 {
		apiutil.BadRequest(c, "queries, direct_urls, drive_urls, or clips required")
		return
	}
	if len(req.Clips) > 0 {
		hasURL := false
		for _, clip := range req.Clips {
			if clip.URL != "" {
				hasURL = true
				break
			}
		}
		if !hasURL {
			apiutil.BadRequest(c, "clips require at least one clip with a non-empty url")
			return
		}
	}
	if req.TotalMinutes <= 0 {
		req.TotalMinutes = 5
	}
	if req.ClipDuration < 0 {
		apiutil.BadRequest(c, "clip_duration must be >= 0")
		return
	}
	if req.ClipDuration == 0 {
		req.ClipDuration = 10
	}
	if req.ClipDuration > 0 && (req.ClipDuration < 3 || req.ClipDuration > 30) {
		apiutil.BadRequest(c, "clip_duration must be between 3 and 30 seconds")
		return
	}
	if !req.Async {
		req.Persist = true
	}

	cmd, err := stockpipeline.FromSearchAndRunRequest(&req)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	jobID, err := h.useCase.Submit(c.Request.Context(), cmd, req.Async)
	if err != nil {
		if errors.Is(err, stockpipeline.ErrJobsServiceRequired) {
			apiutil.Error(c, http.StatusServiceUnavailable,
				"stock async submit requires jobs service (no sync fallback — use /search-and-run with async flag=false on wire jobsSvc)")
			return
		}
		h.log.Error("stock search-and-run failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	resp := gin.H{
		"job_id": jobID,
	}
	if jobID != "" {
		resp["message"] = "Stock search-and-run job enqueued"
		resp["status_url"] = "/api/jobs/" + jobID + "/full"
	} else {
		resp["message"] = "Stock pipeline run completed"
	}
	// DoD #8 (July 2026): drive and indexing fields are empty for
	// async responses — populated when the caller polls the job
	// status via status_url. Pre-populated here so the response
	// shape is stable across both async and sync modes.
	resp["drive"] = stockDrivePlaceholder()
	resp["indexed"] = false
	resp["location"] = stockLocationPlaceholder()
	apiutil.OK(c, resp)
}

// 200/202 rationale: see comment block above SearchAndRun.

// ── POST /api/stock/run ────────────────────────────────────────────────

func (h *Handler) RunStockPipeline(c *gin.Context) {
	// Default Async=true so existing clients (no "async" field in payload)
	// preserve the canonical jobs-broker path. Operators that want
	// in-process sync set "async": false on the wire. Sync mode also
	// flips Persist=true so the runner uses the resilient path and
	// completes upload + finalization + indexing instead of stopping
	// at the legacy manifest-only flow.
	req := stockpipeline.StockRunPayload{Async: true}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	h.log.Info("stock run request received",
		zap.Int("search_queries", len(req.SearchQueries)),
		zap.Int("direct_urls", len(req.DirectURLs)),
		zap.Int("drive_urls", len(req.DriveURLs)),
		zap.Int("clips", len(req.Clips)),
		zap.Int("total_minutes", req.TotalMinutes),
		zap.Int("chunk_duration", req.ChunkDuration),
		zap.Int("clip_duration", req.ClipDuration),
		zap.Int("seconds_per_segment", req.SecondsPerSegment),
		zap.Bool("no_audio", req.NoAudio),
		zap.Bool("no_effects", req.NoEffects),
		zap.Bool("no_transitions", req.NoTransitions),
		zap.Int("max_videos", req.MaxVideos),
		zap.String("subfolder", req.Subfolder),
		zap.String("folder_name", req.FolderName),
		zap.String("drive_folder_id", req.DriveFolderID),
		zap.String("folder_id", req.FolderID),
	)

	// HTTP validation (same shape as SearchAndRun).
	if len(req.SearchQueries) == 0 && len(req.DirectURLs) == 0 && len(req.DriveURLs) == 0 && len(req.Clips) == 0 {
		apiutil.BadRequest(c, "search_queries, direct_urls, drive_urls, or clips required")
		return
	}
	if len(req.Clips) > 0 {
		hasURL := false
		for _, clip := range req.Clips {
			if clip.URL != "" {
				hasURL = true
				break
			}
		}
		if !hasURL {
			apiutil.BadRequest(c, "clips require at least one clip with a non-empty url")
			return
		}
	}
	if req.TotalMinutes <= 0 {
		req.TotalMinutes = 5
	}
	if req.ClipDuration < 0 {
		apiutil.BadRequest(c, "clip_duration must be >= 0")
		return
	}
	if req.ClipDuration == 0 {
		req.ClipDuration = 10
	}
	if req.ClipDuration > 0 && (req.ClipDuration < 3 || req.ClipDuration > 30) {
		apiutil.BadRequest(c, "clip_duration must be between 3 and 30 seconds")
		return
	}
	if !req.Async {
		req.Persist = true
	}

	cmd, err := stockpipeline.FromRunPayload(&req)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	jobID, err := h.useCase.Submit(c.Request.Context(), cmd, req.Async)
	if err != nil {
		if errors.Is(err, stockpipeline.ErrJobsServiceRequired) {
			apiutil.Error(c, http.StatusServiceUnavailable,
				"stock async submit requires jobs service (no sync fallback — use /run with async flag=false or wire jobsSvc)")
			return
		}
		h.log.Error("stock run failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	resp := gin.H{
		"job_id": jobID,
	}
	if jobID != "" {
		resp["message"] = "Stock pipeline job enqueued"
		resp["status_url"] = "/api/jobs/" + jobID + "/full"
	} else {
		resp["message"] = "Stock pipeline run completed"
	}
	// DoD #8 (July 2026): drive and indexing fields populated when
	// the caller polls the job status via status_url.
	resp["drive"] = stockDrivePlaceholder()
	resp["indexed"] = false
	resp["location"] = stockLocationPlaceholder()
	apiutil.OK(c, resp)
}

// stockDrivePlaceholder returns the canonical empty drive response block
// used by both SearchAndRun and RunStockPipeline. The placeholder is
// empty for async responses — populated when the caller polls the job
// status via status_url.
func stockDrivePlaceholder() gin.H {
	return gin.H{
		"path":      "",
		"folder_id": "",
		"file_id":   "",
		"link":      "",
	}
}

// stockLocationPlaceholder returns the canonical empty location response
// block (DoD #10, July 2026). Stock endpoints are async — the location
// is populated when the caller polls the job status via status_url.
func stockLocationPlaceholder() gin.H {
	return gin.H{
		"category": "",
		"subject":  "",
		"provider": "",
		"style":    "",
	}
}

// DownloadStockClip streams the MP4 file for a stock media asset.
// Looks up the asset by ID from media_assets (source=stock), gets its
// drive_file_id, and proxies the file from Google Drive.
//
// Route: POST /api/stock-pipeline/clips/:id/download
//
// POST (not GET) is intentional per the E2E test contract — the
// download is a side-effect-producing operation in the stock pipeline
// context (may trigger lazy indexing). Mirrors the clips DownloadClip
// pattern (clip_action.go) but uses narrow Pattern-0 ports
// (StockAssetLookup + StockDriveReader) so the stock package stays
// free of infrastructure imports.
func (h *Handler) DownloadStockClip(c *gin.Context) {
	clipID := c.Param("id")
	if clipID == "" {
		apiutil.BadRequest(c, "clip id is required")
		return
	}

	if h.assetRepo == nil || h.driveRead == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "stock download not available (asset repo or drive reader not wired)")
		return
	}

	// 1. Look up the asset from media_assets
	ast, err := h.assetRepo.Get(c.Request.Context(), clipID)
	if err != nil {
		h.log.Error("stock download: asset lookup failed", zap.String("clip_id", clipID), zap.Error(err))
		apiutil.InternalError(c, fmt.Errorf("asset lookup failed: %w", err))
		return
	}
	if ast == nil {
		apiutil.NotFound(c, "stock asset not found: "+clipID)
		return
	}

	// 2. Get the drive_file_id from the asset
	driveFileID := ast.DriveFileID()
	if driveFileID == "" {
		// Try local path as fallback
		localPath := ast.LocalPath()
		if localPath != "" {
			c.File(localPath)
			return
		}
		apiutil.NotFound(c, "stock asset has no drive_file_id and no local path")
		return
	}

	// 3. Verify MIME type (block non-media files)
	meta, metaErr := h.driveRead.GetFileMeta(c.Request.Context(), driveFileID)
	if metaErr != nil {
		h.log.Error("stock download: drive metadata lookup failed", zap.String("drive_id", driveFileID), zap.Error(metaErr))
		apiutil.InternalError(c, fmt.Errorf("drive metadata lookup failed: %w", metaErr))
		return
	}

	if !strings.HasPrefix(meta.MimeType, "video/") &&
		!strings.HasPrefix(meta.MimeType, "audio/") &&
		meta.MimeType != "application/octet-stream" {
		h.log.Warn("stock download: refusing to proxy non-media file",
			zap.String("drive_id", driveFileID), zap.String("mime", meta.MimeType))
		apiutil.BadRequest(c, "drive file is not media: "+meta.MimeType)
		return
	}

	// 4. Stream the file from Drive
	body, contentType, dlErr := h.driveRead.DownloadFile(c.Request.Context(), driveFileID)
	if dlErr != nil {
		h.log.Error("stock download: drive download failed", zap.String("drive_id", driveFileID), zap.Error(dlErr))
		apiutil.InternalError(c, fmt.Errorf("drive download failed: %w", dlErr))
		return
	}
	defer body.Close()

	if contentType == "" || contentType == "application/octet-stream" {
		contentType = "video/mp4"
	}

	c.Header("Content-Type", contentType)
	c.Header("Cache-Control", "public, max-age=3600")

	_, copyErr := io.Copy(c.Writer, body)
	if copyErr != nil {
		h.log.Debug("stock download: drive stream interrupted", zap.Error(copyErr))
	}
}

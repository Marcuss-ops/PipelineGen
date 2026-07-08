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
//
// godlike/06 SSOT (one canonical owner per fact): the `Size int64`
// field is populated by the composition-root adapter from
// `drive.FileMeta.Size`, which the underlying *Uploader.GetFileMeta
// fetches in the SAME Drive API call (single files.get?fields=
// "...,size,..." round-trip — zero additional network cost). The
// canonical 2GB cap (`MaxStockDownloadSize`) gates downstream
// DownloadFile streaming — see `ErrStockDownloadOversized` below
// (PR-STOCK-OVERSIZED-DOWNLOAD-GUARD, 2026-07-08).
type DriveFileMeta struct {
	MimeType string
	Size     int64
}

// MaxStockDownloadSize is the canonical upper bound on stock clip
// downloads. 2 binary GB (= 2 << 30 bytes) covers any legitimate
// stock video clip (5-second stock snippets at typical bitrates are
// <500MB) while rejecting pathological inputs (oversized garbage
// flagged as video/mp4 via MIME bypass attempts).
//
// godlike/06 SSOT: this constant is the SOLE owner of the cap value;
// tests + production code MUST reference it by symbol (NOT by hard-
// coded 2GB literal) so future cap changes propagate uniformly.
//
// godlike/07 NO-FAKE-AVAILABILITY: the cap is fail-closed at the file
// size guard boundary (returns 413 BEFORE the DownloadFile streaming
// call opens — operators never see a wasted Drive bandwidth charge).
const MaxStockDownloadSize int64 = 2 << 30 // 2 GiB

// ErrStockDownloadOversized is the canonical typed sentinel for the
// size-guard gate in DownloadStockClip.
//
// godlike/07 typed-error contract: callers probe via
// `errors.Is(err, stock.ErrStockDownloadOversized)` — the typed
// sentinel is the SOLE contract surface (NO string-match fallback).
//
// godlike/06 SSOT: this sentinel lives ONLY in handler.go (the
// canonical SOLE owner of the size-gate contract). Composition-root
// adapters + downstream consumers MUST import this package to use
// errors.Is — NO re-declaration in adapters or wrappers.
//
// The byte-count suffix is computed via fmt.Errorf so future changes
// to MaxStockDownloadSize propagate naturally (a hardcoded literal
// "2 GiB" in the message string would lie if the constant were
// bumped; the runtime apiutil.Error response surfaces the actual
// size-vs-cap diagnostic separately).
var ErrStockDownloadOversized = fmt.Errorf(
	"stock download: drive file exceeds MaxStockDownloadSize (%d bytes); refusing to proxy",
	MaxStockDownloadSize)

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

	// HTTP validation (PR-STOCK-DRY-VALIDATION): single typed-helper
	// call (applyStockDefaults in handler.go, canonical SOLE owner).
	// The "queries, direct_urls, drive_urls, or clips required" wire
	// error literal is preserved byte-equivalent to pre-PR (vs the
	// /run variant which uses "search_queries, ..."). The
	// SearchSourceCount field is len(req.Queries) for the typed source
	// list ([]SearchQuery) — the helper only checks count, not
	// element type, so a future rename of SearchQuery stays
	// transparent.
	adjusted, validateErr := applyStockDefaults("queries, direct_urls, drive_urls, or clips required", stockValidationInput{
		SearchSourceCount: len(req.Queries),
		DirectURLsCount:   len(req.DirectURLs),
		DriveURLsCount:    len(req.DriveURLs),
		Clips:             req.Clips,
		TotalMinutes:      req.TotalMinutes,
		ClipDuration:      req.ClipDuration,
		Async:             req.Async,
	})
	if validateErr != nil {
		apiutil.BadRequest(c, validateErr.Error())
		return
	}
	req.TotalMinutes = adjusted.TotalMinutes
	req.ClipDuration = adjusted.ClipDuration
	req.Persist = adjusted.Persist

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

	// godlike/07 NO-FAKE-AVAILABILITY (PR-STOCK-NO-PLACEHOLDERS, 2026-07-08):
	// the response carries ONLY {job_id, message, status_url}. The
	// pre-PR `drive`/`indexed`/`location` placeholder fields were
	// REMOVED — empty-string placeholders are the canonical
	// silent-success class (per AGENTS.md "cerco ... punti in cui il
	// flusso può dichiarare successo senza aver completato davvero"):
	// a caller reading `drive: {path: "", folder_id: ""}` sees a
	// shape that LOOKS populated but is functionally empty, forcing
	// them to poll status_url anyway. Removing the placeholders
	// makes the contract explicit: the handler resolved the
	// dispatch (job_id + status_url inline); the rest comes from
	// the canonical job status endpoint.
	resp := gin.H{
		"job_id": jobID,
	}
	if jobID != "" {
		resp["message"] = "Stock search-and-run job enqueued"
		resp["status_url"] = "/api/jobs/" + jobID + "/full"
	} else {
		resp["message"] = "Stock pipeline run completed"
	}
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

	// HTTP validation (PR-STOCK-DRY-VALIDATION): the 22-line
	// pre-PR validation block is now a single typed-helper call
	// (applyStockDefaults lives ONLY at handler.go, canonical
	// SOLE owner — godlike/06 SSOT). The "search_queries, ..." vs
	// "queries, ..." wire-field-name difference between /run and
	// /search-and-run is captured via the sourcesEmptyMsg arg.
	adjusted, validateErr := applyStockDefaults("search_queries, direct_urls, drive_urls, or clips required", stockValidationInput{
		SearchSourceCount: len(req.SearchQueries),
		DirectURLsCount:   len(req.DirectURLs),
		DriveURLsCount:    len(req.DriveURLs),
		Clips:             req.Clips,
		TotalMinutes:      req.TotalMinutes,
		ClipDuration:      req.ClipDuration,
		Async:             req.Async,
	})
	if validateErr != nil {
		apiutil.BadRequest(c, validateErr.Error())
		return
	}
	req.TotalMinutes = adjusted.TotalMinutes
	req.ClipDuration = adjusted.ClipDuration
	req.Persist = adjusted.Persist

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

	// godlike/07 NO-FAKE-AVAILABILITY: response carries ONLY
	// {job_id, message, status_url}. See SearchAndRun above for
	// the canonical rationale (PR-STOCK-NO-PLACEHOLDERS, 2026-07-08).
	resp := gin.H{
		"job_id": jobID,
	}
	if jobID != "" {
		resp["message"] = "Stock pipeline job enqueued"
		resp["status_url"] = "/api/jobs/" + jobID + "/full"
	} else {
		resp["message"] = "Stock pipeline run completed"
	}
	apiutil.OK(c, resp)
}

// stockValidationInput is the type-erased input to applyStockDefaults.
//
// godlike/06 SSOT: lives ONLY at handler.go (private to package).
// godlike/07 minimum-blast-radius: pure-data plain struct, no methods.
//
// SearchSourceCount is len(req.SearchQueries) for Run OR
// len(req.Queries) for SearchAndRun — both payload types carry a
// wire-shape list of search-source terms; the validator only cares
// about the count (≥1 requirement on any source field), not the
// element type. DirectURLs and DriveURLs use the same field name on
// both payload types. Clips is the shared []stockpipeline.ClipSpec
// slice; the helper inspects only Clip.URL to enforce the has-URL
// rule.
type stockValidationInput struct {
	SearchSourceCount int
	DirectURLsCount   int
	DriveURLsCount    int
	Clips             []stockpipeline.ClipSpec
	TotalMinutes      int
	ClipDuration      int
	Async             bool
}

// stockValidationDefaults is the canonical return shape.
//
// godlike/06 SSOT: lives ONLY at handler.go. The 3 fields populate
// back into the request struct after validation succeeds — the
// caller assigns them back to req before invoking
// FromRunPayload/FromSearchAndRunRequest so the application-layer
// converter sees the defaulted values verbatim.
type stockValidationDefaults struct {
	TotalMinutes int
	ClipDuration int
	Persist      bool
}

// applyStockDefaults enforces the canonical validation contract
// shared by POST /api/stock-pipeline/run AND POST
// /api/stock-pipeline/search-and-run (PR-STOCK-DRY-VALIDATION,
// 2026-07-08).
//
// godlike/06 SSOT: This helper lives ONLY at handler.go — the
// canonical SOLE owner. NO re-declaration in adapters or wrappers.
// The 2 pre-extraction validation blocks were textually identical
// EXCEPT for the "no sources" error message string:
//
//	/run pre-PR:               "search_queries, direct_urls, drive_urls, or clips required"
//	/search-and-run pre-PR:    "queries,         direct_urls, drive_urls, or clips required"
//
// The sourcesEmptyMsg parameter carries the contextual wire-field
// name difference.
//
// godlike/07 minimum-blast-radius: 0 signature drift, 0 new imports,
// 0 new dependencies. Pure refactor — every operator-readable wire
// error message is preserved byte-equivalent so the existing
// handler_test.go assertions on those literal strings continue to
// pass unchanged.
//
// Validation contract (preserves both pre-extraction blocks EXACTLY):
//
//  1. All 4 source fields empty → return sourcesEmptyMsg error.
//  2. Clips non-empty AND no clip with non-empty URL → return
//     "clips require at least one clip with a non-empty url".
//  3. TotalMinutes ≤ 0 → default 5.
//  4. ClipDuration < 0 → "clip_duration must be >= 0".
//  5. ClipDuration == 0 → default 10.
//  6. ClipDuration > 0 AND (ClipDuration < 3 OR > 30) →
//     "clip_duration must be between 3 and 30 seconds".
//  7. Async=false → Persist=true (sync mode enables the resilient
//     path so the runner completes upload + finalization + indexing
//     instead of stopping at the legacy manifest-only flow).
func applyStockDefaults(sourcesEmptyMsg string, in stockValidationInput) (stockValidationDefaults, error) {
	if in.SearchSourceCount == 0 && in.DirectURLsCount == 0 && in.DriveURLsCount == 0 && len(in.Clips) == 0 {
		return stockValidationDefaults{}, errors.New(sourcesEmptyMsg)
	}
	if len(in.Clips) > 0 {
		hasURL := false
		for _, clip := range in.Clips {
			if clip.URL != "" {
				hasURL = true
				break
			}
		}
		if !hasURL {
			return stockValidationDefaults{}, errors.New("clips require at least one clip with a non-empty url")
		}
	}
	out := stockValidationDefaults{TotalMinutes: in.TotalMinutes}
	if out.TotalMinutes <= 0 {
		out.TotalMinutes = 5
	}
	out.ClipDuration = in.ClipDuration
	if out.ClipDuration < 0 {
		return stockValidationDefaults{}, errors.New("clip_duration must be >= 0")
	}
	if out.ClipDuration == 0 {
		out.ClipDuration = 10
	}
	if out.ClipDuration > 0 && (out.ClipDuration < 3 || out.ClipDuration > 30) {
		return stockValidationDefaults{}, errors.New("clip_duration must be between 3 and 30 seconds")
	}
	out.Persist = !in.Async
	return out, nil
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

	// Size guard (godlike/07 NO-FAKE-AVAILABILITY): reject oversized
	// files BEFORE opening the DownloadFile stream. Pre-PR this gate
	// did not exist — a 5GB garbage file flagged as video/mp4 would
	// stream fully, wasting Drive bandwidth and consuming the
	// caller-side connection. Post-PR, MIME-bypass attempts above
	// 2 GiB are rejected at the typed-sentinel boundary. See
	// MaxStockDownloadSize + ErrStockDownloadOversized for the
	// canonical contract. The size check is `>` (strict inequality,
	// NO epsilon) so exactly-2GiB files pass through (canonical
	// boundary semantics for byte-counting).
	if meta.Size > MaxStockDownloadSize {
		h.log.Warn("stock download: refusing to proxy oversized file",
			zap.String("drive_id", driveFileID),
			zap.String("mime", meta.MimeType),
			zap.Int64("size_bytes", meta.Size),
			zap.Int64("cap_bytes", MaxStockDownloadSize))
		apiutil.Error(c, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("drive file is %d bytes; maximum is %d bytes (%s)",
				meta.Size, MaxStockDownloadSize, ErrStockDownloadOversized.Error()))
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

	// Content-Type fallback chain (godlike/07 NO-FAKE-AVAILABILITY):
	// prefer the Drive DownloadFile response's contentType if non-empty
	// and not the opaque octet-stream sentinel; otherwise fall back to
	// the MIME type we just fetched via GetFileMeta (which preserves
	// the canonical audio/mpeg vs video/mp4 distinction — important
	// because stock clips can be either); finally fall back to
	// application/octet-stream (NEVER video/mp4 — that was a false
	// assumption if the file happens to be audio).
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = meta.MimeType
	}
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = "application/octet-stream"
	}

	c.Header("Content-Type", contentType)
	c.Header("Cache-Control", "public, max-age=3600")

	_, copyErr := io.Copy(c.Writer, body)
	if copyErr != nil {
		h.log.Debug("stock download: drive stream interrupted", zap.Error(copyErr))
	}
}

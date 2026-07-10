// ── POST /api/stock-pipeline/clips/:id/download ────────────────────────
//
// Streams the MP4 file for a stock media asset via the narrow Pattern 0
// ports (StockAssetLookup + StockDriveReader) so the stock package stays
// free of infrastructure imports.

package stock

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

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
	// 2 GiB are rejected at the typed-sentinel boundary. The size
	// check is `>` (strict inequality, NO epsilon) so exactly-2GiB
	// files pass through (canonical boundary semantics for
	// byte-counting).
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

	if _, copyErr := io.Copy(c.Writer, body); copyErr != nil {
		h.log.Debug("stock download: drive stream interrupted", zap.Error(copyErr))
	}
}

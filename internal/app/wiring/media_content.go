package wiring

// media_content.go owns the authenticated media byte-delivery routes:
//
//	GET /api/v1/media/assets/:id/content  (object-store only, CR-19)
//	GET /api/v1/media/assets/:id/download (unified: local -> object_storage -> drive)
//
// The content route resolves the asset in the media SSOT, takes its PRIMARY
// object_storage location, and proxies the bytes with Range/Content-Length.
// The download route serves ANY ACTIVE asset type (video, audio, image,
// script/plate) from the first location that has bytes and exposes the
// expected SHA-256; local files are verified before the first byte is sent.
// Neither route redirects to the RenderingGen store URL: that URL is internal
// (often loopback) and its bearer token must never reach a client.
//
// Both proxies stream; they never buffer a clip in memory.

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/operator"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/httpjson"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// objectStoreClient has no overall timeout (a large clip may stream for
// minutes) but fails fast when the store does not answer.
var objectStoreClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   8,
	},
}

// newMediaContentHandler builds the gin handler. assets is the canonical media
// SSOT read surface; nil leaves the fail-closed 503 path.
func newMediaContentHandler(assets operator.AssetService, log *zap.Logger) gin.HandlerFunc {
	if log == nil {
		log = zap.NewNop()
	}
	return func(c *gin.Context) {
		if assets == nil {
			apiutil.Error(c, http.StatusServiceUnavailable, "media asset reader not available")
			return
		}
		id := strings.TrimSpace(c.Param("id"))
		if id == "" {
			apiutil.NotFound(c, "asset not found")
			return
		}
		details, err := assets.Get(c.Request.Context(), id)
		if err != nil || details == nil || details.Asset == nil {
			apiutil.NotFound(c, "asset not found")
			return
		}
		if string(details.Asset.LifecycleState) != "ACTIVE" {
			apiutil.Error(c, http.StatusConflict, "asset is not ACTIVE")
			return
		}
		loc := details.ObjectStorageLocation()
		if loc == nil {
			apiutil.Error(c, http.StatusNotFound, "no object storage content available")
			return
		}
		proxyObjectStorageLocation(c, loc, log, id)
	}
}

// objectStorageTarget resolves the fetchable URL of an object-storage
// location, preferring the download URL over the internal URI.
func objectStorageTarget(loc *asset.Location) string {
	if loc == nil {
		return ""
	}
	target := strings.TrimSpace(loc.DownloadURL)
	if target == "" {
		target = strings.TrimSpace(loc.URI)
	}
	if target == "" {
		target = strings.TrimSpace(loc.AccessURL)
	}
	return target
}

// proxyObjectStorageLocation streams an object-storage location with Range
// support. It never redirects: the internal URL may be loopback and its bearer
// must not reach the client.
func proxyObjectStorageLocation(c *gin.Context, loc *asset.Location, log *zap.Logger, assetID string) {
	target := objectStorageTarget(loc)
	if target == "" {
		apiutil.Error(c, http.StatusNotFound, "object storage location has no URL")
		return
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, target, nil)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	if rng := strings.TrimSpace(c.GetHeader("Range")); rng != "" {
		req.Header.Set("Range", rng)
	}
	if tok := strings.TrimSpace(os.Getenv("OBJECTSTORE_TOKEN")); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := objectStoreClient.Do(req)
	if err != nil {
		log.Warn("media content: object store fetch failed",
			zap.String("asset_id", assetID), zap.Error(err))
		apiutil.Error(c, http.StatusBadGateway, "object store unavailable")
		return
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
	case http.StatusRequestedRangeNotSatisfiable:
		copyHeaderIfPresent(c, resp, "Content-Range")
		c.Status(http.StatusRequestedRangeNotSatisfiable)
		return
	default:
		log.Warn("media content: object store rejected the request",
			zap.String("asset_id", assetID), zap.Int("status", resp.StatusCode))
		apiutil.Error(c, http.StatusBadGateway, "object store rejected the request")
		return
	}

	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = strings.TrimSpace(loc.MimeType)
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Header("Content-Type", contentType)
	c.Header("Accept-Ranges", "bytes")
	// Keep a caller-supplied cache policy (the unified download route sends
	// no-store for a one-shot asset transfer) and default to the content
	// route's short private cache only when no policy was set.
	if c.Writer.Header().Get("Cache-Control") == "" {
		c.Header("Cache-Control", "private, max-age=300")
	}
	if loc.ContentSHA256 != "" {
		c.Header("X-Content-SHA256", strings.ToLower(loc.ContentSHA256))
	}
	if v := resp.Header.Get("Content-Length"); v != "" {
		c.Header("Content-Length", v)
	} else if resp.StatusCode == http.StatusOK && loc.FileSizeBytes > 0 {
		c.Header("Content-Length", strconv.FormatInt(loc.FileSizeBytes, 10))
	}
	copyHeaderIfPresent(c, resp, "Content-Range")
	copyHeaderIfPresent(c, resp, "ETag")

	c.Status(resp.StatusCode)
	if _, err := io.Copy(c.Writer, resp.Body); err != nil && !isClientGone(err) {
		log.Warn("media content: stream failed",
			zap.String("asset_id", assetID), zap.Error(err))
	}
}

func copyHeaderIfPresent(c *gin.Context, resp *http.Response, name string) {
	if v := resp.Header.Get(name); v != "" {
		c.Header(name, v)
	}
}

// isClientGone treats a client disconnect as a normal end of stream, not a
// server-side failure worth warning about.
func isClientGone(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "broken pipe") ||
		strings.Contains(err.Error(), "connection reset by peer") ||
		strings.Contains(err.Error(), "context canceled"))
}

// DriveArtifactReader is the narrow Drive surface the unified download needs.
// *youtube/adapters.ClipsDriveAdapter satisfies it; DownloadFileRange is
// discovered opportunistically so a deployment without range support still
// serves the full file.
type DriveArtifactReader interface {
	GetFileMeta(ctx context.Context, fileID string) (*appclips.ClipDriveFileMetaDTO, error)
	DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error)
}

// driveRangeReader is the optional range capability.
type driveRangeReader interface {
	DownloadFileRange(ctx context.Context, fileID string, start, end int64) (io.ReadCloser, string, error)
}

// newMediaAssetDownloadHandler builds the unified download handler. assets is
// the canonical media SSOT read surface; drive may be nil (drive-only assets
// then answer 404 instead of pretending the file exists).
func newMediaAssetDownloadHandler(assets operator.AssetService, drive interface{}, log *zap.Logger) gin.HandlerFunc {
	if log == nil {
		log = zap.NewNop()
	}
	driveReader, _ := drive.(DriveArtifactReader)
	return func(c *gin.Context) {
		if assets == nil {
			apiutil.Error(c, http.StatusServiceUnavailable, "media asset reader not available")
			return
		}
		id := strings.TrimSpace(c.Param("id"))
		if id == "" {
			apiutil.NotFound(c, "asset not found")
			return
		}
		details, err := assets.Get(c.Request.Context(), id)
		if err != nil || details == nil || details.Asset == nil {
			apiutil.NotFound(c, "asset not found")
			return
		}
		if string(details.Asset.LifecycleState) != "ACTIVE" {
			apiutil.Error(c, http.StatusConflict, "asset is not ACTIVE")
			return
		}

		expectedSHA := canonicalSHA(details.Asset.ContentHash())
		if expectedSHA == "" {
			expectedSHA = canonicalSHA(details.Asset.BinarySHA256())
		}
		if loc := details.LocalLocation(); loc != nil && expectedSHA == "" {
			expectedSHA = canonicalSHA(loc.ContentSHA256)
		}
		c.Header("Cache-Control", "private, no-store")
		if expectedSHA != "" {
			c.Header("X-Content-SHA256", expectedSHA)
		}

		// 1. Local file. The asset metadata path is the historical field; the
		// canonical asset_locations row wins when both exist.
		localPath := ""
		if loc := details.LocalLocation(); loc != nil {
			localPath = strings.TrimSpace(loc.URI)
		}
		if localPath == "" {
			localPath = strings.TrimSpace(details.Asset.LocalPath())
		}
		if localPath != "" {
			if info, statErr := os.Stat(localPath); statErr == nil && !info.IsDir() {
				serveLocalAsset(c, localPath, info, expectedSHA, log, id)
				return
			}
		}

		// 2. Object storage (CR-19 delivery). An object_storage row without a
		// fetchable URL is not a delivery source: fall through to Drive rather
		// than 404-ing an asset whose bytes live there.
		if loc := details.ObjectStorageLocation(); loc != nil && objectStorageTarget(loc) != "" {
			c.Header("X-Content-Source", "object_storage")
			if expectedSHA == "" {
				expectedSHA = canonicalSHA(loc.ContentSHA256)
				if expectedSHA != "" {
					c.Header("X-Content-SHA256", expectedSHA)
				}
			}
			proxyObjectStorageLocation(c, loc, log, id)
			return
		}

		// 3. Google Drive.
		driveID := ""
		if loc := details.DriveLocation(); loc != nil {
			driveID = strings.TrimSpace(loc.ExternalID)
			if expectedSHA == "" {
				expectedSHA = canonicalSHA(loc.ContentSHA256)
				if expectedSHA != "" {
					c.Header("X-Content-SHA256", expectedSHA)
				}
			}
		}
		if driveID == "" {
			driveID = strings.TrimSpace(details.Asset.DriveFileID())
		}
		if driveID == "" {
			driveID = driveutil.FileIDFromLink(details.Asset.DriveLink())
		}
		if driveID == "" {
			driveID = driveutil.FileIDFromLink(details.Asset.DownloadLink())
		}
		if driveID == "" {
			apiutil.NotFound(c, "asset has no downloadable bytes")
			return
		}
		if driveReader == nil {
			apiutil.Error(c, http.StatusServiceUnavailable, "drive download is not wired")
			return
		}
		c.Header("X-Content-Source", "drive")
		serveDriveAsset(c, driveReader, driveID, id, expectedSHA, log)
	}
}

// serveLocalAsset streams a local file with Range support. A known expected
// hash is verified before serving; a mismatch is a hard 409 so a remote reader
// never publishes corrupted bytes as the asset.
func serveLocalAsset(c *gin.Context, path string, info os.FileInfo, expected string, log *zap.Logger, assetID string) {
	// Verify a full download before sending a byte. A Range request is one
	// slice of a resumable transfer: hashing the whole file per chunk would
	// double the I/O for every resume, so the client verifies the assembled
	// file against X-Content-SHA256 instead.
	if expected != "" && strings.TrimSpace(c.GetHeader("Range")) == "" {
		actual, err := fileSHA256(path)
		if err != nil {
			log.Error("media download: hash local file", zap.String("asset_id", assetID), zap.Error(err))
			apiutil.InternalError(c, err)
			return
		}
		if !strings.EqualFold(actual, expected) {
			log.Error("media download: local content hash mismatch",
				zap.String("asset_id", assetID), zap.String("expected", expected), zap.String("actual", actual))
			apiutil.Error(c, http.StatusConflict, "asset content hash mismatch")
			return
		}
	}

	c.Header("X-Content-Source", "local")
	c.Header("Accept-Ranges", "bytes")
	if ct := assetContentType(path, ""); ct != "" {
		c.Header("Content-Type", ct)
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(path)))

	f, err := os.Open(path)
	if err != nil {
		log.Error("media download: open local file", zap.String("asset_id", assetID), zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	defer func() { _ = f.Close() }()
	http.ServeContent(c.Writer, c.Request, filepath.Base(path), info.ModTime(), f)
}

// serveDriveAsset proxies a Drive file with Range support. Any media type is
// allowed (images and scripts included) — this is the generic asset surface,
// not the legacy clip route.
func serveDriveAsset(c *gin.Context, reader DriveArtifactReader, driveID, assetID, expected string, log *zap.Logger) {
	meta, err := reader.GetFileMeta(c.Request.Context(), driveID)
	if err != nil {
		log.Error("media download: drive metadata", zap.String("asset_id", assetID), zap.Error(err))
		apiutil.Error(c, http.StatusBadGateway, "failed to reach drive")
		return
	}
	if meta == nil {
		meta = &appclips.ClipDriveFileMetaDTO{}
	}
	filename := strings.TrimSpace(meta.Name)
	if filename == "" {
		filename = assetID
	}
	contentType := assetContentType(filename, meta.MimeType)
	if contentType != "" {
		c.Header("Content-Type", contentType)
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

	rangeHeader := strings.TrimSpace(c.GetHeader("Range"))
	if rangeHeader != "" && meta.Size > 0 {
		start, end, ok := httpjson.ParseByteRange(rangeHeader, meta.Size)
		if !ok {
			c.Header("Content-Range", fmt.Sprintf("bytes */%d", meta.Size))
			apiutil.Error(c, http.StatusRequestedRangeNotSatisfiable, "invalid range")
			return
		}
		if rr, ok := reader.(driveRangeReader); ok {
			body, ct, dlErr := rr.DownloadFileRange(c.Request.Context(), driveID, start, end)
			if dlErr == nil {
				defer func() { _ = body.Close() }()
				if ct != "" {
					c.Header("Content-Type", ct)
				}
				c.Header("Accept-Ranges", "bytes")
				c.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, meta.Size))
				c.Header("Content-Length", strconv.FormatInt(end-start+1, 10))
				c.Status(http.StatusPartialContent)
				if _, copyErr := io.Copy(c.Writer, body); copyErr != nil {
					log.Debug("media download: drive range stream interrupted",
						zap.String("asset_id", assetID), zap.Error(copyErr))
				}
				return
			}
			log.Debug("media download: drive range unsupported, serving full file",
				zap.String("asset_id", assetID), zap.Error(dlErr))
		}
	}

	body, ct, err := reader.DownloadFile(c.Request.Context(), driveID)
	if err != nil {
		log.Error("media download: drive stream", zap.String("asset_id", assetID), zap.Error(err))
		apiutil.Error(c, http.StatusBadGateway, "failed to stream from drive")
		return
	}
	defer func() { _ = body.Close() }()
	if ct != "" {
		c.Header("Content-Type", ct)
	}
	if meta.Size > 0 {
		c.Header("Content-Length", strconv.FormatInt(meta.Size, 10))
	}
	_, canRange := reader.(driveRangeReader)
	if canRange {
		c.Header("Accept-Ranges", "bytes")
	} else {
		c.Header("Accept-Ranges", "none")
	}
	hasher := digest.NewSHA256()
	writer := io.MultiWriter(c.Writer, hasher)
	written, copyErr := io.Copy(writer, body)
	if copyErr != nil {
		log.Debug("media download: drive stream interrupted",
			zap.String("asset_id", assetID), zap.Error(copyErr))
		return
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if expected != "" && !strings.EqualFold(actual, expected) {
		log.Error("media download: drive content hash mismatch",
			zap.String("asset_id", assetID), zap.String("expected", expected),
			zap.String("actual", actual), zap.Int64("bytes", written))
	}
}

// canonicalSHA normalises a hash candidate and rejects anything that is not a
// 64-hex SHA-256 (a legacy MD5 in the same slot must not make every download
// fail the integrity check).
func canonicalSHA(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if digest.IsSHA256(value) {
		return value
	}
	return ""
}

func fileSHA256(path string) (string, error) {
	sum, _, err := digest.SHA256File(path)
	return sum, err
}

func assetContentType(name, declared string) string {
	if declared != "" && declared != "application/octet-stream" {
		return declared
	}
	if ext := filepath.Ext(name); ext != "" {
		if ct := mime.TypeByExtension(ext); ct != "" {
			return ct
		}
	}
	return declared
}

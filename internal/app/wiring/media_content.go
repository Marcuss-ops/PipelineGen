package wiring

// media_content.go owns the authenticated object-store content route used by
// CR-19 delivery: GET /api/v1/media/assets/:id/content.
//
// The route resolves the asset in the media SSOT, takes its PRIMARY
// object_storage location, and proxies the bytes with Range/Content-Length.
// It deliberately does not redirect to the RenderingGen store URL: that URL is
// internal (often loopback) and its bearer token must never reach a client.
//
// The proxy streams; it never buffers a clip in memory.

import (
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/operator"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
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
		target := strings.TrimSpace(loc.DownloadURL)
		if target == "" {
			target = strings.TrimSpace(loc.URI)
		}
		if target == "" {
			target = strings.TrimSpace(loc.AccessURL)
		}
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
				zap.String("asset_id", id), zap.Error(err))
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
				zap.String("asset_id", id), zap.Int("status", resp.StatusCode))
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
		c.Header("Cache-Control", "private, max-age=300")
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
				zap.String("asset_id", id), zap.Error(err))
		}
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

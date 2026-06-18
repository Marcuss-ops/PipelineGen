// Package workerasset provides an HTTP endpoint for workers to download
// asset content with lease-based authorization.
//
// GET /api/v1/jobs/:job_id/assets/:asset_id
//
// Required headers:
//   - Authorization: Bearer <worker-token>
//   - X-Velox-Lease-ID: <lease_id>
//
// The endpoint verifies:
//   1. Worker is authenticated (is_worker from middleware)
//   2. Worker owns the lease on the job (LeaseID match)
//   3. The asset is linked to the job via job_assets
//   4. The asset is READY (not PENDING/FAILED/DELETED)
//   5. The job is not terminal (SUCCEEDED/FAILED/CANCELLED rejects download)
//
// Response headers:
//   - Content-Type: <mime_type>
//   - Content-Length: <size_bytes>
//   - ETag: "<sha256>"
//   - X-Velox-SHA256: <sha256>
//   - Accept-Ranges: bytes
package workerasset

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/assetregistry"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// Handler serves asset content to authenticated workers with lease verification.
type Handler struct {
	db           *sql.DB
	assetService *assetregistry.Service
	blobDataDir  string // root directory for local blob store path resolution
	log          *zap.Logger
}

// NewHandler creates a worker asset download handler.
// blobDataDir is the root data directory for the local blob store
// (e.g., /var/lib/pipelinegen). Storage keys are relative paths under
// blobDataDir/blobs/.
func NewHandler(db *sql.DB, assetService *assetregistry.Service, blobDataDir string, log *zap.Logger) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{
		db:           db,
		assetService: assetService,
		blobDataDir:  blobDataDir,
		log:          log,
	}
}

// RegisterRoutes registers the worker asset routes under the given group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/v1/jobs/:job_id/assets/:asset_id", h.GetAsset)
}

// terminalStatuses are job statuses that reject asset downloads.
var terminalStatuses = map[string]bool{
	"SUCCEEDED": true,
	"FAILED":    true,
	"CANCELLED": true,
}

// GetAsset serves asset content to an authorized worker.
func (h *Handler) GetAsset(c *gin.Context) {
	jobID := c.Param("job_id")
	assetID := c.Param("asset_id")
	leaseID := c.GetHeader("X-Velox-Lease-ID")

	ctx := c.Request.Context()

	// Step 0: Verify worker is authenticated (set by Auth middleware)
	isWorker, _ := c.Get("is_worker")
	if isWorker != true {
		apiutil.Error(c, http.StatusForbidden, "endpoint requires worker authentication")
		return
	}

	// Step 1: Load the job and verify lease ownership
	job, err := h.getJob(ctx, jobID)
	if err != nil {
		h.log.Warn("failed to load job for asset request",
			zap.String("job_id", jobID),
			zap.Error(err),
		)
		apiutil.InternalError(c, err)
		return
	}
	if job == nil {
		apiutil.NotFound(c, "job not found")
		return
	}

	// Verify worker owns the lease
	if leaseID == "" {
		apiutil.Error(c, http.StatusForbidden, "missing X-Velox-Lease-ID header")
		return
	}
	if job.LeaseID == "" || job.LeaseID != leaseID {
		apiutil.Error(c, http.StatusForbidden, "invalid lease: worker does not own this job")
		return
	}

	// Step 1.5: Reject terminal jobs (asset downloads after completion)
	if terminalStatuses[job.Status] {
		apiutil.Error(c, http.StatusGone, fmt.Sprintf("job is in terminal state: %s", job.Status))
		return
	}

	// Step 2: Verify asset is linked to this job
	jobAsset, err := h.assetService.GetJobAsset(ctx, jobID, assetID)
	if err != nil {
		h.log.Warn("job_asset lookup failed",
			zap.String("job_id", jobID),
			zap.String("asset_id", assetID),
			zap.Error(err),
		)
		apiutil.InternalError(c, err)
		return
	}
	if jobAsset == nil {
		apiutil.NotFound(c, "asset not linked to this job")
		return
	}

	// Step 3: Load the asset and verify it's READY
	asset, err := h.assetService.Get(ctx, assetID)
	if err != nil {
		h.log.Warn("asset lookup failed",
			zap.String("asset_id", assetID),
			zap.Error(err),
		)
		apiutil.InternalError(c, err)
		return
	}
	if asset == nil {
		apiutil.NotFound(c, "asset not found")
		return
	}
	if asset.Status != assetregistry.StatusReady {
		apiutil.Error(c, http.StatusServiceUnavailable,
			fmt.Sprintf("asset not ready (status=%s)", asset.Status))
		return
	}

	// Step 4: Open the blob file (resolved relative to blob data dir)
	reader, err := h.openBlob(asset)
	if err != nil {
		h.log.Error("failed to open asset blob",
			zap.String("asset_id", assetID),
			zap.String("storage_key", asset.StorageKey),
			zap.Error(err),
		)
		apiutil.Error(c, http.StatusInternalServerError, "failed to read asset content")
		return
	}
	defer reader.Close()

	// Step 5: Set response headers
	c.Header("Content-Type", asset.MimeType)
	c.Header("Content-Length", strconv.FormatInt(asset.SizeBytes, 10))
	c.Header("ETag", `"`+asset.SHA256+`"`)
	c.Header("X-Velox-SHA256", asset.SHA256)
	c.Header("Accept-Ranges", "bytes")

	// Handle Range requests
	rangeHeader := c.GetHeader("Range")
	if rangeHeader != "" {
		h.serveRange(c, rangeHeader, asset, reader)
		return
	}

	// Stream full content
	c.Status(http.StatusOK)
	io.Copy(c.Writer, reader)

	// Touch last accessed asynchronously
	go func() {
		h.assetService.TouchAccess(context.Background(), assetID)
	}()
}

// serveRange handles partial content requests.
func (h *Handler) serveRange(c *gin.Context, rangeHeader string, asset *assetregistry.Asset, reader io.ReadSeeker) {
	var start, end int64
	if _, err := fmt.Sscanf(rangeHeader, "bytes=%d-%d", &start, &end); err != nil {
		// Try open-ended range: "bytes=0-"
		if _, err := fmt.Sscanf(rangeHeader, "bytes=%d-", &start); err != nil {
			apiutil.Error(c, http.StatusRequestedRangeNotSatisfiable, "invalid range format")
			return
		}
		end = asset.SizeBytes - 1
	}

	if start < 0 || start >= asset.SizeBytes || end < start || end >= asset.SizeBytes {
		apiutil.Error(c, http.StatusRequestedRangeNotSatisfiable, "range out of bounds")
		return
	}

	reader.Seek(start, io.SeekStart)
	limitedReader := io.LimitReader(reader, end-start+1)

	c.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, asset.SizeBytes))
	c.Header("Content-Length", strconv.FormatInt(end-start+1, 10))
	c.Status(http.StatusPartialContent)
	io.Copy(c.Writer, limitedReader)

	go func() {
		h.assetService.TouchAccess(context.Background(), asset.AssetID)
	}()
}

// jobRecord is a minimal projection for lease validation.
type jobRecord struct {
	WorkerID string
	LeaseID  string
	Status   string
}

// getJob loads a minimal job record for lease validation.
func (h *Handler) getJob(ctx context.Context, jobID string) (*jobRecord, error) {
	var j jobRecord
	err := h.db.QueryRowContext(ctx,
		`SELECT COALESCE(worker_id, ''), COALESCE(lease_id, ''), status FROM jobs WHERE id = ?`,
		jobID,
	).Scan(&j.WorkerID, &j.LeaseID, &j.Status)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get job %s: %w", jobID, err)
	}
	return &j, nil
}

// readSeekCloser combines Read, Seek, and Close.
type readSeekCloser interface {
	io.ReadSeeker
	io.Closer
}

// openBlob opens the asset's storage file for reading.
// The storage key (e.g., "sha256/ab/abcdef...") is resolved relative to
// the blob store's data directory: <blobDataDir>/blobs/<storageKey>.
func (h *Handler) openBlob(asset *assetregistry.Asset) (readSeekCloser, error) {
	if asset.StorageBackend != "local" {
		return nil, fmt.Errorf("unsupported storage backend: %s", asset.StorageBackend)
	}
	if asset.StorageKey == "" {
		return nil, fmt.Errorf("asset has no storage key")
	}

	// Resolve relative to blob data dir: <dataDir>/blobs/<storageKey>
	fullPath := filepath.Join(h.blobDataDir, "blobs", asset.StorageKey)
	f, err := os.Open(fullPath)
	if err != nil {
		return nil, fmt.Errorf("open local asset %s: %w", fullPath, err)
	}
	return f, nil
}

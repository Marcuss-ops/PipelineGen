// Package workerasset provides an HTTP endpoint for workers to download
// artifact content with lease-based authorization.
//
// GET /api/v1/jobs/:job_id/artifacts/:artifact_id
//
// Required headers:
//   - Authorization: Bearer <worker-token>
//   - X-Velox-Lease-ID: <lease_id>
//
// The endpoint verifies:
//   1. Worker is authenticated (is_worker from middleware)
//   2. Worker owns the lease on the job (LeaseID match)
//   3. The artifact is linked to the job via job_artifacts
//   4. The artifact is READY (not STAGING/VERIFYING/FAILED/DELETED)
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

	"github.com/Marcuss-ops/PipelineGen/internal/artifacts"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// Handler serves artifact content to authenticated workers with lease verification.
type Handler struct {
	db              *sql.DB
	artifactService *artifacts.Service
	blobDataDir     string // root directory for local blob store path resolution
	log             *zap.Logger
}

// NewHandler creates a worker artifact download handler.
// blobDataDir is the root data directory for the local blob store
// (e.g., /var/lib/pipelinegen). Storage keys are relative paths under
// blobDataDir/blobs/.
func NewHandler(db *sql.DB, artifactService *artifacts.Service, blobDataDir string, log *zap.Logger) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{
		db:              db,
		artifactService: artifactService,
		blobDataDir:     blobDataDir,
		log:             log,
	}
}

// RegisterRoutes registers the worker artifact routes under the given group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/v1/jobs/:job_id/artifacts/:artifact_id", h.GetArtifact)
}

// terminalStatuses are job statuses that reject artifact downloads.
var terminalStatuses = map[string]bool{
	"SUCCEEDED": true,
	"FAILED":    true,
	"CANCELLED": true,
}

// GetArtifact serves artifact content to an authorized worker.
func (h *Handler) GetArtifact(c *gin.Context) {
	jobID := c.Param("job_id")
	artifactID := c.Param("artifact_id")
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
		h.log.Warn("failed to load job for artifact request",
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

	// Step 1.5: Reject terminal jobs (artifact downloads after completion)
	if terminalStatuses[job.Status] {
		apiutil.Error(c, http.StatusGone, fmt.Sprintf("job is in terminal state: %s", job.Status))
		return
	}

	// Step 2: Verify artifact is linked to this job
	jobArtifact, err := h.artifactService.GetJobArtifact(ctx, jobID, artifactID)
	if err != nil {
		h.log.Warn("job_artifact lookup failed",
			zap.String("job_id", jobID),
			zap.String("artifact_id", artifactID),
			zap.Error(err),
		)
		apiutil.InternalError(c, err)
		return
	}
	if jobArtifact == nil {
		apiutil.NotFound(c, "artifact not linked to this job")
		return
	}

	// Step 3: Load the artifact and verify it's READY
	artifact, err := h.artifactService.Get(ctx, artifactID)
	if err != nil {
		h.log.Warn("artifact lookup failed",
			zap.String("artifact_id", artifactID),
			zap.Error(err),
		)
		apiutil.InternalError(c, err)
		return
	}
	if artifact == nil {
		apiutil.NotFound(c, "artifact not found")
		return
	}
	if artifact.Status != artifacts.StatusReady {
		apiutil.Error(c, http.StatusServiceUnavailable,
			fmt.Sprintf("artifact not ready (status=%s)", artifact.Status))
		return
	}

	// Step 4: Open the blob file (resolved relative to blob data dir)
	reader, err := h.openBlob(artifact)
	if err != nil {
		h.log.Error("failed to open artifact blob",
			zap.String("artifact_id", artifactID),
			zap.String("storage_key", artifact.StorageKey),
			zap.Error(err),
		)
		apiutil.Error(c, http.StatusInternalServerError, "failed to read artifact content")
		return
	}
	defer reader.Close()

	// Step 5: Set response headers
	c.Header("Content-Type", artifact.MimeType)
	c.Header("Content-Length", strconv.FormatInt(artifact.SizeBytes, 10))
	c.Header("ETag", `"`+artifact.SHA256+`"`)
	c.Header("X-Velox-SHA256", artifact.SHA256)
	c.Header("Accept-Ranges", "bytes")

	// Handle Range requests
	rangeHeader := c.GetHeader("Range")
	if rangeHeader != "" {
		h.serveRange(c, rangeHeader, artifact, reader)
		return
	}

	// Stream full content
	c.Status(http.StatusOK)
	io.Copy(c.Writer, reader)

	// Touch last accessed asynchronously
	go func() {
		h.artifactService.TouchAccess(context.Background(), artifactID)
	}()
}

// serveRange handles partial content requests.
func (h *Handler) serveRange(c *gin.Context, rangeHeader string, artifact *artifacts.Artifact, reader io.ReadSeeker) {
	var start, end int64
	if _, err := fmt.Sscanf(rangeHeader, "bytes=%d-%d", &start, &end); err != nil {
		// Try open-ended range: "bytes=0-"
		if _, err := fmt.Sscanf(rangeHeader, "bytes=%d-", &start); err != nil {
			apiutil.Error(c, http.StatusRequestedRangeNotSatisfiable, "invalid range format")
			return
		}
		end = artifact.SizeBytes - 1
	}

	if start < 0 || start >= artifact.SizeBytes || end < start || end >= artifact.SizeBytes {
		apiutil.Error(c, http.StatusRequestedRangeNotSatisfiable, "range out of bounds")
		return
	}

	reader.Seek(start, io.SeekStart)
	limitedReader := io.LimitReader(reader, end-start+1)

	c.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, artifact.SizeBytes))
	c.Header("Content-Length", strconv.FormatInt(end-start+1, 10))
	c.Status(http.StatusPartialContent)
	io.Copy(c.Writer, limitedReader)

	go func() {
		h.artifactService.TouchAccess(context.Background(), artifact.ID)
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

// openBlob opens the artifact's storage file for reading.
// The storage key (e.g., "sha256/ab/abcdef...") is resolved relative to
// the blob store's data directory: <blobDataDir>/blobs/<storageKey>.
func (h *Handler) openBlob(artifact *artifacts.Artifact) (readSeekCloser, error) {
	if artifact.StorageBackend != "local" {
		return nil, fmt.Errorf("unsupported storage backend: %s", artifact.StorageBackend)
	}
	if artifact.StorageKey == "" {
		return nil, fmt.Errorf("artifact has no storage key")
	}

	// Resolve relative to blob data dir: <dataDir>/blobs/<storageKey>
	fullPath := filepath.Join(h.blobDataDir, "blobs", artifact.StorageKey)
	f, err := os.Open(fullPath)
	if err != nil {
		return nil, fmt.Errorf("open local artifact %s: %w", fullPath, err)
	}
	return f, nil
}

// Package storage — internal_sync_drive_folder.go provides the
// server-to-server variant of POST /sync.
//
// QDRANT-001 (June 2026) closure: this handler is the canonical Go-side
// endpoint invoked by the rewritten `scripts/tools/sync_drive_qdrant.py`
// (a pure-HTTP client, no SQLite / Qdrant / OAuth / SentenceTransformer).
//
//	Public admin:    POST /api/media/sync
//	Server-to-server: POST /internal/v1/media/sync
//
// Both routes share the same job-dispatch logic (drive.folder.sync with
// idempotency-friendly payload). The /internal/v1/ variant additionally
// requires an `Idempotency-Key` header so retries from the Python client
// collapse to a single canonical ingestion (QDRANT-001 H section).
package storage

import (
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/transport"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// InternalSyncDriveFolderRequest is the JSON body for the /internal/v1
// server-to-server variant. Same shape as SyncDriveFolderRequest, kept
// as a separate type so future server-to-server fields (workspace_id,
// source_version) can diverge without breaking the admin surface.
type InternalSyncDriveFolderRequest struct {
	DriveFolderID string `json:"drive_folder_id" binding:"required"`
	Source        string `json:"source,omitempty"`
	Name          string `json:"name,omitempty"`
	MediaType     string `json:"media_type,omitempty"`
}

// idempotencyHeader is the canonical request header used to dedupe
// drive folder syncs across retries. Per QDRANT-001 spec section H:
// "Inviare Idempotency-Key deterministica" — Python client emits
// `drive:<folder-id>:folder-sync` so retries collapse to one record.
const idempotencyHeader = "Idempotency-Key"

// InternalSyncDriveFolder handles POST /internal/v1/media/sync.
//
// This is the server-to-server variant of SyncDriveFolder. Differences:
//   - mounted under /internal/v1/ (worker/service auth, not admin)
//   - requires Idempotency-Key header; missing key -> 400 Bad Request
//   - logs idempotency key + caller workspace alongside the request
//
// Behaviour contract (mirrors the admin variant):
//   - 202 Accepted on job dispatch
//   - 401 if WorkerAuth middleware rejects the bearer token
//   - 400 if drive_folder_id is empty or Idempotency-Key is missing
//   - 503 if catalog sync service is not wired (composition error)
//   - 500 if job dispatch fails
func (h *Handler) InternalSyncDriveFolder(c *gin.Context) {
	idem := strings.TrimSpace(c.GetHeader(idempotencyHeader))
	if idem == "" {
		apiutil.BadRequest(c, "Idempotency-Key header is required for /internal/v1/media/sync (QDRANT-001)")
		return
	}

	var req InternalSyncDriveFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	folderID := strings.TrimSpace(req.DriveFolderID)
	if folderID == "" {
		apiutil.BadRequest(c, "drive_folder_id is required")
		return
	}

	if h.catalogSync == nil {
		apiutil.InternalError(c, errCatalogSyncNotConfigured)
		return
	}

	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "drive"
	}
	mediaType := strings.TrimSpace(req.MediaType)
	if mediaType == "" {
		mediaType = "clip"
	}

	h.log.Info("dispatching async drive folder sync (server-to-server)",
		zap.String("drive_folder_id", folderID),
		zap.String("source", source),
		zap.String("name", req.Name),
		zap.String("media_type", mediaType),
		zap.String("idempotency_key", idem),
	)

	// QDRANT-001 closure: build payload via shared helper so admin
	// (/api) and server-to-server (/internal/v1) variants stay in
	// lockstep on payload schema and job type. The job service already
	// enforces event-level dedup (QDRANT-002 PR4 contract) when the
	// same (type, correlation_id) tuple is replayed.
	payload, correlationID := buildSyncPayload(&SyncPayloadInput{
		FolderID:  folderID,
		Source:    source,
		Name:      req.Name,
		MediaType: mediaType,
		IdemKey:   idem,
		Caller:    "internal_v1",
	})

	if ok := transport.EnqueueAsync(c, h.jobsSvc, &transport.EnqueueInput{
		Type:          "drive.folder.sync",
		Payload:       payload,
		MaxRetries:    2,
		CorrelationID: correlationID,
	}, "Drive folder sync dispatched."); ok {
		return
	}
	// EnqueueAsync returns false if jobsSvc is nil (503) or on error.
}

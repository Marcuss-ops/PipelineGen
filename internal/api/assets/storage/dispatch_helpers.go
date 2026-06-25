// Package storage — dispatch_helpers.go hosts the shared job-dispatch
// path between the admin (POST /api/media/sync-drive-folder) and the
// server-to-server (POST /internal/v1/media/sync-drive-folder) handlers.
//
// QDRANT-001 (June 2026) closure: extracting this dispatch keeps both
// handlers in lockstep on the canonical job-type, payload schema, and
// errors — divergence between them was a QDRANT-001 risk noted in the
// doc (the same handler called from two different auth surfaces must
// not drift).
package storage

import (
	"encoding/json"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/gin-gonic/gin"
)

// errCatalogSyncNotConfigured surfaces the composition-time error when
// the catalog sync service is not wired into the storage Handler. The
// handlers return this verbatim with 500 / generic message to avoid
// leaking internal wiring details.
var errCatalogSyncNotConfigured = simpleErr("catalog sync service not configured")

// simpleErr is a minimal error implementation that does NOT carry any
// sensitive context. We use it instead of fmt.Errorf because the
// caller (apiutil.InternalError) may log it; we want it stable for
// tests.
type stringError string

func (s stringError) Error() string { return string(s) }

func simpleErr(msg string) error { return stringError(msg) }

// DispatchSyncInput is the shared shape used by both admin and
// server-to-server variants of the sync-drive-folder dispatch.
type DispatchSyncInput struct {
	FolderID  string
	Source    string
	Name      string
	MediaType string
	IdemKey   string
	Caller    string
}

// DispatchSyncOutput is the response payload that the variant handlers
// render with their own envelope.
type DispatchSyncOutput struct {
	JobID string
}

// dispatchDriveFolderSync is the single shared job-enqueue path used by
// both /api/media/sync-drive-folder (admin) and /internal/v1/media/
// sync-drive-folder (server-to-server). The only difference between the
// two callers is the "Caller" tag for logs and the auth surface that
// runs upstream.
//
// Idempotency: the underlying jobs.Service.Enqueue already dedups on
// (type, correlation_id) per QDRANT-002 PR4. The IdemKey is attached as
// a payload field so the job handler can log it.
func (h *Handler) dispatchDriveFolderSync(
	c *gin.Context,
	in *DispatchSyncInput,
) (*DispatchSyncOutput, error) {
	payload := jobs.DriveFolderSyncPayload{
		DriveFolderID:  in.FolderID,
		Source:         in.Source,
		Name:           in.Name,
		MediaType:      in.MediaType,
		IdempotencyKey: in.IdemKey,
		Caller:         in.Caller,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	var payloadMap map[string]any
	if err := json.Unmarshal(payloadBytes, &payloadMap); err != nil {
		return nil, err
	}

	jobReq := &job.EnqueueRequest{
		Type:       "drive.folder.sync",
		Payload:    payloadMap,
		MaxRetries: 2,
	}
	if in.IdemKey != "" {
		// Encode the Idempotency-Key in the correlation_id so the job
		// service dedups (type, correlation_id) tuples on retry.
		jobReq.CorrelationID = in.IdemKey
	}

	j, err := h.jobsSvc.Enqueue(c.Request.Context(), jobReq)
	if err != nil {
		return nil, err
	}
	h.log.Info("drive folder sync enqueued",
		zap.String("drive_folder_id", in.FolderID),
		zap.String("job_id", j.ID),
		zap.String("caller", in.Caller),
		zap.String("idempotency_key", in.IdemKey),
	)
	return &DispatchSyncOutput{JobID: j.ID}, nil
}

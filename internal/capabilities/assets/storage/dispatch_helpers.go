// Package storage — dispatch_helpers.go hosts the shared payload
// construction for the Drive folder sync job, used by both admin
// (POST /api/media/sync) and server-to-server
// (POST /internal/v1/media/sync) handlers.
//
// QDRANT-001 (June 2026) closure: extracting this payload builder
// keeps both handlers in lockstep on the canonical job-type and
// payload schema — divergence between them was a QDRANT-001 risk
// noted in the doc (the same handler called from two different auth
// surfaces must not drift).
package storage

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
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

// SyncPayloadInput is the shared shape used by both admin and
// server-to-server variants of the sync dispatch.
type SyncPayloadInput struct {
	FolderID  string
	Source    string
	Name      string
	MediaType string
	IdemKey   string
	Caller    string
}

// buildSyncPayload constructs the canonical drive.folder.sync payload
// map from the given input. This is the single shared payload-
// construction path used by both /api/media/sync (admin)
// and /internal/v1/media/sync (server-to-server).
//
// Callers pass the returned map as Payload to transport.EnqueueAsync.
func buildSyncPayload(in *SyncPayloadInput) (map[string]any, string) {
	payload := jobs.DriveFolderSyncPayload{
		DriveFolderID:  in.FolderID,
		Source:         in.Source,
		Name:           in.Name,
		MediaType:      in.MediaType,
		IdempotencyKey: in.IdemKey,
		Caller:         in.Caller,
	}

	correlationID := ""
	if in.IdemKey != "" {
		correlationID = in.IdemKey
	}

	return map[string]any{
		"drive_folder_id": payload.DriveFolderID,
		"source":          payload.Source,
		"name":            payload.Name,
		"media_type":      payload.MediaType,
		"idempotency_key": payload.IdempotencyKey,
		"caller":          payload.Caller,
	}, correlationID
}

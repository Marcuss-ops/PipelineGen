// Package transport — async enqueue helper for long-running jobs
// (June 2026 Wave-17/18 cherry-pick restoration, Phase 4 unification
// slimmed this file to one responsibility).
//
// Historical scope (Phase 4 / June 2026, REMOVED):
//   - UtilityHandler struct + ctor + Slugify method → absorbed into
//     internal/api/system/handler.go (2026-08-23 Cleanup Day).
//     Prior location `utility.go` deleted; no standalone UtilityModule.
//   - HealthHandler struct + ctor + Health/Ready methods → relocated
//     to `health.go` (the deep-check `/health` URL contract from the
//     codex/health-ready-contract, June 2026, lives there now).
//
// Remaining in this file: ONLY the async-enqueue helper that caller
// sites in `internal/api/assets/{voiceover,clips/bulk_upload_transport,
// storage/{local_to_drive,sync_drive_folder,internal_sync_drive_folder},
// youtube}/handler.go` route through with concrete `job.Service` and
// the typed `*EnqueueInput` literal envelope below.
//
// Per AGENTS.md Pattern 5 (one concept per file): the file is
// technically misnamed post-slim — `async_enqueue.go` would be
// cleaner. The rename is a low-cost follow-up (no caller edits
// required) but deferred here because it doesn't change the build
// outcome.
package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// ── Async enqueue helper (used by every long-running handler) ──────

// EnqueueInput is the typed payload envelope handed to EnqueueAsync.
// Field set is the closed schema used by the bulk_upload +
// voiceover_indexer + youtube_clip_extract + sync_drive_folder +
// local_to_drive jobs; addition of fields is a typed-coupled drift
// (each job handler enforces its own payload type, but the envelope
// is shared).
type EnqueueInput struct {
	Type          string         `json:"type"`
	Payload       map[string]any `json:"payload"`
	MaxRetries    int            `json:"max_retries,omitempty"`
	CorrelationID string         `json:"correlation_id,omitempty"`
	Project       string         `json:"project,omitempty"`
	ActiveKey     string         `json:"active_key,omitempty"`
}

// EnqueueAsync enqueues a long-running job via the canonical jobs
// service. Returns true on enqueue success. Returns false on jobs
// service missing (HTTP 503) or on enqueue error (HTTP 500 with
// mapped message); on false the gin context already carries the
// response — the caller should `return` immediately.
//
// Mirrors the contract pinned by
// internal/api/assets/voiceover/handler.go (Wave 18 follow-up),
// internal/api/assets/clips/bulk_upload_transport.go (Wave 18),
// internal/api/assets/storage/sync_drive_folder.go + local_to_drive.go
// (Wave 17/18), and internal/api/assets/youtube/youtube_handlers.go.
func EnqueueAsync(c *gin.Context, jobsSvc job.Service, in *EnqueueInput, msg string) bool {
	if jobsSvc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "jobs service not configured")
		return false
	}
	req := &job.EnqueueRequest{
		Type:          in.Type,
		Payload:       in.Payload,
		MaxRetries:    in.MaxRetries,
		CorrelationID: in.CorrelationID,
		Project:       in.Project,
		ActiveKey:     in.ActiveKey,
	}
	if _, err := jobsSvc.Enqueue(c.Request.Context(), req); err != nil {
		apiutil.Error(c, http.StatusInternalServerError, msg+": "+err.Error())
		return false
	}
	apiutil.OK(c, gin.H{"ok": true, "message": msg})
	return true
}
